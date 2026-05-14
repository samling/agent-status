package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanUsesClaudeSessionFileName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeClaudeSessionFile(t, home, `{
		"pid": %d,
		"sessionId": "session-1",
		"cwd": "/tmp/project",
		"startedAt": %d,
		"entrypoint": "cli",
		"name": "Named Claude work"
	}`)

	sessions, scanned, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if scanned != 1 || len(sessions) != 1 {
		t.Fatalf("scanned = %d len = %d, want 1 and 1", scanned, len(sessions))
	}
	if sessions[0].Meta.Name != "Named Claude work" {
		t.Fatalf("Meta.Name = %q, want Named Claude work", sessions[0].Meta.Name)
	}
}

func TestScanUsesClaudeTranscriptCustomTitle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/tmp/project"

	writeClaudeSessionFile(t, home, `{
		"pid": %d,
		"sessionId": "session-1",
		"cwd": "`+cwd+`",
		"startedAt": %d,
		"entrypoint": "cli"
	}`)
	transcriptPath := filepath.Join(home, ".claude", "projects", encodePath(cwd), "session-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"custom-title","customTitle":"Renamed Claude session","sessionId":"session-1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, _, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	if sessions[0].Meta.Name != "Renamed Claude session" {
		t.Fatalf("Meta.Name = %q, want Renamed Claude session", sessions[0].Meta.Name)
	}
}

func TestScanIncludesClaudeSubagentChildren(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/tmp/project"

	writeClaudeSessionFile(t, home, `{
		"pid": %d,
		"sessionId": "session-1",
		"cwd": "`+cwd+`",
		"startedAt": %d,
		"entrypoint": "cli"
	}`)
	childPath := filepath.Join(home, ".claude", "projects", encodePath(cwd), "session-1", "subagents", "agent-child.jsonl")
	if err := os.MkdirAll(filepath.Dir(childPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPath, []byte(`{"type":"user","agentId":"child","sessionId":"session-1","cwd":"`+cwd+`","timestamp":"2026-05-14T10:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, _, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	byID := map[string]sourceMeta{}
	for _, sess := range sessions {
		byID[sess.SessionID] = sourceMeta{
			parent: sess.Meta.ParentSessionID,
			path:   sess.Meta.Path,
			count:  sess.Meta.ChildCount,
		}
	}
	if byID["session-1"].count != 1 {
		t.Fatalf("parent child count = %d, want 1", byID["session-1"].count)
	}
	child := byID["session-1:child"]
	if child.parent != "session-1" {
		t.Fatalf("child parent = %q, want session-1", child.parent)
	}
	if child.path != childPath {
		t.Fatalf("child path = %q, want %q", child.path, childPath)
	}
}

type sourceMeta struct {
	parent string
	path   string
	count  int
}

func writeClaudeSessionFile(t *testing.T, home, format string) {
	t.Helper()
	path := filepath.Join(home, ".claude", "sessions", "session.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UnixMilli()
	data := []byte(fmt.Sprintf(format, os.Getpid(), startedAt))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
