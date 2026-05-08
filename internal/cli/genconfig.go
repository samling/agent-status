package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var genConfigCmd = &cobra.Command{
	Use:   "generate-config",
	Short: "Print a default config file in YAML",
	Long: `Print (or write) a YAML config file populated with the same defaults
agent-status uses when no config or flags are supplied.

The output is intended as a starting point: copy it to
$XDG_CONFIG_HOME/agent-status/config.yaml (or the path you pass via
--output), then uncomment / edit the keys you want to override. Every
section is optional; missing keys fall back to the built-in defaults
or the matching CLI flag.`,
	Example: `  # print to stdout
  agent-status generate-config

  # write to the default config location
  agent-status generate-config -o "$XDG_CONFIG_HOME/agent-status/config.yaml"

  # overwrite an existing file
  agent-status generate-config -o ~/.config/agent-status/config.yaml --force`,
	RunE: runGenConfig,
}

func init() {
	genConfigCmd.Flags().StringP("output", "o", "", "write the config to this path instead of stdout")
	genConfigCmd.Flags().Bool("force", false, "overwrite the output file if it already exists")
}

// defaultConfigYAML must stay in sync with flag defaults.
const defaultConfigYAML = `# agent-status config
#
# All keys are optional. Precedence (high → low):
#   1. CLI flags (--addr, --port, --notify, ...)
#   2. AGENT_STATUS_* environment variables (AGENT_STATUS_SERVER_ADDR, ...)
#   3. This file
#   4. Built-in defaults
#
# To find the file agent-status is currently using, look at startup
# logs: "config: loaded /path/to/config.yaml". The default search
# location is $XDG_CONFIG_HOME/agent-status/config.yaml.

# Path to the persistent state file. Defaults to
# $XDG_STATE_HOME/agent-status/state.json (or ~/.local/state/agent-status/
# state.json when XDG_STATE_HOME is unset, per the XDG Base Directory
# Specification). The collector is the sole writer; clients (TUI, focus
# subcommand, statusline) read live state via the HTTP API instead.
# state: ~/.local/state/agent-status/state.json

# Logging for the collector and CLI clients. Each key has a matching
# CLI flag (--log-level, --log-format) and env var (LOG_LEVEL,
# LOG_FORMAT) that takes precedence over this file.
log:
  # Minimum log level: debug | info | warn | error.
  level: info
  # Output format: text (human-friendly key=value) or json.
  format: text

server:
  # Listen address and port for the HTTP collector. The collector
  # exposes POST /hook for agent processes plus GET /state and
  # GET /state/{session_id} for clients (TUI, focus subcommand,
  # statusline). Bound to localhost; do not expose to the network.
  addr: 127.0.0.1
  port: "7878"

  # Desktop notifications when sessions enter the waiting state. One
  # toast fires per waiting session; each toast carries a "Focus"
  # action that brings that session's window to the foreground via
  # the focus subcommand. Requires libnotify (notify-send) on Linux.
  notify:
    enabled: false
    # Delay between a session entering waiting and its first
    # notification. Each session has its own initial timer; re-entering
    # waiting later starts a fresh delay.
    initial-delay: 5s
    # Cadence of repeat notifications while a session stays waiting.
    # Set to 0 to fire only once per waiting episode.
    repeat: 5m
    # Go text/template strings. Each notification renders against a
    # single waiting session. Available fields:
    #   .Session.SessionID  the session id
    #   .Session.Agent      "claude-code", "codex", ...
    #   .Session.PID        the agent process pid
    #   .Session.LastEvent  the most recent hook event name
    #   .Status             derived status: active | waiting | idle
    title: agent-status
    body: "{{.Session.Agent}} session waiting for input"
    # Label for the focus action button shown on each notification.
    action-label: Focus

ui:
  # TUI refresh interval.
  interval: 500ms
  # Exit the TUI after focusing a session. Useful when launched in a
  # tmux popup so the popup auto-closes on focus.
  quit-after-focus: false

statusline:
  # Go template applied to the statusline view. Available fields:
  # .Total, .Active, .Waiting, .Idle, .Connected, .Sessions.
  format: "{{.Active}}/{{.Waiting}}/{{.Idle}}"
  # Emit JSON instead of evaluating the template.
  json: false
`

func runGenConfig(cmd *cobra.Command, _ []string) error {
	output, _ := cmd.Flags().GetString("output")
	force, _ := cmd.Flags().GetBool("force")

	if output == "" {
		_, err := fmt.Fprint(os.Stdout, defaultConfigYAML)
		return err
	}

	if !force {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("%s exists; pass --force to overwrite", output)
		}
	}
	if dir := filepath.Dir(output); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(output, []byte(defaultConfigYAML), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", output)
	return nil
}
