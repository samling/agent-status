package bootstrap

import (
	"encoding/json"
	"reflect"
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
