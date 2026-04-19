package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnletey/cadence/internal/tui"
)

type TUICmd struct {
	Refresh time.Duration `help:"Auto-refresh interval (min 250ms). Overrides config. Default 2s." placeholder:"DUR"`
}

func (c *TUICmd) Run(g *Globals) error {
	b, name, refreshInterval, err := resolveSource(g, c.Refresh)
	if err != nil {
		return err
	}

	p := tea.NewProgram(tui.New(b, name, refreshInterval), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
