package focus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// runPowerShell executes script under powershell.exe (or pwsh.exe)
// and returns its combined stdout/stderr. The shell is located via
// findPowerShell, so callers don't need to care about whether WSL's
// Windows interop has put it on PATH.
//
// script is treated as fully trusted: it is passed verbatim to
// PowerShell via -Command, and any string interpolation a caller
// performs on its way in is the caller's responsibility to make
// safe. Do not concatenate untrusted user input into a script;
// validate it at the API boundary first (see appActivate for the
// pattern).
func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	psPath, err := findPowerShell()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, psPath, "-NoProfile", "-NonInteractive", "-Command", script)
	return cmd.CombinedOutput()
}

// processNameRE limits processName to alphanumerics starting with a
// letter, capped at 64 chars. Real Windows process names are well
// inside this set ("Code", "Cursor", "WindowsTerminal", "chrome",
// "firefox", "explorer", ...). Anything containing a quote, space,
// or PowerShell metacharacter is rejected so the value can be
// interpolated into appActivateScript without becoming an injection
// sink.
var processNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)

// appActivate brings the first process matching processName (e.g.
// "Code", "Cursor", "WindowsTerminal") that owns a top-level window
// to the Windows foreground via the COM WScript.Shell.AppActivate
// API. Returns ErrWindowNotFound when no matching process is running,
// so callers can fall through to other strategies.
//
// processName is validated against processNameRE before being
// interpolated into the PowerShell script; the function returns an
// error rather than executing if validation fails, so a buggy or
// adversarial caller can't drive arbitrary PowerShell.
//
// Multiple-window case: COM enumeration order picks the "first"
// process. Precise targeting (filter by window title, walk a known
// pid up the Windows process tree) is up to the caller — they can
// either compose a richer script or call this with progressively
// narrower process names.
func appActivate(ctx context.Context, processName string) error {
	if !processNameRE.MatchString(processName) {
		return fmt.Errorf("appActivate: refusing to dispatch on unsafe process name %q", processName)
	}
	script := fmt.Sprintf(appActivateScript, processName)
	out, err := runPowerShell(ctx, script)
	if err != nil {
		// Exit status 1 from the script means "no matching process";
		// that's a soft "window not found" rather than a hard error.
		if _, ok := err.(*exec.ExitError); ok && len(strings.TrimSpace(string(out))) == 0 {
			return ErrWindowNotFound
		}
		return fmt.Errorf("AppActivate %s: %v: %s", processName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// appActivateScript finds the first process matching the format-string
// %s that owns a main window and brings it to the foreground via the
// COM WScript.Shell AppActivate API. Exits non-zero with no output
// when no match exists, so appActivate can map that to
// ErrWindowNotFound.
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
