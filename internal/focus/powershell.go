package focus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	psPath, err := findPowerShell()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, psPath, "-NoProfile", "-NonInteractive", "-Command", script)
	return cmd.CombinedOutput()
}

// processNameRE keeps interpolated PowerShell process names inert.
var processNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)

func appActivate(ctx context.Context, processName string) error {
	if !processNameRE.MatchString(processName) {
		return fmt.Errorf("appActivate: refusing to dispatch on unsafe process name %q", processName)
	}
	script := fmt.Sprintf(appActivateScript, processName)
	out, err := runPowerShell(ctx, script)
	if err != nil {
		// Empty exit 1 is the script's "no matching process" signal.
		if _, ok := err.(*exec.ExitError); ok && len(strings.TrimSpace(string(out))) == 0 {
			return ErrWindowNotFound
		}
		return fmt.Errorf("AppActivate %s: %v: %s", processName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

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
