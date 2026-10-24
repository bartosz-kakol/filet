package media

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filet/internal/config"
	"filet/internal/places"
	"filet/internal/tools"
)

func load(t *testing.T, name string) *Info {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	info, err := Normalize(data)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestNormalizeH264AAC(t *testing.T) {
	i := load(t, "h264_aac.json")
	if i.Container != "matroska,webm" || i.Duration < 1.9 || i.Tags.Title != "Test Movie" {
		t.Fatalf("format: %+v", i)
	}
	v := i.Video[0]
	if v.Codec != "h264" || v.Profile != "High" || v.Width != 1920 || v.Height != 1080 || v.FPS != 23.976 || v.BitDepth != 8 || v.HDR != nil {
		t.Fatalf("video: %+v", v)
	}
	if len(i.Audio) != 1 || i.Audio[0].Codec != "aac" {
		t.Fatalf("audio: %+v", i.Audio)
	}
}

func TestNormalizeHEVCHDR10(t *testing.T) {
	v := load(t, "hevc_hdr10.json").Video[0]
	if v.Codec != "hevc" || v.HDR == nil || *v.HDR != "HDR10" || v.BitDepth != 10 || v.Width != 3840 {
		t.Fatalf("video: %+v hdr=%v", v, v.HDR)
	}
}

func TestNormalizeMultiAudio(t *testing.T) {
	i := load(t, "dts_aac.json")
	if len(i.Audio) != 2 || i.Audio[0].Codec != "dts" || i.Audio[1].Codec != "aac" {
		t.Fatalf("audio: %+v", i.Audio)
	}
	if i.Audio[0].Language == nil || *i.Audio[0].Language != "eng" || !i.Audio[0].Default || i.Audio[1].Default {
		t.Fatalf("languages/default: %+v", i.Audio)
	}
}

func TestNormalizeCoverArt(t *testing.T) {
	i := load(t, "song.json")
	if !i.HasCoverArt || len(i.Video) != 0 || i.Audio[0].Codec != "flac" || i.Tags.Artist != "Filet Band" || i.Tags.Album != "Tests" {
		t.Fatalf("info: %+v", i)
	}
}

func TestSRTToVTT(t *testing.T) {
	in := "\xef\xbb\xbf1\r\n00:00:01,000 --> 00:00:04,500\r\nHello\r\n\r\n2\r\n00:01:02,003 --> 00:01:05,000\r\nWorld\r\n12 monkeys\r\n"
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:04.500\nHello\n\n00:01:02.003 --> 00:01:05.000\nWorld\n12 monkeys\n"
	if got := string(SRTToVTT([]byte(in))); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestSidecarMatching(t *testing.T) {
	cases := map[string]bool{"movie.srt": true, "movie.en.srt": true, "movie.pl.vtt": true, "movie.en.forced.srt": false, "other.srt": false, "movie.txt": false}
	for n, want := range cases {
		if got := IsSidecarOf("movie.mkv", n); got != want {
			t.Errorf("%s: %v", n, got)
		}
	}
}

func TestRemuxStreamsFragmentedMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir()) // config resolves place paths
	src := filepath.Join(dir, "clip.mkv")
	if out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=4",
		"-f", "lavfi", "-i", "sine=duration=4", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", src).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := tools.New(tools.Names{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}, log)
	runner.Probe(context.Background())
	reg, err := places.NewRegistry([]config.PlaceConfig{{ID: "p", Name: "P", ResolvedPath: dir}}, t.TempDir(), &runner.Caps, log)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	p, _ := reg.Get("p")
	s := New(runner)
	var buf bytes.Buffer
	if err := s.Remux(context.Background(), p.FullView(), "/clip.mkv", 1, 0, 1, &buf, func() {}, func() {}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if !bytes.Contains(b[:64], []byte("ftyp")) || !bytes.Contains(b, []byte("moof")) {
		t.Fatalf("not a fragmented mp4 (%d bytes)", len(b))
	}

	// Cancelling the request kills ffmpeg.
	ctx, cancel := context.WithCancel(context.Background())
	w := &cancelAfter{n: 4096, cancel: cancel}
	done := make(chan error, 1)
	go func() { done <- s.Remux(ctx, p.FullView(), "/clip.mkv", 0, 0, 1, w, func() {}, func() {}) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("remux did not stop after cancellation")
	}
	if out, _ := exec.Command("pgrep", "-f", "ffmpeg.*"+dir).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatal("ffmpeg still running")
	}
}

type cancelAfter struct {
	n      int
	cancel func()
}

func (c *cancelAfter) Write(p []byte) (int, error) {
	c.n -= len(p)
	if c.n <= 0 {
		c.cancel()
	}
	return len(p), nil
}
