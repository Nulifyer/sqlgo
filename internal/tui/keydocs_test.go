package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestHelpContentUsesIconVocabulary(t *testing.T) {
	t.Parallel()

	lines := helpContent()

	wantSections := []string{
		"Help overlay",
		"Key debug (F8)",
		"Go To",
		"Rename Tab",
		"Active Database Picker",
		"Results Filter",
		"Open SQL",
		"Save As",
		"Export Results",
		"Driver / Transport Picker",
	}
	for _, want := range wantSections {
		if !helpHasSection(lines, want) {
			t.Fatalf("helpContent missing section %q", want)
		}
	}

	if !helpHasEntry(lines, "Ctrl+F", "filter rows") {
		t.Fatal("helpContent missing Results Ctrl+F filter binding")
	}
	if !helpHasEntry(lines, "Alt+Shift+A", "Markdown") {
		t.Fatal("helpContent missing Alt+Shift+A Markdown copy binding")
	}
	if !helpHasEntry(lines, "y", "copy qualified object name") {
		t.Fatal("helpContent missing Explorer copy-name binding")
	}
	if !helpHasEntry(lines, "Ctrl+␣", "autocomplete") {
		t.Fatal("helpContent missing Ctrl+␣ autocomplete binding")
	}
	if !helpHasEntry(lines, "⇥ / ⇤", "indent / dedent") {
		t.Fatal("helpContent missing ⇥ / ⇤ indent binding")
	}
	if !helpHasEntry(lines, "␣", "toggle collapse node") {
		t.Fatal("helpContent missing ␣ EXPLAIN binding")
	}
	if !helpHasEntry(lines, "confirm run", "⇥ / ← / →=switch   ↵=confirm") {
		t.Fatal("helpContent missing iconized confirm-run binding")
	}
	if helpHasEntry(lines, "/", "filter") {
		t.Fatal("helpContent still documents stale '/' results filter binding")
	}
	for _, stale := range []struct {
		key  string
		desc string
	}{
		{key: "Ctrl+Space", desc: "autocomplete"},
		{key: "Tab / Shift+Tab", desc: "indent / dedent"},
		{key: "Enter", desc: "jump"},
		{key: "Space", desc: "toggle collapse node"},
		{key: "Lt / Rt", desc: "cycle selected choice"},
		{key: "confirm run", desc: "Tab / Lt / Rt"},
	} {
		if helpHasEntry(lines, stale.key, stale.desc) {
			t.Fatalf("helpContent still documents stale binding %q", stale.key)
		}
	}
}

func TestHelpFilterKeepsSectionContext(t *testing.T) {
	t.Parallel()

	lines := filterHelpLines(helpContent(), "copy row")
	if !helpHasSection(lines, "Results") {
		t.Fatal("filtered help missing Results section")
	}
	if !helpHasEntry(lines, "y / Y", "copy cell / row") {
		t.Fatal("filtered help missing matching row-copy binding")
	}
	if helpHasSection(lines, "Global") {
		t.Fatal("filtered help kept unrelated Global section")
	}
}

func TestHelpFilterSectionMatchShowsWholeSection(t *testing.T) {
	t.Parallel()

	lines := filterHelpLines(helpContent(), "query tabs")
	if !helpHasSection(lines, "Query tabs") {
		t.Fatal("filtered help missing matching Query tabs section")
	}
	if !helpHasEntry(lines, "Ctrl+T", "new tab") {
		t.Fatal("section match did not keep Query tabs bindings")
	}
	if helpHasEntry(lines, "F5", "run query") {
		t.Fatal("section match kept unrelated Query editor binding")
	}
}

func TestDebugBindCatalogUsesIconVocabulary(t *testing.T) {
	t.Parallel()

	var labels []string
	for _, b := range buildDebugBinds() {
		labels = append(labels, b.label)
	}

	for _, want := range []string{
		"Ctrl+C",
		"Ctrl+F",
		"Ctrl+K",
		"Ctrl+␣",
		"Alt+A",
		"Alt+Shift+A",
		"↵",
		"⇥",
		"⇤",
		"Ctrl+←",
		"Ctrl+→",
		"Alt+↑",
		"Alt+↓",
		"Shift+Alt+↑",
		"Shift+Alt+↓",
		"Ctrl+Alt+↑",
		"Ctrl+Alt+↓",
		"s",
		"e",
		"u",
		"w",
		"y",
		"Y",
		"R",
		"X",
		"K",
		"a",
		"c",
		"d",
		"h",
		"n",
		"q",
		"x",
		"␣",
	} {
		if !contains(labels, want) {
			t.Fatalf("debug bind catalog missing %q", want)
		}
	}

	for _, stale := range []string{
		"Ctrl+Space",
		"Enter",
		"Tab",
		"Shift+Tab",
		"Ctrl+Left",
		"Ctrl+Right",
		"Alt+Up",
		"Alt+Down",
		"Shift+Alt+Up",
		"Shift+Alt+Down",
		"Ctrl+Alt+Up",
		"Ctrl+Alt+Down",
		"Space",
	} {
		if contains(labels, stale) {
			t.Fatalf("debug bind catalog still uses stale label %q", stale)
		}
	}
}

