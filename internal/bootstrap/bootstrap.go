// Package bootstrap installs the agent-status forwarder script and hook
// configuration into Claude Code's and Codex's config directories. It is
// the Go replacement for the previous scripts/bootstrap.sh.
package bootstrap

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed assets/post-agent-status.sh assets/claude-code.json assets/codex.json
var assets embed.FS

const (
	placeholder = "path-to-post-agent-status"

	agentClaude = "claude"
	agentCodex  = "codex"
)

// KnownAgents returns the agent identifiers BuildPlan accepts, in
// presentation order. Exposed so the CLI can mention them in help text.
func KnownAgents() []string {
	return []string{agentClaude, agentCodex}
}

// Agent describes one bootstrap target (Claude Code or Codex).
type Agent struct {
	// ID is the short identifier used by --agents (e.g. "claude").
	ID string
	// Label is shown in human-facing output (e.g. "Claude Code").
	Label string
	// ConfigDir is the agent's config directory (e.g. ~/.claude).
	ConfigDir string
	// HooksTarget is the config file the hook block is merged into.
	HooksTarget string
	// hooksAsset is the embedded template name for this agent.
	hooksAsset string
}

// Options controls which agents to configure and where their files live.
// An empty ClaudeDir / CodexDir falls back to the standard environment
// (CLAUDE_CONFIG_DIR / CODEX_HOME) or ~/.claude / ~/.codex. An empty
// BootstrapDir falls back to $XDG_CONFIG_HOME/agent-status (or
// ~/.config/agent-status).
type Options struct {
	Agents        []string
	ClaudeDir     string
	CodexDir      string
	BootstrapDir  string
}

// Plan describes the work bootstrap will perform.
type Plan struct {
	// ScriptPath is the single shared location for the forwarder.
	ScriptPath string
	// OrphanedScripts are forwarder copies left behind by earlier
	// per-agent installs that bootstrap will remove.
	OrphanedScripts []string
	Agents          []AgentPlan
}

// AgentPlan is the per-agent slice of a Plan.
type AgentPlan struct {
	Agent Agent
	// TargetExists is true when HooksTarget already exists and the hook
	// block will be merged into it (rather than written fresh).
	TargetExists bool
}

// BuildPlan resolves config dirs and selected agents into a Plan. When
// Options.Agents is empty, all KnownAgents() are configured.
func BuildPlan(opts Options) (Plan, error) {
	agentIDs := opts.Agents
	if len(agentIDs) == 0 {
		agentIDs = KnownAgents()
	}

	bootstrapDir, err := resolveBootstrapDir(opts.BootstrapDir)
	if err != nil {
		return Plan{}, err
	}
	claudeDir, err := resolveClaudeDir(opts.ClaudeDir)
	if err != nil {
		return Plan{}, err
	}
	codexDir, err := resolveCodexDir(opts.CodexDir)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{
		ScriptPath: filepath.Join(bootstrapDir, "post-agent-status.sh"),
	}

	seen := map[string]bool{}
	for _, raw := range agentIDs {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true

		var a Agent
		switch id {
		case agentClaude:
			a = Agent{
				ID:          agentClaude,
				Label:       "Claude Code",
				ConfigDir:   claudeDir,
				HooksTarget: filepath.Join(claudeDir, "settings.json"),
				hooksAsset:  "assets/claude-code.json",
			}
		case agentCodex:
			a = Agent{
				ID:          agentCodex,
				Label:       "Codex",
				ConfigDir:   codexDir,
				HooksTarget: filepath.Join(codexDir, "hooks.json"),
				hooksAsset:  "assets/codex.json",
			}
		default:
			return Plan{}, fmt.Errorf("unknown agent %q (known: claude, codex)", raw)
		}

		exists, err := fileExists(a.HooksTarget)
		if err != nil {
			return Plan{}, err
		}
		plan.Agents = append(plan.Agents, AgentPlan{Agent: a, TargetExists: exists})

		legacy := filepath.Join(a.ConfigDir, "scripts", "post-agent-status.sh")
		if legacy != plan.ScriptPath {
			if ok, err := fileExists(legacy); err == nil && ok {
				plan.OrphanedScripts = append(plan.OrphanedScripts, legacy)
			}
		}
	}

	if len(plan.Agents) == 0 {
		return Plan{}, errors.New("no agents selected")
	}
	return plan, nil
}

// Describe renders a human-readable summary of the plan suitable for the
// confirmation prompt. Output is deterministic.
func (p Plan) Describe(w io.Writer) {
	fmt.Fprintln(w, "This will configure agent hooks to post events to the agent-status collector.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Shared forwarder script: %s\n", p.ScriptPath)
	fmt.Fprintln(w)
	for _, ap := range p.Agents {
		fmt.Fprintf(w, "  %s (%s)\n", ap.Agent.Label, ap.Agent.ConfigDir)
		if ap.TargetExists {
			fmt.Fprintf(w, "    • merge hooks into:      %s\n", ap.Agent.HooksTarget)
			fmt.Fprintf(w, "                             (existing file backed up alongside as .bak.<timestamp>)\n")
		} else {
			fmt.Fprintf(w, "    • create new hooks file: %s\n", ap.Agent.HooksTarget)
		}
	}
	if len(p.OrphanedScripts) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Cleanup of legacy per-agent script copies:")
		for _, path := range p.OrphanedScripts {
			fmt.Fprintf(w, "    • remove: %s\n", path)
		}
	}
}

