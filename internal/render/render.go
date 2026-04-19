// Package render turns cadence's backend domain types into formatted strings
// for both the TUI and the headless CLI. The TUI layer composes these helpers
// with selection overlays; the CLI layer prints the output directly.
package render

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/johnletey/cadence/internal/source"
)

// ---- Styles ----

var (
	ColorAccent   = lipgloss.Color("#7C6FF1")
	ColorAccent2  = lipgloss.Color("#40C4AA")
	ColorMuted    = lipgloss.Color("#7A7A8C")
	ColorError    = lipgloss.Color("#E05561")
	ColorSubtle   = lipgloss.Color("#3A3A4A")
	ColorHighlite = lipgloss.Color("#2A2A3A")

	Muted  = lipgloss.NewStyle().Foreground(ColorMuted)
	ErrSty = lipgloss.NewStyle().Foreground(ColorError).Bold(true)
	OkSty  = lipgloss.NewStyle().Foreground(ColorAccent2)
	Hot    = lipgloss.NewStyle().Foreground(ColorAccent2).Bold(true)
	Header = lipgloss.NewStyle().Foreground(ColorMuted).Bold(true).Underline(true)

	barTrack = lipgloss.NewStyle().Foreground(ColorSubtle)

	servicePalette = []lipgloss.Color{
		lipgloss.Color("#7C6FF1"),
		lipgloss.Color("#40C4AA"),
		lipgloss.Color("#E5C07B"),
		lipgloss.Color("#61AFEF"),
		lipgloss.Color("#C678DD"),
		lipgloss.Color("#E06C75"),
		lipgloss.Color("#56B6C2"),
		lipgloss.Color("#98C379"),
		lipgloss.Color("#D19A66"),
		lipgloss.Color("#F78FB3"),
	}
)

// ServiceStyle returns a foreground style whose color is hashed from the
// service name, so each service keeps the same color across every row.
func ServiceStyle(svc string) lipgloss.Style {
	color := ColorAccent
	if svc != "" {
		h := fnv.New32a()
		h.Write([]byte(svc))
		color = servicePalette[int(h.Sum32())%len(servicePalette)]
	}
	return lipgloss.NewStyle().Foreground(color)
}

func StatusGlyph(code string) string {
	switch strings.ToUpper(code) {
	case "ERROR":
		return ErrSty.Render("●")
	case "OK":
		return OkSty.Render("●")
	}
	return Muted.Render("·")
}

// ---- Tree ----

type TreeNode struct {
	Span  *source.Span
	Depth int
	Last  bool
}

// BuildTree organises a flat span slice into a depth-first tree order.
func BuildTree(spans []source.Span) []TreeNode {
	if len(spans) == 0 {
		return nil
	}
	byParent := map[string][]*source.Span{}
	ids := map[string]bool{}
	for i := range spans {
		ids[spans[i].SpanID] = true
	}
	roots := []*source.Span{}
	for i := range spans {
		s := &spans[i]
		if s.ParentSpanID == "" || !ids[s.ParentSpanID] {
			roots = append(roots, s)
			continue
		}
		byParent[s.ParentSpanID] = append(byParent[s.ParentSpanID], s)
	}
	sortByStart := func(ss []*source.Span) {
		sort.SliceStable(ss, func(i, j int) bool { return ss[i].Start.Before(ss[j].Start) })
	}
	sortByStart(roots)
	for k := range byParent {
		sortByStart(byParent[k])
	}
	var out []TreeNode
	var walk func(s *source.Span, depth int, last bool)
	walk = func(s *source.Span, depth int, last bool) {
		out = append(out, TreeNode{Span: s, Depth: depth, Last: last})
		kids := byParent[s.SpanID]
		for i, k := range kids {
			walk(k, depth+1, i == len(kids)-1)
		}
	}
	for i, r := range roots {
		walk(r, 0, i == len(roots)-1)
	}
	return out
}

func treePrefix(depth int) string {
	if depth == 0 {
		return ""
	}
	return strings.Repeat("  ", depth-1) + "└─ "
}

