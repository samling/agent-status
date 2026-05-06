package cli

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/notify"
	"github.com/samling/agent-status/internal/server"
	"github.com/samling/agent-status/internal/state"
)

// focusViaAPI returns a callback that POSTs to the collector's own
// /focus endpoint via the shared server.Focus client. Routing through
// HTTP — even though the call lands in the same process — keeps a
// single source of truth for "focus the right session right now":
// the server picks the first waiting session at click time, so
// activations always reflect the latest state instead of whatever
// was waiting when the notification was rendered.
func focusViaAPI(addr string) func(context.Context) {
	return func(ctx context.Context) {
		resp, err := server.Focus(ctx, addr, "")
		if err != nil {
			log.Printf("notify: focus call: %v", err)
			return
		}
		log.Printf("notify: activation -> %s", resp.Message)
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
	serverCmd.Flags().Bool("notify-activation", false, "attach a focus action button; clicking it POSTs to the local /focus endpoint")
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
	statePath := viper.GetString("state")
	addr := ServerEndpoint()

	s, err := state.Open(statePath)
	if err != nil {
		return err
	}

	go func() {
		if err := discovery.Watch(cmd.Context(), s); err != nil {
			log.Printf("watcher: %v", err)
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
				OnActivate: focusViaAPI(addr),
			}
		}
		w, err := notify.NewWatcher(cfg, s)
		if err != nil {
			log.Printf("notify: disabled: %v", err)
		} else {
			activation := "off"
			if cfg.Activation != nil {
				activation = "on (" + cfg.Activation.Label + ")"
			}
			log.Printf("notify: enabled via %s (initial=%s repeat=%s activation=%s)", w.Backend().Name(), cfg.InitialDelay, cfg.RepeatInterval, activation)
			go w.Run(cmd.Context())
		}
	}

	log.Printf("collector listening on %s (state: %s)", addr, statePath)
	return http.ListenAndServe(addr, server.Handler(s))
}

