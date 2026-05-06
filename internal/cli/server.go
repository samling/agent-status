package cli

import (
	"log"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"agent-status/internal/discovery"
	"agent-status/internal/server"
	"agent-status/internal/state"
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
	statePath := viper.GetString("state")
	addr, _ := cmd.Flags().GetString("addr")
	port, _ := cmd.Flags().GetString("port")
	if port != "" {
		addr = addr + ":" + port
	}

	s, err := state.Open(statePath)
	if err != nil {
		return err
	}

	if r, err := discovery.Run(s); err != nil {
		log.Printf("discovery: error: %v", err)
	} else {
		log.Printf("discovery: scanned=%d alive=%d inserted=%d", r.Scanned, r.Alive, r.Inserted)
	}

	go func() {
		if err := discovery.Watch(cmd.Context(), s); err != nil {
			log.Printf("watcher: %v", err)
		}
	}()

	log.Printf("collector listening on %s (state: %s)", addr, statePath)
	return http.ListenAndServe(addr, server.Handler(s))
}
