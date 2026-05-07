package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/cli/ui"
	"github.com/samling/agent-status/internal/logging"
	"github.com/samling/agent-status/internal/version"
)

var rootCmd = &cobra.Command{
	Use:               "agent-status",
	Short:             "Collect and inspect local coding-agent sessions",
	Version:           version.Get(),
	PersistentPreRunE: bootstrap,
}

// configPathFlag overrides the config file location when set on the
// command line. Empty means "look in the default search paths."
var configPathFlag string

// shutdownFn flushes any pending OTel spans on process exit. Set in
// bootstrap, drained in Execute.
var shutdownFn func(context.Context) error

func Execute() error {
	err := rootCmd.Execute()
	if shutdownFn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownFn(ctx)
		shutdownFn = nil
	}
	return err
}

// defaultConfigDir returns $XDG_CONFIG_HOME/agent-status, falling
// back to $HOME/.config/agent-status. Used as the search root for
// both the config file and the default state path.
func defaultConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "agent-status")
}

// defaultStatePath returns the default state.json path inside the
// config dir, or a relative "state.json" when no home dir is
// resolvable. The --state flag and AGENT_STATUS_STATE env var still
// override.
func defaultStatePath() string {
	dir := defaultConfigDir()
	if dir == "" {
		return "state.json"
	}
	return filepath.Join(dir, "state.json")
}

// ServerEndpoint joins server.addr and server.port into a single
// host:port string. This is the canonical address for both the
// collector's listen socket and every client that talks to it (TUI
// connection probe, statusline probe, focus CLI, notify activation
// callback). Consolidating here means the YAML config defines the
// address exactly once instead of repeating it under each consumer.
func ServerEndpoint() string {
	addr := viper.GetString("server.addr")
	port := viper.GetString("server.port")
	if port == "" {
		return addr
	}
	return addr + ":" + port
}

// loadConfig reads the agent-status config file, if any, into the
// viper instance. Best-effort: a missing file is not an error, since
// most users will run on flag defaults alone. Parse errors are real
// errors and abort startup so a typo doesn't silently fall back.
func loadConfig() error {
	if configPathFlag != "" {
		viper.SetConfigFile(configPathFlag)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		if dir := defaultConfigDir(); dir != "" {
			viper.AddConfigPath(dir)
		}
	}
	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) || os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

// bootstrap runs once before any command's RunE: it loads the YAML
// config, then installs slog and (optionally) OTel using the merged
// env+viper logging settings. Errors here abort the command rather
// than running with no observability.
func bootstrap(cmd *cobra.Command, _ []string) error {
	if err := loadConfig(); err != nil {
		return err
	}
	cfg := logging.Resolve()
	sd, err := logging.Setup(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	shutdownFn = sd
	if path := viper.ConfigFileUsed(); path != "" {
		slog.Info("config loaded", "path", path)
	}
	return nil
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPathFlag, "config", "", "path to config file (default: $XDG_CONFIG_HOME/agent-status/config.yaml)")
	rootCmd.PersistentFlags().String("state", defaultStatePath(), "path to state file")
	rootCmd.PersistentFlags().String("log-level", "", "log level: debug, info, warn, error (also: LOG_LEVEL, log.level)")
	rootCmd.PersistentFlags().String("log-format", "", "log format: text or json (also: LOG_FORMAT, log.format)")
	rootCmd.PersistentFlags().String("log-traces", "", "OTel traces exporter: off or stdout (also: LOG_TRACES, log.traces)")
	_ = viper.BindPFlag("state", rootCmd.PersistentFlags().Lookup("state"))
	_ = viper.BindPFlag("log.level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindPFlag("log.format", rootCmd.PersistentFlags().Lookup("log-format"))
	_ = viper.BindPFlag("log.traces", rootCmd.PersistentFlags().Lookup("log-traces"))

	// AGENT_STATUS_* env vars override config + defaults; flags
	// override env. The replacer maps viper's dotted keys (e.g.
	// "server.notify-body") to env names like
	// AGENT_STATUS_SERVER_NOTIFY_BODY.
	viper.SetEnvPrefix("AGENT_STATUS")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	rootCmd.AddCommand(serverCmd, stateCmd, statuslineCmd, genConfigCmd, ui.Command())
}
