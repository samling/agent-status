package focus

import (
	"context"
	"strings"
)

// wsl is the Focuser for sessions running inside WSL, where the
// windowing system lives on the Windows host. WSL pids and Windows
// window pids are in different kernel namespaces, so the usual
// ancestry → compositor IPC chain can't reach across. Instead Focus
// inspects /proc/<pid>/environ for each ancestor, identifies which
// Windows-side host is running the session (a VS Code-family editor
// today; Windows Terminal etc. as they land), and dispatches to a
// host-specific focus action via the powershell.go helpers.
type wsl struct{}

func (*wsl) Name() string { return "wsl" }

func (*wsl) Focus(ctx context.Context, target Target) error {
	for _, pid := range target.Ancestors {
		env, err := readEnviron(pid)
		if err != nil {
			continue
		}
		if isVSCodeTerminal(env) {
			return appActivate(ctx, vscodeFlavor(env))
		}
		// Future host-strategies plug in here (Windows Terminal:
		// WT_SESSION → appActivate(ctx, "WindowsTerminal"), etc.).
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
