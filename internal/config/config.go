package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DefaultSource string            `yaml:"default_source"`
	Refresh       Duration          `yaml:"refresh"`
	Sources       map[string]Source `yaml:"sources"`
}

// Source is one configured connection cadence reads traces from. Type names
// the underlying backend (tempo, jaeger, …); URL and auth are how to reach it.
type Source struct {
	Type    string            `yaml:"type"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
	User    string            `yaml:"user,omitempty"`
	Pass    string            `yaml:"pass,omitempty"`
	Refresh Duration          `yaml:"refresh,omitempty"`
}

// Duration wraps time.Duration so YAML can decode strings like "2s" or "500ms".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("refresh must be a duration string like \"2s\" or \"500ms\": %w", err)
	}
	if s == "" {
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) Duration() time.Duration { return time.Duration(d) }

// Load reads config from path. If path is empty it tries $CADENCE_CONFIG,
// then $XDG_CONFIG_HOME/cadence/config.yaml, then ~/.config/cadence/config.yaml.
// Returns a default in-memory config if nothing is found.
func Load(path string) (*Config, string, error) {
	if path == "" {
		path = resolveDefaultPath()
	}
	if path == "" {
		return defaultConfig(), "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), path, nil
		}
		return nil, path, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, path, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, path, err
	}
	return &c, path, nil
}

func (c *Config) validate() error {
	if len(c.Sources) == 0 {
		return nil // empty is fine; user can still pass --url
	}
	if c.DefaultSource == "" {
		for name := range c.Sources {
			c.DefaultSource = name
			break
		}
	}
	if _, ok := c.Sources[c.DefaultSource]; !ok {
		return fmt.Errorf("default_source %q not defined", c.DefaultSource)
	}
	for name, s := range c.Sources {
		if s.URL == "" {
			return fmt.Errorf("source %q: url is required", name)
		}
		// Keep this allowlist in sync with supportedBackendTypes in the
		// top-level resolver. Failing here gives the earliest possible
		// error; the resolver's own switch is the defensive backup.
		t := strings.ToLower(s.Type)
		if t == "" {
			t = "tempo"
		}
		if t != "tempo" {
			return fmt.Errorf("source %q: unsupported type %q", name, s.Type)
		}
	}
	return nil
}

func resolveDefaultPath() string {
	if p := os.Getenv("CADENCE_CONFIG"); p != "" {
		return p
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "cadence", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "cadence", "config.yaml")
}

func defaultConfig() *Config {
	return &Config{Sources: map[string]Source{}}
}
