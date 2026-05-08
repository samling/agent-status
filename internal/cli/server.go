package cli

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/notify"
	"github.com/samling/agent-status/internal/server"
	"github.com/samling/agent-status/internal/state"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the HTTP collector",
	RunE:  runServer,
}

// focusOnActivate handles a notification action click by exec'ing the focus
// subcommand against this same binary. Going through the subcommand keeps
// internal/focus out of the daemon's import graph.
func focusOnActivate(ctx context.Context, sessionID string) {
	exe, err := os.Executable()
	if err != nil {
		slog.WarnContext(ctx, "notify activation: os.Executable failed",
			"session", state.ShortID(sessionID), "err", err)
		return
	}
	out, err := exec.CommandContext(ctx, exe, "focus", sessionID).CombinedOutput()
	if err != nil {
		slog.WarnContext(ctx, "notify activation: focus subcommand failed",
			"session", state.ShortID(sessionID),
			"err", err,
			"out", strings.TrimSpace(string(out)))
		return
	}
	slog.InfoContext(ctx, "notify activation: focus dispatched",
		"session", state.ShortID(sessionID),
		"msg", strings.TrimSpace(string(out)))
}

func init() {
	serverCmd.Flags().String("addr", "127.0.0.1", "listen address")
	serverCmd.Flags().String("port", "7878", "listen port")

	serverCmd.Flags().Bool("notify", false, "send a desktop notification when a session enters the waiting state")
	serverCmd.Flags().Duration("notify-initial-delay", 5*time.Second, "delay between a session entering waiting and its first notification")
	serverCmd.Flags().Duration("notify-repeat", 5*time.Minute, "repeat notification interval in seconds for waiting sessions (0 to disable)")
	serverCmd.Flags().String("notify-title", "agent-status", "Go template for the notification title")
	serverCmd.Flags().String("notify-body", "{{.Session.Agent}} session waiting for input", "Go template for the notification body")
	serverCmd.Flags().String("notify-action-label", "Focus", "label for the focus action button on each notification")

	bindings := map[string]string{
		"server.addr":                 "addr",
		"server.port":                 "port",
		"server.notify.enabled":       "notify",
		"server.notify.initial-delay": "notify-initial-delay",
		"server.notify.repeat":        "notify-repeat",
		"server.notify.title":         "notify-title",
		"server.notify.body":          "notify-body",
		"server.notify.action-label":  "notify-action-label",
	}
	for key, flag := range bindings {
		_ = viper.BindPFlag(key, serverCmd.Flags().Lookup(flag))
	}
}

func runServer(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	statePath := viper.GetString("state")
	addr := ServerEndpoint()

	s, err := state.Open(statePath)
	if err != nil {
		return err
	}

	go func() {
		if err := discovery.Watch(ctx, s); err != nil {
			slog.ErrorContext(ctx, "discovery: watcher exited with error", "err", err)
		}
	}()

	if viper.GetBool("server.notify.enabled") {
		cfg := notify.Config{
			InitialDelay:   viper.GetDuration("server.notify.initial-delay"),
			RepeatInterval: viper.GetDuration("server.notify.repeat"),
			TitleTemplate:  viper.GetString("server.notify.title"),
			BodyTemplate:   viper.GetString("server.notify.body"),
			Activation: &notify.Activation{
				Label:      viper.GetString("server.notify.action-label"),
				OnActivate: focusOnActivate,
			},
		}
		w, err := notify.NewWatcher(cfg, s)
		if err != nil {
			slog.WarnContext(ctx, "notify: disabled", "err", err)
		} else {
			slog.InfoContext(ctx, "notify: enabled",
				"backend", w.Backend().Name(),
				"initial_delay", cfg.InitialDelay,
				"repeat", cfg.RepeatInterval,
				"action_label", cfg.Activation.Label)
			go w.Run(ctx)
		}
	}

	slog.InfoContext(ctx, "collector listening", "addr", addr, "state", statePath)
	return http.ListenAndServe(addr, server.Handler(s))
}
