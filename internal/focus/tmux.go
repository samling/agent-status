package focus

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// findTmuxPane searches the ancestor list for a tmux pane_pid and, when
// found, runs select-pane + select-window on that pane id. tmux is a
// secondary step: it composes after a compositor's window focus when the
// host is a terminal running tmux. Returns the descriptive part of a
// status message (e.g. "tmux pane %1") or empty when nothing matched.
func findTmuxPane(ancestors []int) (string, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", nil
	}
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}:#{pane_pid}").Output()
	if err != nil {
		// tmux not running or no panes; not an error for our purposes.
		return "", nil
	}
	panePIDs := map[int]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
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
		if out, err := exec.Command("tmux", "select-pane", "-t", paneID).CombinedOutput(); err != nil {
			return "", fmt.Errorf("tmux select-pane: %v: %s", err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.Command("tmux", "select-window", "-t", paneID).CombinedOutput(); err != nil {
			return "", fmt.Errorf("tmux select-window: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("tmux pane %s", paneID), nil
	}
	return "", nil
}
