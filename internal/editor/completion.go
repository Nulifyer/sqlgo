package editor

import (
	"slices"
	"strings"

	"github.com/nulifyer/sqlgo/internal/db"
)

type CompletionContext struct {
	Start     int
	End       int
	Prefix    string
	Qualifier string
	Aliases   []aliasBinding
}

type CompletionItem struct {
	Label  string
	Insert string
	Detail string
	Kind   string
	Score  int
}

type aliasBinding struct {
	Alias string
	Table string
}

var completionKeywords = []string{
	"SELECT", "FROM", "WHERE", "GROUP BY", "ORDER BY", "HAVING", "JOIN",
	"LEFT JOIN", "RIGHT JOIN", "INNER JOIN", "OUTER JOIN", "ON", "INSERT INTO",
	"UPDATE", "DELETE FROM", "VALUES", "SET", "LIMIT", "OFFSET", "UNION",
	"WITH", "AS", "NULL", "AND", "OR", "NOT", "IN", "EXISTS",
}

func DetectCompletionContext(text string, cursor int) CompletionContext {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(text) {
		cursor = len(text)
	}

	start := cursor
	for start > 0 && isIdentChar(text[start-1]) {
		start--
	}

	qualifier := ""
	if start > 0 && text[start-1] == '.' {
		qEnd := start - 1
		qStart := qEnd
		for qStart > 0 && isIdentChar(text[qStart-1]) {
			qStart--
		}
		qualifier = text[qStart:qEnd]
	}

	return CompletionContext{
		Start:     start,
		End:       cursor,
		Prefix:    text[start:cursor],
		Qualifier: qualifier,
	}
}

func BuildCompletionItems(meta db.CompletionMetadata, sqlText string, ctx CompletionContext) []CompletionItem {
	aliases := ctx.Aliases
	if len(aliases) == 0 {
		aliases = extractAliases(sqlText)
	}
	prefixLower := strings.ToLower(ctx.Prefix)

	var items []CompletionItem
	push := func(item CompletionItem) {
		score := scoreItem(item, prefixLower)
		if score < 0 {
			return
		}
		item.Score = score
		items = append(items, item)
	}

	if ctx.Qualifier != "" {
		resolved := resolveAliasTable(ctx.Qualifier, aliases)
		for _, object := range meta.Objects {
			if !strings.EqualFold(object.Name, resolved) && !strings.EqualFold(object.Qualified, resolved) {
				continue
			}
			for _, column := range object.Columns {
				push(CompletionItem{
					Label:  column,
					Insert: column,
					Detail: object.Name + " column",
					Kind:   "column",
				})
			}
		}
		return dedupeAndSort(items)
	}

	for _, keyword := range completionKeywords {
		push(CompletionItem{
			Label:  keyword,
			Insert: keyword,
			Detail: "SQL keyword",
			Kind:   "keyword",
		})
	}

	for _, catalog := range meta.Catalogs {
		push(CompletionItem{
			Label:  catalog,
			Insert: catalog,
			Detail: "catalog",
			Kind:   "catalog",
		})
	}
	for _, schema := range meta.Schemas {
		push(CompletionItem{
			Label:  schema,
			Insert: schema,
			Detail: "schema",
			Kind:   "schema",
		})
	}

	for _, alias := range aliases {
		push(CompletionItem{
			Label:  alias.Alias,
			Insert: alias.Alias,
			Detail: alias.Table + " alias",
			Kind:   "alias",
		})
	}

	for _, object := range meta.Objects {
		kind := string(object.Type)
		push(CompletionItem{
			Label:  object.Name,
			Insert: object.Name,
			Detail: kind,
			Kind:   kind,
		})
		for _, column := range object.Columns {
			push(CompletionItem{
				Label:  column,
				Insert: column,
				Detail: object.Name + " column",
				Kind:   "column",
			})
		}
	}

	return dedupeAndSort(items)
}

