package app

import "sort"

type CRAPResult struct {
	Symbol     string  `json:"symbol"`
	Path       string  `json:"path"`
	Range      Range   `json:"range"`
	Complexity int     `json:"complexity"`
	Coverage   float64 `json:"coverage"`
	Score      float64 `json:"score"`
}

func failingCRAP(results []CRAPResult, threshold float64) []CRAPResult {
	failures := make([]CRAPResult, 0)
	for _, result := range results {
		if result.Score > threshold {
			failures = append(failures, result)
		}
	}
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Score != failures[j].Score {
			return failures[i].Score > failures[j].Score
		}
		if failures[i].Path != failures[j].Path {
			return failures[i].Path < failures[j].Path
		}
		return failures[i].Range.Start.Offset < failures[j].Range.Start.Offset
	})
	return failures
}
