package logging

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// InjectHTTP writes W3C trace context into request headers.
func InjectHTTP(r *http.Request) {
	Propagator().Inject(r.Context(), propagation.HeaderCarrier(r.Header))
}

// ExtractHTTP reads W3C trace context from request headers.
func ExtractHTTP(r *http.Request) (context.Context, trace.SpanContext) {
	ctx := Propagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	return ctx, trace.SpanContextFromContext(ctx)
}
