package app

import "sort"

type ComplexityResult struct {
	Symbol     string `json:"symbol"`
	Path       string `json:"path"`
	Range      Range  `json:"range"`
	Complexity int    `json:"complexity"`
}

func failingComplexity(results []ComplexityResult, threshold int) []ComplexityResult {
	failures := make([]ComplexityResult, 0)
	for _, result := range results {
		if result.Complexity > threshold {
			failures = append(failures, result)
		}
	}
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Complexity != failures[j].Complexity {
			return failures[i].Complexity > failures[j].Complexity
		}
		if failures[i].Path != failures[j].Path {
			return failures[i].Path < failures[j].Path
		}
		return failures[i].Range.Start.Offset < failures[j].Range.Start.Offset
	})
	return failures
}
