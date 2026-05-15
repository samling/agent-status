package cli

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/discovery/source"
	"github.com/samling/agent-status/internal/notify"
	"github.com/samling/agent-status/internal/server"
	"github.com/samling/agent-status/internal/sessionview"
	"github.com/samling/agent-status/internal/state"
)

// discoveryMeta adapts the discovery package's exported helpers to the
// server's MetaProvider interface. Keeping this adapter in cli/server.go
// (rather than in internal/server) avoids importing the discovery backends
// from the bare HTTP layer.
type discoveryMeta struct{}

func (discoveryMeta) LatestMeta() map[string]source.SessionMeta {
	return discovery.LatestMeta()
}

func (discoveryMeta) Transcript(sessionID, agent string, meta source.SessionMeta) (source.TranscriptInfo, error) {
	return discovery.LoadTranscript(sessionID, agent, meta)
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the HTTP collector",
	RunE:  runServer,
}

// endpointFilePath returns the path of the file the forwarder script
// reads to discover the running collector's address. Lives next to the
// state file under $XDG_STATE_HOME/agent-status/. Returns "" if the home
// directory can't be resolved (in which case the script just falls back
// to its compiled-in default).
func endpointFilePath() string {
	dir := defaultStateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "endpoint")
}

// writeEndpointFile atomically writes the collector's "<addr>:<port>" so
// the forwarder script can discover it without parsing config.
func writeEndpointFile(path, addr string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(addr + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
	serverCmd.Flags().Duration("notify-repeat", 5*time.Minute, "repeat notification interval for waiting sessions (0 to disable)")
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
	views := sessionview.Provider{
		Store:     s,
		Meta:      discoveryMeta{},
		NotesPath: state.NotesPath(statePath),
	}

	go s.Run(ctx)

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

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.HandlerWithViews(s, discoveryMeta{}, views),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	if path := endpointFilePath(); path != "" {
		if err := writeEndpointFile(path, addr); err != nil {
			slog.WarnContext(ctx, "endpoint file: write failed (hooks will fall back to default)",
				"path", path, "err", err)
		} else {
			slog.InfoContext(ctx, "endpoint file written", "path", path, "endpoint", addr)
		}
	}

	errCh := make(chan error, 1)
	go func() {
		slog.InfoContext(ctx, "collector listening", "addr", addr, "state", statePath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		slog.InfoContext(ctx, "collector: shutting down", "reason", ctx.Err())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.WarnContext(ctx, "collector: shutdown error", "err", err)
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}
