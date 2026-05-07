package focus

import (
	"context"
	"strings"
)

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
		// Future host strategies plug in here (Windows Terminal, etc.).
	}
	return ErrWindowNotFound
}

func isVSCodeTerminal(env map[string]string) bool {
	return env["TERM_PROGRAM"] == "vscode" || env["VSCODE_IPC_HOOK_CLI"] != ""
}

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
