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

func (*wsl) Focus(ctx context.Context, target Target) error {
	for _, pid := range target.Ancestors {
		env, err := readEnviron(pid)
		if err != nil {
			continue
		}
		if isVSCodeTerminal(env) {
			return focusViaVSCode(ctx, vscodeFlavor(env))
		}
		// Future host-strategies plug in here (Windows Terminal:
		// WT_SESSION → wt.exe AppActivate, etc.).
	}
	return ErrWindowNotFound
}

// isVSCodeTerminal returns true when env looks like a VS Code-family
// terminal — VS Code itself or any of its forks (Cursor, Windsurf,
// etc.) all set TERM_PROGRAM=vscode and the VSCODE_* IPC vars for
// extension-API compatibility.
func isVSCodeTerminal(env map[string]string) bool {
	return env["TERM_PROGRAM"] == "vscode" || env["VSCODE_IPC_HOOK_CLI"] != ""
}

// vscodeFlavor returns the Windows-side process name to AppActivate
// for the running VS Code-family editor. VS Code forks all set the
// same TERM_PROGRAM=vscode flag, so we have to look at the install
// paths embedded in the IPC env vars to tell them apart. Defaults to
// "Code" (vanilla VS Code) on no match.
func vscodeFlavor(env map[string]string) string {
	for _, key := range []string{
		"VSCODE_GIT_ASKPASS_NODE",
		"VSCODE_GIT_ASKPASS_MAIN",
		"VSCODE_IPC_HOOK_CLI",
	} {
		v := strings.ToLower(env[key])
		if v == "" {
			continue
		}
		switch {
		case strings.Contains(v, "cursor"):
			return "Cursor"
		case strings.Contains(v, "windsurf"):
			return "Windsurf"
		}
	}
	return "Code"
}

// focusViaVSCode brings the running VS Code-family window to the
// foreground by shelling out to powershell.exe and calling AppActivate
// against the first process matching flavor (Code, Cursor, Windsurf,
// ...) that owns a top-level window.
//
// We deliberately don't invoke the WSL-side `code` shim here. That's
// an "open" verb: it loads a path into a window and (with -r) replaces
// whatever workspace was there. We want a pure focus action against
// the already-open window, which the COM WScript.Shell.AppActivate
// API provides.
//
// For users with one editor instance open this is exact. With
// multiple windows of the same flavor the COM enumerator picks the
// first; precise targeting (filter by workspace title, or follow the
// session's VSCODE_PID up the Windows process tree) is a follow-up.
func focusViaVSCode(ctx context.Context, flavor string) error {
	psPath, err := findPowerShell()
	if err != nil {
		return err
	}
	script := fmt.Sprintf(appActivateScript, flavor)
	cmd := exec.CommandContext(ctx, psPath, "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("powershell AppActivate %s.exe: %v: %s", flavor, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// findPowerShell locates a PowerShell executable usable from WSL.
// Tries PATH first (the common case when WSL appends Windows paths),
// then falls back to the canonical Windows-side install paths via the
// /mnt/c mount. Users with `appendWindowsPath = false` in /etc/wsl.conf
// hit the fallback; users with a non-default WSL drvfs prefix get a
// clear error.
func findPowerShell() (string, error) {
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		return p, nil
	}
	candidates := []string{
		"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		"/mnt/c/Program Files/PowerShell/7/pwsh.exe",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("powershell.exe not found: not on PATH and not at /mnt/c/Windows/System32/WindowsPowerShell/v1.0/. Check WSL interop is enabled (appendWindowsPath in /etc/wsl.conf) or that /mnt/c is the WSL drvfs root")
}

// appActivateScript finds the first process matching the format-string
// %s (Code / Cursor / Windsurf / ...) that owns a main window and
// brings it to the foreground via the COM WScript.Shell AppActivate
// API. Exits non-zero when no matching process is running, so the
// caller surfaces a clear error in the TUI footer.
const appActivateScript = `
$ws = New-Object -ComObject WScript.Shell
$proc = Get-Process -Name '%s' -ErrorAction SilentlyContinue |
    Where-Object MainWindowHandle -ne 0 |
    Select-Object -First 1
if ($proc) {
    $null = $ws.AppActivate($proc.Id)
    exit 0
}
exit 1
`

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
