package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/johnletey/cadence/internal/render"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(render.ColorAccent).
			Padding(0, 1)

	crumbStyle = lipgloss.NewStyle().Foreground(render.ColorMuted)

	selectedRow = lipgloss.NewStyle().
			Background(render.ColorHighlite).
			Foreground(lipgloss.Color("#FFFFFF"))

	selectedDim = lipgloss.NewStyle().
			Background(render.ColorSubtle).
			Foreground(lipgloss.Color("#BDBDCC"))
)
