package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"

	"github.com/johnletey/cadence/internal/render"
	"github.com/johnletey/cadence/internal/source"
)

type PreviewCmd struct {
	TraceID string        `arg:"" help:"Trace ID to render."`
	Color   string        `help:"Color output." enum:"always,auto,never" default:"always"`
	Width   int           `help:"Output width in columns (0 = auto-detect)." default:"0"`
	Last    time.Duration `help:"How far back to look (some backends need a time hint for block storage)." default:"72h" placeholder:"DUR"`
}

func (c *PreviewCmd) Run(g *Globals) error {
	applyColorDefault(c.Color)

	b, _, _, err := resolveSource(g, 0)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	end := time.Now()
	trace, err := b.GetTrace(ctx, c.TraceID, source.TraceLookup{
		Start: end.Add(-c.Last),
		End:   end,
	})
	if err != nil {
		return err
	}

	w := c.Width
	if w == 0 {
		w = detectWidth()
	}
	fmt.Println(render.Trace(trace, w))
	return nil
}

// ---- shared cli helpers ----

// applyColorDefault overrides lipgloss's color profile. "auto" keeps the
// built-in autodetect (TTY gets color, pipe gets stripped). "always" forces
// true color. "never" strips everything.
func applyColorDefault(mode string) {
	switch mode {
	case "always":
		lipgloss.SetColorProfile(termenv.TrueColor)
	case "never":
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// detectWidth returns the effective output width: $COLUMNS > stty on stdout >
// fallback 100.
func detectWidth() int {
	if s := os.Getenv("COLUMNS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 100
}
