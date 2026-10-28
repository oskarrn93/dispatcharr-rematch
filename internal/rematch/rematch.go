// Package rematch re-points Dispatcharr channels whose stream is stale,
// deleted or missing at the best-matching healthy stream by name.
package rematch

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/dispatcharr"
	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/telemetry"
)

// API is the subset of the Dispatcharr client rematch uses.
type API interface {
	ListChannels(ctx context.Context) ([]dispatcharr.Channel, error)
	ListStreams(ctx context.Context) ([]dispatcharr.Stream, error)
	PatchChannelStreams(ctx context.Context, channelID int, streamIDs []int) error
	RefreshAllM3UAccounts(ctx context.Context) error
	ListActiveM3UAccounts(ctx context.Context) ([]dispatcharr.M3UAccount, error)
}

type Options struct {
	Threshold     float64
	SameGroupOnly bool
	NameFilter    string // case-insensitive substring; empty means all channels
	Apply         bool
}

// RefreshOptions control RefreshAndWait. ErrorGrace is how long an account
// that is in `error` without ever being seen fetching/parsing (e.g. one with
// no URL, which fails the same way every time) is waited on before it counts
// as finished.
type RefreshOptions struct {
	Timeout      time.Duration
	PollInterval time.Duration
	ErrorGrace   time.Duration
}

// RefreshAndWait refreshes all active M3U accounts and blocks until each one
// finishes: its updated_at changes with status success, or it is in error
// after having been seen busy (or after ErrorGrace). A failed account is
// logged and counted but doesn't fail the run; a timeout does.
func RefreshAndWait(ctx context.Context, api API, tel *telemetry.Telemetry, opts RefreshOptions) error {
	accounts, err := api.ListActiveM3UAccounts(ctx)
	if err != nil {
		return err
	}
	before := make(map[int]*string, len(accounts))
	pending := make(map[int]bool, len(accounts))
	for _, a := range accounts {
		before[a.ID] = a.UpdatedAt
		pending[a.ID] = true
	}

	if err := api.RefreshAllM3UAccounts(ctx); err != nil {
		return err
	}
	tel.Logger.Info("triggered M3U refresh, waiting for it to finish", "accounts", len(accounts))

	started := time.Now()
	defer func() { tel.M3URefreshDuration.Record(ctx, time.Since(started).Seconds()) }()
	seenBusy := map[int]bool{}

	for len(pending) > 0 {
		if time.Since(started) >= opts.Timeout {
			return fmt.Errorf("timed out after %s waiting for M3U account(s) %v", opts.Timeout, slices.Sorted(maps.Keys(pending)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opts.PollInterval):
		}

		accounts, err := api.ListActiveM3UAccounts(ctx)
		if err != nil {
			return err
		}
		elapsed := time.Since(started)
		for _, a := range accounts {
			if !pending[a.ID] {
				continue
			}
			switch {
			case a.Status == "fetching" || a.Status == "parsing":
				seenBusy[a.ID] = true
			case a.Status == "success" && !sameTime(a.UpdatedAt, before[a.ID]):
				tel.Logger.Info("M3U account refreshed", "account", a.Name)
				delete(pending, a.ID)
			case a.Status == "error" && (seenBusy[a.ID] || elapsed >= opts.ErrorGrace):
				tel.Logger.Warn("M3U account refresh failed", "account", a.Name, "message", deref(a.LastMessage))
				tel.M3URefreshFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("account", a.Name)))
				delete(pending, a.ID)
			}
		}
	}
	return nil
}

// Result summarizes one rematch pass.
type Result struct {
	ChannelsChecked int
	ChannelsChanged int
	SlotsRematched  int
	SlotsUnmatched  int
	StreamsHealthy  int
	StreamsStale    int
}

