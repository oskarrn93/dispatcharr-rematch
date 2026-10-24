package rematch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/dispatcharr"
	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/telemetry"
)

type fakeAPI struct {
	channels []dispatcharr.Channel
	streams  []dispatcharr.Stream
	patched  map[int][]int

	// accountPolls is returned by successive ListActiveM3UAccounts calls;
	// the last entry repeats once exhausted.
	accountPolls [][]dispatcharr.M3UAccount
	polls        int
	refreshed    bool
}

func (f *fakeAPI) ListChannels(context.Context) ([]dispatcharr.Channel, error) {
	return f.channels, nil
}
func (f *fakeAPI) ListStreams(context.Context) ([]dispatcharr.Stream, error) { return f.streams, nil }
func (f *fakeAPI) RefreshAllM3UAccounts(context.Context) error               { f.refreshed = true; return nil }

func (f *fakeAPI) PatchChannelStreams(_ context.Context, id int, streams []int) error {
	if f.patched == nil {
		f.patched = map[int][]int{}
	}
	f.patched[id] = streams
	return nil
}

func (f *fakeAPI) ListActiveM3UAccounts(context.Context) ([]dispatcharr.M3UAccount, error) {
	i := min(f.polls, len(f.accountPolls)-1)
	f.polls++
	return f.accountPolls[i], nil
}

func newTelemetry(t *testing.T) *telemetry.Telemetry {
	t.Helper()
	tel, err := telemetry.New(context.Background(), false, "test")
	require.NoError(t, err)
	return tel
}

func ptr[T any](v T) *T { return &v }

func TestRunReplacesStaleMissingAndUnassigned(t *testing.T) {
	sports := 1
	api := &fakeAPI{
		channels: []dispatcharr.Channel{
			{ID: 1, Name: "ESPN", ChannelGroupID: &sports, Streams: []int{10}}, // stale
			{ID: 2, Name: "Sky Sports Main Event", Streams: []int{99}},         // deleted
			{ID: 3, Name: "Eurosport 1", Streams: nil},                         // unassigned
			{ID: 4, Name: "CNN", Streams: []int{30}},                           // healthy
			{ID: 5, Name: "Totally Unique Channel", Streams: []int{40}},        // stale, no match
		},
		streams: []dispatcharr.Stream{
			{ID: 10, Name: "ESPN HD", ChannelGroup: &sports, IsStale: true},
			{ID: 11, Name: "ESPN FHD", ChannelGroup: &sports},
			{ID: 20, Name: "UK: Sky Sports Main Event UHD"},
			{ID: 21, Name: "Eurosport 1 4K"},
			{ID: 30, Name: "CNN"},
			{ID: 40, Name: "Totally Unique Channel", IsStale: true},
		},
	}

	res, err := Run(context.Background(), api, newTelemetry(t), Options{Threshold: 0.72, SameGroupOnly: true, Apply: true})
	require.NoError(t, err)

	assert.Equal(t, map[int][]int{1: {11}, 2: {20}, 3: {21}}, api.patched)
	assert.Equal(t, Result{
		ChannelsChecked: 5, ChannelsChanged: 3, SlotsRematched: 3, SlotsUnmatched: 1,
		StreamsHealthy: 4, StreamsStale: 2,
	}, res)
}

func TestRunDryRunDoesNotPatch(t *testing.T) {
	api := &fakeAPI{
		channels: []dispatcharr.Channel{{ID: 1, Name: "ESPN", Streams: []int{10}}},
		streams:  []dispatcharr.Stream{{ID: 10, Name: "ESPN", IsStale: true}, {ID: 11, Name: "ESPN HD"}},
	}
	res, err := Run(context.Background(), api, newTelemetry(t), Options{Threshold: 0.72})
	require.NoError(t, err)
	assert.Nil(t, api.patched)
	assert.Equal(t, 1, res.ChannelsChanged)
}

