package widget

import "testing"

func TestFuzzyPickerRefilterUsesSecondaryKey(t *testing.T) {
	t.Parallel()
	fp := NewFuzzyPicker([]FuzzyPickerItem{
		{Key: "postgres", Label: "PostgreSQL"},
		{Key: "mysql", Label: "MySQL"},
	})
	fp.SecondaryKey = true
	fp.Query.SetString("postg")
	fp.Refilter()

	if len(fp.Filtered) != 1 {
		t.Fatalf("len(filtered) = %d, want 1", len(fp.Filtered))
	}
	if fp.Filtered[0].Key != "postgres" {
		t.Fatalf("key = %q, want postgres", fp.Filtered[0].Key)
	}
}

func TestFuzzyPickerRefilterSortsByScoreThenAlpha(t *testing.T) {
	t.Parallel()
	fp := NewFuzzyPicker([]FuzzyPickerItem{
		{Key: "users", Label: "users"},
		{Key: "sessions", Label: "sessions"},
		{Key: "settings", Label: "settings"},
	})
	fp.SortAlpha = true
	fp.Query.SetString("se")
	fp.Refilter()

	if len(fp.Filtered) != 3 {
		t.Fatalf("len(filtered) = %d, want 3", len(fp.Filtered))
	}
	if fp.Filtered[0].Label != "sessions" || fp.Filtered[1].Label != "settings" {
		t.Fatalf("filtered order = %+v", fp.Filtered)
	}
}
