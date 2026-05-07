package focus

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type niri struct{}

func (*niri) Name() string { return "niri" }

// Probe confirms niri IPC is reachable.
func (*niri) Probe(ctx context.Context) error {
	return exec.CommandContext(ctx, "niri", "msg", "version").Run()
}

func (n *niri) Focus(ctx context.Context, target Target) error {
	windows, err := n.windows(ctx)
	if err != nil {
		return fmt.Errorf("niri windows: %w", err)
	}
	pidToWindow := map[int]uint64{}
	for _, w := range windows {
		if w.PID != nil {
			pidToWindow[*w.PID] = w.ID
		}
	}
	for _, a := range target.Ancestors {
		id, ok := pidToWindow[a]
		if !ok {
			continue
		}
		out, err := exec.CommandContext(ctx, "niri", "msg", "action", "focus-window", "--id", strconv.FormatUint(id, 10)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("niri focus-window: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return ErrWindowNotFound
}

type niriWindow struct {
	ID  uint64 `json:"id"`
	PID *int   `json:"pid"`
}

func (*niri) windows(ctx context.Context) ([]niriWindow, error) {
	out, err := exec.CommandContext(ctx, "niri", "msg", "--json", "windows").Output()
	if err != nil {
		return nil, err
	}
	var ws []niriWindow
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, fmt.Errorf("parse niri windows json: %w", err)
	}
	return ws, nil
}
