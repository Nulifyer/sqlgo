package fzfmatch

import "strings"

// Result is the score and matched rune positions for one candidate.
// Higher scores rank better.
type Result struct {
	Score     int
	Positions []int
}

// Match returns the current sqlgo fuzzy-match result for query against
// candidate. Matching is case-insensitive; Positions refer to rune
// indices in candidate.
func Match(query, candidate string) (Result, bool) {
	if query == "" {
		return Result{}, true
	}
	nOrig := []rune(query)
	hOrig := []rune(candidate)
	n := len(nOrig)
	m := len(hOrig)
	if n > m {
		return Result{}, false
	}

	// Cap candidate length so pathological inputs can't make the DP
	// cost explode. 128 runes is comfortably above any real identifier.
	const hayCap = 128
	if m > hayCap {
		hOrig = hOrig[:hayCap]
		m = hayCap
	}
	nLow := []rune(strings.ToLower(query))
	hLow := []rune(strings.ToLower(string(hOrig)))

	isPrefix := true
	for i := 0; i < n; i++ {
		if hLow[i] != nLow[i] {
			isPrefix = false
			break
		}
	}
	if isPrefix {
		positions := make([]int, n)
		for i := 0; i < n; i++ {
			positions[i] = i
		}
		return Result{Score: 1000, Positions: positions}, true
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
		best[0][i] = charBonus(hOrig, hLow, nOrig, 0, i)
	}

	for j := 1; j < n; j++ {
		for i := j; i < m; i++ {
			if hLow[i] != nLow[j] {
				continue
			}
			bonus := charBonus(hOrig, hLow, nOrig, j, i)
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
		return Result{}, false
	}

	positions := make([]int, n)
	idx := bestEnd
	for j := n - 1; j >= 0; j-- {
		positions[j] = idx
		idx = prev[j][idx]
	}
	bestScore -= (m - n) / 2
	return Result{Score: bestScore, Positions: positions}, true
}

// BestMatch returns the best-scoring result among candidates along with the
// winning candidate index. Returns ok=false when none match.
func BestMatch(query string, candidates ...string) (Result, int, bool) {
	bestIdx := -1
	var best Result
	for i, candidate := range candidates {
		result, ok := Match(query, candidate)
		if !ok {
			continue
		}
		if bestIdx < 0 || result.Score > best.Score {
			bestIdx = i
			best = result
		}
	}
	if bestIdx < 0 {
		return Result{}, -1, false
	}
	return best, bestIdx, true
}

// charBonus scores one candidate[i] -> query[j] match, independent of the
// predecessor edge bonus for consecutive runs.
func charBonus(hOrig, hLow, nOrig []rune, j, i int) int {
	bonus := 10
	if i == 0 {
		bonus += 30
	} else {
		switch hLow[i-1] {
		case '_', '.', ' ', '-', '/':
			bonus += 30
		default:
			prev := hOrig[i-1]
			curr := hOrig[i]
			if prev >= 'a' && prev <= 'z' && curr >= 'A' && curr <= 'Z' {
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
