// Package media wraps ffprobe and ffmpeg: normalised media info, on-the-fly
// remuxing to fragmented MP4 (stream copy only, never re-encoding) and
// subtitle extraction.
package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"filet/internal/apierr"
	"filet/internal/fsx"
	"filet/internal/lru"
	"filet/internal/places"
	"filet/internal/thumbs"
	"filet/internal/tools"
)

// Service is the media service.
type Service struct {
	runner *tools.Runner
	cache  *lru.Cache[fsx.Identity, *Info]
	probe  tools.Semaphore
	remux  tools.Semaphore
}

// New creates the service.
func New(runner *tools.Runner) *Service {
	return &Service{
		runner: runner,
		cache:  lru.New[fsx.Identity, *Info](2048),
		probe:  tools.NewSemaphore(8),
		remux:  tools.NewSemaphore(4),
	}
}

// Info is the normalised media info.
type Info struct {
	Container        string        `json:"container"`
	Duration         float64       `json:"duration"`
	Bitrate          int64         `json:"bitrate"`
	Size             int64         `json:"size"`
	Tags             Tags          `json:"tags"`
	Video            []VideoStream `json:"video"`
	Audio            []AudioStream `json:"audio"`
	Subtitles        []SubStream   `json:"subtitles"`
	Chapters         []Chapter     `json:"chapters"`
	HasCoverArt      bool          `json:"hasCoverArt"`
	SidecarSubtitles []Sidecar     `json:"sidecarSubtitles"`
}

// Tags are the normalised container tags.
type Tags struct {
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"albumArtist,omitempty"`
	Track       string `json:"track,omitempty"`
	Date        string `json:"date,omitempty"`
	Genre       string `json:"genre,omitempty"`
}

// VideoStream describes one video stream.
type VideoStream struct {
	Index    int     `json:"index"`
	Codec    string  `json:"codec"`
	Profile  string  `json:"profile,omitempty"`
	Level    int     `json:"level,omitempty"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	DAR      string  `json:"dar,omitempty"`
	FPS      float64 `json:"fps"`
	PixFmt   string  `json:"pixFmt,omitempty"`
	BitDepth int     `json:"bitDepth,omitempty"`
	HDR      *string `json:"hdr"`
	Rotation int     `json:"rotation"`
	Bitrate  int64   `json:"bitrate,omitempty"`
	Default  bool    `json:"default"`
}

// AudioStream describes one audio stream.
type AudioStream struct {
	Index      int     `json:"index"`
	Codec      string  `json:"codec"`
	Profile    string  `json:"profile,omitempty"`
	Channels   int     `json:"channels"`
	Layout     string  `json:"layout,omitempty"`
	SampleRate int     `json:"sampleRate"`
	Language   *string `json:"language"`
	Title      *string `json:"title"`
	Default    bool    `json:"default"`
}

// SubStream describes one subtitle stream.
type SubStream struct {
	Index    int     `json:"index"`
	Codec    string  `json:"codec"`
	Language *string `json:"language"`
	Title    *string `json:"title"`
	Default  bool    `json:"default"`
	Text     bool    `json:"text"`
}

// Chapter is one chapter.
type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title *string `json:"title"`
}

// Sidecar is a subtitle file next to the media file.
type Sidecar struct {
	Name     string  `json:"name"`
	Language *string `json:"language"`
}

var textSubCodecs = map[string]bool{"subrip": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true}

// IsMedia reports whether a file name is video or audio.
func IsMedia(name string) bool {
	k := places.KindOf(name)
	return k == places.KindVideo || k == places.KindAudio
}

// Info probes rel (cached by file identity).
func (s *Service) Info(ctx context.Context, v *places.View, rel string) (*Info, error) {
	if !s.runner.Caps.FFprobe {
		return nil, apierr.ToolUnavailable("ffprobe")
	}
	_, name := places.Split(v.PlacePath(rel))
	if !IsMedia(name) {
		return nil, apierr.ErrNotMedia
	}
	fi, err := v.Stat(rel)
	if err != nil {
		return nil, apierr.FromFS(err)
	}
	if !fi.Mode().IsRegular() {
		return nil, apierr.ErrNotMedia
	}
	src, err := v.RealPath(rel)
	if err != nil {
		return nil, err
	}
	info, err := s.probeCached(ctx, src, fsx.IdentityOf(fi))
	if err != nil {
		return nil, err
	}
	out := *info
	out.SidecarSubtitles = sidecars(v, rel)
	return &out, nil
}

