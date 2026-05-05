package cli

import (
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

func init() {
	rootCmd.PersistentFlags().String("db", "state.db", "path to SQLite database")
	_ = viper.BindPFlag("db", rootCmd.PersistentFlags().Lookup("db"))
	viper.SetEnvPrefix("AGENT_STATUS")
	viper.AutomaticEnv()

	rootCmd.AddCommand(serverCmd, stateCmd, tailCmd)
}
