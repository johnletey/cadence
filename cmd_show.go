package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/johnletey/cadence/internal/render"
	"github.com/johnletey/cadence/internal/source"
)

type ShowCmd struct {
	TraceID string        `arg:"" help:"Trace ID to fetch."`
	Format  string        `help:"Output format." enum:"json,tree" default:"json"`
	Last    time.Duration `help:"How far back to look (some backends need a time hint for block storage)." default:"72h" placeholder:"DUR"`
}

func (c *ShowCmd) Run(g *Globals) error {
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

	switch c.Format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(trace)
	case "tree":
		applyColorDefault("never")
		fmt.Println(render.Trace(trace, detectWidth()))
	}
	return nil
}
