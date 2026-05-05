package focus

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Niri implements Compositor for the niri Wayland compositor via its
// `niri msg` IPC.
type Niri struct{}

func (Niri) Name() string { return "niri" }

func (Niri) Available() bool {
	_, err := exec.LookPath("niri")
	return err == nil
}

func (n Niri) Focus(ancestors []int) (string, bool, error) {
	windows, err := n.windows()
	if err != nil {
		return "", false, fmt.Errorf("niri windows: %w", err)
	}
	pidToWindow := map[int]uint64{}
	for _, w := range windows {
		if w.PID != nil {
			pidToWindow[*w.PID] = w.ID
		}
	}
	for _, a := range ancestors {
		id, ok := pidToWindow[a]
		if !ok {
			continue
		}
		out, err := exec.Command("niri", "msg", "action", "focus-window", "--id", strconv.FormatUint(id, 10)).CombinedOutput()
		if err != nil {
			return "", false, fmt.Errorf("niri focus-window: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("focused niri window %d", id), true, nil
	}
	return "", false, nil
}

type niriWindow struct {
	ID  uint64 `json:"id"`
	PID *int   `json:"pid"`
}

func (Niri) windows() ([]niriWindow, error) {
	out, err := exec.Command("niri", "msg", "--json", "windows").Output()
	if err != nil {
		return nil, err
	}
	var ws []niriWindow
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, fmt.Errorf("parse niri windows json: %w", err)
	}
	return ws, nil
}
