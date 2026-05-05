package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"agent-status/internal/store"
)

var tailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Stream events as they arrive",
	RunE:  runTail,
}

func init() {
	tailCmd.Flags().Duration("interval", 500*time.Millisecond, "poll interval")
	tailCmd.Flags().Bool("json", false, "output JSON lines")
	tailCmd.Flags().Int64("from", -1, "start ID (default: only new events)")
}

func runTail(cmd *cobra.Command, _ []string) error {
	db, err := store.Open(viper.GetString("db"))
	if err != nil {
		return err
	}
	defer db.Close()

	interval, _ := cmd.Flags().GetDuration("interval")
	asJSON, _ := cmd.Flags().GetBool("json")
	from, _ := cmd.Flags().GetInt64("from")

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var lastID int64
	if from >= 0 {
		lastID = from
	} else {
		lastID, err = store.MaxEventID(ctx, db)
		if err != nil {
			return err
		}
	}

	enc := json.NewEncoder(os.Stdout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		events, err := store.QueryEventsAfter(ctx, db, lastID)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, e := range events {
			if asJSON {
				_ = enc.Encode(e)
			} else {
				fmt.Printf("[%s] %-20s session=%s %s\n",
					e.ReceivedAt, e.HookEventName, e.SessionID, string(e.Payload))
			}
			lastID = e.ID
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
