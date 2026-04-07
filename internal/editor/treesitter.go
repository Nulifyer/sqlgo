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

func AnalyzeSQL(text string, cursor int) SQLAnalysis {
	analysis := SQLAnalysis{
		Context:        DetectCompletionContext(text, cursor),
		StatementStart: 0,
		StatementEnd:   len(text),
	}

	parser := sitter.NewParser()
	defer parser.Close()
	language := sitter.NewLanguage(unsafe.Pointer(sql.Language()))
	if err := parser.SetLanguage(language); err != nil {
		return analysis
	}

	source := []byte(text)
	tree := parser.Parse(source, nil)
	if tree == nil {
		return analysis
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return analysis
	}

	start, end := byteRangeForCursor(text, cursor)
	node := root.NamedDescendantForByteRange(uint(start), uint(end))
	if node == nil {
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
		return max(cursor-1, 0), cursor
	}
	if cursor > 0 {
		return cursor - 1, cursor
	}
	return 0, min(1, len(text))
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
