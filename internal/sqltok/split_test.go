package sqltok

import (
	"fmt"
	"testing"
)

func TestSplitStatementsOnlySplitsTopLevelSemicolons(t *testing.T) {
	sql := `SELECT ';' AS quoted;
-- comment with ;
SELECT fn(1; 2);
SELECT $$body; still body$$;
/* block ; comment */ SELECT 4;`
	want := []string{
		`SELECT ';' AS quoted;`,
		"-- comment with ;\nSELECT fn(1; 2);",
		`SELECT $$body; still body$$;`,
		`/* block ; comment */ SELECT 4;`,
	}
	if got := SplitStatements(sql); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("SplitStatements = %#v, want %#v", got, want)
	}
}

func TestSplitStatementsSkipsEmptyAndCommentOnlySegments(t *testing.T) {
	sql := `; -- only a comment
; SELECT 1;;`
	want := []string{"SELECT 1;"}
	if got := SplitStatements(sql); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("SplitStatements = %#v, want %#v", got, want)
	}
}