// Duration implements thumbs.DurationProber.
func (s *Service) Duration(ctx context.Context, realPath string, id fsx.Identity) (float64, error) {
	info, err := s.probeCached(ctx, realPath, id)
	if err != nil {
		return 0, err
	}
	return info.Duration, nil
}

func (s *Service) probeCached(ctx context.Context, src string, id fsx.Identity) (*Info, error) {
	if info, ok := s.cache.Get(id); ok {
		return info, nil
	}
	if err := s.probe.Acquire(ctx); err != nil {
		return nil, err
	}
	defer s.probe.Release()
	res, err := s.runner.Run(ctx, tools.Opts{Timeout: 15 * time.Second}, s.runner.Names.FFprobe,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-show_chapters", src)
	if err != nil {
		return nil, apierr.New(422, "probe_failed", "Could not read media information")
	}
	info, err := Normalize(res.Stdout)
	if err != nil {
		return nil, err
	}
	info.Size = id.Size
	s.cache.Add(id, info)
	return info, nil
}

type probeOut struct {
	Streams []struct {
		Index         int               `json:"index"`
		CodecName     string            `json:"codec_name"`
		CodecType     string            `json:"codec_type"`
		Profile       string            `json:"profile"`
		Level         int               `json:"level"`
		Width         int               `json:"width"`
		Height        int               `json:"height"`
		DAR           string            `json:"display_aspect_ratio"`
		AvgFrameRate  string            `json:"avg_frame_rate"`
		RFrameRate    string            `json:"r_frame_rate"`
		PixFmt        string            `json:"pix_fmt"`
		BitsPerRaw    string            `json:"bits_per_raw_sample"`
		ColorTransfer string            `json:"color_transfer"`
		BitRate       string            `json:"bit_rate"`
		Channels      int               `json:"channels"`
		ChannelLayout string            `json:"channel_layout"`
		SampleRate    string            `json:"sample_rate"`
		Disposition   map[string]int    `json:"disposition"`
		Tags          map[string]string `json:"tags"`
		SideData      []map[string]any  `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		Size       string            `json:"size"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
	Chapters []struct {
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

func tag(m map[string]string, keys ...string) string {
	for _, k := range keys {
		for mk, mv := range m {
			if strings.EqualFold(mk, k) {
				return mv
			}
		}
	}
	return ""
}

func strp(s string) *string {
	if s == "" || s == "und" {
		return nil
	}
	return &s
}

func pf(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func pi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func ratio(s string) float64 {
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return pf(s)
	}
	d := pf(b)
	if d == 0 {
		return 0
	}
	return math.Round(pf(a)/d*1000) / 1000
}

func bitDepth(raw, pix string) int {
	if n := pi(raw); n > 0 {
		return int(n)
	}
	switch {
	case pix == "":
		return 0
	case strings.Contains(pix, "12"):
		return 12
	case strings.Contains(pix, "10"):
		return 10
	}
	return 8
}

// Normalize converts ffprobe JSON into Info.
func Normalize(data []byte) (*Info, error) {
	var p probeOut
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("ffprobe output: %w", err)
	}
	info := &Info{
		Container: p.Format.FormatName, Duration: pf(p.Format.Duration), Bitrate: pi(p.Format.BitRate),
		Size: pi(p.Format.Size), Video: []VideoStream{}, Audio: []AudioStream{}, Subtitles: []SubStream{},
		Chapters: []Chapter{}, SidecarSubtitles: []Sidecar{},
	}
	ft := p.Format.Tags
	info.Tags = Tags{
		Title: tag(ft, "title"), Artist: tag(ft, "artist"), Album: tag(ft, "album"),
		AlbumArtist: tag(ft, "album_artist", "albumartist"), Track: tag(ft, "track"),
		Date: tag(ft, "date", "year"), Genre: tag(ft, "genre"),
	}
	for _, st := range p.Streams {
		def := st.Disposition["default"] == 1
		switch st.CodecType {
		case "video":
			if st.Disposition["attached_pic"] == 1 {
				info.HasCoverArt = true
				continue
			}
			vs := VideoStream{
				Index: st.Index, Codec: st.CodecName, Profile: st.Profile, Level: st.Level, Width: st.Width,
				Height: st.Height, DAR: st.DAR, FPS: ratio(st.AvgFrameRate), PixFmt: st.PixFmt,
				BitDepth: bitDepth(st.BitsPerRaw, st.PixFmt), Bitrate: pi(st.BitRate), Default: def,
			}
			if vs.FPS == 0 {
				vs.FPS = ratio(st.RFrameRate)
			}
			var hdr string
			switch st.ColorTransfer {
			case "smpte2084":
				hdr = "HDR10"
			case "arib-std-b67":
				hdr = "HLG"
			}
			for _, sd := range st.SideData {
				typ, _ := sd["side_data_type"].(string)
				if strings.Contains(typ, "DOVI") {
					hdr = "Dolby Vision"
				}
				if strings.EqualFold(typ, "Display Matrix") {
					if r, ok := sd["rotation"].(float64); ok {
						vs.Rotation = int(r)
					}
				}
			}
			if r := tag(st.Tags, "rotate"); r != "" && vs.Rotation == 0 {
				vs.Rotation = int(pi(r))
			}
			if hdr != "" {
				vs.HDR = &hdr
			}
			info.Video = append(info.Video, vs)
		case "audio":
			info.Audio = append(info.Audio, AudioStream{
				Index: st.Index, Codec: st.CodecName, Profile: st.Profile, Channels: st.Channels,
				Layout: st.ChannelLayout, SampleRate: int(pi(st.SampleRate)),
				Language: strp(tag(st.Tags, "language")), Title: strp(tag(st.Tags, "title")), Default: def,
			})
		case "subtitle":
			info.Subtitles = append(info.Subtitles, SubStream{
				Index: st.Index, Codec: st.CodecName, Language: strp(tag(st.Tags, "language")),
				Title: strp(tag(st.Tags, "title")), Default: def, Text: textSubCodecs[st.CodecName],
			})
		}
	}
	for _, ch := range p.Chapters {
		info.Chapters = append(info.Chapters, Chapter{Start: pf(ch.StartTime), End: pf(ch.EndTime), Title: strp(tag(ch.Tags, "title"))})
	}
	return info, nil
}

func baseNoExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// sidecars lists <base>.srt|.vtt and <base>.<lang>.srt|.vtt next to rel.
func sidecars(v *places.View, rel string) []Sidecar {
	out := []Sidecar{}
	dir, name := places.Split(rel)
	f, err := v.Open(dir)
	if err != nil {
		return out
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return out
	}
	base := baseNoExt(name)
	for _, n := range names {
		if s, ok := matchSidecar(base, n); ok && (!v.Public || !strings.HasPrefix(n, ".")) {
			out = append(out, s)
		}
	}
	return out
}

func matchSidecar(base, n string) (Sidecar, bool) {
	lower := strings.ToLower(n)
	if !strings.HasSuffix(lower, ".srt") && !strings.HasSuffix(lower, ".vtt") {
		return Sidecar{}, false
	}
	stem := n[:len(n)-4]
	if stem == base {
		return Sidecar{Name: n}, true
	}
	if strings.HasPrefix(stem, base+".") {
		lang := stem[len(base)+1:]
		if lang != "" && !strings.Contains(lang, ".") {
			return Sidecar{Name: n, Language: &lang}, true
		}
	}
	return Sidecar{}, false
}

// IsSidecarOf reports whether sidecar is a subtitle file belonging to name.
func IsSidecarOf(name, sidecar string) bool {
	_, ok := matchSidecar(baseNoExt(name), sidecar)
	return ok
}

// RemuxArgs builds the ffmpeg argv for a remux stream.
func RemuxArgs(src string, start float64, video, audio int, hevc bool) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", src)
	if video >= 0 {
		args = append(args, "-map", "0:"+strconv.Itoa(video))
	}
	if audio >= 0 {
		args = append(args, "-map", "0:"+strconv.Itoa(audio))
	}
	args = append(args, "-c", "copy", "-sn", "-dn", "-map_metadata", "-1")
	if hevc && video >= 0 {
		args = append(args, "-tag:v", "hvc1")
	}
	return append(args, "-movflags", "+frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
}

// Remux streams a fragmented MP4 of rel to w. flush is called after each
// write. It returns apierr.ErrBusy when 4 remuxes are already running.
func (s *Service) Remux(ctx context.Context, v *places.View, rel string, start float64, video, audio int, w io.Writer, onStart func(), flush func()) error {
	if !s.runner.Caps.FFmpeg {
		return apierr.ToolUnavailable("ffmpeg")
	}
	info, err := s.Info(ctx, v, rel)
	if err != nil {
		return err
	}
	hevc := false
	if video >= 0 {
		found := false
		for _, vs := range info.Video {
			if vs.Index == video {
				found, hevc = true, vs.Codec == "hevc"
			}
		}
		if !found {
			return apierr.Invalid("video", "No such video stream")
		}
	}
	if audio >= 0 {
		found := false
		for _, as := range info.Audio {
			found = found || as.Index == audio
		}
		if !found {
			return apierr.Invalid("audio", "No such audio stream")
		}
	}
	if video < 0 && audio < 0 {
		return apierr.Invalid("video", "Select a video or audio stream")
	}
	if !s.remux.TryAcquire() {
		e := *apierr.ErrBusy
		e.RetryAfter = 2
		return &e
	}
	defer s.remux.Release()
	src, err := v.RealPath(rel)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out, wait, err := s.runner.Stream(ctx, tools.Opts{}, s.runner.Names.FFmpeg, RemuxArgs(src, start, video, audio, hevc)...)
	if err != nil {
		return err
	}
	onStart()
	buf := make([]byte, 256<<10)
	for {
		n, rerr := out.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				cancel()
				break
			}
			flush()
		}
		if rerr != nil {
			break
		}
	}
	cancel()
	_ = wait()
	return nil
}

// Subtitle returns WebVTT for an embedded text stream (cached in the thumb
// directory) or a sidecar file next to rel.
func (s *Service) Subtitle(ctx context.Context, v *places.View, rel string, stream int, sidecar string) ([]byte, error) {
	dir, name := places.Split(rel)
	if sidecar != "" {
		if !IsSidecarOf(name, sidecar) || places.ValidateName(sidecar) != nil {
			return nil, apierr.ErrNotFound
		}
		sv := *v
		sv.OnlyName = ""
		f, fi, err := sv.OpenRegular(places.Join(dir, sidecar))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if fi.Size() > 20<<20 {
			return nil, apierr.ErrTooLarge
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(strings.ToLower(sidecar), ".srt") {
			return SRTToVTT(data), nil
		}
		return data, nil
	}
	info, err := s.Info(ctx, v, rel)
	if err != nil {
		return nil, err
	}
	ok := false
	for _, st := range info.Subtitles {
		if st.Index == stream {
			ok = st.Text
		}
	}
	if !ok {
		return nil, apierr.New(400, "unsupported_format", "Subtitle stream is not a text track")
	}
	fi, err := v.Stat(rel)
	if err != nil {
		return nil, apierr.FromFS(err)
	}
	cache := thumbs.CachePath(v.Place, thumbs.Key(fsx.IdentityOf(fi)), ".s"+strconv.Itoa(stream)+".vtt")
	if data, err := os.ReadFile(cache); err == nil { //nolint:gosec // cache path
		return data, nil
	}
	src, err := v.RealPath(rel)
	if err != nil {
		return nil, err
	}
	res, err := s.runner.Run(ctx, tools.Opts{Timeout: 5 * time.Minute}, s.runner.Names.FFmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-i", src, "-map", "0:"+strconv.Itoa(stream), "-f", "webvtt", "pipe:1")
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, apierr.New(422, "subtitle_failed", "Could not extract subtitles")
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o700); err == nil {
		tmp := cache + ".tmp"
		if os.WriteFile(tmp, res.Stdout, 0o600) == nil {
			_ = os.Rename(tmp, cache)
		}
	}
	return res.Stdout, nil
}

var srtTime = regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}),(\d{3})`)

// SRTToVTT converts SubRip to WebVTT: header, '.' millisecond separators in
// cue timings, numeric cue ids dropped.
func SRTToVTT(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for i, l := range lines {
		if strings.Contains(l, "-->") {
			b.WriteString(srtTime.ReplaceAllString(l, "$1.$2"))
			b.WriteByte('\n')
			continue
		}
		if isDigits(l) && i+1 < len(lines) && strings.Contains(lines[i+1], "-->") {
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
