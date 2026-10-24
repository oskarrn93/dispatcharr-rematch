// Command dispatcharr-rematch re-matches Dispatcharr channels to fresh
// (non-stale) streams by name.
//
// A channel shows red/unplayable in Dispatcharr when it has no stream
// assigned at all, or when its assigned stream is stale (is_stale, set when
// an M3U refresh no longer sees it from the provider) or has been deleted
// outright. This finds every channel in one of those states, picks the
// best-matching healthy stream by fuzzy name match (scoped to the same channel
// group when possible), and assigns it.
//
// Usage:
//
//	dispatcharr-rematch                       # dry run, all channels
//	dispatcharr-rematch -apply                # actually patch channels
//	dispatcharr-rematch -refresh-m3u          # trigger M3U refresh and exit
//	dispatcharr-rematch -refresh -apply       # refresh, wait for it, then apply (cron)
//	dispatcharr-rematch -channel ESPN         # limit to matching channel names
//	dispatcharr-rematch -threshold 0.8 -apply
//
// Config comes from flags, else environment variables, else defaults:
// DISPATCHARR_URL (-base-url, default http://localhost:9191),
// DISPATCHARR_USERNAME (-username), DISPATCHARR_PASSWORD (-password) and the
// TELEMETRY_ENABLED feature flag (-telemetry, default false; see
// internal/telemetry). -env-file loads a KEY=VALUE file first (default
// ./dispatcharr-rematch.env if present); variables already set in the
// environment take precedence over it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/config"
	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/dispatcharr"
	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/rematch"
	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/telemetry"
)

const (
	defaultBaseURL = "http://localhost:9191"
	defaultEnvFile = "dispatcharr-rematch.env"
	version        = "1.0.0"
)

type flags struct {
	envFile, baseURL, username, password, channel string
	threshold                                     float64
	anyGroup, apply, refreshM3U, refresh          bool
	telemetry                                     bool
	refreshTimeout, pollInterval                  time.Duration
}

func parseFlags(args []string) (flags, error) {
	var f flags
	fs := flag.NewFlagSet("dispatcharr-rematch", flag.ContinueOnError)
	fs.StringVar(&f.envFile, "env-file", "", "KEY=VALUE file to load config from (default ./"+defaultEnvFile+" if present)")
	fs.StringVar(&f.baseURL, "base-url", "", "Dispatcharr base URL (env DISPATCHARR_URL, default "+defaultBaseURL+")")
	fs.StringVar(&f.username, "username", "", "login username (env DISPATCHARR_USERNAME)")
	fs.StringVar(&f.password, "password", "", "login password (env DISPATCHARR_PASSWORD)")
	fs.Float64Var(&f.threshold, "threshold", 0.72, "minimum name-similarity score (0-1) to accept a replacement match")
	fs.BoolVar(&f.anyGroup, "any-group", false, "match against streams outside the channel's group (default: prefer same group, fall back to any)")
	fs.StringVar(&f.channel, "channel", "", "only process channels whose name contains this substring (case-insensitive)")
	fs.BoolVar(&f.apply, "apply", false, "actually patch channels; without it, only log planned changes")
	fs.BoolVar(&f.refreshM3U, "refresh-m3u", false, "trigger a refresh of all M3U accounts and exit (the refresh is async)")
	fs.BoolVar(&f.refresh, "refresh", false, "refresh all M3U accounts and wait for it to finish before rematching (combine with -apply for cron)")
	fs.DurationVar(&f.refreshTimeout, "refresh-timeout", 30*time.Minute, "how long -refresh waits for the refresh")
	fs.DurationVar(&f.pollInterval, "poll-interval", 10*time.Second, "how often -refresh checks refresh status")
	fs.BoolVar(&f.telemetry, "telemetry", false, "export OpenTelemetry metrics/logs (env TELEMETRY_ENABLED; -telemetry=false to force off)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.refreshM3U && (f.refresh || f.apply) {
		return f, errors.New("-refresh-m3u only triggers a refresh and exits; use -refresh to wait for it and rematch")
	}

	envFile := f.envFile
	if envFile == "" {
		if _, err := os.Stat(defaultEnvFile); err == nil {
			envFile = defaultEnvFile
		}
	}
	if envFile != "" {
		if err := config.LoadEnvFile(envFile); err != nil {
			return f, fmt.Errorf("load env file: %w", err)
		}
	}

	set := map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
	fromEnv := func(dst *string, name, env, def string) {
		if !set[name] {
			*dst = os.Getenv(env)
		}
		if *dst == "" {
			*dst = def
		}
	}
	fromEnv(&f.baseURL, "base-url", "DISPATCHARR_URL", defaultBaseURL)
	fromEnv(&f.username, "username", "DISPATCHARR_USERNAME", "")
	fromEnv(&f.password, "password", "DISPATCHARR_PASSWORD", "")
	if !set["telemetry"] {
		f.telemetry = config.Bool(os.Getenv("TELEMETRY_ENABLED"))
	}

	if f.username == "" || f.password == "" {
		return f, errors.New("-username/-password (or DISPATCHARR_USERNAME/DISPATCHARR_PASSWORD) are required")
	}
	return f, nil
}

func main() {
	f, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatcharr-rematch:", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tel, err := telemetry.New(ctx, f.telemetry, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatcharr-rematch: setup telemetry:", err)
		os.Exit(1)
	}

	started := time.Now()
	err = run(ctx, f, tel)
	if err != nil {
		tel.Logger.Error("run failed", "error", err)
	}
	// Record and flush with a fresh context so an interrupted run still reports.
	tel.RecordRun(context.Background(), err == nil, started)
	tel.Shutdown(context.Background())
	if err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, f flags, tel *telemetry.Telemetry) error {
	client, err := dispatcharr.New(ctx, f.baseURL, f.username, f.password)
	if err != nil {
		return err
	}

	if f.refreshM3U {
		if err := client.RefreshAllM3UAccounts(ctx); err != nil {
			return err
		}
		tel.Logger.Info("triggered refresh of all M3U accounts; it runs asynchronously, re-run without -refresh-m3u once it finishes")
		return nil
	}

	if f.refresh {
		err := rematch.RefreshAndWait(ctx, client, tel, rematch.RefreshOptions{
			Timeout:      f.refreshTimeout,
			PollInterval: f.pollInterval,
			ErrorGrace:   time.Minute,
		})
		if err != nil {
			return err
		}
	}

	_, err = rematch.Run(ctx, client, tel, rematch.Options{
		Threshold:     f.threshold,
		SameGroupOnly: !f.anyGroup,
		NameFilter:    f.channel,
		Apply:         f.apply,
	})
	return err
}
