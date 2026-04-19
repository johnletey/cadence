// Package otlp decodes OpenTelemetry OTLP trace payloads into cadence's
// backend domain types. Any backend that serves traces in OTLP JSON format
// (Tempo, OTLP-compatible Jaeger, SigNoz, …) can share this decoder.
package otlp

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/johnletey/cadence/internal/source"
)

// DecodeTrace parses a canonical OTLP JSON trace payload (keyed at
// "resourceSpans") into a source.Trace. Callers whose backend uses a
// different envelope should rewrite it before calling this.
func DecodeTrace(body []byte, traceID string) (*source.Trace, error) {
	var td tracev1.TracesData
	u := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := u.Unmarshal(body, &td); err != nil {
		return nil, fmt.Errorf("decode OTLP trace: %w", err)
	}

	out := &source.Trace{TraceID: traceID, Services: map[string]struct{}{}}
	for _, rs := range td.GetResourceSpans() {
		service := AttrString(rs.GetResource().GetAttributes(), "service.name")
		if service != "" {
			out.Services[service] = struct{}{}
		}
		for _, ss := range rs.GetScopeSpans() {
			for _, s := range ss.GetSpans() {
				out.Spans = append(out.Spans, ConvertSpan(s, service))
			}
		}
	}
	sort.Slice(out.Spans, func(i, j int) bool { return out.Spans[i].Start.Before(out.Spans[j].Start) })
	return out, nil
}

// ConvertSpan maps an OTLP Span to source.Span. Exported so backends that
// assemble their own payload shape (e.g. one not sourced from TracesData) can
// reuse the conversion.
func ConvertSpan(s *tracev1.Span, service string) source.Span {
	start := time.Unix(0, int64(s.GetStartTimeUnixNano()))
	end := time.Unix(0, int64(s.GetEndTimeUnixNano()))

	attrs := map[string]string{}
	for _, a := range s.GetAttributes() {
		attrs[a.GetKey()] = AnyValueString(a.GetValue())
	}
	events := make([]source.SpanEvent, 0, len(s.GetEvents()))
	for _, e := range s.GetEvents() {
		ea := map[string]string{}
		for _, a := range e.GetAttributes() {
			ea[a.GetKey()] = AnyValueString(a.GetValue())
		}
		events = append(events, source.SpanEvent{
			Time:       time.Unix(0, int64(e.GetTimeUnixNano())),
			Name:       e.GetName(),
			Attributes: ea,
		})
	}
	return source.Span{
		SpanID:       hex.EncodeToString(s.GetSpanId()),
		ParentSpanID: hex.EncodeToString(s.GetParentSpanId()),
		TraceID:      hex.EncodeToString(s.GetTraceId()),
		Name:         s.GetName(),
		Service:      service,
		Kind:         strings.TrimPrefix(s.GetKind().String(), "SPAN_KIND_"),
		Start:        start,
		Duration:     end.Sub(start),
		StatusCode:   strings.TrimPrefix(s.GetStatus().GetCode().String(), "STATUS_CODE_"),
		StatusMsg:    s.GetStatus().GetMessage(),
		Attributes:   attrs,
		Events:       events,
	}
}

// AttrString looks up a single attribute by key and returns its string form,
// or "" if absent.
func AttrString(attrs []*commonv1.KeyValue, key string) string {
	for _, a := range attrs {
		if a.GetKey() == key {
			return AnyValueString(a.GetValue())
		}
	}
	return ""
}

// AnyValueString renders an OTLP AnyValue as a display string. Arrays are
// comma-separated and wrapped in brackets; kvlists are rendered as
// {k=v,k=v}; bytes are hex.
func AnyValueString(v *commonv1.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.GetValue().(type) {
	case *commonv1.AnyValue_StringValue:
		return x.StringValue
	case *commonv1.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	case *commonv1.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *commonv1.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64)
	case *commonv1.AnyValue_ArrayValue:
		parts := make([]string, 0, len(x.ArrayValue.GetValues()))
		for _, e := range x.ArrayValue.GetValues() {
			parts = append(parts, AnyValueString(e))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *commonv1.AnyValue_KvlistValue:
		parts := make([]string, 0, len(x.KvlistValue.GetValues()))
		for _, kv := range x.KvlistValue.GetValues() {
			parts = append(parts, kv.GetKey()+"="+AnyValueString(kv.GetValue()))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case *commonv1.AnyValue_BytesValue:
		return hex.EncodeToString(x.BytesValue)
	}
	return ""
}
