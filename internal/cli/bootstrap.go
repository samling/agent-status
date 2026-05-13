package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	bootstrappkg "github.com/samling/agent-status/internal/bootstrap"
)

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Install hook config for Claude Code and Codex",
	Long: `Install the agent-status forwarder script into each agent's config
directory and merge the matching hook entries into the agent's hook
configuration file.

By default all known agents are configured. Pass --agents to restrict
to a subset (comma-separated). Use --claude-dir or --codex-dir (or
CLAUDE_CONFIG_DIR / CODEX_HOME) to override the agent config
directories.

The command prints the planned changes and asks for confirmation before
writing anything. Pass --yes to skip the prompt or --dry-run to see the
plan and a diff of each file that would change.

Available agents:
  claude   Claude Code  (config dir: $CLAUDE_CONFIG_DIR or ~/.claude)
  codex    Codex        (config dir: $CODEX_HOME or ~/.codex)`,
	Example: `  # all agents, interactive
  agent-status bootstrap

  # preview the diff without writing
  agent-status bootstrap --dry-run

  # claude only, non-interactive
  agent-status bootstrap --agents=claude --yes`,
	SilenceUsage: true,
	RunE:         runBootstrap,
}

func init() {
	bootstrapCmd.Flags().StringSlice("agents", nil, "subset of agents to configure (default: all; see Available agents)")
	bootstrapCmd.Flags().String("claude-dir", "", "Claude Code config dir (default: $CLAUDE_CONFIG_DIR or ~/.claude)")
	bootstrapCmd.Flags().String("codex-dir", "", "Codex config dir (default: $CODEX_HOME or ~/.codex)")
	bootstrapCmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	bootstrapCmd.Flags().Bool("dry-run", false, "print the planned changes and diffs without making them")
}

func runBootstrap(cmd *cobra.Command, _ []string) error {
	agents, _ := cmd.Flags().GetStringSlice("agents")
	claudeDir, _ := cmd.Flags().GetString("claude-dir")
	codexDir, _ := cmd.Flags().GetString("codex-dir")
	autoYes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	plan, err := bootstrappkg.BuildPlan(bootstrappkg.Options{
		Agents:    agents,
		ClaudeDir: claudeDir,
		CodexDir:  codexDir,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	plan.Describe(out)
	fmt.Fprintln(out)

	if dryRun {
		return plan.Execute(true, out)
	}

	if !autoYes {
		ok, err := confirm(cmd.InOrStdin(), out, "Proceed? [y/N]: ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "aborted")
			return nil
		}
	}

	if err := plan.Execute(false, out); err != nil {
		return err
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "bootstrap complete. start the collector and the TUI:")
	fmt.Fprintln(out, "  agent-status server")
	fmt.Fprintln(out, "  agent-status ui          # in another terminal")
	return nil
}

func confirm(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
