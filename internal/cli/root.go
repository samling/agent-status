package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "agent-status",
	Short: "Collect and inspect Claude Code hook events",
}

func Execute() error {
	return rootCmd.Execute()
}

// defaultStatePath returns ~/.config/agent-status/state.json, honoring
// XDG_CONFIG_HOME when set. Falls back to a relative "state.json" if we
// can't determine a home directory; the --state flag and AGENT_STATUS_STATE
// env var still override.
func defaultStatePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "state.json"
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "agent-status", "state.json")
}

func init() {
	rootCmd.PersistentFlags().String("state", defaultStatePath(), "path to state file")
	_ = viper.BindPFlag("state", rootCmd.PersistentFlags().Lookup("state"))
	viper.SetEnvPrefix("AGENT_STATUS")
	viper.AutomaticEnv()

	rootCmd.AddCommand(serverCmd, stateCmd, uiCmd)
}
