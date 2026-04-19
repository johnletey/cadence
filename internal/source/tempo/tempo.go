package tempo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnletey/cadence/internal/otlp"
	"github.com/johnletey/cadence/internal/source"
)

// Config configures a Tempo client. URL is required; everything else is
// optional.
type Config struct {
	URL        string
	Headers    map[string]string
	User, Pass string
}

type Client struct {
	url     string
	http    *http.Client
	headers map[string]string
	user    string
	pass    string
}

func New(cfg Config) *Client {
	return &Client{
		url:     strings.TrimRight(cfg.URL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
		headers: cfg.Headers,
		user:    cfg.User,
		pass:    cfg.Pass,
	}
}

func (c *Client) Name() string { return "tempo" }

// get runs a GET against the Tempo HTTP API and returns the raw body on 2xx.
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	u := c.url + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tempo %s %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// Search hits /api/search and returns trace summaries sorted newest-first.
func (c *Client) Search(ctx context.Context, q source.SearchQuery) ([]source.TraceSummary, error) {
	v := url.Values{}
	if q.Query != "" {
		v.Set("q", q.Query)
	}
	if !q.Start.IsZero() {
		v.Set("start", strconv.FormatInt(q.Start.Unix(), 10))
	}
	if !q.End.IsZero() {
		v.Set("end", strconv.FormatInt(q.End.Unix(), 10))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}

	body, err := c.get(ctx, "/api/search", v)
	if err != nil {
		return nil, err
	}

	var r struct {
		Traces []struct {
			TraceID           string `json:"traceID"`
			RootServiceName   string `json:"rootServiceName"`
			RootTraceName     string `json:"rootTraceName"`
			StartTimeUnixNano string `json:"startTimeUnixNano"`
			DurationMs        int64  `json:"durationMs"`
			SpanSet           struct {
				Matched int `json:"matched"`
			} `json:"spanSet"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	out := make([]source.TraceSummary, 0, len(r.Traces))
	for _, t := range r.Traces {
		ns, _ := strconv.ParseInt(t.StartTimeUnixNano, 10, 64)
		out = append(out, source.TraceSummary{
			TraceID:         padTraceID(t.TraceID),
			RootServiceName: t.RootServiceName,
			RootName:        t.RootTraceName,
			Start:           time.Unix(0, ns),
			Duration:        time.Duration(t.DurationMs) * time.Millisecond,
			SpanCount:       t.SpanSet.Matched,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out, nil
}

// GetTrace hits /api/traces/<id>. Tempo wraps the OTLP payload under
// "batches" instead of the canonical "resourceSpans" key, so we unwrap and
// rekey before handing it to the shared OTLP decoder. The start/end hints
// tell the query-frontend which storage blocks to scan.
func (c *Client) GetTrace(ctx context.Context, traceID string, lookup source.TraceLookup) (*source.Trace, error) {
	v := url.Values{}
	if !lookup.Start.IsZero() {
		v.Set("start", strconv.FormatInt(lookup.Start.Unix(), 10))
	}
	if !lookup.End.IsZero() {
		v.Set("end", strconv.FormatInt(lookup.End.Unix(), 10))
	}
	body, err := c.get(ctx, "/api/traces/"+traceID, v)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Batches json.RawMessage `json:"batches"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode trace envelope: %w", err)
	}
	canonical, _ := json.Marshal(struct {
		ResourceSpans json.RawMessage `json:"resourceSpans"`
	}{ResourceSpans: envelope.Batches})
	return otlp.DecodeTrace(canonical, padTraceID(traceID))
}

// Tempo's search API returns hex-encoded trace IDs with leading zeros stripped,
// so a 128-bit ID can come back as 30 or 31 chars. Pad back to the canonical
// 32-char width so downstream comparisons, caches, and displays stay aligned.
func padTraceID(id string) string {
	if len(id) >= 32 || id == "" {
		return id
	}
	return strings.Repeat("0", 32-len(id)) + id
}
