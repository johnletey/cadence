package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
)

// Populated via ldflags at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Globals are the resolver-facing flags every subcommand accepts. They live
// at the top level of the CLI so kong applies them uniformly across
// subcommands and shows them in one help section.
type Globals struct {
	Config    string `help:"Path to config file (defaults to $CADENCE_CONFIG or ~/.config/cadence/config.yaml)." type:"path" placeholder:"PATH"`
	Source    string `help:"Source name defined in config (defaults to default_source)." placeholder:"NAME"`
	URL       string `help:"Ad-hoc source URL, overrides --config lookup." placeholder:"URL"`
	Type      string `help:"Backend type when using --url." enum:"tempo" default:"tempo" placeholder:"TYPE"`
	Header    string `help:"Extra HTTP header for --url, formatted 'Key: Value' (comma-separated)." placeholder:"'K: V'"`
	BasicAuth string `name:"basic-auth" help:"user:pass for --url." placeholder:"USER:PASS"`
}

// CLI is the root structure that kong parses into.
type CLI struct {
	Globals

	TUI     TUICmd     `cmd:"" name:"tui" default:"withargs" help:"Launch the interactive TUI (default)."`
	List    ListCmd    `cmd:"" name:"list" help:"Print matching traces, one per line."`
	Show    ShowCmd    `cmd:"" name:"show" help:"Print a single trace as structured data."`
	Preview PreviewCmd `cmd:"" name:"preview" help:"Render a styled trace for pipe consumers."`

	Version kong.VersionFlag `help:"Print version and exit."`
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("cadence"),
		kong.Description("OTLP traces in your terminal. Browse with the TUI, pipe with the CLI."),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}),
		kong.Vars{
			"version": fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		},
	)
	if err := ctx.Run(&cli.Globals); err != nil {
		fmt.Fprintln(os.Stderr, "cadence:", err)
		os.Exit(1)
	}
}
