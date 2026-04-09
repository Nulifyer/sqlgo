package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func sampleHistoryEntry(conn, sql string, rowCount int64) HistoryEntry {
	return HistoryEntry{
		ConnectionName: conn,
		SQL:            sql,
		ExecutedAt:     time.Now().UTC(),
		Elapsed:        250 * time.Millisecond,
		RowCount:       rowCount,
	}
}

func TestRecordAndListHistory(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()

	for i, sql := range []string{
		`SELECT 1`,
		`SELECT * FROM users`,
		`UPDATE users SET name = 'bob'`,
	} {
		e := sampleHistoryEntry("local", sql, int64(i))
		// Stagger timestamps so ORDER BY executed_at DESC is deterministic.
		e.ExecutedAt = time.Now().UTC().Add(time.Duration(i) * time.Second)
		if err := s.RecordHistory(ctx, e); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	got, err := s.ListRecentHistory(ctx, "local", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Newest-first.
	if got[0].SQL != `UPDATE users SET name = 'bob'` {
		t.Errorf("got[0].SQL = %q", got[0].SQL)
	}
	if got[0].RowCount != 2 {
		t.Errorf("got[0].RowCount = %d, want 2", got[0].RowCount)
	}
	if got[0].Elapsed != 250*time.Millisecond {
		t.Errorf("got[0].Elapsed = %v, want 250ms", got[0].Elapsed)
	}
}

func TestHistoryRingBufferTrims(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()
	s.SetHistoryRingMax(5)

	base := time.Now().UTC()
	for i := 0; i < 12; i++ {
		e := sampleHistoryEntry("local", fmt.Sprintf("SELECT %d", i), int64(i))
		e.ExecutedAt = base.Add(time.Duration(i) * time.Second)
		if err := s.RecordHistory(ctx, e); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	got, err := s.ListRecentHistory(ctx, "local", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("ring not trimmed: len = %d, want 5", len(got))
	}
	// Should have kept the newest 5: rows 11..7.
	wantTop := []int64{11, 10, 9, 8, 7}
	for i, w := range wantTop {
		if got[i].RowCount != w {
			t.Errorf("got[%d].RowCount = %d, want %d", i, got[i].RowCount, w)
		}
	}
}

func TestHistoryRingIsPerConnection(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()
	s.SetHistoryRingMax(3)

	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		for _, conn := range []string{"a", "b"} {
			e := sampleHistoryEntry(conn, fmt.Sprintf("SELECT %s%d", conn, i), int64(i))
			e.ExecutedAt = base.Add(time.Duration(i) * time.Second)
			if err := s.RecordHistory(ctx, e); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
	}
	for _, conn := range []string{"a", "b"} {
		got, err := s.ListRecentHistory(ctx, conn, 10)
		if err != nil {
			t.Fatalf("list %s: %v", conn, err)
		}
		if len(got) != 3 {
			t.Errorf("conn %s: len = %d, want 3 (per-connection trim)", conn, len(got))
		}
	}
}

func TestSearchHistoryFTS(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()

	for _, sql := range []string{
		`SELECT * FROM users WHERE id = 1`,
		`SELECT COUNT(*) FROM orders`,
		`UPDATE customers SET email = 'a@b.c'`,
		`SELECT * FROM products INNER JOIN categories ON p.cat_id = c.id`,
	} {
		if err := s.RecordHistory(ctx, sampleHistoryEntry("local", sql, 0)); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	// Prefix search: "user" should match "users" via the prefix expansion.
	got, err := s.SearchHistory(ctx, "local", "user", 10)
	if err != nil {
		t.Fatalf("search user: %v", err)
	}
	if len(got) != 1 || !containsText(got[0].SQL, "users") {
		t.Errorf("search 'user' got %+v", got)
	}

	// Multi-token AND: "order count" should find the COUNT() on orders.
	got, err = s.SearchHistory(ctx, "local", "order count", 10)
	if err != nil {
		t.Fatalf("search order count: %v", err)
	}
	if len(got) != 1 || !containsText(got[0].SQL, "orders") {
		t.Errorf("search 'order count' got %+v", got)
	}

	// Empty query falls through to recent listing.
	got, err = s.SearchHistory(ctx, "local", "", 10)
	if err != nil {
		t.Fatalf("search empty: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("empty search len = %d, want 4", len(got))
	}
}

func containsText(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

// indexOf is a tiny substring search kept local to this test file so the
// test doesn't pull in strings just for one call.
func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i <= n-m; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
