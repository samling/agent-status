package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/state"
)

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Show current session state",
	RunE:  runState,
}

func init() {
	stateCmd.Flags().Bool("json", false, "output JSON")
}

func runState(cmd *cobra.Command, _ []string) error {
	sessions, err := state.Load(viper.GetString("state"))
	if err != nil {
		return err
	}

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sessions)
	}

	if len(sessions) == 0 {
		fmt.Println("(no sessions)")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tAGENT\tSESSION_ID\tLAST_EVENT\tLAST_EVENT_AT\tFIRST_SEEN_AT")
	for _, s := range sessions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Status, s.Agent, s.SessionID, s.LastEvent, s.LastEventAt, s.FirstSeenAt)
	}
	return w.Flush()
}
