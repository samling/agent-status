package logging

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// InjectHTTP writes the current span's trace context into r's
// headers as W3C Traceparent so the receiving server continues the
// same trace.
func InjectHTTP(r *http.Request) {
	Propagator().Inject(r.Context(), propagation.HeaderCarrier(r.Header))
}

// ExtractHTTP returns a context derived from r.Context() that carries
// the W3C trace context from r's headers (if any).
func ExtractHTTP(r *http.Request) (context.Context, trace.SpanContext) {
	ctx := Propagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	return ctx, trace.SpanContextFromContext(ctx)
}
