package focus

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// findTmuxPane searches the ancestor list for a tmux pane_pid and, when
// found, runs select-pane + select-window on that pane id. tmux is a
// secondary step: it composes after a window-level focus when the
// session's terminal is running inside tmux. Returns the descriptive
// part of a status message (e.g. "Switched to tmux pane") or empty
// when nothing matched.
func findAndFocusTmuxPane(ctx context.Context, ancestors []int) (string, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", nil
	}
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id}:#{pane_pid}").Output()
	if err != nil {
		// tmux not running or no panes; not an error for our purposes.
		return "", nil
	}
	panePIDs := map[int]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		ppid, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		panePIDs[ppid] = parts[0]
	}
	for _, a := range ancestors {
		paneID, ok := panePIDs[a]
		if !ok {
			continue
		}
		if out, err := exec.CommandContext(ctx, "tmux", "select-pane", "-t", paneID).CombinedOutput(); err != nil {
			return "", fmt.Errorf("tmux select-pane: %v: %s", err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.CommandContext(ctx, "tmux", "select-window", "-t", paneID).CombinedOutput(); err != nil {
			return "", fmt.Errorf("tmux select-window: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return "Switched to tmux pane", nil
	}
	return "", nil
}
