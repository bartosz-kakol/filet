package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func minimal(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	place := filepath.Join(dir, "media")
	if err := os.Mkdir(place, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, "database: " + filepath.Join(dir, "filet.db") + "\nplaces:\n  - name: Media\n    path: " + place + "\n"
}

func TestMinimalConfigDefaults(t *testing.T) {
	_, y := minimal(t)
	c, err := Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8080" || !c.LockoutEnabled() || c.Auth.LoginLockout.Duration.D().Seconds() != 5 ||
		!c.SecureCookies() || c.ShareDefaultExpiry().Hours() != 168 || c.StreamLinks.TTL.D().Hours() != 12 ||
		c.Places[0].ID != "media" || c.Log.Level != "info" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestValidationErrors(t *testing.T) {
	dir, base := minimal(t)
	file := filepath.Join(dir, "file")
	_ = os.WriteFile(file, nil, 0o644)
	cases := map[string]string{
		"field bogus not found": base + "bogus: 1\n",
		"database":              "places:\n  - name: A\n    path: " + dir + "\n",
		"places":                "database: " + filepath.Join(dir, "x.db") + "\n",
		"places[0].path":        "database: " + filepath.Join(dir, "x.db") + "\nplaces:\n  - name: A\n    path: relative\n",
		"is not a directory":    "database: " + filepath.Join(dir, "x.db") + "\nplaces:\n  - name: A\n    path: " + file + "\n",
		"duplicate place name":  base + "  - name: media\n    path: " + dir + "\n",
		"same directory":        base + "  - name: B\n    path: " + filepath.Join(dir, "media") + "\n",
		"collides":              base + "  - name: \"MEDIA!\"\n    path: " + dir + "\n",
		"smart_devices":         base + "    smart_devices: [/tmp/x]\n",
		"login_lockout":         base + "auth:\n  login_lockout:\n    duration: -1s\n",
		"stream_links.ttl":      base + "stream_links:\n  ttl: 30s\n",
		"share_links":           base + "share_links:\n  default_expiry: 10m\n",
		"smart.interval":        base + "smart:\n  interval: 1m\n",
		"thumbnails.workers":    base + "thumbnails:\n  workers: 65\n",
		"64 characters":         "database: " + filepath.Join(dir, "x.db") + "\nplaces:\n  - name: " + strings.Repeat("n", 65) + "\n    path: " + dir + "\n",
		"parent directory":      "database: /nonexistent/dir/x.db\nplaces:\n  - name: A\n    path: " + dir + "\n",
	}
	for want, y := range cases {
		_, err := Parse([]byte(y))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	// Nested places are allowed (warning only).
	if _, err := Parse([]byte(base + "  - name: Outer\n    path: " + dir + "\n")); err != nil {
		t.Fatalf("nested places: %v", err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Media Drive (4TB)": "media-drive-4tb",
		"Zdjęcia":           "zdjecia",
		"  --Hello--  ":     "hello",
		"日本":                "place-3",
		"Crème Brûlée":      "creme-brulee",
	} {
		if got := Slug(in, 3); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if len(Slug(strings.Repeat("a", 80), 1)) != 48 {
		t.Error("slug not truncated to 48")
	}
}

func TestEnv(t *testing.T) {
	c := &Config{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := c.LoadEnv([]string{"FILET_USERNAME=a"}, log); err == nil {
		t.Fatal("missing password accepted")
	}
	c = &Config{}
	if err := c.LoadEnv([]string{"FILET_PASSWORD=b"}, log); err == nil {
		t.Fatal("missing username accepted")
	}
	var buf strings.Builder
	warn := slog.New(slog.NewTextHandler(&buf, nil))
	c = &Config{}
	if err := c.LoadEnv([]string{"FILET_USERNAME=a", "FILET_PASSWORD=b", "FILET_FOO=1", "PATH=/bin"}, warn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "unknown FILET_ variable FILET_FOO ignored") || !strings.Contains(buf.String(), "shorter than 12") {
		t.Fatalf("warnings: %s", buf.String())
	}
}
