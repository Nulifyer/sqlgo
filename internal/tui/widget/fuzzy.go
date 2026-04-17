package widget

import "strings"

// FuzzyScore returns a match score, the list of haystack rune indices
// matched by the needle, and whether needle is a subsequence of
// haystack (case-insensitive). Higher score = better match.
//
// Bonuses:
//   - match at position 0: +30.
//   - match right after a word boundary (_, ., space, -, /) or camel case: +30/+28.
//   - exact-case match: +2.
//   - streak bonus (consecutive matches): +15 per edge.
//   - all-prefix match: flat 1000 minus tiebreak.
//
// Penalties:
//   - later positions: small -i/4 penalty (earlier is better).
//   - longer haystacks: -(len(h)-len(n))/2 tiebreak so shorter wins.
//
// Complexity is O(n*m^2) with n = len(needle), m = len(haystack).
// Both are small in practice (identifiers, <~50 runes), so the
// naive DP is fine and keeps the code readable.
func FuzzyScore(needle, haystack string) (int, []int, bool) {
	if needle == "" {
		return 0, nil, true
	}
	nOrig := []rune(needle)
	hOrig := []rune(haystack)
	n := len(nOrig)
	m := len(hOrig)
	if n > m {
		return 0, nil, false
	}
	// Cap haystack length so pathological inputs (e.g. a pasted multi-KB
	// identifier or a long literal string surfaced as a candidate) can't
	// run the O(n*m^2) DP into seconds. 128 runes is more than any real
	// SQL identifier and keeps worst-case work bounded.
	const fuzzyHayCap = 128
	if m > fuzzyHayCap {
		hOrig = hOrig[:fuzzyHayCap]
		m = fuzzyHayCap
	}
	nLow := []rune(strings.ToLower(needle))
	hLow := []rune(strings.ToLower(string(hOrig)))

	isPrefix := true
	for i := 0; i < n; i++ {
		if hLow[i] != nLow[i] {
			isPrefix = false
			break
		}
	}
	if isPrefix {
		matches := make([]int, n)
		for i := 0; i < n; i++ {
			matches[i] = i
		}
		return 1000, matches, true
	}

	const neg = -1 << 30
	best := make([][]int, n)
	prev := make([][]int, n)
	for j := 0; j < n; j++ {
		best[j] = make([]int, m)
		prev[j] = make([]int, m)
		for i := 0; i < m; i++ {
			best[j][i] = neg
			prev[j][i] = -1
		}
	}

	for i := 0; i < m; i++ {
		if hLow[i] != nLow[0] {
			continue
		}
		best[0][i] = fuzzyCharBonus(hOrig, hLow, nOrig, 0, i)
	}

	for j := 1; j < n; j++ {
		for i := j; i < m; i++ {
			if hLow[i] != nLow[j] {
				continue
			}
			bonus := fuzzyCharBonus(hOrig, hLow, nOrig, j, i)
			bestPrev := neg
			bestK := -1
			for k := j - 1; k < i; k++ {
				if best[j-1][k] == neg {
					continue
				}
				add := bonus
				if k == i-1 {
					add += 15
				}
				total := best[j-1][k] + add
				if total > bestPrev {
					bestPrev = total
					bestK = k
				}
			}
			if bestK >= 0 {
				best[j][i] = bestPrev
				prev[j][i] = bestK
			}
		}
	}

	bestEnd := -1
	bestScore := neg
	for i := n - 1; i < m; i++ {
		if best[n-1][i] > bestScore {
			bestScore = best[n-1][i]
			bestEnd = i
		}
	}
	if bestEnd < 0 {
		return 0, nil, false
	}

	matches := make([]int, n)
	idx := bestEnd
	for j := n - 1; j >= 0; j-- {
		matches[j] = idx
		idx = prev[j][idx]
	}
	bestScore -= (m - n) / 2
	return bestScore, matches, true
}

// fuzzyCharBonus scores a single haystack[i] -> needle[j] match
// independent of predecessor streaks (the streak bonus is applied by
// the DP edge itself).
func fuzzyCharBonus(hOrig, hLow, nOrig []rune, j, i int) int {
	bonus := 10
	if i == 0 {
		bonus += 30
	} else {
		switch hLow[i-1] {
		case '_', '.', ' ', '-', '/':
			bonus += 30
		default:
			po := hOrig[i-1]
			co := hOrig[i]
			if po >= 'a' && po <= 'z' && co >= 'A' && co <= 'Z' {
				bonus += 28
			}
		}
	}
	if nOrig[j] == hOrig[i] {
		bonus += 2
	}
	bonus -= i / 4
	return bonus
}
