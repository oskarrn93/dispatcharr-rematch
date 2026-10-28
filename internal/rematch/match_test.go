package rematch

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Expected values were produced by the original Python script's normalize()
// and similarity() (difflib.SequenceMatcher), so the port scores identically.
func TestNormalizeAndSimilarityMatchPython(t *testing.T) {
	cases := []struct {
		a, b       string
		na, nb     string
		similarity float64
	}{
		{"ESPN HD", "ESPN FHD", "espn", "espn", 1.0},
		{"SE| SVT1 HD", "SVT 1", "se svt1", "svt 1", 0.6666666666666666},
		{"UK: Sky Sports Main Event", "Sky Sports Main Event UHD", "uk sky sports main event", "sky sports main event", 0.9333333333333333},
		{"TV4 Hockey", "TV4 Sport", "tv4 hockey", "tv4 sport", 0.5263157894736842},
		{"CNN International", "CNN Intl", "cnn international", "cnn intl", 0.64},
		{"Discovery Channel East", "Discovery Channel", "discovery channel", "discovery channel", 1.0},
		{"ÅHD Kanal", "hd kanal", "hd kanal", "kanal", 0.7692307692307693},
		{"V Sport 1 FHD (backup)", "V Sport 1", "v sport 1", "v sport 1", 1.0},
		{"", "ESPN", "", "espn", 0.0},
		{"abc", "xyz", "abc", "xyz", 0.0},
		{"Eurosport 1 4K", "EUROSPORT 1 H265", "eurosport 1", "eurosport 1", 1.0},
		{"C More First", "CMore First HD", "c more first", "cmore first", 0.9565217391304348},
	}
	// Not in the Python script: resolution tags are noise too.
	assert.Equal(t, "espn", Normalize("ESPN 1080p"))
	assert.Equal(t, "eurosport 1", Normalize("Eurosport 1 2160P"))
	for _, tc := range cases {
		t.Run(tc.a+"|"+tc.b, func(t *testing.T) {
			assert.Equal(t, tc.na, Normalize(tc.a))
			assert.Equal(t, tc.nb, Normalize(tc.b))
			assert.InDelta(t, tc.similarity, Similarity(tc.a, tc.b), 1e-12)
		})
	}
}

func TestRatioAutojunkMatchesPython(t *testing.T) {
	// difflib drops "popular" elements of b from its index once len(b) >= 200.
	a := []rune(strings.Repeat("ab", 150) + "xyz")
	b := []rune(strings.Repeat("ba", 150) + "xy")
	assert.InDelta(t, 0.006611570247933884, ratio(a, b), 1e-12)
}
