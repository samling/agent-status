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

// defaultConfigYAML is the canonical "fresh install" config. Keep in
// sync with the flag defaults defined in each subcommand's init().
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

# Path to the persistent state file. The collector is the sole writer;
# readers (TUI, statusline, state subcommand) load it directly.
# state: ~/.config/agent-status/state.json

server:
  # Listen address and port for the HTTP collector. The same values
  # are reused by every client (TUI connection probe, statusline
  # probe, focus CLI, notification activation callback), so the
  # collector address only needs to be defined here.
  addr: 127.0.0.1
  port: "7878"

  # Desktop notifications when sessions enter the waiting state.
  notify:
    enabled: false
    # Delay between the first 0->1 waiting transition and the first
    # notification. Re-entering 0->1 restarts the timer; sessions
    # joining an already-waiting bucket do not.
    initial-delay: 5s
    # Interval between subsequent notifications while sessions remain
    # waiting. Set to 0 to fire only once per waiting episode.
    repeat: 5m
    # Go text/template strings. Available fields: .Total, .Active,
    # .Waiting, .Idle, .Sessions, .WaitingSessions, .First.
    title: agent-status
    body: "{{.Waiting}} session(s) waiting for input"
    # Optional action button on the notification. When enabled, clicks
    # POST to the local /focus endpoint, which focuses the first waiting
    # session at click time. Requires libnotify (notify-send) on Linux.
    activation:
      enabled: false
      label: Focus

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
