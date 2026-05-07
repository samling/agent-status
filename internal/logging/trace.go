package logging

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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

// StartAt opens a span backdated to start. Use when work runs before the
// decision to emit a span (e.g. only-on-change spans where you've measured
// the work but want the span timing to reflect that real cost). The caller
// is expected to call span.End() promptly so the span's duration is
// (now - start).
func StartAt(ctx context.Context, name string, start time.Time, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name,
		trace.WithAttributes(attrs...),
		trace.WithTimestamp(start),
	)
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

// NewSessionRoot emits a one-shot "session.start" root span and returns its
// (TraceID, RootSpanID) hex strings. The returned IDs are guaranteed to
// reference an *exported* span, so downstream spans that use them as a
// parent reference will not produce "invalid parent span" warnings in
// Jaeger / Tempo.
//
// On a NoOp tracer (tracing disabled) the returned IDs are the canonical
// zero values; callers can still persist them, and ContextWithSessionTrace
// will treat them as absent.
func NewSessionRoot(ctx context.Context, sessionID, agent string) (traceIDHex, rootSpanIDHex string) {
	_, span := Tracer().Start(ctx, "session.start",
		trace.WithAttributes(
			attribute.String("session.id", sessionID),
			attribute.String("agent", agent),
		),
	)
	sc := span.SpanContext()
	span.End()
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}

// ContextWithSessionTrace returns a context whose current SpanContext points
// at the persisted (TraceID, RootSpanID) pair so that any Tracer.Start call
// against it produces a child of the session's long-lived trace.
//
// On any decoding failure the input ctx is returned unchanged; callers should
// log but otherwise proceed.
func ContextWithSessionTrace(ctx context.Context, traceIDHex, spanIDHex string) context.Context {
	if traceIDHex == "" || spanIDHex == "" {
		return ctx
	}
	tid, err := trace.TraceIDFromHex(traceIDHex)
	if err != nil {
		return ctx
	}
	sid, err := trace.SpanIDFromHex(spanIDHex)
	if err != nil {
		return ctx
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithSpanContext(ctx, sc)
}

func setupTracer(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})

	if !cfg.TracesEnabled {
		return func(context.Context) error { return nil }, nil
	}
	mode := normExporter(cfg.TracesExporter)

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

	exp, err := buildSpanExporter(ctx, mode, cfg)
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

// buildSpanExporter builds the selected span exporter, threading viper-sourced
// OTLP knobs (endpoint / headers / TLS / compression / timeout) through to
// the underlying client. Empty fields fall through to the OTel SDK's
// OTEL_EXPORTER_OTLP_* env-var defaults.
func buildSpanExporter(ctx context.Context, mode string, cfg Config) (sdktrace.SpanExporter, error) {
	switch mode {
	case "stdout":
		return stdouttrace.New(
			stdouttrace.WithWriter(os.Stderr),
			stdouttrace.WithPrettyPrint(),
		)
	case "otlp-http":
		return otlptrace.New(ctx, otlptracehttp.NewClient(otlpHTTPOpts(cfg)...))
	case "otlp-grpc":
		return otlptrace.New(ctx, otlptracegrpc.NewClient(otlpGRPCOpts(cfg)...))
	default:
		return nil, fmt.Errorf("unknown traces mode %q (want off|stdout|otlp|otlp-grpc)", mode)
	}
}

func otlpHTTPOpts(cfg Config) []otlptracehttp.Option {
	var opts []otlptracehttp.Option
	if endpoint := strings.TrimSpace(cfg.OTLPEndpoint); endpoint != "" {
		if strings.Contains(endpoint, "://") {
			opts = append(opts, otlptracehttp.WithEndpointURL(endpoint))
		} else {
			opts = append(opts, otlptracehttp.WithEndpoint(endpoint))
		}
	}
	if cfg.OTLPInsecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(cfg.OTLPHeaders) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(cfg.OTLPHeaders))
	}
	if cfg.OTLPTimeout > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(cfg.OTLPTimeout))
	}
	switch strings.ToLower(strings.TrimSpace(cfg.OTLPCompression)) {
	case "gzip":
		opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	case "", "none":
		// leave default (no compression)
	}
	return opts
}

func otlpGRPCOpts(cfg Config) []otlptracegrpc.Option {
	var opts []otlptracegrpc.Option
	if endpoint := strings.TrimSpace(cfg.OTLPEndpoint); endpoint != "" {
		if strings.Contains(endpoint, "://") {
			opts = append(opts, otlptracegrpc.WithEndpointURL(endpoint))
		} else {
			opts = append(opts, otlptracegrpc.WithEndpoint(endpoint))
		}
	}
	if cfg.OTLPInsecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	if len(cfg.OTLPHeaders) > 0 {
		opts = append(opts, otlptracegrpc.WithHeaders(cfg.OTLPHeaders))
	}
	if cfg.OTLPTimeout > 0 {
		opts = append(opts, otlptracegrpc.WithTimeout(cfg.OTLPTimeout))
	}
	switch strings.ToLower(strings.TrimSpace(cfg.OTLPCompression)) {
	case "gzip":
		opts = append(opts, otlptracegrpc.WithCompressor("gzip"))
	case "", "none":
		// leave default (no compressor)
	}
	return opts
}
