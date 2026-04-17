package fzfmatch

import "testing"

func TestMatchEmptyQuery(t *testing.T) {
	t.Parallel()
	result, ok := Match("", "users")
	if !ok {
		t.Fatal("empty query should match")
	}
	if result.Score != 0 || result.Positions != nil {
		t.Fatalf("result = %+v, want zero result", result)
	}
}

func TestMatchPrefixBeatsGeneralSubsequence(t *testing.T) {
	t.Parallel()
	prefix, ok := Match("se", "select")
	if !ok {
		t.Fatal("prefix should match")
	}
	subseq, ok := Match("se", "users")
	if !ok {
		t.Fatal("subsequence should match")
	}
	if prefix.Score <= subseq.Score {
		t.Fatalf("prefix score %d should beat subsequence score %d", prefix.Score, subseq.Score)
	}
}

func TestMatchBoundaryBeatsInterior(t *testing.T) {
	t.Parallel()
	boundary, ok := Match("vh", "VECTOR_HITCOUNT")
	if !ok {
		t.Fatal("boundary candidate should match")
	}
	interior, ok := Match("vh", "savehistory")
	if !ok {
		t.Fatal("interior candidate should match")
	}
	if boundary.Score <= interior.Score {
		t.Fatalf("boundary score %d should beat interior score %d", boundary.Score, interior.Score)
	}
}

func TestBestMatchReturnsHighestScoringCandidate(t *testing.T) {
	t.Parallel()
	result, idx, ok := BestMatch("pg", "postgres", "my postgres", "debug")
	if !ok {
		t.Fatal("expected a match")
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
	if result.Score <= 0 {
		t.Fatalf("score = %d, want > 0", result.Score)
	}
}

func TestBestMatchNoMatch(t *testing.T) {
	t.Parallel()
	if _, _, ok := BestMatch("zzz", "postgres", "mysql"); ok {
		t.Fatal("unexpected match")
	}
}
