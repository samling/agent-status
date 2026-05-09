package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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

var configPathFlag string

var shutdownFn func(context.Context) error

func Execute() error {
	// Bind the root command context to SIGINT/SIGTERM so long-running
	// subcommands (server, ui) can shut down gracefully on Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := rootCmd.ExecuteContext(ctx)
	if shutdownFn != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownFn(shutdownCtx)
		shutdownFn = nil
	}
	return err
}

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

// defaultStateDir resolves $XDG_STATE_HOME/agent-status, falling back to
// $HOME/.local/state/agent-status per the XDG Base Directory Specification
// (which prescribes the fallback when $XDG_STATE_HOME is unset or empty).
func defaultStateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "agent-status")
}

func defaultStatePath() string {
	dir := defaultStateDir()
	if dir == "" {
		return "state.json"
	}
	return filepath.Join(dir, "state.json")
}

// ServerEndpoint returns server.addr[:server.port].
func ServerEndpoint() string {
	addr := viper.GetString("server.addr")
	port := viper.GetString("server.port")
	if port == "" {
		return addr
	}
	return addr + ":" + port
}

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
	rootCmd.PersistentFlags().String("log-level", "", "log level: debug|info|warn|error (also: LOG_LEVEL, log.level)")
	rootCmd.PersistentFlags().String("log-format", "", "log format: text|json (also: LOG_FORMAT, log.format)")
	_ = viper.BindPFlag("state", rootCmd.PersistentFlags().Lookup("state"))
	_ = viper.BindPFlag("log.level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindPFlag("log.format", rootCmd.PersistentFlags().Lookup("log-format"))

	// Map dotted config keys to AGENT_STATUS_* env vars.
	viper.SetEnvPrefix("AGENT_STATUS")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	rootCmd.AddCommand(serverCmd, statuslineCmd, focusCmd, genConfigCmd, ui.Command())
}
