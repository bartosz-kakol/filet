package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"filet/internal/config"
	"filet/internal/places"
	"filet/internal/tools"
)

func setup(t *testing.T) (*places.View, string) {
	t.Helper()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg, err := places.NewRegistry([]config.PlaceConfig{{ID: "p", Name: "P", ResolvedPath: dir}}, t.TempDir(), &tools.Caps{}, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reg.Close)
	p, _ := reg.Get("p")
	return p.FullView(), dir
}

var files = map[string]string{"a/hello.txt": "hello", "b.md": "# b", "../evil.txt": "evil"}

func writeZip(t *testing.T, path string, encrypted string) {
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	for n, c := range files {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: time.Unix(1700000000, 0)})
		_, _ = w.Write([]byte(c))
	}
	if encrypted != "" {
		h := &zip.FileHeader{Name: encrypted, Method: zip.Store}
		h.Flags |= 1
		w, _ := zw.CreateHeader(h)
		_, _ = w.Write([]byte("xx"))
	}
	_ = zw.Close()
	_ = f.Close()
}

func writeTar(t *testing.T, path string, gz bool) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "a/", Typeflag: tar.TypeDir, Mode: 0o755})
	for n, c := range files {
		_ = tw.WriteHeader(&tar.Header{Name: n, Size: int64(len(c)), Mode: 0o644, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(c))
	}
	_ = tw.Close()
	data := buf.Bytes()
	if gz {
		var g bytes.Buffer
		zw := gzip.NewWriter(&g)
		_, _ = zw.Write(data)
		_ = zw.Close()
		data = g.Bytes()
	}
	_ = os.WriteFile(path, data, 0o644)
}

func check(t *testing.T, s *Service, v *places.View, rel string) {
	t.Helper()
	l, err := s.List(context.Background(), v, rel)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range l.Entries {
		got[e.Path] = true
	}
	for _, want := range []string{"a/hello.txt", "b.md", "__/evil.txt"} {
		if !got[want] {
			t.Errorf("%s: missing %s in %v", rel, want, got)
		}
	}
	rc, size, err := s.OpenEntry(context.Background(), v, rel, "a/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "hello" || size != 5 {
		t.Errorf("%s: entry %q size %d", rel, b, size)
	}
}

func TestZipTarTgz(t *testing.T) {
	v, dir := setup(t)
	s := New()
	writeZip(t, filepath.Join(dir, "x.zip"), "secret.bin")
	writeTar(t, filepath.Join(dir, "x.tar"), false)
	writeTar(t, filepath.Join(dir, "x.tar.gz"), true)
	writeTar(t, filepath.Join(dir, "x.tgz"), true)
	for _, n := range []string{"/x.zip", "/x.tar", "/x.tar.gz", "/x.tgz"} {
		check(t, s, v, n)
	}
	l, _ := s.List(context.Background(), v, "/x.zip")
	enc := false
	for _, e := range l.Entries {
		if e.Path == "secret.bin" {
			enc = e.Encrypted
		}
	}
	if !enc {
		t.Error("encrypted zip entry not flagged")
	}
	if _, _, err := s.OpenEntry(context.Background(), v, "/x.zip", "secret.bin"); err != ErrEncrypted {
		t.Errorf("encrypted entry: %v", err)
	}
}

func Test7z(t *testing.T) {
	bin, err := exec.LookPath("7zz")
	if err != nil {
		if bin, err = exec.LookPath("7z"); err != nil {
			t.Skip("7z not installed")
		}
	}
	v, dir := setup(t)
	src := filepath.Join(t.TempDir(), "src")
	_ = os.MkdirAll(filepath.Join(src, "a"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "a", "hello.txt"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "b.md"), []byte("# b"), 0o644)
	cmd := exec.Command(bin, "a", filepath.Join(dir, "x.7z"), "a", "b.md")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("7z: %v %s", err, out)
	}
	s := New()
	rc, _, err := s.OpenEntry(context.Background(), v, "/x.7z", "a/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "hello" {
		t.Fatalf("7z entry %q", b)
	}
}
