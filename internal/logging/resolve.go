package logging

import (
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Resolve builds Config from env first, then viper.
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
