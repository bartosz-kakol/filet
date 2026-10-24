// Package config loads and validates the YAML configuration file and the
// FILET_ environment variables.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
	"golang.org/x/text/unicode/norm"
)

// Duration is a time.Duration that unmarshals from Go duration syntax.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	if s == "0" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q", s)
	}
	*d = Duration(v)
	return nil
}

// D returns the value as time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// PlaceConfig is one entry of the places list.
type PlaceConfig struct {
	Name         string   `yaml:"name"`
	Path         string   `yaml:"path"`
	ReadOnly     bool     `yaml:"readonly"`
	SmartDevices []string `yaml:"smart_devices"`

	// Derived fields.
	ID           string `yaml:"-"`
	ResolvedPath string `yaml:"-"`
}

// Config is the parsed configuration file.
type Config struct {
	Listen   string        `yaml:"listen"`
	Database string        `yaml:"database"`
	Places   []PlaceConfig `yaml:"places"`
	Auth     struct {
		LoginLockout struct {
			Enabled  *bool    `yaml:"enabled"`
			Duration Duration `yaml:"duration"`
		} `yaml:"login_lockout"`
		SecureCookies  *bool    `yaml:"secure_cookies"`
		TrustedOrigins []string `yaml:"trusted_origins"`
	} `yaml:"auth"`
	ShareLinks struct {
		DefaultExpiry *Duration `yaml:"default_expiry"`
	} `yaml:"share_links"`
	StreamLinks struct {
		TTL Duration `yaml:"ttl"`
	} `yaml:"stream_links"`
	Thumbnails struct {
		Workers int `yaml:"workers"`
	} `yaml:"thumbnails"`
	Smart struct {
		Enabled  *bool    `yaml:"enabled"`
		Interval Duration `yaml:"interval"`
	} `yaml:"smart"`
	Tools struct {
		FFmpeg        string `yaml:"ffmpeg"`
		FFprobe       string `yaml:"ffprobe"`
		VipsThumbnail string `yaml:"vipsthumbnail"`
		Vips          string `yaml:"vips"`
		Smartctl      string `yaml:"smartctl"`
	} `yaml:"tools"`
	Log struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	} `yaml:"log"`

	// Credentials from the environment.
	Username string `yaml:"-"`
	Password string `yaml:"-"`
}

// LockoutEnabled reports whether the login lockout is on.
func (c *Config) LockoutEnabled() bool {
	return c.Auth.LoginLockout.Enabled == nil || *c.Auth.LoginLockout.Enabled
}

// SecureCookies reports whether cookies carry the Secure attribute.
func (c *Config) SecureCookies() bool { return c.Auth.SecureCookies == nil || *c.Auth.SecureCookies }

// SmartEnabled reports whether SMART polling is on.
func (c *Config) SmartEnabled() bool { return c.Smart.Enabled == nil || *c.Smart.Enabled }

// ShareDefaultExpiry is the preselected share-link expiry; 0 means never.
func (c *Config) ShareDefaultExpiry() time.Duration {
	if c.ShareLinks.DefaultExpiry == nil {
		return 168 * time.Hour
	}
	return c.ShareLinks.DefaultExpiry.D()
}

// ThumbWorkers returns the effective thumbnail worker count.
func (c *Config) ThumbWorkers() int {
	if c.Thumbnails.Workers > 0 {
		return c.Thumbnails.Workers
	}
	return max(1, runtime.NumCPU()/2)
}

// Error is a validation error naming the offending key.
type Error struct {
	Key string
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("config: %s: %s", e.Key, e.Msg) }

func errf(key, format string, args ...any) error {
	return &Error{Key: key, Msg: fmt.Sprintf(format, args...)}
}

// Load reads, parses, defaults and validates the config file at path. It
// does not read environment variables; see LoadEnv.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // admin-supplied path
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return Parse(data)
}

