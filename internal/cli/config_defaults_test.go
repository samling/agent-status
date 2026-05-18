package cli

import (
	"strings"
	"testing"
)

func TestNotificationDefaultsEnabled(t *testing.T) {
	flag := serverCmd.Flags().Lookup("notify")
	if flag == nil {
		t.Fatal("notify flag missing")
	}
	if flag.DefValue != "true" {
		t.Fatalf("notify flag default = %q, want true", flag.DefValue)
	}

	if !strings.Contains(defaultConfigYAML, "    enabled: true") {
		t.Fatalf("default config should enable notifications by default; output:\n%s", defaultConfigYAML)
	}
}

func TestNotificationInitialDelayDefaultsImmediate(t *testing.T) {
	flag := serverCmd.Flags().Lookup("notify-initial-delay")
	if flag == nil {
		t.Fatal("notify-initial-delay flag missing")
	}
	if flag.DefValue != "0s" {
		t.Fatalf("notify-initial-delay flag default = %q, want 0s", flag.DefValue)
	}

	if !strings.Contains(defaultConfigYAML, "    initial-delay: 0s") {
		t.Fatalf("default config should notify immediately by default; output:\n%s", defaultConfigYAML)
	}
}
