package cli

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"agent-status/internal/discovery"
	"agent-status/internal/server"
	"agent-status/internal/store"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the HTTP collector",
	RunE:  runServer,
}

func init() {
	serverCmd.Flags().String("addr", "127.0.0.1", "listen address")
	serverCmd.Flags().String("port", "7878", "listen port")
	serverCmd.Flags().Duration("scan-interval", 10*time.Second, "interval between session liveness scans")
}

func runServer(cmd *cobra.Command, _ []string) error {
	dbFile := viper.GetString("db")
	addr, _ := cmd.Flags().GetString("addr")
	port, _ := cmd.Flags().GetString("port")
	if port != "" {
		addr = addr + ":" + port
	}

	db, err := store.Open(dbFile)
	if err != nil {
		return err
	}
	defer db.Close()

	if r, err := discovery.Run(cmd.Context(), db); err != nil {
		log.Printf("discovery: error: %v", err)
	} else {
		log.Printf("discovery: scanned=%d alive=%d inserted=%d", r.Scanned, r.Alive, r.Inserted)
	}

	scanInterval, _ := cmd.Flags().GetDuration("scan-interval")
	go runReaper(cmd.Context(), db, scanInterval)

	log.Printf("collector listening on %s (db: %s)", addr, dbFile)
	return http.ListenAndServe(addr, server.Handler(db))
}

func runReaper(ctx context.Context, db *sql.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := discovery.Reap(ctx, db)
			if err != nil {
				log.Printf("reaper: error: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("reaper: marked %d session(s) as ended", n)
			}
		}
	}
}