// Parse parses and validates config bytes.
func Parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config: %w", err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.Auth.LoginLockout.Duration == 0 {
		c.Auth.LoginLockout.Duration = Duration(5 * time.Second)
	}
	if c.StreamLinks.TTL == 0 {
		c.StreamLinks.TTL = Duration(12 * time.Hour)
	}
	if c.Smart.Interval == 0 {
		c.Smart.Interval = Duration(time.Hour)
	}
	def := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	def(&c.Tools.FFmpeg, "ffmpeg")
	def(&c.Tools.FFprobe, "ffprobe")
	def(&c.Tools.VipsThumbnail, "vipsthumbnail")
	def(&c.Tools.Vips, "vips")
	def(&c.Tools.Smartctl, "smartctl")
	def(&c.Log.Level, "info")
	def(&c.Log.Format, "text")
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Database) == "" {
		return errf("database", "is required")
	}
	dbDir := filepath.Dir(c.Database)
	if fi, err := os.Stat(dbDir); err != nil || !fi.IsDir() {
		return errf("database", "parent directory %s does not exist", dbDir)
	}
	if err := unix.Access(dbDir, unix.W_OK); err != nil {
		return errf("database", "parent directory %s is not writable", dbDir)
	}
	if len(c.Places) == 0 {
		return errf("places", "at least one place is required")
	}
	names := map[string]int{}
	ids := map[string]int{}
	resolved := map[string]int{}
	for i := range c.Places {
		p := &c.Places[i]
		key := fmt.Sprintf("places[%d]", i)
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return errf(key+".name", "is required")
		}
		if len([]rune(p.Name)) > 64 {
			return errf(key+".name", "must be at most 64 characters")
		}
		lower := strings.ToLower(p.Name)
		if j, dup := names[lower]; dup {
			return errf(key+".name", "duplicate place name %q (also places[%d])", p.Name, j)
		}
		names[lower] = i
		if p.Path == "" {
			return errf(key+".path", "is required")
		}
		if !filepath.IsAbs(p.Path) {
			return errf(key+".path", "must be an absolute path")
		}
		rp, err := filepath.EvalSymlinks(p.Path)
		if err != nil {
			return errf(key+".path", "%s does not exist", p.Path)
		}
		fi, err := os.Stat(rp)
		if err != nil || !fi.IsDir() {
			return errf(key+".path", "%s is not a directory", p.Path)
		}
		p.ResolvedPath = rp
		if j, dup := resolved[rp]; dup {
			return errf(key+".path", "resolves to the same directory as places[%d]", j)
		}
		resolved[rp] = i
		p.ID = Slug(p.Name, i+1)
		if j, dup := ids[p.ID]; dup {
			return errf(key+".name", "derived id %q collides with places[%d]; rename one of the places", p.ID, j)
		}
		ids[p.ID] = i
		for k, d := range p.SmartDevices {
			if !filepath.IsAbs(d) || !strings.HasPrefix(filepath.Clean(d), "/dev/") {
				return errf(fmt.Sprintf("%s.smart_devices[%d]", key, k), "must be an absolute path under /dev/")
			}
		}
	}
	if c.LockoutEnabled() && c.Auth.LoginLockout.Duration <= 0 {
		return errf("auth.login_lockout.duration", "must be > 0 when the lockout is enabled")
	}
	if ttl := c.StreamLinks.TTL.D(); ttl < time.Minute || ttl > 7*24*time.Hour {
		return errf("stream_links.ttl", "must be between 1m and 168h")
	}
	if e := c.ShareDefaultExpiry(); e != 0 && (e < time.Hour || e > 3650*24*time.Hour) {
		return errf("share_links.default_expiry", "must be 0 or between 1h and 87600h")
	}
	if c.Smart.Interval.D() < 5*time.Minute {
		return errf("smart.interval", "must be at least 5m")
	}
	if c.Thumbnails.Workers < 0 || c.Thumbnails.Workers > 64 {
		return errf("thumbnails.workers", "must be between 0 and 64")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return errf("log.level", "must be one of debug, info, warn, error")
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		return errf("log.format", "must be text or json")
	}
	return nil
}

// NestedPlaces returns pairs of place names where one path lies inside the
// other. Nesting is allowed but logged as a warning.
func (c *Config) NestedPlaces() [][2]string {
	var out [][2]string
	for i, a := range c.Places {
		for j, b := range c.Places {
			if i != j && strings.HasPrefix(b.ResolvedPath, a.ResolvedPath+string(filepath.Separator)) {
				out = append(out, [2]string{b.Name, a.Name})
			}
		}
	}
	return out
}

// LoadEnv reads FILET_USERNAME and FILET_PASSWORD into c and logs warnings for
// short passwords and unknown FILET_ variables.
func (c *Config) LoadEnv(environ []string, log *slog.Logger) error {
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(k, "FILET_") {
			continue
		}
		switch k {
		case "FILET_USERNAME":
			c.Username = v
		case "FILET_PASSWORD":
			c.Password = v
		default:
			log.Warn(fmt.Sprintf("unknown FILET_ variable %s ignored", k))
		}
	}
	if c.Username == "" {
		return errors.New("environment: FILET_USERNAME must be set and non-empty")
	}
	if c.Password == "" {
		return errors.New("environment: FILET_PASSWORD must be set and non-empty")
	}
	if len([]rune(c.Password)) < 12 {
		log.Warn("FILET_PASSWORD is shorter than 12 characters")
	}
	return nil
}

// Slug derives a URL id from a place name (1-based index used as fallback).
func Slug(name string, index int) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(name) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		r = unicode.ToLower(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	if s == "" {
		s = fmt.Sprintf("place-%d", index)
	}
	return s
}
