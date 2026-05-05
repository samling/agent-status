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
	rootCmd.PersistentFlags().String("state", "state.json", "path to state file")
	_ = viper.BindPFlag("state", rootCmd.PersistentFlags().Lookup("state"))
	viper.SetEnvPrefix("AGENT_STATUS")
	viper.AutomaticEnv()

	rootCmd.AddCommand(serverCmd, stateCmd, uiCmd)
}
