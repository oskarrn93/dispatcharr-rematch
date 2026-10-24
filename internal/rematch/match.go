package rematch

import (
	"strings"
	"unicode"
)

// noiseWords are quality/region/language tags that make otherwise-identical
// channel and stream names look different to a naive string match.
var noiseWords = map[string]bool{
	"fhd": true, "uhd": true, "hd": true, "sd": true, "4k": true,
	"h265": true, "h264": true, "hevc": true, "vip": true, "backup": true,
	"east": true, "west": true, "feed": true, "raw": true, "multi": true,
	"sub": true, "dub": true,
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Normalize lowercases name, drops noise words (whole Unicode word runs, so
// "Åhd" keeps its "hd"), then turns everything outside [a-z0-9] into single
// spaces.
func Normalize(name string) string {
	runes := []rune(strings.ToLower(name))
	var b strings.Builder
	for i := 0; i < len(runes); {
		if !isWordRune(runes[i]) {
			b.WriteRune(runes[i])
			i++
			continue
		}
		j := i
		for j < len(runes) && isWordRune(runes[j]) {
			j++
		}
		if word := string(runes[i:j]); noiseWords[word] {
			b.WriteByte(' ')
		} else {
			b.WriteString(word)
		}
		i = j
	}

	cleaned := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return ' '
	}, b.String())
	return strings.Join(strings.Fields(cleaned), " ")
}

// Similarity scores two names from 0 to 1 after normalizing both.
func Similarity(a, b string) float64 {
	return similarity(normalizedRunes(a), normalizedRunes(b))
}

func normalizedRunes(name string) []rune {
	return []rune(Normalize(name))
}

// similarity is Similarity on already-normalized names.
func similarity(na, nb []rune) float64 {
	if len(na) == 0 || len(nb) == 0 {
		return 0
	}
	return ratio(na, nb)
}

// ratio is Python's difflib.SequenceMatcher(None, a, b).ratio(), ported
// exactly (including the autojunk heuristic) so match scores, and therefore
// the -threshold default, match the original Python script.
func ratio(a, b []rune) float64 {
	total := len(a) + len(b)
	if total == 0 {
		return 1
	}
	return 2 * float64(matchingCharacters(a, b)) / float64(total)
}

func matchingCharacters(a, b []rune) int {
	b2j := make(map[rune][]int)
	for j, r := range b {
		b2j[r] = append(b2j[r], j)
	}
	if n := len(b); n >= 200 {
		ntest := n/100 + 1
		for r, idxs := range b2j {
			if len(idxs) > ntest {
				delete(b2j, r)
			}
		}
	}

	type span struct{ alo, ahi, blo, bhi int }
	matched := 0
	queue := []span{{0, len(a), 0, len(b)}}
	for len(queue) > 0 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := longestMatch(a, b, b2j, s.alo, s.ahi, s.blo, s.bhi)
		if k == 0 {
			continue
		}
		matched += k
		if s.alo < i && s.blo < j {
			queue = append(queue, span{s.alo, i, s.blo, j})
		}
		if i+k < s.ahi && j+k < s.bhi {
			queue = append(queue, span{i + k, s.ahi, j + k, s.bhi})
		}
	}
	return matched
}

// longestMatch is SequenceMatcher.find_longest_match with no junk function:
// the earliest longest block in a[alo:ahi] / b[blo:bhi], extended over
// "popular" elements that autojunk dropped from b2j.
func longestMatch(a, b []rune, b2j map[rune][]int, alo, ahi, blo, bhi int) (int, int, int) {
	besti, bestj, bestsize := alo, blo, 0
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range b2j[a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}

	for besti > alo && bestj > blo && a[besti-1] == b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && a[besti+bestsize] == b[bestj+bestsize] {
		bestsize++
	}
	return besti, bestj, bestsize
}
