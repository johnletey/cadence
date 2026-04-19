# cadence

OTLP traces in your terminal. Browse with the TUI, pipe with the CLI.

Point it at a Tempo instance and you get a fuzzy-findable trace list with span trees on the right. There's also a headless CLI for wiring into [Television](https://github.com/alexpasmantier/television), [fzf](https://github.com/junegunn/fzf), or shell pipelines.

Tempo is the only backend wired up right now. The interface is generic, so Jaeger, SigNoz, or any other trace store is fair game; you just need to write the search + fetch layer. Trace decoding is already shared for anything that speaks OTLP. PRs welcome.

## What it looks like

```
 cadence  [tempo]  q={}  ·  50 results · live 2s
 SERVICE        NAME                                 DUR      AGE  │ a1b2c3d4e5f6a7b8  12 spans  48.21ms  ·  14:02:11 (5s ago)
 frontend       GET /api/checkout              12.34ms     5s      │ services: frontend, checkout, inventory
 checkout       POST /order/submit             45.21ms    12s      │ SPAN                                   DUR  TIMELINE
 inventory      SELECT inventory WHERE ...      2.11ms    34s      │ ● GET /api/checkout                12.34ms  ━━━━━━────────────
 ...                                                               │   ● └─ POST /order/submit           45.21ms  ────━━━━━━━━━━━━━━
                                                                   │     ● └─ SELECT inventory WHERE…    2.11ms  ────────────━─────
                                                                   │
                                                                   │ ATTRIBUTES · GET /api/checkout
                                                                   │ http.method          GET
                                                                   │ http.status_code     200
                                                                   │ http.route           /api/checkout
                                                                   │ user.id              u_8c1a
 j/k move  ·  gg/G top/bot  ·  tab → trace  ·  / query  ·  r refresh  ·  p auto:on  ·  q quit
```

Left pane is the live trace list. Right pane is the selected trace: header, services, span tree with a waterfall, and attributes for whichever span is highlighted.

## Install

