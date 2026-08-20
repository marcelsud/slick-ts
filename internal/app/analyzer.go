package app

import "context"

type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Offset int `json:"offset"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Source      string   `json:"source"`
	Code        int      `json:"code"`
	Category    string   `json:"category"`
	Title       string   `json:"title,omitempty"`
	Message     string   `json:"message"`
	Explanation string   `json:"explanation,omitempty"`
	Fact        string   `json:"fact,omitempty"`
	Repairs     []string `json:"repairs,omitempty"`
	Path        string   `json:"path,omitempty"`
	Range       *Range   `json:"range,omitempty"`
}

type Failure struct {
	Kind         string   `json:"kind"`
	Message      string   `json:"message"`
	Alternatives []string `json:"alternatives,omitempty"`
}
type CacheStats struct {
	Hits   int `json:"hits"`
	Misses int `json:"misses"`
}

type Analysis struct {
	Diagnostics  []Diagnostic         `json:"diagnostics"`
	Summaries    []OperationalSummary `json:"summaries"`
	Descriptions []SymbolDescription  `json:"descriptions"`
	Cache        CacheStats           `json:"cache"`
	Failure      *Failure             `json:"failure,omitempty"`
}

// Analyzer is the only boundary between the Go CLI and TypeScript.
type Analyzer interface {
	Analyze(context.Context, string) Analysis
}
