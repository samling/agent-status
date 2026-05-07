package cli

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/focus"
	"github.com/samling/agent-status/internal/notify"
	"github.com/samling/agent-status/internal/server"
	"github.com/samling/agent-status/internal/state"
)

// focusFirstWaiting returns a notification activation callback. The
// notify watcher runs in-process with the collector, so this picks
// the freshest waiting session straight from the store, scans for
// its live PID, and calls focus.PID directly. No HTTP indirection —
// the activation handler and the window-owning desktop are always
// the same machine.
func focusFirstWaiting(s *state.Store) func(context.Context) {
	return func(ctx context.Context) {
		var sessionID string
		for _, sess := range s.Sessions() {
			if sess.Status == "waiting" {
				sessionID = sess.SessionID
				break
			}
		}
		if sessionID == "" {
			slog.DebugContext(ctx, "notify: no waiting session at click time")
			return
		}
		meta, err := discovery.LiveSessionMeta()
		if err != nil {
			slog.WarnContext(ctx, "notify: live meta lookup failed", "err", err)
			return
		}
		sm, ok := meta[sessionID]
		if !ok {
			slog.WarnContext(ctx, "notify: session not in live meta",
				"session", state.ShortID(sessionID))
			return
		}
		if sm.PID <= 0 {
			slog.WarnContext(ctx, "notify: session has no live PID",
				"session", state.ShortID(sessionID))
			return
		}
		msg, err := focus.PID(sm.PID)
		if err != nil {
			slog.WarnContext(ctx, "notify: focus failed",
				"session", state.ShortID(sessionID), "pid", sm.PID, "err", err)
			return
		}
		slog.InfoContext(ctx, "notify: activation handled",
			"session", state.ShortID(sessionID), "pid", sm.PID, "msg", msg)
	}
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the HTTP collector",
	RunE:  runServer,
}

func init() {
	serverCmd.Flags().String("addr", "127.0.0.1", "listen address")
	serverCmd.Flags().String("port", "7878", "listen port")

	serverCmd.Flags().Bool("notify", false, "send a desktop notification when sessions enter the waiting state")
	serverCmd.Flags().Duration("notify-initial-delay", 5*time.Second, "delay between the first 0->1 waiting transition and the first notification")
	serverCmd.Flags().Duration("notify-repeat", 5*time.Minute, "interval between subsequent notifications while sessions remain waiting (0 disables repeats)")
	serverCmd.Flags().String("notify-title", "agent-status", "Go template for the notification title")
	serverCmd.Flags().String("notify-body", "{{.Waiting}} session(s) waiting for input", "Go template for the notification body; see internal/notify TemplateData for available fields")
	serverCmd.Flags().Bool("notify-activation", false, "attach a focus action button; clicking it focuses the first waiting session")
	serverCmd.Flags().String("notify-activation-label", "Focus", "label for the activation button when --notify-activation is set")

	bindings := map[string]string{
		"server.addr":                     "addr",
		"server.port":                     "port",
		"server.notify.enabled":           "notify",
		"server.notify.initial-delay":     "notify-initial-delay",
		"server.notify.repeat":            "notify-repeat",
		"server.notify.title":             "notify-title",
		"server.notify.body":              "notify-body",
		"server.notify.activation.enabled": "notify-activation",
		"server.notify.activation.label":   "notify-activation-label",
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
		}
		if viper.GetBool("server.notify.activation.enabled") {
			label := viper.GetString("server.notify.activation.label")
			cfg.Activation = &notify.Activation{
				Label:      label,
				OnActivate: focusFirstWaiting(s),
			}
		}
		w, err := notify.NewWatcher(cfg, s)
		if err != nil {
			slog.WarnContext(ctx, "notify: disabled", "err", err)
		} else {
			activation := "off"
			if cfg.Activation != nil {
				activation = "on (" + cfg.Activation.Label + ")"
			}
			slog.InfoContext(ctx, "notify: enabled",
				"backend", w.Backend().Name(),
				"initial_delay", cfg.InitialDelay,
				"repeat", cfg.RepeatInterval,
				"activation", activation)
			go w.Run(ctx)
		}
	}

	slog.InfoContext(ctx, "collector listening", "addr", addr, "state", statePath)
	return http.ListenAndServe(addr, server.Handler(s))
}

