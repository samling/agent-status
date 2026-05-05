package cli

import (
	"log"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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

	log.Printf("collector listening on %s (db: %s)", addr, dbFile)
	return http.ListenAndServe(addr, server.Handler(db))
}