// Execute carries out the plan. When dryRun is true it performs no writes.
func (p Plan) Execute(dryRun bool, w io.Writer) error {
	script, err := assets.ReadFile("assets/post-agent-status.sh")
	if err != nil {
		return err
	}

	if dryRun {
		previewFile(w, p.ScriptPath, script, true)
	} else {
		if err := installScript(p.ScriptPath, script); err != nil {
			return fmt.Errorf("install forwarder: %w", err)
		}
		fmt.Fprintf(w, "installed forwarder to %s\n", p.ScriptPath)
	}

	for _, ap := range p.Agents {
		if err := executeAgent(p.ScriptPath, ap, dryRun, w); err != nil {
			return fmt.Errorf("%s: %w", ap.Agent.Label, err)
		}
	}

	for _, path := range p.OrphanedScripts {
		if err := cleanupOrphan(path, script, dryRun, w); err != nil {
			return fmt.Errorf("cleanup %s: %w", path, err)
		}
	}

	return nil
}

func executeAgent(scriptPath string, ap AgentPlan, dryRun bool, w io.Writer) error {
	hookTmpl, err := assets.ReadFile(ap.Agent.hooksAsset)
	if err != nil {
		return err
	}
	rendered, err := canonicalizeJSON(bytes.ReplaceAll(hookTmpl, []byte(placeholder), []byte(scriptPath)))
	if err != nil {
		return err
	}

	if dryRun {
		if !ap.TargetExists {
			previewFile(w, ap.Agent.HooksTarget, rendered, false)
			return nil
		}
		existing, merged, err := computeMerged(ap.Agent.HooksTarget, rendered)
		if err != nil {
			return err
		}
		previewMerge(w, ap.Agent.HooksTarget, existing, merged)
		return nil
	}

	if ap.TargetExists {
		backup, err := mergeHooksFile(ap.Agent.HooksTarget, rendered)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "merged %s hooks into %s (backup at %s)\n", ap.Agent.Label, ap.Agent.HooksTarget, backup)
		return nil
	}

	if err := writeFile(ap.Agent.HooksTarget, rendered, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(w, "wrote %s\n", ap.Agent.HooksTarget)
	return nil
}

// cleanupOrphan removes a legacy per-agent forwarder copy. If its
// contents diverge from the canonical script we know about, a backup is
// taken first so the user can recover any local edits.
func cleanupOrphan(path string, canonical []byte, dryRun bool, w io.Writer) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	differs := !bytes.Equal(existing, canonical)
	if dryRun {
		if differs {
			fmt.Fprintf(w, "[dry-run] would remove orphaned %s (backing up first: contents differ from installed script)\n", path)
		} else {
			fmt.Fprintf(w, "[dry-run] would remove orphaned %s\n", path)
		}
		return nil
	}
	if differs {
		backup, err := backupFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "backed up modified orphan %s -> %s\n", path, backup)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	fmt.Fprintf(w, "removed orphaned %s\n", path)
	return nil
}

// previewFile diffs path's current contents against want. If executable
// is true, the file is reported as a script (mode 0755) in new-file
// output; otherwise it's reported as a plain file.
func previewFile(w io.Writer, path string, want []byte, executable bool) {
	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		mode := "0644"
		if executable {
			mode = "0755"
		}
		fmt.Fprintf(w, "[dry-run] would create %s (mode %s, %d bytes)\n", path, mode, len(want))
		fmt.Fprint(w, indent(prefixAll(string(want), "+ "), "    "))
		return
	}
	if err != nil {
		fmt.Fprintf(w, "[dry-run] cannot read %s: %v\n", path, err)
		return
	}
	if bytes.Equal(existing, want) {
		fmt.Fprintf(w, "[dry-run] unchanged: %s\n", path)
		return
	}
	fmt.Fprintf(w, "[dry-run] would update %s:\n", path)
	fmt.Fprint(w, indent(unifiedDiff(string(existing), string(want), 3), "    "))
}

// previewMerge prints the diff between the existing hook file and the
// merged result. existing comes from disk (as-is); merged is the JSON
// agent-status would write.
func previewMerge(w io.Writer, path string, existing, merged []byte) {
	if bytes.Equal(existing, merged) {
		fmt.Fprintf(w, "[dry-run] hooks already up to date: %s\n", path)
		return
	}
	fmt.Fprintf(w, "[dry-run] would merge hooks into %s (backup alongside):\n", path)
	fmt.Fprint(w, indent(unifiedDiff(string(existing), string(merged), 3), "    "))
}