Grab a binary from the [releases page](https://github.com/johnletey/cadence/releases) (macOS and Linux, amd64/arm64), or install from source:

```sh
go install github.com/johnletey/cadence@latest
```

## Quick start

Point it at a local Tempo:

```sh
cadence --url http://localhost:3200
```

Default query is `{}`, window is the last hour, auto-refresh every 2 seconds.

## Config file

For anything past a single local Tempo, put a YAML file at `~/.config/cadence/config.yaml` (or wherever `$XDG_CONFIG_HOME/cadence/config.yaml` resolves). Example:

```yaml
default_source: prod
refresh: 2s                # global default, overridden per-source or by --refresh

sources:
  prod:
    type: tempo
    url: https://tempo.prod.example.com
    headers:
      X-Scope-OrgID: "1"
    refresh: 5s            # prod gets a gentler refresh

  local:
    type: tempo
    url: http://localhost:3200

  grafana-cloud:
    type: tempo
    url: https://tempo-us-central1.grafana.net
    user: "12345"          # basic auth (your stack id)
    pass: "glc_…"          # your Grafana Cloud API token
```

A *source* is a configured connection. Each has a `type` naming its backend (only `tempo` today). Pick one at launch with `--source local`. Omit the flag and cadence uses `default_source`.

Refresh precedence, specific beats general: `--refresh` flag, then the source's `refresh`, then the top-level `refresh`, then 2s.

## The TUI

Run `cadence` with no subcommand (or `cadence tui` if you prefer being explicit).

### Layout

Above 100 columns, horizontal split with the list on the left. Below 100, vertical stack with the list on top. I picked 100 because narrower made the detail pane too cramped to be useful. The breakpoint isn't configurable yet.

### Keys

Vim-ish where it fits:

```
↑/↓, j/k                   move cursor in the focused pane
gg, G                      jump to top / bottom
ctrl-u, ctrl-d             half-page up / down
ctrl-b, ctrl-f, pgup/pgdn  full-page up / down
←/→                        focus list / detail pane
ctrl-w h, ctrl-w l         focus list / detail pane (vim-style)
ctrl-w w, tab              toggle focus
enter                      load the selected trace into the right pane
/                          edit the TraceQL query
r                          manual refresh
p                          pause/resume auto-refresh
q, ctrl-c                  quit
esc                        close query input, or move focus back to list
```

### How the right pane fills in

Cursor moves in the list debounce for 150ms, then load the trace. Neighbours (two above, two below) prefetch in the background and cache, so scrolling through a region you've already visited is instant. Cache is per-session.

The attribute pane always tracks the highlighted span, not the trace root. Hit `tab` or `→` to move into the tree, walk down, and attributes update.

### Auto-refresh without the cursor jumping

Tempo traces are immutable once ingested, so new entries prepend to the top of the list and nothing else moves. Your cursor stays on the trace it was on; its index shifts down, but the selection and detail pane don't reload. If the list was empty and the first trace arrives, the cursor lands on row 0.

The header shows `· live 2s` while auto-refresh is running, so you can see if it's on and how often it fires.

## The CLI (headless mode)

Three subcommands for scripts and pipes. Each shares source-selection flags with the TUI (`--config`, `--source`, `--url`, `--header`, `--basic-auth`).

### `cadence list`

Prints matching traces, one per line. Built for pipe consumption.

```sh
cadence list                              # last hour, default {} query
cadence list --query '{ status = error }' # only failed traces
cadence list --last 24h --limit 200       # wider window
cadence list --format json | jq           # structured output
cadence list --format table               # styled table, human readable
```

TSV columns, tab-separated:

```
trace_id   service   name   duration_ms   start_rfc3339   span_count
```

### `cadence show <id>`

Prints a single trace as structured data.

```sh
cadence show a1b2c3d4...               # JSON (default)
cadence show a1b2c3d4... --format tree # plain text tree, no color
cadence show a1b2c3d4... --last 7d     # widen lookup window
```

Tempo needs a time hint for the lookup to reach block storage rather than just the ingester, so `show` defaults to a 72-hour window. Bump it with `--last` if the trace is older.

### `cadence preview <id>`

Styled render of the trace, same layout as the TUI's right pane but as a one-shot printout. Defaults to `--color=always` because the usual consumer is a preview pane in another tool.

```sh
cadence preview a1b2c3d4...
cadence preview a1b2c3d4... --width 120
cadence preview a1b2c3d4... --color never | less # plain output
```

## Backends

Cadence separates a *source* (a configured connection: URL, headers, auth) from a *backend* (what's on the other end). Tempo, Jaeger, and SigNoz are obvious candidates, but nothing in the design is tied to any of them. A source's `type` names its backend.

Tempo is the only one today. Adding more:

- `internal/source/` holds the `Source` interface and each backend as a subpackage (`source/tempo/`, `source/jaeger/`, ...).
- `internal/otlp/` is a shared OTLP-JSON decoder. If the backend returns OTLP, a new implementation can call `otlp.DecodeTrace` and skip re-parsing spans and attributes. If it doesn't, you'll need a format-specific decoder alongside.

Search APIs are a separate problem. OTLP standardises the trace data model, not the query endpoint. Tempo gives us TraceQL and `/api/search`; other backends each bring their own search layer.

## Architecture

```
internal/
  source/         Source interface + backend implementations
    source.go
    tempo/        Tempo HTTP client (search + trace fetch)
  otlp/           Shared OTLP JSON decoder for trace payloads
  config/         YAML config loader, Duration type
  render/         Styles, span tree, waterfall bars, list rows, attribute block
  tui/            Bubble Tea model
cmd_tui.go        Launches the TUI
cmd_list.go       cadence list
cmd_show.go       cadence show
cmd_preview.go    cadence preview
resolver.go       Source resolution + refresh-interval precedence
main.go           Subcommand dispatch
```

Rendering lives in one place so the TUI and `preview` can't drift. The TUI adds selection highlighting on top; the CLI just prints the raw rendered output.

## Why "cadence"

Traces are about timing: when things happen and how long they take. "Cadence" is the word for that rhythm in prose and music.

## License

Apache-2.0. See `LICENSE`.
