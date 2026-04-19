package main

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/johnletey/cadence/internal/config"
	"github.com/johnletey/cadence/internal/source"
	"github.com/johnletey/cadence/internal/source/tempo"
)

const (
	defaultRefresh     = 2 * time.Second
	defaultBackendType = "tempo"
)

// supportedBackendTypes lists the backend kinds this build knows how to
// construct. Keep this in sync with the switch in buildSource and the enum
// on the --type CLI flag.
var supportedBackendTypes = []string{"tempo"}

// resolveSource builds a source.Source from either an ad-hoc --url or the
// config file lookup. Returns the source, a display name, and the effective
// auto-refresh interval (CLI > per-source config > global config > default).
func resolveSource(g *Globals, cliRefresh time.Duration) (source.Source, string, time.Duration, error) {
	if g.URL != "" {
		headers, err := parseHeaderFlag(g.Header)
		if err != nil {
			return nil, "", 0, err
		}
		user, pass, err := parseBasicAuthFlag(g.BasicAuth)
		if err != nil {
			return nil, "", 0, err
		}
		typ := cmp.Or(strings.ToLower(g.Type), defaultBackendType)
		s, err := buildSource(typ, g.URL, headers, user, pass)
		if err != nil {
			return nil, "", 0, err
		}
		return s, typ + " (" + g.URL + ")", pickRefresh(cliRefresh, 0, 0), nil
	}

	cfg, loaded, err := config.Load(g.Config)
	if err != nil {
		return nil, "", 0, err
	}
	if len(cfg.Sources) == 0 {
		return nil, "", 0, fmt.Errorf("no sources configured. Create %s or pass --url http://localhost:3200",
			cmp.Or(loaded, "~/.config/cadence/config.yaml"))
	}
	name := cmp.Or(g.Source, cfg.DefaultSource)
	sc, ok := cfg.Sources[name]
	if !ok {
		have := slices.Sorted(maps.Keys(cfg.Sources))
		return nil, "", 0, fmt.Errorf("unknown source %q (have: %s)", name, strings.Join(have, ", "))
	}
	typ := cmp.Or(strings.ToLower(sc.Type), defaultBackendType)
	s, err := buildSource(typ, sc.URL, sc.Headers, sc.User, sc.Pass)
	if err != nil {
		return nil, "", 0, fmt.Errorf("source %q: %w", name, err)
	}
	return s, name, pickRefresh(cliRefresh, sc.Refresh.Duration(), cfg.Refresh.Duration()), nil
}

// buildSource dispatches on the backend type. Unknown types fail here with
// a clear message rather than silently falling through to a default.
func buildSource(typ, url string, headers map[string]string, user, pass string) (source.Source, error) {
	switch typ {
	case "tempo":
		return tempo.New(tempo.Config{
			URL:     url,
			Headers: headers,
			User:    user,
			Pass:    pass,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported backend type %q (have: %s)", typ, strings.Join(supportedBackendTypes, ", "))
	}
}

// parseHeaderFlag turns the --header "Key: Value,Key: Value" form into a map.
func parseHeaderFlag(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, h := range strings.Split(raw, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header %q, want 'Key: Value'", h)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

// parseBasicAuthFlag splits the --basic-auth "user:pass" form.
func parseBasicAuthFlag(raw string) (string, string, error) {
	if raw == "" {
		return "", "", nil
	}
	u, p, ok := strings.Cut(raw, ":")
	if !ok {
		return "", "", fmt.Errorf("invalid --basic-auth, want user:pass")
	}
	return u, p, nil
}

// pickRefresh returns the first non-zero duration from the precedence chain:
// --refresh flag, per-source config, top-level config, built-in default.
func pickRefresh(cli, perSource, global time.Duration) time.Duration {
	return cmp.Or(cli, perSource, global, defaultRefresh)
}
