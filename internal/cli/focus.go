package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/samling/agent-status/internal/client"
)

var focusCmd = &cobra.Command{
	Use:   "focus <session_id>",
	Short: "Bring an agent session's window to the foreground",
	Long: `Look up the live session via the running collector and focus the
window and tmux pane associated with its PID. Intended as a CLI verb
invoked by notification activation, keybinds, or scripts.`,
	Args: cobra.ExactArgs(1),
	RunE: runFocus,
}

func runFocus(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
	defer cancel()

	msg, err := client.New(ServerEndpoint()).Focus(ctx, args[0])
	if err != nil {
		return err
	}
	if msg != "" {
		fmt.Fprintln(cmd.OutOrStdout(), msg)
	}
	return nil
}
