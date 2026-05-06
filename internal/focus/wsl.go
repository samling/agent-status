package focus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

func (*wsl) Focus(_ context.Context, target Target) error {
	for _, pid := range target.Ancestors {
		env, err := readEnviron(pid)
		if err != nil {
			continue
		}
		if isVSCodeTerminal(env) {
			return focusViaVSCode(pid)
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
func focusViaVSCode(pid int) error {
	if _, err := exec.LookPath("code"); err != nil {
		return fmt.Errorf("`code` not on PATH; install the VS Code Remote-WSL shell helper")
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil || cwd == "" {
		return fmt.Errorf("read /proc/%d/cwd: %v", pid, err)
	}
	out, err := exec.Command("code", cwd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("code %s: %v: %s", cwd, err, strings.TrimSpace(string(out)))
	}
	return nil
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
