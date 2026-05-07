package focus

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func findAndFocusTmuxPane(ctx context.Context, ancestors []int) (string, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", nil
	}
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id}:#{pane_pid}").Output()
	if err != nil {
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
