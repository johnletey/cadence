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

type ListCmd struct {
	Query  string        `help:"TraceQL query." default:"{}" placeholder:"TRACEQL"`
	Limit  int           `help:"Maximum number of traces." default:"50"`
	Last   time.Duration `help:"Time window ending now." default:"1h" placeholder:"DUR"`
	Format string        `help:"Output format." enum:"tsv,json,table" default:"tsv"`
}

func (c *ListCmd) Run(g *Globals) error {
	b, _, _, err := resolveSource(g, 0)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	end := time.Now()
	start := end.Add(-c.Last)
	results, err := b.Search(ctx, source.SearchQuery{
		Query: c.Query,
		Start: start,
		End:   end,
		Limit: c.Limit,
	})
	if err != nil {
		return err
	}

	switch c.Format {
	case "tsv":
		for _, t := range results {
			fmt.Printf("%s\t%s\t%s\t%d\t%s\t%d\n",
				t.TraceID,
				t.RootServiceName,
				t.RootName,
				t.Duration.Milliseconds(),
				t.Start.Format(time.RFC3339Nano),
				t.SpanCount,
			)
		}
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		payload := make([]map[string]any, 0, len(results))
		for _, t := range results {
			payload = append(payload, map[string]any{
				"trace_id":    t.TraceID,
				"service":     t.RootServiceName,
				"name":        t.RootName,
				"duration_ms": t.Duration.Milliseconds(),
				"start":       t.Start.Format(time.RFC3339Nano),
				"span_count":  t.SpanCount,
			})
		}
		return enc.Encode(payload)
	case "table":
		applyColorDefault("auto")
		width := detectWidth()
		fmt.Println(render.ListHeader(width))
		for _, t := range results {
			fmt.Println(render.ListRow(t, width))
		}
	}
	return nil
}
