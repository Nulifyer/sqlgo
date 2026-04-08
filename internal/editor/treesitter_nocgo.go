//go:build !cgo

package editor

type SQLAnalysis struct {
	Context        CompletionContext
	StatementStart int
	StatementEnd   int
}

// Analyzer is a no-op stand-in for the cgo build's tree-sitter backed
// analyzer. It still provides statement range detection via the pure-Go
// tokenizer so the editor can run statement-under-cursor without cgo.
type Analyzer struct{}

func NewAnalyzer() *Analyzer { return &Analyzer{} }

func (a *Analyzer) Close() {}

func (a *Analyzer) Analyze(text string, cursor int) SQLAnalysis {
	analysis := SQLAnalysis{
		Context: DetectCompletionContext(text, cursor),
	}
	analysis.StatementStart, analysis.StatementEnd = StatementRangeAt(text, cursor)
	if analysis.StatementEnd <= analysis.StatementStart {
		analysis.StatementStart = 0
		analysis.StatementEnd = len(text)
	}
	// Populate aliases via the regex-style fallback so non-cgo builds still
	// resolve qualified columns.
	analysis.Context.Aliases = extractAliases(text)
	return analysis
}

func AnalyzeSQL(text string, cursor int) SQLAnalysis {
	return (&Analyzer{}).Analyze(text, cursor)
}
