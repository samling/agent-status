package focus

import "fmt"

// PID focuses the OS window owning the given pid in its process ancestry,
// then if a tmux pane is also in the ancestry, drills into that pane.
//
// The returned status string is human-readable and intended for a TUI
// footer; the error is non-nil only on hard failures of subordinate
// commands. Soft cases (no compositor available, no matching window, no
// tmux pane in ancestry) return a descriptive status with nil error.
func PID(pid int) (string, error) {
	ancestors := walkAncestors(pid)

	var winMsg string
	var winFound bool
	for _, c := range compositors() {
		if !c.Available() {
			continue
		}
		msg, found, err := c.Focus(ancestors)
		if err != nil {
			return "", fmt.Errorf("%s: %w", c.Name(), err)
		}
		if found {
			winMsg = msg
			winFound = true
			break
		}
	}

	paneMsg, err := findTmuxPane(ancestors)
	if err != nil {
		return winMsg, err
	}

	switch {
	case winFound && paneMsg != "":
		return "Focused window and tmux pane", nil
	case winFound:
		return winMsg, nil
	case paneMsg != "":
		return paneMsg, nil
	default:
		return "Couldn't find a window or pane for this session", nil
	}
}
