package editor

import "testing"

func TestAnalyzeSQLUsesTreeSitterAliases(t *testing.T) {
	t.Parallel()

	src := "SELECT u.name FROM users u JOIN projects p ON p.owner_id = u.id WHERE u.name LIKE 'a%'"
	analysis := AnalyzeSQL(src, len("SELECT u.na"))

	if analysis.Context.Qualifier != "u" {
		t.Fatalf("Qualifier = %q, want %q", analysis.Context.Qualifier, "u")
	}
	if len(analysis.Context.Aliases) < 2 {
		t.Fatalf("expected aliases, got %#v", analysis.Context.Aliases)
	}
	if analysis.Context.Aliases[0].Alias == "" {
		t.Fatalf("expected non-empty alias data")
	}
	if analysis.StatementEnd <= analysis.StatementStart {
		t.Fatalf("unexpected statement range: %d-%d", analysis.StatementStart, analysis.StatementEnd)
	}
}
