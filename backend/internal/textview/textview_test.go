package textview

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestIndexOffsets(t *testing.T) {
	var b strings.Builder
	for i := range 2500 {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	text := b.String()
	for name, tc := range map[string]struct {
		text  string
		lines int64
	}{
		"trailing newline":    {text, 2500},
		"no trailing newline": {strings.TrimSuffix(text, "\n"), 2500},
		"crlf":                {strings.ReplaceAll(text, "\n", "\r\n"), 2500},
		"empty":               {"", 0},
		"single":              {"x", 1},
	} {
		ix, err := Build(context.Background(), strings.NewReader(tc.text), int64(len(tc.text)))
		if err != nil {
			t.Fatal(err)
		}
		if ix.LineCount != tc.lines {
			t.Errorf("%s: lineCount %d want %d", name, ix.LineCount, tc.lines)
		}
		if tc.lines < 2 {
			continue
		}
		for k, off := range ix.Offsets {
			want := fmt.Sprintf("line %d", k*Step)
			if !strings.HasPrefix(tc.text[off:], want) {
				t.Errorf("%s: offset %d points at %q", name, k, tc.text[off:min(int(off)+10, len(tc.text))])
			}
		}
	}
	// Exactly 1000 lines with a trailing newline: no offset at EOF.
	exact := strings.Repeat("x\n", 1000)
	ix, _ := Build(context.Background(), strings.NewReader(exact), int64(len(exact)))
	if len(ix.Offsets) != 1 {
		t.Fatalf("offsets %v", ix.Offsets)
	}
}
