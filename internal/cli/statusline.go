package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/state"
)

var statuslineCmd = &cobra.Command{
	Use:   "statusline",
	Short: "Print a one-line summary for status bar widgets",
	Long: `Print a one-line summary of the live session state, formatted via a Go
template. Intended for status bar widgets (tmux statusline, polybar,
waybar, i3blocks, eww, sketchybar, ...) that want a compact string to
display on a poll.

The template has access to:
  .Total      total session count
  .Active     count of sessions with status=active
  .Waiting    count of sessions with status=waiting (user attention)
  .Idle       count of sessions with status=idle
  .Connected  bool: collector reachable on its listen port
  .Sessions   slice of state.Session for advanced layouts`,
	Example: `  # default: "active/waiting/idle"
  agent-status statusline

  # human-readable
  agent-status statusline --format '{{.Active}} active, {{.Waiting}} waiting'

  # only show non-zero counts
  agent-status statusline --format '{{if .Waiting}}!{{.Waiting}} {{end}}{{if .Active}}{{.Active}}A{{end}}'

  # waybar-style with click handling
  agent-status statusline --json`,
	RunE: runStatusline,
}

func init() {
	statuslineCmd.Flags().String("format", "{{.Active}}/{{.Waiting}}/{{.Idle}}", "Go template applied to the statusline view")
	statuslineCmd.Flags().String("server", "127.0.0.1:7878", "host:port to probe for the .Connected variable")
	statuslineCmd.Flags().Bool("json", false, "emit JSON instead of evaluating the template")
}

// statuslineView is the data passed to the user-provided template.
// Field names are stable; new fields can be added without breaking
// existing templates.
type statuslineView struct {
	Total     int
	Active    int
	Waiting   int
	Idle      int
	Connected bool
	Sessions  []state.Session
}

func runStatusline(cmd *cobra.Command, _ []string) error {
	statePath := viper.GetString("state")
	format, _ := cmd.Flags().GetString("format")
	serverAddr, _ := cmd.Flags().GetString("server")
	asJSON, _ := cmd.Flags().GetBool("json")

	sessions, err := state.Load(statePath)
	if err != nil {
		return err
	}

	view := statuslineView{
		Total:     len(sessions),
		Connected: probeServerReachable(serverAddr),
		Sessions:  sessions,
	}
	for _, s := range sessions {
		switch s.Status {
		case "active":
			view.Active++
		case "waiting":
			view.Waiting++
		case "idle":
			view.Idle++
		}
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(view)
	}

	tmpl, err := template.New("statusline").Parse(format)
	if err != nil {
		return fmt.Errorf("parse format: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, view); err != nil {
		return fmt.Errorf("render: %w", err)
	}
	fmt.Fprintln(os.Stdout, sb.String())
	return nil
}

// probeServerReachable does a short TCP dial against addr to populate
// .Connected. Mirrors the TUI's title-bar indicator so widget output
// agrees with what the live UI shows.
func probeServerReachable(addr string) bool {
	if addr == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
