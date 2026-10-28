package rematch

import (
	"strconv"
	"strings"

	"github.com/oskarrn93/media-services/dispatcharr-rematch/internal/dispatcharr"
)

// Quality ranks a stream's resolution so equally well-named candidates can be
// told apart. Untagged streams rank above explicit SD: they're usually HD,
// but nothing says so.
type Quality int

const (
	QualitySD Quality = iota + 1
	QualityUnknown
	QualityHD
	QualityFHD
	QualityUHD
)

func (q Quality) String() string {
	switch q {
	case QualitySD:
		return "sd"
	case QualityHD:
		return "hd"
	case QualityFHD:
		return "fhd"
	case QualityUHD:
		return "uhd"
	}
	return "unknown"
}

var qualityTags = map[string]Quality{
	"4k": QualityUHD, "uhd": QualityUHD, "2160p": QualityUHD,
	"fhd": QualityFHD, "1080p": QualityFHD, "1080i": QualityFHD,
	"hd": QualityHD, "720p": QualityHD,
	"sd": QualitySD, "576p": QualitySD, "480p": QualitySD, "low": QualitySD,
}

// StreamQuality prefers the resolution Dispatcharr probed when it played the
// stream (providers' tags are often optimistic), falling back to the best
// quality tag in the name.
func StreamQuality(s *dispatcharr.Stream) Quality {
	if s.StreamStats != nil {
		if q := resolutionQuality(s.StreamStats.Resolution); q != QualityUnknown {
			return q
		}
	}
	return nameQuality(s.Name)
}

func resolutionQuality(res string) Quality {
	_, h, ok := strings.Cut(res, "x")
	height, err := strconv.Atoi(h)
	switch {
	case !ok || err != nil || height <= 0:
		return QualityUnknown
	case height >= 2160:
		return QualityUHD
	case height >= 1080:
		return QualityFHD
	case height >= 720:
		return QualityHD
	}
	return QualitySD
}

func nameQuality(name string) Quality {
	best := QualityUnknown
	found := false
	for _, word := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !isWordRune(r) }) {
		if q, ok := qualityTags[word]; ok && (!found || q > best) {
			best, found = q, true
		}
	}
	return best
}