func TestRunNameFilter(t *testing.T) {
	api := &fakeAPI{
		channels: []dispatcharr.Channel{{ID: 1, Name: "ESPN"}, {ID: 2, Name: "CNN"}},
		streams:  []dispatcharr.Stream{{ID: 11, Name: "ESPN HD"}, {ID: 12, Name: "CNN HD"}},
	}
	res, err := Run(context.Background(), api, newTelemetry(t), Options{Threshold: 0.72, NameFilter: "espn", Apply: true})
	require.NoError(t, err)
	assert.Equal(t, map[int][]int{1: {11}}, api.patched)
	assert.Equal(t, 1, res.ChannelsChecked)
}

func TestFindReplacementPrefersSameGroupAndSkipsUsed(t *testing.T) {
	g1, g2 := 1, 2
	ch := dispatcharr.Channel{ID: 1, Name: "ESPN", ChannelGroupID: &g1}
	candidates := newCandidates([]dispatcharr.Stream{
		{ID: 1, Name: "ESPN", ChannelGroup: &g2},
		{ID: 2, Name: "ESPN HD", ChannelGroup: &g1},
		{ID: 3, Name: "ESPN FHD", ChannelGroup: &g1},
	})

	got, _ := FindReplacement(ch, nil, candidates, map[int]bool{2: true}, 0.72, true)
	require.NotNil(t, got)
	assert.Equal(t, 3, got.ID)

	got, _ = FindReplacement(ch, nil, candidates, map[int]bool{2: true, 3: true}, 0.72, true)
	require.NotNil(t, got, "falls back to any group when the channel's group has no candidates")
	assert.Equal(t, 1, got.ID)
}

func TestRefreshAndWait(t *testing.T) {
	quick := RefreshOptions{Timeout: time.Second, PollInterval: time.Millisecond, ErrorGrace: 20 * time.Millisecond}

	t.Run("waits for updated_at to change", func(t *testing.T) {
		api := &fakeAPI{accountPolls: [][]dispatcharr.M3UAccount{
			{{ID: 1, Name: "a", Status: "success", UpdatedAt: ptr("2026-10-24T10:00:00Z")}},
			{{ID: 1, Name: "a", Status: "fetching", UpdatedAt: ptr("2026-10-24T10:00:00Z")}},
			{{ID: 1, Name: "a", Status: "success", UpdatedAt: ptr("2026-10-24T10:05:00Z")}},
		}}
		require.NoError(t, RefreshAndWait(context.Background(), api, newTelemetry(t), quick))
		assert.True(t, api.refreshed)
		assert.Equal(t, 3, api.polls)
	})

	t.Run("an account stuck in the same error finishes after the grace period", func(t *testing.T) {
		api := &fakeAPI{accountPolls: [][]dispatcharr.M3UAccount{
			{{ID: 1, Name: "custom", Status: "error", LastMessage: ptr("No M3U source available")}},
		}}
		started := time.Now()
		require.NoError(t, RefreshAndWait(context.Background(), api, newTelemetry(t), quick))
		assert.GreaterOrEqual(t, time.Since(started), quick.ErrorGrace)
	})

	t.Run("an error after being busy finishes immediately", func(t *testing.T) {
		api := &fakeAPI{accountPolls: [][]dispatcharr.M3UAccount{
			{{ID: 1, Name: "a", Status: "success", UpdatedAt: ptr("2026-10-24T10:00:00Z")}},
			{{ID: 1, Name: "a", Status: "parsing", UpdatedAt: ptr("2026-10-24T10:00:00Z")}},
			{{ID: 1, Name: "a", Status: "error", UpdatedAt: ptr("2026-10-24T10:00:00Z")}},
		}}
		opts := quick
		opts.ErrorGrace = time.Hour
		require.NoError(t, RefreshAndWait(context.Background(), api, newTelemetry(t), opts))
	})

	t.Run("times out", func(t *testing.T) {
		api := &fakeAPI{accountPolls: [][]dispatcharr.M3UAccount{
			{{ID: 1, Name: "a", Status: "fetching", UpdatedAt: ptr("2026-10-24T10:00:00Z")}},
		}}
		opts := quick
		opts.Timeout = 20 * time.Millisecond
		require.ErrorContains(t, RefreshAndWait(context.Background(), api, newTelemetry(t), opts), "timed out")
	})
}
