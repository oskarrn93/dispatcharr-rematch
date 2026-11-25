package rematch

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/oskarrn93/dispatcharr-rematch/internal/dispatcharr"
)

func TestStreamQuality(t *testing.T) {
	cases := []struct {
		name       string
		resolution string
		want       Quality
	}{
		{"UK: Sky Sport Main Event UHD 4K", "", QualityUHD},
		{"Eurosport 1 2160p", "", QualityUHD},
		{"ESPN FHD", "", QualityFHD},
		{"ESPN 1080i", "", QualityFHD},
		{"US: ABC HD", "", QualityHD},
		{"CNN 720p", "", QualityHD},
		{"CNN", "", QualityUnknown},
		{"ÅHD Kanal", "", QualityUnknown},
		{"TV4 SD", "", QualitySD},
		{"TV4 HD (SD backup)", "", QualityHD},
		// The probed resolution wins over an optimistic tag.
		{"UK: Sky Sport Premier League HD", "1024x576", QualitySD},
		{"US: ABC", "1280x720", QualityHD},
		{"BBC One", "1920x1080", QualityFHD},
		{"BBC One 4K", "garbage", QualityUHD},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := dispatcharr.Stream{Name: tc.name}
			if tc.resolution != "" {
				s.StreamStats = &dispatcharr.StreamStats{Resolution: tc.resolution}
			}
			assert.Equal(t, tc.want, StreamQuality(&s))
		})
	}
}

func TestQualityOrder(t *testing.T) {
	assert.Less(t, QualitySD, QualityUnknown)
	assert.Less(t, QualityUnknown, QualityHD)
	assert.Less(t, QualityHD, QualityFHD)
	assert.Less(t, QualityFHD, QualityUHD)
}
