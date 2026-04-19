package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/johnletey/cadence/internal/render"
	"github.com/johnletey/cadence/internal/source"
)

const (
	defaultQuery      = "{}"
	cursorSettleDelay = 150 * time.Millisecond
	clockTickEvery    = 1 * time.Second
	minRefreshEvery   = 250 * time.Millisecond
)

type focus int

const (
	focusList focus = iota
	focusDetail
)

type Model struct {
	src     source.Source
	srcName string

	// Query overlay
	input     textinput.Model
	searching bool

	// List state
	results    []source.TraceSummary
	listCursor int
	listOffset int
	lastQuery  string
	loading    bool

	// Detail state
	trace           *source.Trace
	tree            []render.TreeNode
	detailCursor    int
	detailOffset    int
	loadingTrace    bool
	loadingTraceID  string
	cache           map[string]*source.Trace
	settleGen       int
	refreshGen      int
	autoRefresh     bool
	refreshInterval time.Duration

	// Shared
	focus        focus
	spin         spinner.Model
	err          error
	width        int
	height       int
	gPending     bool // first `g` of a `gg` jump-to-top
	ctrlWPending bool // `ctrl-w` window-navigation prefix
}

func New(b source.Source, srcName string, refreshInterval time.Duration) Model {
	if refreshInterval < minRefreshEvery {
		refreshInterval = minRefreshEvery
	}
	ti := textinput.New()
	ti.Placeholder = `TraceQL — e.g. { resource.service.name = "frontend" }`
	ti.Prompt = "/ "
	ti.CharLimit = 512

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(render.ColorAccent2)

	return Model{
		src:             b,
		srcName:         srcName,
		input:           ti,
		spin:            sp,
		focus:           focusList,
		loading:         true,
		lastQuery:       defaultQuery,
		cache:           map[string]*source.Trace{},
		autoRefresh:     true,
		refreshInterval: refreshInterval,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.doSearch(defaultQuery, false),
		m.spin.Tick,
		textinput.Blink,
		m.scheduleAutoRefresh(m.refreshGen),
		clockTick(),
	)
}

// ---- Messages ----

type searchDoneMsg struct {
	q    string
	data []source.TraceSummary
	err  error
	auto bool
}

type traceDoneMsg struct {
	id      string
	data    *source.Trace
	err     error
	display bool // if false, treat as a prefetch (cache-only)
}

type cursorSettleMsg struct {
	gen int
	id  string
}

type autoRefreshMsg struct {
	gen int
}

type clockTickMsg struct{}

func clockTick() tea.Cmd {
	return tea.Tick(clockTickEvery, func(_ time.Time) tea.Msg { return clockTickMsg{} })
}

func (m Model) doSearch(q string, auto bool) tea.Cmd {
	b := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		end := time.Now()
		start := end.Add(-1 * time.Hour)
		r, err := b.Search(ctx, source.SearchQuery{
			Query: q,
			Start: start,
			End:   end,
			Limit: 50,
		})
		return searchDoneMsg{q: q, data: r, err: err, auto: auto}
	}
}

func (m Model) doGetTrace(id string, display bool) tea.Cmd {
	b := m.src
	lookup := m.lookupFor(id)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		t, err := b.GetTrace(ctx, id, lookup)
		return traceDoneMsg{id: id, data: t, err: err, display: display}
	}
}

// lookupFor builds a time-range hint from the matching search summary, if we
// have one. Backends that need a time window to find the trace get one;
// backends that don't care ignore it.
func (m Model) lookupFor(id string) source.TraceLookup {
	for _, r := range m.results {
		if r.TraceID == id && !r.Start.IsZero() {
			// Pad by an hour on each side. Summary start times aren't
			// span-accurate and a trace can straddle a storage boundary.
			return source.TraceLookup{
				Start: r.Start.Add(-1 * time.Hour),
				End:   r.Start.Add(r.Duration).Add(1 * time.Hour),
			}
		}
	}
	return source.TraceLookup{}
}

func (m Model) scheduleSettle(gen int, id string) tea.Cmd {
	return tea.Tick(cursorSettleDelay, func(_ time.Time) tea.Msg {
		return cursorSettleMsg{gen: gen, id: id}
	})
}

func (m Model) scheduleAutoRefresh(gen int) tea.Cmd {
	return tea.Tick(m.refreshInterval, func(_ time.Time) tea.Msg {
		return autoRefreshMsg{gen: gen}
	})
}

