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
	Source   string `json:"source"`
	Code     int    `json:"code"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	Range    *Range `json:"range,omitempty"`
}

type Failure struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}
type CacheStats struct {
	Hits   int `json:"hits"`
	Misses int `json:"misses"`
}

type Analysis struct {
	Diagnostics []Diagnostic         `json:"diagnostics"`
	Summaries   []OperationalSummary `json:"summaries"`
	Cache       CacheStats           `json:"cache"`
	Failure     *Failure             `json:"failure,omitempty"`
}

// Analyzer is the only boundary between the Go CLI and TypeScript.
type Analyzer interface {
	Analyze(context.Context, string) Analysis
}
