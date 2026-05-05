package cli

import (
	"log"
	"net/http"

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

	go func() {
		if err := discovery.Watch(cmd.Context(), db); err != nil {
			log.Printf("watcher: %v", err)
		}
	}()

	log.Printf("collector listening on %s (db: %s)", addr, dbFile)
	return http.ListenAndServe(addr, server.Handler(db))
}
