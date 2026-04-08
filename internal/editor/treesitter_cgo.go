//go:build cgo

package editor

import (
	"strings"
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
	sql "github.com/wippyai/tree-sitter-sql/bindings/go"
)

type SQLAnalysis struct {
	Context        CompletionContext
	StatementStart int
	StatementEnd   int
}

// Analyzer holds a reusable tree-sitter parser and caches the most recently
// parsed tree so the editor only re-parses when the buffer text actually
// changes. The zero value is not usable; construct via NewAnalyzer.
type Analyzer struct {
	parser   *sitter.Parser
	lastText string
	lastTree *sitter.Tree
}

// NewAnalyzer constructs an Analyzer with the SQL grammar loaded. If the
// grammar fails to load the returned Analyzer is still safe to use; it just
// degrades to the regex-style fallback (no tree-sitter aliases).
func NewAnalyzer() *Analyzer {
	parser := sitter.NewParser()
	language := sitter.NewLanguage(unsafe.Pointer(sql.Language()))
	if err := parser.SetLanguage(language); err != nil {
		parser.Close()
		return &Analyzer{}
	}
	return &Analyzer{parser: parser}
}

// Close releases the parser and any cached tree. After Close the Analyzer
// must not be used.
func (a *Analyzer) Close() {
	if a == nil {
		return
	}
	if a.lastTree != nil {
		a.lastTree.Close()
		a.lastTree = nil
	}
	if a.parser != nil {
		a.parser.Close()
		a.parser = nil
	}
}

// Analyze parses (or returns the cached parse of) the buffer and returns the
// completion context plus the byte range of the statement under the cursor.
func (a *Analyzer) Analyze(text string, cursor int) SQLAnalysis {
	analysis := SQLAnalysis{
		Context: DetectCompletionContext(text, cursor),
	}
	analysis.StatementStart, analysis.StatementEnd = StatementRangeAt(text, cursor)
	if analysis.StatementEnd <= analysis.StatementStart {
		analysis.StatementStart = 0
		analysis.StatementEnd = len(text)
	}

	if a == nil || a.parser == nil {
		return analysis
	}

	tree := a.parseCached(text)
	if tree == nil {
		return analysis
	}

	root := tree.RootNode()
	if root == nil {
		return analysis
	}

	source := []byte(text)
	start, end := byteRangeForCursor(text, cursor)
	node := root.NamedDescendantForByteRange(uint(start), uint(end))
	if node == nil {
		analysis.Context.Aliases = collectAliases(root, source)
		return analysis
	}

	statement := ascendToKind(node, "statement")
	if statement != nil {
		analysis.StatementStart = int(statement.StartByte())
		analysis.StatementEnd = int(statement.EndByte())
		analysis.Context.Aliases = collectAliases(statement, source)
	} else {
		analysis.Context.Aliases = collectAliases(root, source)
	}

	return analysis
}

// AnalyzeSQL is a convenience wrapper that allocates a one-shot Analyzer.
// Long-running editors should hold an Analyzer instance and reuse it.
func AnalyzeSQL(text string, cursor int) SQLAnalysis {
	a := NewAnalyzer()
	defer a.Close()
	return a.Analyze(text, cursor)
}

func (a *Analyzer) parseCached(text string) *sitter.Tree {
	if a.lastTree != nil && a.lastText == text {
		return a.lastTree
	}
	tree := a.parser.Parse([]byte(text), nil)
	if a.lastTree != nil {
		a.lastTree.Close()
	}
	a.lastTree = tree
	a.lastText = text
	return tree
}

func byteRangeForCursor(text string, cursor int) (int, int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(text) {
		cursor = len(text)
	}
	if len(text) == 0 {
		return 0, 0
	}
	if cursor == len(text) {
		if cursor > 0 {
			return cursor - 1, cursor
		}
		return 0, 0
	}
	if cursor > 0 {
		return cursor - 1, cursor
	}
	return 0, minInt(1, len(text))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ascendToKind(node *sitter.Node, kind string) *sitter.Node {
	for node != nil {
		if node.Kind() == kind {
			return node
		}
		node = node.Parent()
	}
	return nil
}

func collectAliases(root *sitter.Node, source []byte) []aliasBinding {
	if root == nil {
		return nil
	}
	cursor := root.Walk()
	defer cursor.Close()
	return collectAliasesWalk(root, source, cursor)
}

func collectAliasesWalk(node *sitter.Node, source []byte, cursor *sitter.TreeCursor) []aliasBinding {
	if node == nil {
		return nil
	}

	var aliases []aliasBinding
	if node.Kind() == "relation" {
		aliasNode := node.ChildByFieldName("alias")
		nameNode := firstObjectReference(node, cursor)
		if aliasNode != nil && nameNode != nil {
			alias := strings.TrimSpace(aliasNode.Utf8Text(source))
			table := objectReferenceName(nameNode, source)
			if alias != "" && table != "" {
				aliases = append(aliases, aliasBinding{Alias: alias, Table: table})
			}
		}
	}

	for _, child := range node.NamedChildren(cursor) {
		childCopy := child
		aliases = append(aliases, collectAliasesWalk(&childCopy, source, cursor)...)
	}
	return aliases
}

func firstObjectReference(node *sitter.Node, cursor *sitter.TreeCursor) *sitter.Node {
	for _, child := range node.NamedChildren(cursor) {
		childCopy := child
		if childCopy.Kind() == "object_reference" {
			return &childCopy
		}
	}
	return nil
}

func objectReferenceName(node *sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil {
		return strings.TrimSpace(nameNode.Utf8Text(source))
	}
	return strings.TrimSpace(node.Utf8Text(source))
}
