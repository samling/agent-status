package codex

import (
	"encoding/json"
	"io"
	"os"

	"github.com/samling/agent-status/internal/discovery/source"
)

// detectWaitingFor returns a short label like "approve shell" when the rollout
// JSONL at path ends with an unpaired function_call whose arguments declare a
// sandbox escalation request. Returns "" when no approval is pending.
//
// Codex doesn't write a transient "current state" field, so we infer the
// waiting condition by pairing function_call records with function_call_output
// records by call_id. Reading only the file tail keeps this cheap on the 2s
// scan cadence even for long-running sessions.
func detectWaitingFor(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	const tailBytes = 64 * 1024
	var offset int64
	if fi.Size() > tailBytes {
		offset = fi.Size() - tailBytes
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}

	type callInfo struct {
		name        string
		sandboxPerm string
	}
	calls := map[string]callInfo{}
	outputs := map[string]struct{}{}
	var lastUnpaired string

	skipFirst := offset > 0
	_ = source.ScanJSONL(f, func(buf []byte) bool {
		if skipFirst {
			skipFirst = false
			return true
		}
		var line transcriptLine
		if err := json.Unmarshal(buf, &line); err != nil {
			return true
		}
		if line.Type != "response_item" {
			return true
		}
		var p struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
		}
		if err := json.Unmarshal(line.Payload, &p); err != nil {
			return true
		}
		switch p.Type {
		case "function_call":
			if p.CallID == "" {
				return true
			}
			var args struct {
				SandboxPermissions string `json:"sandbox_permissions"`
			}
			_ = json.Unmarshal([]byte(p.Arguments), &args)
			calls[p.CallID] = callInfo{name: p.Name, sandboxPerm: args.SandboxPermissions}
			lastUnpaired = p.CallID
		case "function_call_output":
			if p.CallID == "" {
				return true
			}
			outputs[p.CallID] = struct{}{}
			if lastUnpaired == p.CallID {
				lastUnpaired = ""
			}
		}
		return true
	})

	if lastUnpaired == "" {
		return ""
	}
	if _, paired := outputs[lastUnpaired]; paired {
		return ""
	}
	c := calls[lastUnpaired]
	if c.sandboxPerm != "require_escalated" {
		return ""
	}
	return "approve " + shortFunctionName(c.name)
}

func shortFunctionName(name string) string {
	switch name {
	case "shell_command":
		return "shell"
	case "apply_patch":
		return "patch"
	case "":
		return "tool"
	default:
		return name
	}
}
