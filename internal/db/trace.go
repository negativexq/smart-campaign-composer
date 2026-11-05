package db

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// statementLimit keeps a span's copy of a query short enough to read.
const statementLimit = 300

// spanTracer shows the wait for a connection and each query inside the trace that
// asked for them. It records the query text and never its arguments, which is where
// personal data would be. Work outside a recorded trace, such as housekeeping, costs
// nothing and leaves no spans.
type spanTracer struct {
	tracer trace.Tracer
}

var (
	_ pgx.QueryTracer       = spanTracer{}
	_ pgxpool.AcquireTracer = spanTracer{}
)

type spanKey struct{}

func (t spanTracer) start(ctx context.Context, name string, attributes ...attribute.KeyValue) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}
	ctx, span := t.tracer.Start(ctx, name, trace.WithAttributes(attributes...))
	return context.WithValue(ctx, spanKey{}, span)
}

func finish(ctx context.Context, err error) {
	span, ok := ctx.Value(spanKey{}).(trace.Span)
	if !ok {
		return
	}
	if err != nil {
		span.SetStatus(codes.Error, "database call failed")
	}
	span.End()
}

func (t spanTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}
	return t.start(ctx, "db.query", attribute.String("db.statement", compactStatement(data.SQL)))
}

func (t spanTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	finish(ctx, data.Err)
}

func (t spanTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return t.start(ctx, "db.acquire")
}

func (t spanTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	finish(ctx, data.Err)
}

func compactStatement(statement string) string {
	compact := strings.Join(strings.Fields(statement), " ")
	if len(compact) > statementLimit {
		return compact[:statementLimit]
	}
	return compact
}