func TestRuntimeHintsUseIconVocabulary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		hints string
		want  []string
		stale []string
	}{
		{
			name:  "catalog",
			hints: (&catalogLayer{}).Hints(nil),
			want:  []string{"↑/↓=move", "↵=use"},
			stale: []string{"Up/Dn=move", "Enter=use"},
		},
		{
			name:  "find",
			hints: newFindLayer("").Hints(nil),
			want:  []string{"↵=next/replace", "⇤=prev", "⇥=field"},
			stale: []string{"Enter=next/replace", "Shift+Tab=prev", "Tab=field"},
		},
		{
			name:  "confirm run",
			hints: (&confirmRunLayer{}).Hints(nil),
			want:  []string{"⇥/←/→=switch", "↵=confirm"},
			stale: []string{"Tab/Lt/Rt=switch", "Enter=confirm"},
		},
		{
			name:  "trust",
			hints: (&trustLayer{}).Hints(nil),
			want:  []string{"↵=arm/confirm"},
			stale: []string{"Enter=arm/confirm"},
		},
	} {
		for _, want := range tc.want {
			if !strings.Contains(tc.hints, want) {
				t.Fatalf("%s hints missing %q in %q", tc.name, want, tc.hints)
			}
		}
		for _, stale := range tc.stale {
			if strings.Contains(tc.hints, stale) {
				t.Fatalf("%s hints still use stale %q in %q", tc.name, stale, tc.hints)
			}
		}
	}
}

func TestMainLayerFooterHintsUseIconVocabulary(t *testing.T) {
	t.Parallel()

	explorer := newExplorer()
	explorer.items = []explorerItem{{kind: itemTable}}
	m := &mainLayer{explorer: explorer}
	if hints := m.explorerHints(nil); !strings.Contains(hints, "↵=SELECT") || strings.Contains(hints, "Enter=SELECT") {
		t.Fatalf("explorer footer hints not iconized: %q", hints)
	}

	sess := newSession()
	sess.table.Init([]db.Column{{Name: "id"}})
	if !sess.table.Append([]any{1}) {
		t.Fatal("seed result row for resultsHints")
	}
	m = &mainLayer{session: sess, sessions: []*session{sess}}
	if hints := m.resultsHints(nil); !strings.Contains(hints, "↵=inspect") || strings.Contains(hints, "Enter=inspect") {
		t.Fatalf("results footer hints not iconized: %q", hints)
	}
}

func TestREADMEUsesIconVocabulary(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "README.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	text := string(data)

	for _, want := range []string{
		"**Help overlay**",
		"**Key debug** (`F8`)",
		"**Go to**",
		"**Rename tab**",
		"**Active database picker**",
		"**Results filter**",
		"**Open SQL**",
		"**Save As**",
		"**Export results**",
		"**Driver / transport picker**",
		"`Ctrl+Home` / `Ctrl+End`",
		"`Left-click tab`",
		"`Middle-click tab`",
		"`Ctrl+F` | Filter rows",
		"(`Ctrl+␣`)",
		"(`Ctrl+Alt+↑/↓`)",
		"`↵` or `s` on a table",
		"`↑/↓/←/→` cell nav",
		"`Ctrl+␣` | Autocomplete",
		"`⇥` / `⇤` | Indent / dedent",
		"`Ctrl+Alt+↑ / ↓` | Add multi-cursor line",
		"`Alt+↑ / ↓` | Move line up / down",
		"`Shift+Alt+↑ / ↓` | Duplicate line up / down",
		"`Ctrl+←` / `Ctrl+→` | Word-jump",
		"`↵` | Jump",
		"`↵` | Save",
		"`↵` | Inspect cell",
		"`Alt+Shift+A`",
		"`␣` | Toggle collapse node",
		"`⇥` / `⇤` | Next / previous field",
		"confirm run: `y` / `n` / `Esc` / `⇥` / `←` / `→` / `↵`",
		"SSH trust: `y` / `n` / `Esc` / `↵`",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("README missing %q", want)
		}
	}

	for _, stale := range []string{
		"`/` | Filter rows",
		"`Ctrl+Space`",
		"`Tab` / `Shift+Tab` | Indent / dedent",
		"`Ctrl+Alt+Up/Dn`",
		"`Alt+Up/Dn`",
		"`Shift+Alt+Up/Dn`",
		"`Ctrl+Left` / `Ctrl+Right`",
		"`Enter` | Jump",
		"`Enter` | Save",
		"`Enter` | Inspect cell",
		"`Space` | Toggle collapse node",
		"`Lt` / `Rt`",
		"confirm run: `y` / `n` / `Esc` / `Tab` / `Lt` / `Rt` / `Enter`",
		"SSH trust: `y` / `n` / `Esc` / `Enter`",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("README still documents stale binding %q", stale)
		}
	}
}

func helpHasSection(lines []helpLine, want string) bool {
	for _, line := range lines {
		if line.key == "" && line.desc == want {
			return true
		}
	}
	return false
}

func helpHasEntry(lines []helpLine, key, descSubstr string) bool {
	for _, line := range lines {
		if line.key == key && strings.Contains(line.desc, descSubstr) {
			return true
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
