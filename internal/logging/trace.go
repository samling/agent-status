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

const tracerName = "github.com/samling/agent-status"

// Tracer returns the package-level OTel tracer.
func Tracer() trace.Tracer { return otel.Tracer(tracerName) }

// Start opens a span on ctx. Always defer span.End().
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

// TraceID returns the current span's trace ID.
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// Propagator returns the W3C TraceContext propagator.
func Propagator() propagation.TextMapPropagator { return otel.GetTextMapPropagator() }

func setupTracer(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	mode := normTraces(cfg.Traces)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	if mode == "off" {
		return func(context.Context) error { return nil }, nil
	}

	service := strings.TrimSpace(cfg.Service)
	if service == "" {
		service = "agent-status"
	}
	// Use a schemaless fallback to avoid resource schema URL conflicts.
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

// buildSpanExporter builds the selected span exporter.
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
