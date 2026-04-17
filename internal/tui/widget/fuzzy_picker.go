package widget

import (
	"sort"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/search/fzfmatch"
	"github.com/Nulifyer/sqlgo/internal/tui/term"
)

// FuzzyPickerItem is one row in a FuzzyPicker. Key is the stable
// identifier callers resolve back after selection; Label is what the
// user sees and the primary fuzzy-scoring target.
type FuzzyPickerItem struct {
	Key   string
	Label string
	// Score is populated during Refilter when a query is non-empty.
	// Callers may inspect it when rendering (e.g. to hide near-zero
	// matches), but it is overwritten every Refilter.
	Score int
}

// FuzzyPicker is the shared search + selection state for any
// list-picker modal that wants "type to filter, Up/Down to choose,
// Enter to confirm". It owns the query Input, the pool of items, the
// filtered view, and a ScrollList for selection/scroll bookkeeping.
//
// The picker does NOT render anything itself; callers decide how to
// draw each row (so they can add port suffixes, secondary keys, mark
// indicators, etc.). Typical call sites use:
//
//	Refilter()           after Items or query changes
//	HandleNav(k)         for arrow/pgup keys
//	HandleQuery(k)       for typed characters (reports whether the
//	                     query actually changed so the caller can
//	                     Refilter + reset scroll)
//	Selected()           to fetch the committed row on Enter
type FuzzyPicker struct {
	Query    *Input
	Items    []FuzzyPickerItem
	Filtered []FuzzyPickerItem
	List     ScrollList
	// SortAlpha sorts the filtered set alphabetically (case-insensitive,
	// by Label then Key) after scoring. When true and a query is set,
	// rows are sorted by score-desc then alpha; when true and the query
	// is empty, purely alpha.
	SortAlpha bool
	// SecondaryKey adds the Key as a secondary fuzzy target alongside
	// Label. Needed when the user might search for the internal ID
	// (driver name) even though the display label differs.
	SecondaryKey bool
}

// NewFuzzyPicker constructs a picker seeded with items and an empty
// query. Refilter is run so Filtered mirrors the full item list.
func NewFuzzyPicker(items []FuzzyPickerItem) *FuzzyPicker {
	fp := &FuzzyPicker{
		Query: NewInput(""),
		Items: items,
	}
	fp.Refilter()
	return fp
}

// SetItems replaces the item pool and re-runs the filter. Useful for
// pickers that load their items asynchronously.
func (fp *FuzzyPicker) SetItems(items []FuzzyPickerItem) {
	fp.Items = items
	fp.Refilter()
}

// Refilter recomputes Filtered from Items and the current query.
// Clamps the ScrollList so Selected stays inside the new visible set.
func (fp *FuzzyPicker) Refilter() {
	q := fp.Query.String()
	out := make([]FuzzyPickerItem, 0, len(fp.Items))
	if q == "" {
		out = append(out, fp.Items...)
		if fp.SortAlpha {
			sort.SliceStable(out, func(i, j int) bool {
				return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
			})
		}
	} else {
		for _, it := range fp.Items {
			candidates := []string{it.Label}
			if fp.SecondaryKey {
				candidates = append(candidates, it.Key)
			}
			result, _, ok := fzfmatch.BestMatch(q, candidates...)
			if !ok {
				continue
			}
			it.Score = result.Score
			out = append(out, it)
		}
		if fp.SortAlpha {
			sort.SliceStable(out, func(i, j int) bool {
				if out[i].Score != out[j].Score {
					return out[i].Score > out[j].Score
				}
				return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
			})
		}
	}
	fp.Filtered = out
	fp.List.Len = len(out)
	fp.List.Clamp()
}

// Selected returns the current Filtered row and ok=true when a row is
// in range. Callers use this on Enter.
func (fp *FuzzyPicker) Selected() (FuzzyPickerItem, bool) {
	i := fp.List.Selected
	if i < 0 || i >= len(fp.Filtered) {
		return FuzzyPickerItem{}, false
	}
	return fp.Filtered[i], true
}

// HandleNav routes Up/Down/PgUp/PgDn through the embedded ScrollList.
// Returns true when consumed.
func (fp *FuzzyPicker) HandleNav(k term.Key) bool {
	return fp.List.HandleKey(k)
}

// HandleQuery routes the key to the query Input and refilters when the
// text changed. Returns true when the query changed (so the caller
// knows to reset any domain-specific highlight state); false otherwise.
// Reset ScrollList.Selected/Scroll before refilter so the new top row
// is the most relevant match instead of scrolling off-screen.
func (fp *FuzzyPicker) HandleQuery(k term.Key) bool {
	if !fp.Query.Handle(k) {
		return false
	}
	fp.List.Selected = 0
	fp.List.Scroll = 0
	fp.Refilter()
	return true
}
