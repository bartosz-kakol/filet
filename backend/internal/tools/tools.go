// Package tools runs external programs (ffmpeg, ffprobe, vips, smartctl)
// with timeouts, process-group kills, capped stderr and optional niceness,
// and probes which tools and loaders are available.
package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Names configures the executable for each tool.
type Names struct {
	FFmpeg, FFprobe, VipsThumbnail, Vips, Smartctl string
}

// Caps is the probed capability set.
type Caps struct {
	FFmpeg, FFprobe, VipsThumbnail, Vips, Smartctl bool
	// FFmpegWebP is true when ffmpeg can encode WebP (needed for video and
	// audio-cover thumbnails).
	FFmpegWebP bool
	// vips loaders
	HEIF, PDF, SVG, Magick, RAW bool
}

// Capabilities is the JSON shape exposed by GET /api/settings.
type Capabilities struct {
	Thumbnails struct {
		Image      bool `json:"image"`
		HEIC       bool `json:"heic"`
		RAW        bool `json:"raw"`
		SVG        bool `json:"svg"`
		PDF        bool `json:"pdf"`
		Video      bool `json:"video"`
		AudioCover bool `json:"audioCover"`
	} `json:"thumbnails"`
	Renditions bool `json:"renditions"`
	MediaInfo  bool `json:"mediaInfo"`
	Remux      bool `json:"remux"`
	Smart      bool `json:"smart"`
}

// JSON converts Caps into the API shape.
func (c Caps) JSON(smartEnabled bool) Capabilities {
	var o Capabilities
	o.Thumbnails.Image = c.VipsThumbnail
	o.Thumbnails.HEIC = c.VipsThumbnail && c.HEIF
	o.Thumbnails.RAW = c.VipsThumbnail && c.RAW
	o.Thumbnails.SVG = c.VipsThumbnail && c.SVG
	o.Thumbnails.PDF = c.VipsThumbnail && c.PDF
	o.Thumbnails.Video = c.FFmpeg && c.FFprobe && c.FFmpegWebP
	o.Thumbnails.AudioCover = c.FFmpeg && c.FFmpegWebP
	o.Renditions = c.Vips
	o.MediaInfo = c.FFprobe
	o.Remux = c.FFmpeg
	o.Smart = smartEnabled && c.Smartctl
	return o
}

// ErrUnavailable is returned when a tool is not installed.
var ErrUnavailable = errors.New("tool unavailable")

// Runner executes tools.
type Runner struct {
	Names Names
	Caps  Caps
	log   *slog.Logger

	mu    sync.Mutex
	procs map[*exec.Cmd]struct{}
}

// New creates a runner; call Probe to fill Caps.
func New(names Names, log *slog.Logger) *Runner {
	return &Runner{Names: names, log: log, procs: map[*exec.Cmd]struct{}{}}
}

// Probe detects installed tools and vips loaders.
func (r *Runner) Probe(ctx context.Context) {
	try := func(name string, args ...string) (string, bool) {
		res, err := r.Run(ctx, Opts{Timeout: 10 * time.Second}, name, args...)
		if err != nil {
			return "", false
		}
		return string(res.Stdout) + string(res.Stderr), true
	}
	_, r.Caps.FFmpeg = try(r.Names.FFmpeg, "-version")
	if encoders, ok := try(r.Names.FFmpeg, "-hide_banner", "-encoders"); ok {
		r.Caps.FFmpegWebP = strings.Contains(encoders, "libwebp")
	}
	_, r.Caps.FFprobe = try(r.Names.FFprobe, "-version")
	_, r.Caps.VipsThumbnail = try(r.Names.VipsThumbnail, "--vips-version")
	loaders, ok := try(r.Names.Vips, "-l")
	r.Caps.Vips = ok
	if ok {
		r.Caps.HEIF = strings.Contains(loaders, "heifload")
		r.Caps.PDF = strings.Contains(loaders, "pdfload")
		r.Caps.SVG = strings.Contains(loaders, "svgload")
		r.Caps.Magick = strings.Contains(loaders, "magickload")
		r.Caps.RAW = strings.Contains(loaders, "dcrawload") || strings.Contains(loaders, "rawload")
	}
	_, r.Caps.Smartctl = try(r.Names.Smartctl, "--version")
	for name, ok := range map[string]bool{
		"ffmpeg": r.Caps.FFmpeg, "ffprobe": r.Caps.FFprobe, "vipsthumbnail": r.Caps.VipsThumbnail,
		"vips": r.Caps.Vips, "smartctl": r.Caps.Smartctl,
	} {
		if !ok {
			r.log.Warn("external tool not available; dependent features disabled", "tool", name)
		}
	}
}

