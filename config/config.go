// Package config loads ttywx settings from config.yaml and edits its list
// of locations. A missing file is reported to the caller (as an error
// matching os.ErrNotExist) so it can run first-time setup.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jdankney/ttywx/units"
)

// PlaceholderContact is the contact written to a fresh config. NWS asks that
// the User-Agent identify a real way to reach you, so the UI nags until it
// is changed.
const PlaceholderContact = "you@example.com"

type Location struct {
	Name    string  `yaml:"name"`
	Lat     float64 `yaml:"lat"`
	Lon     float64 `yaml:"lon"`
	Primary bool    `yaml:"primary,omitempty"`
}

type Refresh struct {
	Alerts   time.Duration `yaml:"alerts"`
	Forecast time.Duration `yaml:"forecast"`
}

type Config struct {
	Contact   string     `yaml:"contact"`
	Refresh   Refresh    `yaml:"refresh"`
	LogFile   string     `yaml:"log_file"`
	Bell      bool       `yaml:"bell"`
	Notify    bool       `yaml:"notify"`
	Units     string     `yaml:"units"` // imperial | metric
	Clock     string     `yaml:"clock"` // 12h | 24h
	Locations []Location `yaml:"locations"`

	// path is where the config was loaded from; relative paths in the
	// config (log_file) are resolved against its directory.
	path string
}

const templateYAML = `# ttywx configuration. Edit freely and restart ttywx.
# Add or remove places with: ttywx add "City, ST" / ttywx remove NAME

# api.weather.gov requires a User-Agent that identifies you. Put an email
# address or website here so NWS can reach you if your traffic misbehaves.
# Can also be overridden with --contact or the TTYWX_CONTACT env var.
contact: %s

units: imperial   # imperial (°F, mph) or metric (°C, km/h)
clock: 12h        # 12h or 24h

refresh:
  alerts: 60s     # active alerts
  forecast: 10m   # observations + forecasts

# Mode changes are appended to a log. The default location is
# $XDG_STATE_HOME/ttywx/ttywx.log (~/.local/state/ttywx/ttywx.log).
# Uncomment to override; relative paths are relative to this file.
# log_file: ~/ttywx.log

bell: true     # ring the terminal bell when a location enters TAKE COVER
notify: true   # best-effort desktop notification (notify-send / osascript)

# The location marked primary is shown on the Main tab. If none is marked,
# the first one is used. Coordinates are rounded to 4 decimals for the API.
# NWS covers the United States and its territories only.
locations:
%s`

// Template renders a fresh, commented config file with the given contact
// (PlaceholderContact if empty) and locations.
func Template(contact string, locs []Location) []byte {
	if contact == "" {
		contact = PlaceholderContact
	}
	var b strings.Builder
	for _, l := range locs {
		b.WriteString("  - " + flowLocation(l) + "\n")
	}
	return []byte(fmt.Sprintf(templateYAML, yamlScalar(contact), b.String()))
}

func flowLocation(l Location) string {
	s := fmt.Sprintf("{ name: %s, lat: %.4f, lon: %.4f", yamlScalar(l.Name), l.Lat, l.Lon)
	if l.Primary {
		s += ", primary: true"
	}
	return s + " }"
}

// yamlScalar quotes s if it wouldn't survive as a plain YAML scalar.
func yamlScalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimSuffix(string(out), "\n")
}

// DefaultPath is $XDG_CONFIG_HOME/ttywx/config.yaml (~/.config/ttywx on
// Linux, the platform equivalent elsewhere).
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ttywx", "config.yaml"), nil
}

// defaultLogPath is $XDG_STATE_HOME/ttywx/ttywx.log, falling back to
// ~/.local/state, which is where the XDG spec puts logs.
func defaultLogPath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "ttywx.log"
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "ttywx", "ttywx.log")
}

// Load reads the config at path. A missing file yields an error matching
// os.ErrNotExist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.path = path
	return cfg, nil
}

// Write creates path (and its directory) with the given contents, refusing
// to replace an existing file.
func Write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Parse decodes and validates config YAML, filling in defaults for any
// omitted settings.
func Parse(data []byte) (*Config, error) {
	cfg := &Config{
		Contact: PlaceholderContact,
		Refresh: Refresh{Alerts: time.Minute, Forecast: 10 * time.Minute},
		Bell:    true,
		Notify:  true,
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if len(cfg.Locations) == 0 {
		return nil, errors.New(`no locations configured (add one with: ttywx add "City, ST")`)
	}
	switch cfg.Units {
	case "", "imperial", "metric":
	default:
		return nil, fmt.Errorf("units must be imperial or metric, not %q", cfg.Units)
	}
	switch cfg.Clock {
	case "", "12h", "24h":
	default:
		return nil, fmt.Errorf("clock must be 12h or 24h, not %q", cfg.Clock)
	}
	if cfg.Refresh.Alerts < 15*time.Second {
		cfg.Refresh.Alerts = 15 * time.Second
	}
	if cfg.Refresh.Forecast < time.Minute {
		cfg.Refresh.Forecast = time.Minute
	}
	primary := -1
	seen := map[string]bool{}
	for i, l := range cfg.Locations {
		if l.Name == "" {
			return nil, fmt.Errorf("location %d has no name", i+1)
		}
		if l.Lat < -90 || l.Lat > 90 || l.Lon < -180 || l.Lon > 180 {
			return nil, fmt.Errorf("location %q has invalid coordinates", l.Name)
		}
		// Names identify locations in the log and in `ttywx remove`.
		key := strings.ToLower(l.Name)
		if seen[key] {
			return nil, fmt.Errorf("location name %q is used twice", l.Name)
		}
		seen[key] = true
		if l.Primary && primary < 0 {
			primary = i
		}
		cfg.Locations[i].Primary = false
	}
	if primary < 0 {
		primary = 0
	}
	// Keep the primary location first so index 0 always means "primary".
	p := cfg.Locations[primary]
	p.Primary = true
	cfg.Locations = append([]Location{p}, append(cfg.Locations[:primary:primary], cfg.Locations[primary+1:]...)...)
	return cfg, nil
}

// LogPath returns the mode-change log path: the XDG state default when
// log_file is unset, otherwise log_file with ~ expanded and relative paths
// resolved against the config file's directory.
func (c *Config) LogPath() string {
	p := c.LogFile
	if p == "" {
		return defaultLogPath()
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(c.path), p)
}

// Path is the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// Display returns the configured units and clock.
func (c *Config) Display() units.Units {
	return units.Units{Metric: c.Units == "metric", Clock24: c.Clock == "24h"}
}
