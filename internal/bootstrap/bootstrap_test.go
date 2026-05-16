package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestKnownAgentsIncludesOpencode(t *testing.T) {
	want := []string{"claude", "codex", "opencode"}
	if got := KnownAgents(); !reflect.DeepEqual(got, want) {
		t.Fatalf("KnownAgents() = %#v, want %#v", got, want)
	}
}

func TestMergeOpencodePluginAddsAndDedupesAgentStatusPlugin(t *testing.T) {
	base := mustJSON(t, `{"plugin":["existing","/old/agent-status/opencode-agent-status-plugin.js"]}`)
	pluginPath := "/home/u/.config/agent-status/opencode-agent-status-plugin.js"

	got := mergeOpencodePlugin(base, pluginPath)
	plugins, ok := got["plugin"].([]any)
	if !ok {
		t.Fatalf("plugin = %#v, want array", got["plugin"])
	}
	want := []any{"existing", pluginPath}
	if !reflect.DeepEqual(plugins, want) {
		t.Fatalf("plugin = %#v, want %#v", plugins, want)
	}
}

func TestComputeMergedOpencodeConfigAcceptsJSONCCommentsAndTrailingCommas(t *testing.T) {
	target := filepath.Join(t.TempDir(), "opencode.jsonc")
	if err := os.WriteFile(target, []byte(`{
		// Keep this plugin.
		"plugin": [
			"existing//not-a-comment/*also-not*/",
		],
		/* Keep unrelated string content unchanged. */
		"note": "text, // not comment, /* not block */",
	}`), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	_, merged, err := computeMergedOpencodeConfig(target, "/home/u/.config/agent-status/opencode-agent-status-plugin.js")
	if err != nil {
		t.Fatalf("computeMergedOpencodeConfig: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	plugins := got["plugin"].([]any)
	wantPlugins := []any{"existing//not-a-comment/*also-not*/", "/home/u/.config/agent-status/opencode-agent-status-plugin.js"}
	if !reflect.DeepEqual(plugins, wantPlugins) {
		t.Fatalf("plugin = %#v, want %#v", plugins, wantPlugins)
	}
	if got["note"] != "text, // not comment, /* not block */" {
		t.Fatalf("note = %#v", got["note"])
	}
}

func TestBuildPlanIncludesOpencode(t *testing.T) {
	dir := t.TempDir()
	plan, err := BuildPlan(Options{
		Agents:       []string{"opencode"},
		OpencodeDir:  dir,
		BootstrapDir: filepath.Join(dir, "agent-status"),
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Agents) != 1 {
		t.Fatalf("len(plan.Agents) = %d, want 1", len(plan.Agents))
	}
	if got := plan.Agents[0].Agent.ID; got != "opencode" {
		t.Fatalf("agent ID = %q, want opencode", got)
	}
}

func TestOpencodePluginNormalizesBareEndpointAsset(t *testing.T) {
	plugin, err := assets.ReadFile("assets/opencode-agent-status-plugin.js")
	if err != nil {
		t.Fatalf("read plugin asset: %v", err)
	}
	contents := string(plugin)
	for _, want := range []string{
		"function normalizeEndpoint(value)",
		"return `http://${trimmed}`",
		"new URL(\"/hook\", normalizeEndpoint(endpoint))",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("plugin asset missing %q", want)
		}
	}
}

func TestOpencodePluginSendsTopLevelHookEnvelopeFields(t *testing.T) {
	plugin, err := assets.ReadFile("assets/opencode-agent-status-plugin.js")
	if err != nil {
		t.Fatalf("read plugin asset: %v", err)
	}
	contents := string(plugin)
	for _, want := range []string{
		"function sessionID(input)",
		"input?.sessionID",
		"input?.event?.properties?.sessionID",
		"input?.event?.properties?.info?.id",
		"input?.event?.properties?.info?.sessionID",
		"input?.event?.sessionID",
		"input?.message?.sessionID",
		"function turnID(input)",
		"input?.messageID",
		"input?.callID",
		"function toolName(input)",
		"input?.tool",
		"input?.event?.properties?.tool",
		"input?.event?.properties?.info?.tool",
		"session_id: session",
		"hook_event_name: eventName",
		"turn_id: turnID(event)",
		"tool_name: toolName(event)",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("plugin asset missing %q", want)
		}
	}
}

func TestOpencodePluginClassifiesMessageRoleAsset(t *testing.T) {
	plugin, err := assets.ReadFile("assets/opencode-agent-status-plugin.js")
	if err != nil {
		t.Fatalf("read plugin asset: %v", err)
	}
	contents := string(plugin)
	for _, want := range []string{
		"function messageRole(input)",
		"function messageComplete(input)",
		"if (name === \"chat.message\" || name === \"message.updated\")",
		"if (role === \"assistant\" && messageComplete(event))",
		"return \"Stop\"",
		"if (role === \"user\")",
		"return \"UserPromptSubmit\"",
		"return name === \"chat.message\" ? \"UserPromptSubmit\" : \"\"",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("plugin asset missing %q", want)
		}
	}
}

func TestOpencodePluginReturnsHooksObjectAsset(t *testing.T) {
	plugin, err := assets.ReadFile("assets/opencode-agent-status-plugin.js")
	if err != nil {
		t.Fatalf("read plugin asset: %v", err)
	}
	contents := string(plugin)
	for _, want := range []string{
		"export default async function agentStatusPlugin()",
		"return {",
		"async event(input)",
		"\"chat.message\": async function",
		"\"tool.execute.before\": async function",
		"\"tool.execute.after\": async function",
		"\"permission.ask\": async function",
		"session.created",
		"message.updated",
		"name === \"SessionStart\"",
		"return \"Stop\"",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("plugin asset missing %q", want)
		}
	}
	for _, dontWant := range []string{"session.updated", "name?.startsWith(\"session.\")", "\"session.update\"", "\"message.update\"", "app.on("} {
		if strings.Contains(contents, dontWant) {
			t.Fatalf("plugin asset should not contain %q", dontWant)
		}
	}
}