// Opts tunes one invocation.
type Opts struct {
	Timeout time.Duration
	Nice    bool // lower CPU priority (nice 10)
	Stdin   io.Reader
}

// Result is the captured output of a finished command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

const stderrCap = 64 << 10

// Run executes name with args and captures stdout (unbounded) and stderr
// (capped at 64 KiB). A non-zero exit returns an *ExitError together with
// the result.
func (r *Runner) Run(ctx context.Context, o Opts, name string, args ...string) (*Result, error) {
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	var stdout bytes.Buffer
	stderr := &capped{max: stderrCap}
	cmd := r.command(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	cmd.Stdin = o.Stdin
	if err := r.start(cmd, o.Nice); err != nil {
		return nil, err
	}
	err := cmd.Wait()
	r.untrack(cmd)
	res := &Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, &ExitError{Code: res.ExitCode, Stderr: string(res.Stderr), err: err}
	}
	return res, nil
}

// Stream starts name and returns its stdout. The caller must call wait()
// when done reading; cancelling ctx kills the whole process group.
func (r *Runner) Stream(ctx context.Context, o Opts, name string, args ...string) (stdout io.ReadCloser, wait func() error, err error) {
	cmd := r.command(ctx, name, args...)
	stderr := &capped{max: stderrCap}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := r.start(cmd, o.Nice); err != nil {
		return nil, nil, err
	}
	wait = func() error {
		err := cmd.Wait()
		r.untrack(cmd)
		if err != nil && ctx.Err() == nil {
			return &ExitError{Code: cmd.ProcessState.ExitCode(), Stderr: string(stderr.Bytes()), err: err}
		}
		return nil
	}
	return out, wait, nil
}

func (r *Runner) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // argv only, never a shell
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (r *Runner) start(cmd *exec.Cmd, nice bool) error {
	if err := cmd.Start(); err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) {
			return fmt.Errorf("%w: %s", ErrUnavailable, cmd.Path)
		}
		return err
	}
	if nice {
		_ = setPriority(cmd.Process.Pid, 10)
	}
	r.mu.Lock()
	r.procs[cmd] = struct{}{}
	r.mu.Unlock()
	return nil
}

func (r *Runner) untrack(cmd *exec.Cmd) {
	r.mu.Lock()
	delete(r.procs, cmd)
	r.mu.Unlock()
}

// KillAll kills every running subprocess group (used on shutdown).
func (r *Runner) KillAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for cmd := range r.procs {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}

// ExitError is a non-zero exit.
type ExitError struct {
	Code   int
	Stderr string
	err    error
}

func (e *ExitError) Error() string {
	s := strings.TrimSpace(e.Stderr)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return fmt.Sprintf("exit %d: %s", e.Code, s)
}

func (e *ExitError) Unwrap() error { return e.err }

type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *capped) Bytes() []byte { return c.buf.Bytes() }

// Semaphore is a counting semaphore.
type Semaphore chan struct{}

// NewSemaphore returns a semaphore with n slots.
func NewSemaphore(n int) Semaphore { return make(Semaphore, n) }

// Acquire blocks until a slot is free or ctx ends.
func (s Semaphore) Acquire(ctx context.Context) error {
	select {
	case s <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire takes a slot if one is free.
func (s Semaphore) TryAcquire() bool {
	select {
	case s <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees a slot.
func (s Semaphore) Release() { <-s }
