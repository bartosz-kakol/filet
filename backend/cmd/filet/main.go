// Command filet is a minimalistic self-hosted web file manager.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"filet/internal/archive"
	"filet/internal/auth"
	"filet/internal/config"
	"filet/internal/db"
	"filet/internal/events"
	"filet/internal/fsops"
	"filet/internal/httpapi"
	"filet/internal/jobs"
	"filet/internal/media"
	"filet/internal/places"
	"filet/internal/share"
	"filet/internal/smart"
	"filet/internal/textview"
	"filet/internal/thumbs"
	"filet/internal/tools"
	"filet/internal/uploads"
	"filet/internal/web"
)

var version = "dev"

// Exit codes.
const (
	exitOK      = 0
	exitConfig  = 1
	exitUsage   = 2
	exitRuntime = 3
)

func main() { os.Exit(run()) }

func run() int {
	fl := flag.NewFlagSet("filet", flag.ContinueOnError)
	var cfgPath string
	var showVersion bool
	fl.StringVar(&cfgPath, "config", "", "path to the YAML config file (required)")
	fl.StringVar(&cfgPath, "c", "", "alias for --config")
	fl.BoolVar(&showVersion, "version", false, "print the version and exit")
	fl.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: filet --config /etc/filet/config.yaml\n       filet --version\n")
	}
	if err := fl.Parse(os.Args[1:]); err != nil {
		return exitUsage
	}
	if showVersion {
		fmt.Printf("filet %s\n", version)
		return exitOK
	}
	if cfgPath == "" {
		fl.Usage()
		return exitUsage
	}

	boot := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load(cfgPath)
	if err != nil {
		boot.Error(err.Error())
		return exitConfig
	}
	log := newLogger(cfg)
	if err := cfg.LoadEnv(os.Environ(), log); err != nil {
		log.Error(err.Error())
		return exitConfig
	}
	for _, n := range cfg.NestedPlaces() {
		log.Warn("place is nested inside another place", "place", n[0], "parent", n[1])
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	// 2. database
	d, err := db.Open(ctx, cfg.Database)
	if err != nil {
		log.Error("database", "err", err)
		return exitRuntime
	}
	defer func() { _ = d.Close() }()

	// 3. auth
	authSvc := auth.New(d, auth.Options{
		Username: cfg.Username, Password: cfg.Password, LockoutEnabled: cfg.LockoutEnabled(),
		LockoutDuration: cfg.Auth.LoginLockout.Duration.D(), SecureCookies: cfg.SecureCookies(),
	}, log)
	if err := authSvc.Init(ctx); err != nil {
		log.Error("auth init", "err", err)
		return exitRuntime
	}

	// 4–5. tools and places
	runner := tools.New(tools.Names{
		FFmpeg: cfg.Tools.FFmpeg, FFprobe: cfg.Tools.FFprobe, VipsThumbnail: cfg.Tools.VipsThumbnail,
		Vips: cfg.Tools.Vips, Smartctl: cfg.Tools.Smartctl,
	}, log)
	runner.Probe(ctx)
	reg, err := places.NewRegistry(cfg.Places, filepath.Join(filepath.Dir(cfg.Database), "cache"), &runner.Caps, log)
	if err != nil {
		log.Error("places", "err", err)
		return exitRuntime
	}
	defer reg.Close()

	hub := events.NewHub()
	mediaSvc := media.New(runner)
	thumbCache := thumbs.New(runner, mediaSvc, log)
	ops := &fsops.Ops{DB: d, Reg: reg, Hub: hub, Thumbs: thumbCache, Log: log}
	upl := uploads.New(d, reg, hub, ops, thumbCache, log)
	ops.Uploads = upl
	jobRunner := jobs.New(d, reg, hub, ops, thumbCache, log)
	shareSvc := share.New(d, reg, authSvc)
	smartSvc := smart.New(d, reg, runner, hub, log, cfg.Smart.Interval.D(), cfg.SmartEnabled() && runner.Caps.Smartctl)

	// 6. reconciliation
	if err := upl.Reconcile(ctx, log); err != nil {
		log.Error("reconcile uploads", "err", err)
		return exitRuntime
	}
	if err := jobRunner.Reconcile(ctx); err != nil {
		log.Error("reconcile jobs", "err", err)
		return exitRuntime
	}
	if err := ops.ReconcileTrash(ctx, log); err != nil {
		log.Error("reconcile trash", "err", err)
		return exitRuntime
	}

	// 7. workers
	thumbCache.Start(cfg.ThumbWorkers())
	smartSvc.Start(ctx)
	go sweeper(ctx, d, jobRunner, shareSvc, smartSvc, log)

	// 8. HTTP
	spa := web.Dist()
	srv := &httpapi.Server{
		Cfg: cfg, Version: version, DB: d, Auth: authSvc, Reg: reg, Hub: hub, Ops: ops, Jobs: jobRunner,
		Uploads: upl, Thumbs: thumbCache, Media: mediaSvc, Archive: archive.New(), Text: textview.New(),
		Share: shareSvc, Smart: smartSvc, Tools: runner, Log: log, SPA: spa,
	}
	httpSrv := &http.Server{
		Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second,
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	httpSrv.RegisterOnShutdown(hub.CloseAll)
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Error("listen", "addr", cfg.Listen, "err", err)
		return exitRuntime
	}
	log.Info(fmt.Sprintf("filet %s listening on %s with %d places", version, ln.Addr(), len(reg.All())))

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			return exitRuntime
		}
	case <-sig:
	}

	// Shutdown: a second signal exits immediately.
	go func() {
		<-sig
		log.Error("second signal; exiting immediately")
		os.Exit(exitRuntime)
	}()
	log.Info("shutting down")
	shCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shCtx)
	jobRunner.Shutdown()
	runner.KillAll()
	stop()
	thumbCache.Stop()
	upl.Close()
	return exitOK
}

func newLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.Log.Level))
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Log.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

// sweeper runs hourly maintenance.
func sweeper(ctx context.Context, d *db.DB, jr *jobs.Runner, sh *share.Service, sm *smart.Service, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := d.W.DeleteExpiredSessions(ctx, time.Now().UnixMilli()); err == nil && n > 0 {
				log.Debug("swept sessions", "n", n)
			}
			sh.Sweep(ctx)
			jr.Sweep(ctx)
			sm.Sweep(ctx)
		}
	}
}
