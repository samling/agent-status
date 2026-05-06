//go:build linux

package notify

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// New returns a notify-send-backed Notifier when the binary is on
// PATH, otherwise an error explaining how to install it. notify-send
// speaks the freedesktop.org org.freedesktop.Notifications D-Bus
// spec, which every major Linux notification daemon implements
// (Dunst, Mako, GNOME, KDE/Plasma, swaync, ...), so we don't pin to
// any one daemon.
func New() (Notifier, error) {
	bin, err := exec.LookPath("notify-send")
	if err != nil {
		return nil, fmt.Errorf("notify-send not on PATH; install libnotify (libnotify-bin on Debian/Ubuntu, libnotify on Arch/Fedora)")
	}
	return &notifySend{bin: bin}, nil
}

type notifySend struct {
	bin string
}

func (*notifySend) Name() string { return "notify-send" }

// Notify shells to notify-send. For action-less notifications it's
// fire-and-forget. With actions, we spawn notify-send under --wait
// (which keeps it running until the notification is dismissed or
// activated) and stream activation IDs from stdout to the returned
// channel; the channel closes when notify-send exits.
func (n *notifySend) Notify(ctx context.Context, msg Notification) (<-chan string, error) {
	args := []string{"--app-name=agent-status"}
	for _, a := range msg.Actions {
		args = append(args, fmt.Sprintf("--action=%s=%s", a.ID, a.Label))
	}
	if len(msg.Actions) > 0 {
		args = append(args, "--wait")
	}
	args = append(args, msg.Title, msg.Body)

	if len(msg.Actions) == 0 {
		out, err := exec.CommandContext(ctx, n.bin, args...).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("notify-send: %v: %s", err, strings.TrimSpace(string(out)))
		}
		ch := make(chan string)
		close(ch)
		return ch, nil
	}

	cmd := exec.CommandContext(ctx, n.bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("notify-send pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("notify-send start: %w", err)
	}
	ch := make(chan string, 1)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				select {
				case ch <- line:
				case <-ctx.Done():
					return
				}
			}
		}
		_ = cmd.Wait()
	}()
	return ch, nil
}