func TestOpencodePluginGuardsSessionAndUsesTimeoutAsset(t *testing.T) {
	plugin, err := assets.ReadFile("assets/opencode-agent-status-plugin.js")
	if err != nil {
		t.Fatalf("read plugin asset: %v", err)
	}
	contents := string(plugin)
	for _, want := range []string{
		"const session = sessionID(event)",
		"const eventName = hookName(event)",
		"if (!session || !eventName)",
		"return",
		"session_id: session",
		"hook_event_name: eventName",
		"signal: AbortSignal.timeout(1000)",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("plugin asset missing %q", want)
		}
	}
}

func TestMergeHooks_PreservesUnrelatedTopLevelKeys(t *testing.T) {
	base := mustJSON(t, `{"theme":"dark","permissions":{"allow":["bash"]}}`)
	add := mustJSON(t, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"x"}]}]}}`)
	got := mergeHooks(base, add)
	if got["theme"] != "dark" {
		t.Errorf("theme dropped: %v", got["theme"])
	}
	if !reflect.DeepEqual(got["permissions"], base["permissions"]) {
		t.Errorf("permissions mutated: %v", got["permissions"])
	}
	if _, ok := got["hooks"]; !ok {
		t.Errorf("hooks not added")
	}
}

func TestMergeHooks_ConcatsAndDedupesPerEvent(t *testing.T) {
	base := mustJSON(t, `{"hooks":{
		"Stop":[{"hooks":[{"type":"command","command":"keep"}]}],
		"SessionStart":[{"hooks":[{"type":"command","command":"existing"}]}]
	}}`)
	add := mustJSON(t, `{"hooks":{
		"Stop":[
			{"hooks":[{"type":"command","command":"keep"}]},
			{"hooks":[{"type":"command","command":"new"}]}
		],
		"UserPromptSubmit":[{"hooks":[{"type":"command","command":"submit"}]}]
	}}`)
	got := mergeHooks(base, add)
	hooks := got["hooks"].(map[string]any)

	stop := hooks["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop dedup failed: %#v", stop)
	}
	session := hooks["SessionStart"].([]any)
	if len(session) != 1 {
		t.Errorf("SessionStart should be preserved untouched, got %#v", session)
	}
	submit := hooks["UserPromptSubmit"].([]any)
	if len(submit) != 1 {
		t.Errorf("UserPromptSubmit should be added, got %#v", submit)
	}
}

func TestMergeHooks_IdempotentReRun(t *testing.T) {
	add := mustJSON(t, `{"hooks":{
		"Stop":[{"hooks":[{"type":"command","command":"once"}]}],
		"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"pre"}]}]
	}}`)
	first := mergeHooks(map[string]any{}, add)
	second := mergeHooks(first, add)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second merge changed result:\nfirst:  %v\nsecond: %v", first, second)
	}
}

func TestMergeHooks_NilSafe(t *testing.T) {
	got := mergeHooks(nil, nil)
	if len(got) != 0 {
		t.Errorf("nil merge produced %v", got)
	}
}

func TestMergeHooks_StripsStaleAgentStatusEntries(t *testing.T) {
	// Simulate a base file from an earlier bootstrap that wrote per-agent
	// script copies. The new merge should replace those with the new
	// shared-script entry rather than keeping both.
	base := mustJSON(t, `{"theme":"dark","hooks":{
		"Stop":[
			{"hooks":[{"type":"command","command":"/home/u/.claude/scripts/post-agent-status.sh --agent claude-code"}]},
			{"hooks":[{"type":"command","command":"user-custom-hook"}]}
		]
	}}`)
	add := mustJSON(t, `{"hooks":{
		"Stop":[{"hooks":[{"type":"command","command":"/home/u/.config/agent-status/post-agent-status.sh --agent claude-code"}]}]
	}}`)
	got := mergeHooks(base, add)

	if got["theme"] != "dark" {
		t.Errorf("unrelated top-level key lost: %v", got["theme"])
	}
	stop := got["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("expected user-custom + new shared entry, got %#v", stop)
	}
	// First kept entry should be the user's custom hook, then the new one.
	first := stop[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
	second := stop[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
	if first != "user-custom-hook" {
		t.Errorf("expected user-custom-hook first, got %q", first)
	}
	if !strings.Contains(second, "/.config/agent-status/post-agent-status.sh") {
		t.Errorf("expected new shared-script entry second, got %q", second)
	}
}

func TestMergeHooks_DropsEmptyHooksKeyWhenEverythingStripped(t *testing.T) {
	base := mustJSON(t, `{"theme":"dark","hooks":{
		"Stop":[{"hooks":[{"type":"command","command":"/old/post-agent-status.sh --agent claude-code"}]}]
	}}`)
	got := mergeHooks(base, map[string]any{})
	if _, present := got["hooks"]; present {
		t.Errorf("empty hooks map should be dropped, got %v", got)
	}
	if got["theme"] != "dark" {
		t.Errorf("theme lost: %v", got["theme"])
	}
}
