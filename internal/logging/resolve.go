package logging

import (
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Resolve builds Config from env first, then viper. Bare LOG_LEVEL and
// LOG_FORMAT env vars are honored as a convenience because they're widely
// recognized; everything else (including tracing) lives under viper keys
// and the AGENT_STATUS_* env mapping wired up in cli/root.go.
func Resolve() Config {
	level := envOr("LOG_LEVEL", viper.GetString("log.level"))
	format := envOr("LOG_FORMAT", viper.GetString("log.format"))
	return Config{
		Level:           ParseLevel(level),
		Format:          format,
		Service:         viper.GetString("log.service"),
		TracesEnabled:   viper.GetBool("log.traces.enabled"),
		TracesExporter:  viper.GetString("log.traces.exporter"),
		OTLPEndpoint:    viper.GetString("log.traces.otlp.endpoint"),
		OTLPInsecure:    viper.GetBool("log.traces.otlp.insecure"),
		OTLPHeaders:     viper.GetStringMapString("log.traces.otlp.headers"),
		OTLPTimeout:     viper.GetDuration("log.traces.otlp.timeout"),
		OTLPCompression: viper.GetString("log.traces.otlp.compression"),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