// ---- Time helpers ----

func TraceBounds(spans []source.Span) time.Duration {
	if len(spans) == 0 {
		return 0
	}
	start := spans[0].Start
	end := spans[0].Start.Add(spans[0].Duration)
	for _, s := range spans[1:] {
		if s.Start.Before(start) {
			start = s.Start
		}
		e := s.Start.Add(s.Duration)
		if e.After(end) {
			end = e
		}
	}
	return end.Sub(start)
}

func TraceStartTime(spans []source.Span) time.Time {
	if len(spans) == 0 {
		return time.Time{}
	}
	t := spans[0].Start
	for _, s := range spans[1:] {
		if s.Start.Before(t) {
			t = s.Start
		}
	}
	return t
}

func FmtDur(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
	case d >= time.Microsecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d > 0:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
	return "-"
}

func FmtAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func FmtStamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	if time.Since(t) < 24*time.Hour && t.Day() == time.Now().Day() {
		return t.Format("15:04:05")
	}
	return t.Format("2006-01-02 15:04:05")
}

// ---- String helpers ----

// Truncate shortens s so its display width is at most w, preserving any ANSI
// escape sequences it contains. Adds "…" when truncation happens.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return ansi.Truncate(s, w-1, "") + "…"
}

// PadDisplay right-pads s with spaces so its display width becomes w. Safe
// against ANSI escapes.
func PadDisplay(s string, w int) string {
	pad := w - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// ---- Primitives ----

// Bar draws a horizontal timeline showing where a span sits within the trace.
// leftPad is the muted track before the span; the filled region uses the
// service's hashed color.
func Bar(offset, dur, total time.Duration, width int, service string) string {
	if total <= 0 || width <= 0 {
		return ""
	}
	startRatio := float64(offset) / float64(total)
	widthRatio := float64(dur) / float64(total)
	if startRatio < 0 {
		startRatio = 0
	}
	if startRatio > 1 {
		startRatio = 1
	}
	leftPad := int(startRatio * float64(width))
	barW := int(widthRatio * float64(width))
	if barW < 1 && dur > 0 {
		barW = 1
	}
	if leftPad+barW > width {
		barW = width - leftPad
	}
	barW = max(barW, 0)
	rightPad := max(width-leftPad-barW, 0)
	fill := ServiceStyle(service)
	return barTrack.Render(strings.Repeat("─", leftPad)) +
		fill.Render(strings.Repeat("━", barW)) +
		barTrack.Render(strings.Repeat("─", rightPad))
}

// ---- List rows ----

// ListColumns returns column widths (service, name, duration, age) for the
// given total width.
func ListColumns(width int) (svc, name, dur, age int) {
	svc = 14
	if width < 50 {
		svc = 10
	}
	dur = 8
	age = 5
	name = max(width-svc-dur-age-3, 8)
	return
}

func ListHeader(width int) string {
	svcCol, nameCol, durCol, ageCol := ListColumns(width)
	return Header.Render(fmt.Sprintf("%-*s %-*s %*s %*s",
		svcCol, "SERVICE",
		nameCol, "NAME",
		durCol, "DUR",
		ageCol, "AGE"))
}

func ListRow(t source.TraceSummary, width int) string {
	svcCol, nameCol, durCol, ageCol := ListColumns(width)
	return fmt.Sprintf("%-*s %-*s %*s %*s",
		svcCol, Truncate(t.RootServiceName, svcCol),
		nameCol, Truncate(t.RootName, nameCol),
		durCol, FmtDur(t.Duration),
		ageCol, FmtAge(t.Start))
}

// ---- Span rows ----

// SpanColumns returns column widths (name, duration, bar) for the span tree.
func SpanColumns(width int) (name, dur, bar int) {
	name = max(width*55/100, 16)
	dur = 8
	bar = max(width-name-dur-2, 6)
	return
}

func SpanTreeHeader(width int) string {
	nameCol, durCol, barCol := SpanColumns(width)
	return Header.Render(fmt.Sprintf("%-*s %*s %-*s",
		nameCol, "SPAN",
		durCol, "DUR",
		barCol, "TIMELINE"))
}

// SpanRow renders a single tree row. The caller is responsible for any
// selection highlighting.
func SpanRow(n TreeNode, traceStart time.Time, total time.Duration, width int) string {
	nameCol, durCol, barCol := SpanColumns(width)
	nameCell := PadDisplay(Truncate(StatusGlyph(n.Span.StatusCode)+" "+treePrefix(n.Depth)+n.Span.Name, nameCol), nameCol)
	durCell := fmt.Sprintf("%*s", durCol, FmtDur(n.Span.Duration))
	bar := Bar(n.Span.Start.Sub(traceStart), n.Span.Duration, total, barCol, n.Span.Service)
	return nameCell + " " + durCell + " " + bar
}

// SpanTree renders the full tree as a newline-joined block of rows
// (header + rows). Used by the headless CLI; the TUI walks its own loop so it
// can apply per-row selection styling.
func SpanTree(t *source.Trace, width int) string {
	tree := BuildTree(t.Spans)
	if len(tree) == 0 {
		return Muted.Render("(no spans)")
	}
	total := TraceBounds(t.Spans)
	start := TraceStartTime(t.Spans)

	out := []string{SpanTreeHeader(width)}
	for _, n := range tree {
		out = append(out, SpanRow(n, start, total, width))
	}
	return strings.Join(out, "\n")
}

// ---- Attributes ----

func Attributes(s *source.Span, width int) string {
	if s == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(Header.Render("ATTRIBUTES · " + Truncate(s.Name, width-16)))
	sb.WriteString("\n")
	keys := make([]string, 0, len(s.Attributes))
	for k := range s.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	keyW := 0
	for _, k := range keys {
		if w := lipgloss.Width(k); w > keyW {
			keyW = w
		}
	}
	if max := width / 2; keyW > max {
		keyW = max
	}
	for _, k := range keys {
		label := k
		if lipgloss.Width(label) > keyW {
			label = Truncate(label, keyW)
		}
		pad := keyW - lipgloss.Width(label)
		line := Muted.Render(label+strings.Repeat(" ", pad)) + "  " + s.Attributes[k]
		sb.WriteString(Truncate(line, width))
		sb.WriteString("\n")
	}
	if s.StatusMsg != "" {
		sb.WriteString(ErrSty.Render("status: ") + s.StatusMsg + "\n")
	}
	if len(s.Events) > 0 {
		sb.WriteString("\n" + Header.Render("EVENTS") + "\n")
		for _, e := range s.Events {
			sb.WriteString(Muted.Render(e.Time.Format("15:04:05.000")+"  ") + e.Name + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ---- Full trace (preview-style) ----

// Trace renders a complete trace preview: a one-line header, the services
// involved, the span tree, and attributes for the root span. Meant for
// piping into tools like Television or fzf as a preview command.
func Trace(t *source.Trace, width int) string {
	if t == nil {
		return ErrSty.Render("no trace")
	}
	services := make([]string, 0, len(t.Services))
	for s := range t.Services {
		services = append(services, s)
	}
	sort.Strings(services)

	total := TraceBounds(t.Spans)
	start := TraceStartTime(t.Spans)

	var sb strings.Builder
	sb.WriteString(Hot.Render(ShortID(t.TraceID)))
	sb.WriteString(Muted.Render(fmt.Sprintf("  %d spans  %s  ·  %s (%s ago)",
		len(t.Spans), FmtDur(total), FmtStamp(start), FmtAge(start))))
	sb.WriteString("\n")
	sb.WriteString(Muted.Render("services: ") + Truncate(strings.Join(services, ", "), width-10))
	sb.WriteString("\n\n")
	sb.WriteString(SpanTree(t, width))

	tree := BuildTree(t.Spans)
	if len(tree) > 0 {
		sb.WriteString("\n\n")
		sb.WriteString(Attributes(tree[0].Span, width))
	}
	return sb.String()
}

// ShortID trims a trace ID for display.
func ShortID(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:16]
}
