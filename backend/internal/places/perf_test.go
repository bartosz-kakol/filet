package places

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filet/internal/config"
	"filet/internal/tools"
)

func bigDir(tb testing.TB, n int) *View {
	tb.Helper()
	dir, _ := filepath.EvalSymlinks(tb.TempDir())
	for i := range n {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("IMG_%05d.jpg", i)), nil, 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	reg, err := NewRegistry([]config.PlaceConfig{{ID: "p", Name: "P", ResolvedPath: dir}}, tb.TempDir(),
		&tools.Caps{VipsThumbnail: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(reg.Close)
	p, _ := reg.Get("p")
	return p.FullView()
}

// Target: listing 10,000 entries takes < 300 ms server time.
func TestList10kUnder300ms(t *testing.T) {
	if testing.Short() {
		t.Skip("performance check")
	}
	v := bigDir(t, 10000)
	_, _ = v.List(context.Background(), "/") // warm the dentry cache
	start := time.Now()
	l, err := v.List(context.Background(), "/")
	took := time.Since(start)
	if err != nil || len(l.Entries) != 10000 {
		t.Fatalf("list: %v %d", err, len(l.Entries))
	}
	t.Logf("10,000 entries in %v", took)
	if took > 300*time.Millisecond {
		t.Fatalf("listing took %v", took)
	}
}

func BenchmarkList10k(b *testing.B) {
	v := bigDir(b, 10000)
	b.ResetTimer()
	for range b.N {
		if _, err := v.List(context.Background(), "/"); err != nil {
			b.Fatal(err)
		}
	}
}