// selectCurrent shows the cached trace when we have one, or kicks off a
// fetch. On a cache hit it also warms the neighbouring rows.
func (m Model) selectCurrent(moveFocus bool) (Model, tea.Cmd) {
	if len(m.results) == 0 {
		return m, nil
	}
	id := m.results[m.listCursor].TraceID
	if t, ok := m.cache[id]; ok {
		m.trace = t
		m.tree = render.BuildTree(t.Spans)
		m.detailCursor = 0
		m.detailOffset = 0
		m.loadingTrace = false
		m.loadingTraceID = ""
		if moveFocus {
			m.focus = focusDetail
		}
		return m, m.prefetchNeighbours()
	}
	m.loadingTrace = true
	m.loadingTraceID = id
	if moveFocus {
		m.focus = focusDetail
	}
	return m, tea.Batch(m.doGetTrace(id, true), m.spin.Tick)
}

func (m Model) prefetchNeighbours() tea.Cmd {
	cmds := []tea.Cmd{}
	for _, off := range []int{-1, 1, 2} {
		i := m.listCursor + off
		if i < 0 || i >= len(m.results) {
			continue
		}
		id := m.results[i].TraceID
		if _, ok := m.cache[id]; ok {
			continue
		}
		cmds = append(cmds, m.doGetTrace(id, false))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// ---- Update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(20, msg.Width-6)
		return m, nil

	case tea.KeyMsg:
		if m.searching {
			return m.updateSearchInput(msg)
		}
		return m.updateMain(msg)

	case searchDoneMsg:
		if !msg.auto {
			m.loading = false
		}
		if msg.err != nil {
			if !msg.auto {
				m.err = msg.err
			}
			return m, nil
		}
		m.err = nil
		if msg.auto {
			wasEmpty := len(m.results) == 0
			m = m.mergeResults(msg.data)
			if wasEmpty && len(m.results) > 0 {
				m.settleGen++
				return m, m.scheduleSettle(m.settleGen, currentID(m))
			}
			return m, nil
		}
		m.results = msg.data
		m.lastQuery = msg.q
		m.listCursor = 0
		m.listOffset = 0
		m.settleGen++
		return m, tea.Batch(
			m.scheduleSettle(m.settleGen, currentID(m)),
			m.prefetchNeighbours(),
		)

	case traceDoneMsg:
		if msg.err == nil && msg.data != nil {
			m.cache[msg.id] = msg.data
		}
		if !msg.display {
			return m, nil
		}
		// Stale response: user already moved on to a different trace.
		if msg.id != m.loadingTraceID {
			return m, nil
		}
		m.loadingTrace = false
		if msg.err != nil {
			m.err = msg.err
			m.trace = nil
			m.tree = nil
			return m, nil
		}
		m.err = nil
		m.trace = msg.data
		m.tree = render.BuildTree(m.trace.Spans)
		m.detailCursor = 0
		m.detailOffset = 0
		return m, m.prefetchNeighbours()

	case cursorSettleMsg:
		if msg.gen != m.settleGen {
			return m, nil // superseded by a newer cursor move
		}
		if currentID(m) != msg.id {
			return m, nil
		}
		var cmd tea.Cmd
		m, cmd = m.selectCurrent(false)
		return m, cmd

	case autoRefreshMsg:
		if msg.gen != m.refreshGen {
			return m, nil
		}
		next := m.scheduleAutoRefresh(m.refreshGen)
		if !m.autoRefresh || m.searching || m.loading {
			return m, next
		}
		return m, tea.Batch(m.doSearch(m.lastQuery, true), next)

	case clockTickMsg:
		return m, clockTick()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func currentID(m Model) string {
	if m.listCursor < 0 || m.listCursor >= len(m.results) {
		return ""
	}
	return m.results[m.listCursor].TraceID
}

// mergeResults folds an auto-refresh payload into the current list without
// reshuffling it. Traces are immutable, so new IDs get prepended and the
// cursor stays on whichever trace it was already pointing at.
func (m Model) mergeResults(incoming []source.TraceSummary) Model {
	seen := make(map[string]struct{}, len(m.results))
	for _, r := range m.results {
		seen[r.TraceID] = struct{}{}
	}
	newOnes := make([]source.TraceSummary, 0, len(incoming))
	for _, r := range incoming {
		if _, ok := seen[r.TraceID]; ok {
			continue
		}
		newOnes = append(newOnes, r)
	}
	if len(newOnes) == 0 {
		return m
	}
	sort.SliceStable(newOnes, func(i, j int) bool { return newOnes[i].Start.After(newOnes[j].Start) })

	wasEmpty := len(m.results) == 0
	m.results = append(newOnes, m.results...)
	if wasEmpty {
		m.listCursor = 0
		m.listOffset = 0
		return m
	}
	// Keep the cursor on the same trace. Its index shifts down by len(newOnes)
	// so the selected trace doesn't change underneath the user.
	m.listCursor += len(newOnes)
	return m
}

func (m Model) updateSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.input.Blur()
		return m, nil
	case "enter":
		q := strings.TrimSpace(m.input.Value())
		if q == "" {
			q = defaultQuery
		}
		m.searching = false
		m.input.Blur()
		m.loading = true
		m.err = nil
		return m, tea.Batch(m.doSearch(q, false), m.spin.Tick)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	// `ctrl-w {h,l,w}` — vim window navigation.
	if m.ctrlWPending {
		m.ctrlWPending = false
		m.gPending = false
		switch k {
		case "h":
			m.focus = focusList
			return m, nil
		case "l":
			m.focus = focusDetail
			return m, nil
		case "w":
			if m.focus == focusList {
				m.focus = focusDetail
			} else {
				m.focus = focusList
			}
			return m, nil
		}
		// anything else cancels the prefix; fall through.
	}

	switch k {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "ctrl+w":
		m.ctrlWPending = true
		m.gPending = false
		return m, nil
	case "/":
		m.gPending = false
		m.searching = true
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "r":
		m.gPending = false
		if m.loading {
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.doSearch(m.lastQuery, false), m.spin.Tick)
	case "p":
		m.gPending = false
		m.autoRefresh = !m.autoRefresh
		return m, nil
	case "tab":
		m.gPending = false
		if m.focus == focusList {
			m.focus = focusDetail
		} else {
			m.focus = focusList
		}
		return m, nil
	case "left":
		m.gPending = false
		m.focus = focusList
		return m, nil
	case "right":
		m.gPending = false
		m.focus = focusDetail
		return m, nil
	}

	if m.focus == focusList {
		return m.updateList(msg)
	}
	return m.updateDetail(msg)
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	moved := false
	page := m.paneBodyHeight()
	half := max(1, page/2)

	// `gg` jump-to-top (vim). First `g` arms the prefix; second `g` fires.
	if m.gPending {
		m.gPending = false
		if k == "g" {
			if m.listCursor != 0 {
				m.listCursor = 0
				m.settleGen++
				return m, m.scheduleSettle(m.settleGen, currentID(m))
			}
			return m, nil
		}
		// fall through to handle the key normally
	}

	switch k {
	case "up", "k":
		if m.listCursor > 0 {
			m.listCursor--
			moved = true
		}
	case "down", "j":
		if m.listCursor < len(m.results)-1 {
			m.listCursor++
			moved = true
		}
	case "g":
		m.gPending = true
		return m, nil
	case "home":
		if m.listCursor != 0 {
			m.listCursor = 0
			moved = true
		}
	case "end", "G":
		if len(m.results) > 0 && m.listCursor != len(m.results)-1 {
			m.listCursor = len(m.results) - 1
			moved = true
		}
	case "ctrl+u":
		prev := m.listCursor
		m.listCursor = max(0, min(m.listCursor-half, len(m.results)-1))
		moved = m.listCursor != prev
	case "ctrl+d":
		prev := m.listCursor
		m.listCursor = max(0, min(m.listCursor+half, len(m.results)-1))
		moved = m.listCursor != prev
	case "pgup", "ctrl+b":
		prev := m.listCursor
		m.listCursor = max(0, min(m.listCursor-page, len(m.results)-1))
		moved = m.listCursor != prev
	case "pgdown", "ctrl+f":
		prev := m.listCursor
		m.listCursor = max(0, min(m.listCursor+page, len(m.results)-1))
		moved = m.listCursor != prev
	case "enter":
		var cmd tea.Cmd
		m, cmd = m.selectCurrent(true)
		return m, cmd
	}
	if moved {
		m.settleGen++
		return m, m.scheduleSettle(m.settleGen, currentID(m))
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	page := m.paneBodyHeight()
	half := max(1, page/2)

	if m.gPending {
		m.gPending = false
		if k == "g" {
			m.detailCursor = 0
			return m, nil
		}
	}

	switch k {
	case "esc":
		m.focus = focusList
		return m, nil
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
	case "down", "j":
		if m.detailCursor < len(m.tree)-1 {
			m.detailCursor++
		}
	case "g":
		m.gPending = true
	case "home":
		m.detailCursor = 0
	case "end", "G":
		if len(m.tree) > 0 {
			m.detailCursor = len(m.tree) - 1
		}
	case "ctrl+u":
		m.detailCursor = max(0, min(m.detailCursor-half, len(m.tree)-1))
	case "ctrl+d":
		m.detailCursor = max(0, min(m.detailCursor+half, len(m.tree)-1))
	case "pgup", "ctrl+b":
		m.detailCursor = max(0, min(m.detailCursor-page, len(m.tree)-1))
	case "pgdown", "ctrl+f":
		m.detailCursor = max(0, min(m.detailCursor+page, len(m.tree)-1))
	}
	return m, nil
}

// ---- View ----

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(m.renderHeader())
	sb.WriteString("\n")
	if m.searching {
		sb.WriteString(m.input.View())
		sb.WriteString("\n")
	}

	sb.WriteString(m.renderBody())
	sb.WriteString("\n")
	sb.WriteString(m.renderFooter())
	return sb.String()
}

func (m Model) renderHeader() string {
	title := titleStyle.Render(" cadence ")
	be := crumbStyle.Render(fmt.Sprintf("[%s]", m.srcName))
	q := crumbStyle.Render("q=" + m.lastQuery)

	status := ""
	switch {
	case m.loading:
		status = m.spin.View() + render.Muted.Render(" searching")
	case m.loadingTrace:
		status = m.spin.View() + render.Muted.Render(" loading trace")
	case m.err != nil:
		status = render.ErrSty.Render("error: " + render.Truncate(m.err.Error(), max(20, m.width-40)))
	default:
		extra := ""
		if m.autoRefresh {
			extra = " · live " + m.refreshInterval.String()
		}
		status = render.Muted.Render(fmt.Sprintf("%d results%s", len(m.results), extra))
	}
	return title + " " + be + "  " + q + "  " + crumbStyle.Render("·") + "  " + status
}

func (m Model) renderFooter() string {
	keys := []string{}
	if m.searching {
		keys = append(keys, "enter run", "esc cancel")
	} else {
		auto := "auto:on"
		if !m.autoRefresh {
			auto = "auto:off"
		}
		keys = append(keys,
			"j/k move",
			"gg/G top/bot",
			"tab "+focusToggleHint(m.focus),
			"/ query",
			"r refresh",
			"p "+auto,
			"q quit",
		)
	}
	return render.Muted.Render(strings.Join(keys, "  ·  "))
}

func focusToggleHint(f focus) string {
	if f == focusList {
		return "→ trace"
	}
	return "→ list"
}

// ---- Body (split pane) ----

func (m Model) paneBodyHeight() int {
	// header(1) + optional search(1) + blank(0) + footer(1) + separator newline
	reserved := 3
	if m.searching {
		reserved++
	}
	return max(3, m.height-reserved)
}

const stackBreakpoint = 100

func (m Model) renderBody() string {
	h := m.paneBodyHeight()
	if m.width < stackBreakpoint {
		return m.renderBodyVertical(h)
	}
	return m.renderBodyHorizontal(h)
}

func (m Model) renderBodyHorizontal(h int) string {
	leftW, rightW := m.paneWidths()

	leftContent := m.renderListPane(leftW, h)
	rightContent := m.renderDetailPane(rightW, h)

	leftStyle := lipgloss.NewStyle().Width(leftW).Height(h)
	rightStyle := lipgloss.NewStyle().Width(rightW).Height(h)
	sepStyle := lipgloss.NewStyle().Foreground(render.ColorSubtle).Height(h)

	sep := sepStyle.Render(strings.Repeat("│\n", h))
	return lipgloss.JoinHorizontal(lipgloss.Top,
		leftStyle.Render(leftContent),
		sep,
		rightStyle.Render(rightContent),
	)
}

func (m Model) renderBodyVertical(h int) string {
	topH, bottomH := m.paneHeights(h)
	w := m.width

	topContent := m.renderListPane(w, topH)
	bottomContent := m.renderDetailPane(w, bottomH)

	topStyle := lipgloss.NewStyle().Width(w).Height(topH)
	bottomStyle := lipgloss.NewStyle().Width(w).Height(bottomH)
	sep := lipgloss.NewStyle().Foreground(render.ColorSubtle).Render(strings.Repeat("─", w))

	return lipgloss.JoinVertical(lipgloss.Left,
		topStyle.Render(topContent),
		sep,
		bottomStyle.Render(bottomContent),
	)
}

func (m Model) paneHeights(total int) (int, int) {
	// one row consumed by the horizontal separator
	available := max(total-1, 6)
	// List keeps fewer rows than the detail pane. Once a trace is loaded,
	// the detail is what you're reading.
	top := max(3, min(available-8, min(12, max(5, available*35/100))))
	return top, available - top
}

func (m Model) paneWidths() (int, int) {
	// separator takes 1 column
	available := max(m.width-1, 40)
	left := max(20, min(available-30, min(60, max(32, available*40/100))))
	return left, available - left
}

// ---- Left pane: trace list ----

func (m Model) renderListPane(width, height int) string {
	if m.loading && len(m.results) == 0 {
		return render.Muted.Render("loading traces…")
	}
	if len(m.results) == 0 {
		return render.Muted.Render("No traces in the last hour.\nPress / to change the query, r to retry.")
	}

	lines := []string{render.ListHeader(width)}

	body := max(1, height-1)
	m.listOffset = clampOffset(m.listCursor, m.listOffset, body)
	end := min(m.listOffset+body, len(m.results))

	for i := m.listOffset; i < end; i++ {
		row := render.ListRow(m.results[i], width)
		if i == m.listCursor {
			if m.focus == focusList {
				row = selectedRow.Render(row)
			} else {
				row = selectedDim.Render(row)
			}
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

// ---- Right pane: trace detail ----

func (m Model) renderDetailPane(width, height int) string {
	if m.loadingTrace {
		return render.Muted.Render("loading trace " + render.ShortID(m.loadingTraceID) + "…")
	}
	if m.trace == nil {
		if len(m.results) == 0 {
			return ""
		}
		return render.Muted.Render("Press enter on a trace to view its spans.")
	}

	services := make([]string, 0, len(m.trace.Services))
	for s := range m.trace.Services {
		services = append(services, s)
	}
	sort.Strings(services)

	total := render.TraceBounds(m.trace.Spans)
	start := render.TraceStartTime(m.trace.Spans)

	var sb strings.Builder
	sb.WriteString(render.Hot.Render(render.ShortID(m.trace.TraceID)))
	meta := fmt.Sprintf("  %d spans  %s  ·  %s (%s ago)",
		len(m.trace.Spans), render.FmtDur(total), render.FmtStamp(start), render.FmtAge(start))
	sb.WriteString(render.Muted.Render(meta))
	sb.WriteString("\n")
	sb.WriteString(render.Muted.Render("services: ") + render.Truncate(strings.Join(services, ", "), width-10))
	sb.WriteString("\n\n")

	reserved := 3 // 2 header lines + blank
	attrBlock := ""
	if m.detailCursor < len(m.tree) {
		attrBlock = render.Attributes(m.tree[m.detailCursor].Span, width)
		reserved += 2 + strings.Count(attrBlock, "\n")
	}
	bodyH := max(1, height-reserved)

	rows := m.renderSpanTree(total, width, bodyH)
	m.detailOffset = clampOffset(m.detailCursor, m.detailOffset, bodyH)
	visibleStart := 1 // row 0 is the header row
	// rows[0] is header; treat cursor-relative offset against the data rows
	dataRows := rows[visibleStart:]
	offset := m.detailOffset
	if offset > len(dataRows) {
		offset = 0
	}
	end := min(offset+bodyH, len(dataRows))
	sb.WriteString(rows[0])
	sb.WriteString("\n")
	sb.WriteString(strings.Join(dataRows[offset:end], "\n"))

	if attrBlock != "" {
		sb.WriteString("\n\n")
		sb.WriteString(attrBlock)
	}
	return sb.String()
}

func (m Model) renderSpanTree(total time.Duration, width, _ int) []string {
	if len(m.tree) == 0 {
		return []string{render.Muted.Render("(no spans)")}
	}
	out := []string{render.SpanTreeHeader(width)}
	traceStart := render.TraceStartTime(m.trace.Spans)
	for i, n := range m.tree {
		row := render.SpanRow(n, traceStart, total, width)
		if i == m.detailCursor {
			if m.focus == focusDetail {
				row = selectedRow.Render(row)
			} else {
				row = selectedDim.Render(row)
			}
		}
		out = append(out, row)
	}
	return out
}

// ---- Helpers ----

func clampOffset(cursor, offset, page int) int {
	if page <= 0 {
		return 0
	}
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+page {
		return cursor - page + 1
	}
	return offset
}
