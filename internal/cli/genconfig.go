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
# Specification). The collector is the sole writer; readers (TUI,
# statusline, state subcommand) load it directly.
# state: ~/.local/state/agent-status/state.json

# Logging and tracing for the collector and CLI clients. Each key has
# a matching CLI flag (--log-level, --log-format, --log-traces-*) and
# env var (LOG_LEVEL / LOG_FORMAT for the bare ones; AGENT_STATUS_LOG_*
# for everything else) that takes precedence over this file.
log:
  # Minimum log level: debug | info | warn | error.
  level: info
  # Output format: text (human-friendly key=value) or json.
  format: text
  # OTel service.name attribute. Defaults to "agent-status".
  # service: agent-status

  # OpenTelemetry tracing. trace_id and span_id are stamped on every log
  # record while a span is in scope, regardless of whether export is
  # enabled here.
  traces:
    # Toggle trace export. When false, the global TracerProvider stays
    # NoOp and no spans leave the process.
    enabled: false
    # Exporter to use when enabled:
    #   stdout    pretty-print spans to stderr; useful for local debugging.
    #   otlp-http send via OTLP/HTTP (default port 4318).
    #   otlp-grpc send via OTLP/gRPC (default port 4317).
    exporter: stdout

    # OTLP exporter knobs (used when exporter is otlp-*). Empty / unset
    # values fall through to the SDK's OTEL_EXPORTER_OTLP_* env-var
    # defaults, so existing OTel deployments keep working without
    # touching this file.
    otlp:
      # host:port or full URL. URLs (https://...) are passed via the
      # WithEndpointURL option; bare host:port goes through WithEndpoint.
      # endpoint: localhost:4318
      # Skip TLS (use http:// for HTTP, h2c for gRPC). Useful for local
      # collectors like Jaeger / Tempo running unencrypted.
      # insecure: false
      # Per-export deadline. Leave 0 / unset for the SDK default (10s).
      # timeout: 10s
      # Compression: none | gzip.
      # compression: none
      # Headers sent on every export request. Common use: bearer tokens
      # for hosted backends. Values are taken verbatim (no shell
      # expansion); for secret-injection at process-start time, set
      # AGENT_STATUS_LOG_TRACES_OTLP_HEADERS=key1=value1,key2=value2.
      # headers:
      #   authorization: "Bearer paste-token-here"

server:
  # Listen address and port for the HTTP collector. The collector
  # exposes only POST /hook for agent processes; the TUI and
  # statusline use this address purely as a TCP-dial liveness probe.
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
    # focus the first waiting session's window. Requires libnotify
    # (notify-send) on Linux.
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
