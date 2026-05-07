package logging

import (
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Resolve builds a Config from viper keys (log.level, log.format,
// log.traces) with environment variables (LOG_LEVEL, LOG_FORMAT,
// LOG_TRACES) taking precedence so a one-off `LOG_LEVEL=debug
// agent-status server` works without touching the YAML.
//
// Resolve is safe to call before Viper has read its config file: it
// will simply use whatever defaults Viper has registered, plus the
// process environment.
func Resolve() Config {
	level := envOr("LOG_LEVEL", viper.GetString("log.level"))
	format := envOr("LOG_FORMAT", viper.GetString("log.format"))
	traces := envOr("LOG_TRACES", viper.GetString("log.traces"))
	return Config{
		Level:  ParseLevel(level),
		Format: format,
		Traces: traces,
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