// Run finds every channel with an unassigned, stale or deleted stream and,
// with opts.Apply, patches in the best healthy match scoring >= Threshold.
func Run(ctx context.Context, api API, tel *telemetry.Telemetry, opts Options) (Result, error) {
	var res Result
	channels, err := api.ListChannels(ctx)
	if err != nil {
		return res, err
	}
	streams, err := api.ListStreams(ctx)
	if err != nil {
		return res, err
	}

	streamByID := make(map[int]*dispatcharr.Stream, len(streams))
	var healthy []dispatcharr.Stream
	for i := range streams {
		streamByID[streams[i].ID] = &streams[i]
		if !streams[i].IsStale {
			healthy = append(healthy, streams[i])
		}
	}
	res.StreamsHealthy, res.StreamsStale = len(healthy), len(streams)-len(healthy)
	candidates := newCandidates(healthy)
	tel.StreamsHealthy.Record(ctx, int64(res.StreamsHealthy))
	tel.StreamsStale.Record(ctx, int64(res.StreamsStale))

	if opts.NameFilter != "" {
		channels = slices.DeleteFunc(channels, func(c dispatcharr.Channel) bool {
			return !containsFold(c.Name, opts.NameFilter)
		})
	}

	mode := "dry_run"
	if opts.Apply {
		mode = "apply"
	}
	res.ChannelsChecked = len(channels)
	tel.ChannelsChecked.Add(ctx, int64(len(channels)), metric.WithAttributes(attribute.String("mode", mode)))

	for _, ch := range channels {
		newStreamIDs := make([]int, 0, len(ch.Streams))
		used := make(map[int]bool, len(ch.Streams))
		for _, id := range ch.Streams {
			used[id] = true
		}
		changed := false

		// A channel with no streams at all still needs exactly one match
		// attempt (this is Dispatcharr's most common "red"/unplayable case).
		slots := make([]*int, 0, max(1, len(ch.Streams)))
		for i := range ch.Streams {
			slots = append(slots, &ch.Streams[i])
		}
		if len(slots) == 0 {
			slots = append(slots, nil)
		}

		for _, sid := range slots {
			var stream *dispatcharr.Stream
			if sid != nil {
				stream = streamByID[*sid]
			}
			if stream != nil && !stream.IsStale {
				newStreamIDs = append(newStreamIDs, *sid)
				continue
			}

			reason, oldName := "unassigned", "(none)"
			switch {
			case sid != nil && stream == nil:
				reason, oldName = "missing (deleted)", fmt.Sprintf("id=%d", *sid)
			case stream != nil:
				reason, oldName = "stale", stream.Name
			}
			attrs := metric.WithAttributes(attribute.String("mode", mode), attribute.String("reason", reason))

			replacement, score := FindReplacement(ch, stream, candidates, used, opts.Threshold, opts.SameGroupOnly)
			if replacement != nil {
				tel.Logger.Info("rematch channel", "mode", mode, "channel", ch.Name, "channel_id", ch.ID,
					"old_stream", oldName, "reason", reason, "new_stream", replacement.Name,
					"new_stream_id", replacement.ID, "quality", StreamQuality(replacement).String(), "score", round2(score))
				tel.ChannelsRematched.Add(ctx, 1, attrs)
				res.SlotsRematched++
				newStreamIDs = append(newStreamIDs, replacement.ID)
				used[replacement.ID] = true
				changed = true
				continue
			}

			tel.Logger.Info("no match, leaving as-is", "mode", mode, "channel", ch.Name, "channel_id", ch.ID,
				"old_stream", oldName, "reason", reason, "threshold", opts.Threshold, "best_score", round2(score))
			tel.ChannelsUnmatched.Add(ctx, 1, attrs)
			res.SlotsUnmatched++
			if sid != nil {
				newStreamIDs = append(newStreamIDs, *sid)
			}
		}

		if changed {
			res.ChannelsChanged++
			if opts.Apply {
				if err := api.PatchChannelStreams(ctx, ch.ID, newStreamIDs); err != nil {
					return res, err
				}
			}
		}
	}

	verb := "would apply"
	if opts.Apply {
		verb = "applied"
	}
	tel.Logger.Info(verb+" changes", "channels", res.ChannelsChanged)
	return res, nil
}

// candidate is a stream with its name normalized once up front, since every
// stale slot scores the whole healthy pool.
type candidate struct {
	*dispatcharr.Stream
	norm    []rune
	tag     Quality // from the name alone
	quality Quality // probed resolution when known, else tag
}

func newCandidates(streams []dispatcharr.Stream) []candidate {
	out := make([]candidate, len(streams))
	for i := range streams {
		out[i] = candidate{&streams[i], normalizedRunes(streams[i].Name), nameQuality(streams[i].Name), StreamQuality(&streams[i])}
	}
	return out
}

// FindReplacement returns the best unused healthy stream for ch, preferring
// streams in ch's group when SameGroupOnly is set (falling back to all
// streams if the group has none), or nil if nothing scores >= threshold.
// Each candidate is scored against both the channel name and the old
// stream's name, if any. Quality tags are noise to the name match, so
// "ESPN HD" and "ESPN 4K" tie. Ties go first to a stream carrying the same
// tag as the channel (or, for an untagged channel, the old stream), so
// "ESPN FHD" stays FHD rather than drifting to HD or 4K, then to the higher
// Quality.
func FindReplacement(
	ch dispatcharr.Channel,
	old *dispatcharr.Stream,
	candidates []candidate,
	used map[int]bool,
	threshold float64,
	sameGroupOnly bool,
) (*dispatcharr.Stream, float64) {
	inGroup := func(c candidate) bool {
		return c.ChannelGroup != nil && *c.ChannelGroup == *ch.ChannelGroupID
	}
	scoped := sameGroupOnly && ch.ChannelGroupID != nil &&
		slices.ContainsFunc(candidates, func(c candidate) bool { return !used[c.ID] && inGroup(c) })

	names := [][]rune{normalizedRunes(ch.Name)}
	wantTag := nameQuality(ch.Name)
	if old != nil {
		names = append(names, normalizedRunes(old.Name))
		if wantTag == QualityUnknown {
			wantTag = nameQuality(old.Name)
		}
	}
	// better orders two candidates with equal name scores.
	better := func(a, b *candidate) bool {
		if wantTag != QualityUnknown {
			if aTag, bTag := a.tag == wantTag, b.tag == wantTag; aTag != bTag {
				return aTag
			}
		}
		return a.quality > b.quality
	}

	var best *candidate
	bestScore := 0.0
	for i, c := range candidates {
		if used[c.ID] || (scoped && !inGroup(c)) {
			continue
		}
		score := 0.0
		for _, name := range names {
			score = max(score, similarity(name, c.norm))
		}
		if score > bestScore || (best != nil && score == bestScore && better(&candidates[i], best)) {
			best, bestScore = &candidates[i], score
		}
	}
	if best != nil && bestScore >= threshold {
		return best.Stream, bestScore
	}
	return nil, bestScore
}
