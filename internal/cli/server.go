package cli

import (
	"log"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/discovery"
	"github.com/samling/agent-status/internal/server"
	"github.com/samling/agent-status/internal/state"
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

	go func() {
		if err := discovery.Watch(cmd.Context(), s); err != nil {
			log.Printf("watcher: %v", err)
		}
	}()

	log.Printf("collector listening on %s (state: %s)", addr, statePath)
	return http.ListenAndServe(addr, server.Handler(s))
}
