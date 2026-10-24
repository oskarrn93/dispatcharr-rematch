package rematch

import (
	"math"
	"strings"
	"time"
)

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sameTime compares two updated_at values by instant, not by string, in case
// Dispatcharr ever changes how it formats them.
func sameTime(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	ta, errA := time.Parse(time.RFC3339Nano, *a)
	tb, errB := time.Parse(time.RFC3339Nano, *b)
	if errA != nil || errB != nil {
		return *a == *b
	}
	return ta.Equal(tb)
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}
