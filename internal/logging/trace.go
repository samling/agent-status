package logging

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the instrumentation library name used for all
// agent-status spans.
const tracerName = "github.com/samling/agent-status"

// Tracer returns the package-level OTel tracer. When tracing is
// disabled, this is a no-op tracer and Start returns a no-op span.
func Tracer() trace.Tracer { return otel.Tracer(tracerName) }

// Start opens a span on ctx. Always defer span.End().
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

// TraceID returns the current span's trace ID, or "" when there is
// no span in scope.
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// Propagator returns the W3C TraceContext propagator used to inject
// and extract trace headers on HTTP traffic between agent-status
// processes (e.g. CLI client -> server).
func Propagator() propagation.TextMapPropagator { return otel.GetTextMapPropagator() }

func setupTracer(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	mode := normTraces(cfg.Traces)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	if mode == "off" {
		// otel ships a no-op TracerProvider by default, so we just
		// leave it in place and return a noop shutdown.
		return func(context.Context) error { return nil }, nil
	}

	service := strings.TrimSpace(cfg.Service)
	if service == "" {
		service = "agent-status"
	}
	// resource.Default already reads OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES from the environment, so users get the
	// standard knobs for free. We merge in a schemaless fallback for
	// service.name in case neither env var was set; using a schemaless
	// resource avoids the schema-URL conflict that arises when the
	// SDK and our semconv import advance at different rates.
	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(attribute.String("service.name", service)),
	)
	if err != nil {
		return nil, fmt.Errorf("trace resource: %w", err)
	}

	exp, err := buildSpanExporter(ctx, mode)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// buildSpanExporter builds a span exporter for the given mode. OTLP
// exporters honour the standard OTEL_EXPORTER_OTLP_* env vars (e.g.
// OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS,
// OTEL_EXPORTER_OTLP_PROTOCOL, OTEL_EXPORTER_OTLP_INSECURE), so the
// caller does not need to plumb endpoint/headers through Config.
func buildSpanExporter(ctx context.Context, mode string) (sdktrace.SpanExporter, error) {
	switch mode {
	case "stdout":
		return stdouttrace.New(
			stdouttrace.WithWriter(os.Stderr),
			stdouttrace.WithPrettyPrint(),
		)
	case "otlp-http":
		return otlptrace.New(ctx, otlptracehttp.NewClient())
	case "otlp-grpc":
		return otlptrace.New(ctx, otlptracegrpc.NewClient())
	default:
		return nil, fmt.Errorf("unknown traces mode %q (want off|stdout|otlp|otlp-grpc)", mode)
	}
}
