package places

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"

	"filet/internal/apierr"
)

var update = flag.Bool("update", false, "rewrite testdata/kinds.json")

func TestValidateRelPath(t *testing.T) {
	ok := []string{"/", "/a", "/a/b c/d.txt", "/.hidden", "/a/.filet2"}
	for _, p := range ok {
		if got, err := ValidateRelPath(p); err != nil || got != p {
			t.Errorf("%q: %v", p, err)
		}
	}
	bad := map[string]string{
		"":                               "invalid_path",
		"a":                              "invalid_path",
		"/a/../b":                        "invalid_path",
		"/..":                            "invalid_path",
		"/a/./b":                         "invalid_path",
		"/a//b":                          "invalid_path",
		"/a/":                            "invalid_path",
		"/a\x00b":                        "invalid_path",
		"/\xff\xfe":                      "invalid_path",
		"/" + strings.Repeat("x", 256):   "invalid_path",
		"/" + strings.Repeat("a/", 2100): "invalid_path",
		"/.filet":                        "reserved_name",
		"/x/.filet/y":                    "reserved_name",
		"/a/.filet-part-123":             "reserved_name",
		"/.filet-copy-x-1":               "reserved_name",
	}
	for p, code := range bad {
		_, err := ValidateRelPath(p)
		if !apierr.Is(err, code) {
			t.Errorf("%q: got %v want %s", p, err, code)
		}
	}
}

func TestKeepBothName(t *testing.T) {
	taken := map[string]bool{"report (1).pdf": true}
	f := func(n string) bool { return taken[n] }
	cases := []struct {
		name  string
		isDir bool
		want  string
	}{
		{"report.pdf", false, "report (2).pdf"},
		{".env", false, ".env (1)"},
		{"archive.tar.gz", false, "archive.tar (1).gz"},
		{"photos.2024", true, "photos.2024 (1)"},
		{"noext", false, "noext (1)"},
	}
	for _, c := range cases {
		if got := KeepBothName(c.name, c.isDir, f); got != c.want {
			t.Errorf("%s: %s want %s", c.name, got, c.want)
		}
	}
}

// TestKindFixture keeps the Go and TypeScript kind tables in sync through
// testdata/kinds.json (checked by kinds.test.ts too).
func TestKindFixture(t *testing.T) {
	got, _ := json.MarshalIndent(Fixture(), "", "  ")
	if *update {
		if err := os.WriteFile("testdata/kinds.json", append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile("testdata/kinds.json")
	if err != nil {
		t.Fatal(err)
	}
	var want, have KindFixture
	_ = json.Unmarshal(data, &want)
	_ = json.Unmarshal(got, &have)
	if !reflect.DeepEqual(want, have) {
		t.Fatal("kinds.go differs from testdata/kinds.json; run go test ./internal/places -update and update kinds.ts")
	}
	for ext, kind := range want.Extensions {
		if KindOf("x."+ext) != kind {
			t.Errorf("KindOf(x.%s) = %s want %s", ext, KindOf("x."+ext), kind)
		}
	}
	for name, kind := range want.Filenames {
		if KindOf(name) != kind {
			t.Errorf("KindOf(%s) = %s", name, KindOf(name))
		}
	}
}
