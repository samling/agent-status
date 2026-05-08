package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/samling/agent-status/internal/logging"
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
	// statusline output is consumed by status-bar widgets, so its stderr
	// must stay clean. Override the root bootstrap with a quiet variant
	// that loads config but discards all log output.
	PersistentPreRunE: bootstrapQuiet,
	RunE:              runStatusline,
}

func bootstrapQuiet(cmd *cobra.Command, _ []string) error {
	if err := loadConfig(); err != nil {
		return err
	}
	cfg := logging.Resolve()
	cfg.Output = io.Discard
	sd, err := logging.Setup(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	shutdownFn = sd
	return nil
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

// statuslineView is exposed to the user template.
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

	sessions, err := state.Load(viper.GetString("state"))
	if err != nil {
		sessions = nil
	}
	connected := server.Reachable(ServerEndpoint())

	view := statuslineView{
		Total:     len(sessions),
		Connected: connected,
		Sessions:  sessions,
	}
	for _, s := range sessions {
		switch state.DeriveStatus(s) {
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
