package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/server"
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
	statuslineCmd.Flags().Bool("json", false, "emit JSON instead of evaluating the template")

	bindings := map[string]string{
		"statusline.format": "format",
		"statusline.json":   "json",
	}
	for key, flag := range bindings {
		_ = viper.BindPFlag(key, statuslineCmd.Flags().Lookup(flag))
	}
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

func runStatusline(_ *cobra.Command, _ []string) error {
	format := viper.GetString("statusline.format")
	asJSON := viper.GetBool("statusline.json")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sessions, err := server.LoadState(ctx, ServerEndpoint())
	connected := err == nil
	if err != nil {
		// Statusline is meant to render every poll; surface "no
		// sessions, disconnected" instead of failing so tmux/waybar
		// don't show their fallback text on every transient blip.
		sessions = nil
	}

	view := statuslineView{
		Total:     len(sessions),
		Connected: connected,
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