func extractAliases(sqlText string) []aliasBinding {
	tokens := tokenize(sqlText)
	var aliases []aliasBinding
	for i := 0; i < len(tokens); i++ {
		if tokens[i].kind != tokenWord {
			continue
		}
		word := strings.ToUpper(tokens[i].text)
		if word != "FROM" && word != "JOIN" && word != "UPDATE" && word != "INTO" {
			continue
		}

		tableIndex := nextWordToken(tokens, i+1)
		if tableIndex < 0 {
			continue
		}
		tableName := tokens[tableIndex].text

		aliasIndex := nextWordToken(tokens, tableIndex+1)
		if aliasIndex < 0 {
			continue
		}
		alias := tokens[aliasIndex].text
		if strings.EqualFold(alias, "AS") {
			aliasIndex = nextWordToken(tokens, aliasIndex+1)
			if aliasIndex < 0 {
				continue
			}
			alias = tokens[aliasIndex].text
		}
		if _, ok := keywords[strings.ToUpper(alias)]; ok {
			continue
		}
		aliases = append(aliases, aliasBinding{Alias: alias, Table: tableName})
	}
	return aliases
}

func nextWordToken(tokens []token, index int) int {
	for index < len(tokens) {
		if tokens[index].kind == tokenWord || tokens[index].kind == tokenQuotedIdentifier {
			return index
		}
		index++
	}
	return -1
}

func resolveAliasTable(qualifier string, aliases []aliasBinding) string {
	for _, alias := range aliases {
		if strings.EqualFold(alias.Alias, qualifier) {
			return alias.Table
		}
	}
	return qualifier
}

func dedupeAndSort(items []CompletionItem) []CompletionItem {
	seen := map[string]struct{}{}
	filtered := make([]CompletionItem, 0, len(items))
	for _, item := range items {
		key := strings.ToLower(item.Kind + "|" + item.Insert)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, item)
	}

	slices.SortFunc(filtered, func(a, b CompletionItem) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label))
	})
	return filtered
}

func scoreItem(item CompletionItem, prefix string) int {
	if prefix == "" {
		switch item.Kind {
		case "alias":
			return 120
		case "table", "view":
			return 110
		case "column":
			return 90
		case "keyword":
			return 70
		default:
			return 60
		}
	}

	labelLower := strings.ToLower(item.Label)
	insertLower := strings.ToLower(item.Insert)

	// Exact and prefix matches always rank highest so deterministic typing
	// behavior is preserved.
	switch {
	case labelLower == prefix || insertLower == prefix:
		return 1000
	case strings.HasPrefix(labelLower, prefix) || strings.HasPrefix(insertLower, prefix):
		return 700
	}

	// Otherwise fall back to a fuzzy subsequence match. This lets users
	// type "uid" to find "user_id" and similar abbreviations.
	labelScore := fuzzySubsequenceScore(prefix, labelLower)
	insertScore := fuzzySubsequenceScore(prefix, insertLower)
	best := labelScore
	if insertScore > best {
		best = insertScore
	}
	if best < 0 {
		return -1
	}
	return best
}

// fuzzySubsequenceScore returns -1 if every byte of pattern does not appear,
// in order, somewhere in target. Otherwise it returns a positive score that
// rewards consecutive matches and matches that fall on word boundaries.
func fuzzySubsequenceScore(pattern, target string) int {
	if pattern == "" {
		return 0
	}
	if len(target) < len(pattern) {
		return -1
	}

	score := 0
	streak := 0
	pi := 0
	firstMatch := -1
	for ti := 0; ti < len(target) && pi < len(pattern); ti++ {
		if target[ti] != pattern[pi] {
			streak = 0
			continue
		}
		if firstMatch < 0 {
			firstMatch = ti
		}
		streak++
		score += 10 + streak*5
		if ti == 0 || isFuzzyBoundary(target[ti-1]) {
			score += 15
		}
		pi++
	}
	if pi < len(pattern) {
		return -1
	}
	if firstMatch > 0 {
		score -= firstMatch
	}
	return score
}

func isFuzzyBoundary(prev byte) bool {
	return prev == '_' || prev == '.' || prev == ' ' || prev == '-'
}

func isIdentChar(ch byte) bool {
	return ch == '_' || ch == '$' || ch == '"' || ch == ']' || ch == '[' ||
		(ch >= '0' && ch <= '9') ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z')
}
