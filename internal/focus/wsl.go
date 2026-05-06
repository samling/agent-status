package focus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// wsl is the Focuser for sessions running inside WSL, where the
// windowing system lives on the Windows host. WSL pids and Windows
// window pids are in different kernel namespaces, so the usual
// ancestry → compositor IPC chain can't reach across. Instead Focus
// inspects /proc/<pid>/environ for each ancestor, identifies which
// Windows-side host is running the session (VS Code Remote-WSL today;
// Windows Terminal etc. as they land), and dispatches to a
// host-specific focus action.
type wsl struct{}

func (*wsl) Name() string { return "wsl" }

func (*wsl) Focus(ctx context.Context, target Target) error {
	for _, pid := range target.Ancestors {
		env, err := readEnviron(pid)
		if err != nil {
			continue
		}
		if isVSCodeTerminal(env) {
			return focusViaVSCode(ctx, pid, env)
		}
		// Future host-strategies plug in here (Windows Terminal:
		// WT_SESSION → wt.exe AppActivate, etc.).
	}
	return ErrWindowNotFound
}

// isVSCodeTerminal returns true when env looks like a VS Code
// terminal environment — either the Remote-WSL bridge or a native
// Linux VS Code terminal (the same env vars work in both cases).
func isVSCodeTerminal(env map[string]string) bool {
	return env["TERM_PROGRAM"] == "vscode" || env["VSCODE_IPC_HOOK_CLI"] != ""
}

// focusViaVSCode invokes the WSL-side `code` shim with the matching
// process's cwd. The shim forwards to Windows VS Code, which focuses
// the existing window for that folder (or opens it in the most recent
// window if the folder isn't already loaded).
//
// targetEnv is the matching process's parsed /proc/<pid>/environ. We
// forward VSCODE_* and WSL_* keys into the shim's environment because
// the shim itself gates on VSCODE_IPC_HOOK_CLI (the socket back to
// the VS Code window) plus the WSL identification vars; without them
// it refuses with "Command is only available in WSL or inside a
// Visual Studio Code terminal", since our process (especially when
// launched via systemd or another non-VS-Code parent) doesn't carry
// them itself.
func focusViaVSCode(ctx context.Context, pid int, targetEnv map[string]string) error {
	bin, err := vscodeCLI()
	if err != nil {
		return err
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil || cwd == "" {
		return fmt.Errorf("read /proc/%d/cwd: %v", pid, err)
	}
	// -r / --reuse-window forces the shim to forward the open to the
	// existing window the IPC handle points at, instead of letting
	// VS Code's window-picking heuristic decide (which often spawns
	// a new window even when the folder is already open).
	cmd := exec.CommandContext(ctx, bin, "-r", cwd)
	cmd.Env = forwardVSCodeEnv(targetEnv)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s -r %s: %v: %s", bin, cwd, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// forwardVSCodeEnv returns os.Environ() augmented with the VSCODE_*
// and WSL_* entries from target. Target wins on conflict so a stale
// VSCODE_IPC_HOOK_CLI inherited by agent-status doesn't override the
// session's fresh socket path.
func forwardVSCodeEnv(target map[string]string) []string {
	keep := []string{
		"VSCODE_IPC_HOOK_CLI",
		"VSCODE_PID",
		"VSCODE_GIT_IPC_HANDLE",
		"VSCODE_INJECTION",
		"WSL_DISTRO_NAME",
		"WSL_INTEROP",
		"WSLENV",
	}
	keepSet := make(map[string]bool, len(keep))
	for _, k := range keep {
		keepSet[k] = true
	}
	out := make([]string, 0, len(os.Environ())+len(keep))
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || !keepSet[k] {
			out = append(out, kv)
		}
	}
	for _, k := range keep {
		if v, ok := target[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// vscodeCLI locates the VS Code CLI shim usable from WSL. Looks at
// PATH first (the case when this process was launched by a VS Code
// integrated terminal), then falls back to the per-server shim at
// ~/.vscode-server/bin/<commit>/bin/remote-cli/code which exists
// whenever the Remote-WSL server is installed in this distro,
// regardless of how the caller's shell PATH is configured. When
// multiple server installs are present (after a VS Code update,
// before old commits are reaped) we pick the most recently modified.
func vscodeCLI() (string, error) {
	if path, err := exec.LookPath("code"); err == nil {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate VS Code CLI: %w", err)
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".vscode-server", "bin", "*", "bin", "remote-cli", "code"))
	if len(matches) == 0 {
		return "", fmt.Errorf("VS Code CLI not found: `code` is not on PATH and no shim exists under ~/.vscode-server/bin/*/bin/remote-cli/. Open this WSL session from VS Code (Remote-WSL) at least once to install the server, or run agent-status from VS Code's integrated terminal")
	}
	sort.Slice(matches, func(i, j int) bool {
		fi, _ := os.Stat(matches[i])
		fj, _ := os.Stat(matches[j])
		if fi == nil || fj == nil {
			return false
		}
		return fi.ModTime().After(fj.ModTime())
	})
	return matches[0], nil
}

// readEnviron parses /proc/<pid>/environ into a map. The file is a
// NUL-separated list of KEY=VALUE entries. Returns an error only when
// the file can't be read; malformed entries are skipped.
func readEnviron(pid int) (map[string]string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for kv := range strings.SplitSeq(string(b), "\x00") {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out, nil
}