// computeMerged returns (existing, merged) where merged is the JSON
// agent-status would write to target after merging rendered into it.
func computeMerged(target string, rendered []byte) ([]byte, []byte, error) {
	existing, err := os.ReadFile(target)
	if err != nil {
		return nil, nil, err
	}
	var base map[string]any
	if err := json.Unmarshal(existing, &base); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", target, err)
	}
	var add map[string]any
	if err := json.Unmarshal(rendered, &add); err != nil {
		return nil, nil, fmt.Errorf("parse rendered hooks: %w", err)
	}
	merged := mergeHooks(base, add)
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return existing, out, nil
}

// canonicalizeJSON round-trips raw through map[string]any so it matches
// the exact byte layout mergeHooksFile would later write. This keeps
// fresh-install + re-run idempotent (the on-disk format never differs
// from what computeMerged emits).
func canonicalizeJSON(raw []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse rendered hooks: %w", err)
	}
	return json.MarshalIndent(m, "", "  ")
}

func prefixAll(s, prefix string) string {
	if s == "" {
		return s
	}
	var out strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if line == "" {
			continue
		}
		out.WriteString(prefix)
		out.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func installScript(dest string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return writeFile(dest, contents, 0o755)
}

func writeFile(path string, contents []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(contents); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// mergeHooksFile merges rendered hooks into the existing target JSON,
// backing up the original first. Returns the backup path.
func mergeHooksFile(target string, rendered []byte) (string, error) {
	_, merged, err := computeMerged(target, rendered)
	if err != nil {
		return "", err
	}
	backup, err := backupFile(target)
	if err != nil {
		return "", err
	}
	if err := writeFile(target, merged, 0o644); err != nil {
		return "", err
	}
	return backup, nil
}

func backupFile(path string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	base := fmt.Sprintf("%s.bak.%s", path, time.Now().UTC().Format("20060102T150405Z"))
	backup := base
	if _, err := os.Stat(backup); err == nil {
		backup = fmt.Sprintf("%s.%d", base, os.Getpid())
	}
	if err := os.WriteFile(backup, src, 0o644); err != nil {
		return "", err
	}
	return backup, nil
}

// staleScriptMarker is the substring used to recognise prior agent-status
// hook entries during merges. Any hook entry whose command contains this
// marker is dropped from the base before the new entries are merged in,
// so re-running bootstrap (after the script's location or path scheme
// changes) replaces stale entries instead of leaving them alongside the
// new ones.
const staleScriptMarker = "post-agent-status.sh"

// mergeHooks merges add into base. Top-level keys from base are preserved.
// The "hooks" key is handled specially: any pre-existing entries that look
// like prior agent-status hooks are dropped, then for each event name
// entries from base and add are concatenated and de-duplicated by
// canonical JSON. Other top-level keys in add overwrite their
// counterparts in base.
func mergeHooks(base, add map[string]any) map[string]any {
	base = stripStaleHookEntries(base, staleScriptMarker)
	if base == nil {
		base = map[string]any{}
	}
	if add == nil {
		add = map[string]any{}
	}
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range add {
		if k == "hooks" {
			continue
		}
		out[k] = v
	}

	baseHooks := mapOrEmpty(base["hooks"])
	addHooks := mapOrEmpty(add["hooks"])
	if len(baseHooks) == 0 && len(addHooks) == 0 {
		return out
	}

	merged := map[string]any{}
	keys := map[string]struct{}{}
	for k := range baseHooks {
		keys[k] = struct{}{}
	}
	for k := range addHooks {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, k := range ordered {
		combined := append([]any{}, listOrEmpty(baseHooks[k])...)
		combined = append(combined, listOrEmpty(addHooks[k])...)
		merged[k] = dedupeByJSON(combined)
	}
	out["hooks"] = merged
	return out
}

// stripStaleHookEntries returns a shallow copy of m with any hook entry
// whose embedded command contains marker removed. An empty event list is
// dropped; an empty hooks map is dropped entirely so the JSON marshals
// cleanly.
func stripStaleHookEntries(m map[string]any, marker string) map[string]any {
	if m == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	hooks := mapOrEmpty(m["hooks"])
	if len(hooks) == 0 {
		return out
	}
	cleaned := map[string]any{}
	for event, raw := range hooks {
		entries := listOrEmpty(raw)
		kept := make([]any, 0, len(entries))
		for _, entry := range entries {
			if !entryMatchesMarker(entry, marker) {
				kept = append(kept, entry)
			}
		}
		if len(kept) > 0 {
			cleaned[event] = kept
		}
	}
	if len(cleaned) == 0 {
		delete(out, "hooks")
	} else {
		out["hooks"] = cleaned
	}
	return out
}

func entryMatchesMarker(entry any, marker string) bool {
	em, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	for _, h := range listOrEmpty(em["hooks"]) {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, ok := hm["command"].(string)
		if !ok {
			continue
		}
		if strings.Contains(cmd, marker) {
			return true
		}
	}
	return false
}

func mapOrEmpty(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func listOrEmpty(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func dedupeByJSON(entries []any) []any {
	seen := map[string]bool{}
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			out = append(out, e)
			continue
		}
		key := string(b)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func resolveBootstrapDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "agent-status"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "agent-status"), nil
}

func resolveClaudeDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func resolveCodexDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}
