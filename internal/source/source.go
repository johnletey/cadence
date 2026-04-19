// Package source models a configured connection to a trace backend (Tempo,
// Jaeger, SigNoz, or anything else that exposes traces) and the domain types
// cadence reads from it.
//
// A *source* is a configured connection: URL, headers, auth. A *backend* is
// the underlying framework the source speaks to. One backend implementation
// lives per subpackage (source/tempo, source/jaeger, …); each implements the
// Source interface.
package source

import (
	"context"
	"time"
)

// Source is the abstraction over a configured trace source.
type Source interface {
	Name() string
	Search(ctx context.Context, q SearchQuery) ([]TraceSummary, error)
	GetTrace(ctx context.Context, traceID string, lookup TraceLookup) (*Trace, error)
}

type SearchQuery struct {
	// TraceQL or backend-specific query string. Empty means "recent traces".
	Query string
	// Time range. Zero values mean "use backend default" (usually last hour).
	Start time.Time
	End   time.Time
	Limit int
}

type TraceSummary struct {
	TraceID         string
	RootServiceName string
	RootName        string
	Start           time.Time
	Duration        time.Duration
	SpanCount       int
}

// TraceLookup carries optional hints for fetching a single trace. Zero values
// mean "no hint": the backend should behave as if the caller didn't know.
//
// Some backends use the time range to narrow which storage blocks to scan;
// without it, older traces may return not-found even though they exist.
type TraceLookup struct {
	Start time.Time
	End   time.Time
}

type Trace struct {
	TraceID  string
	Spans    []Span
	Services map[string]struct{}
}

type Span struct {
	SpanID       string
	ParentSpanID string
	TraceID      string
	Name         string
	Service      string
	Kind         string
	Start        time.Time
	Duration     time.Duration
	StatusCode   string
	StatusMsg    string
	Attributes   map[string]string
	Events       []SpanEvent
}

type SpanEvent struct {
	Time       time.Time
	Name       string
	Attributes map[string]string
}
