package app

import "sort"

type MaintainabilityResult struct {
	Symbol            string  `json:"symbol"`
	Path              string  `json:"path"`
	Range             Range   `json:"range"`
	Complexity        int     `json:"complexity"`
	DistinctOperators int     `json:"distinctOperators"`
	DistinctOperands  int     `json:"distinctOperands"`
	OperatorCount     int     `json:"operatorCount"`
	OperandCount      int     `json:"operandCount"`
	Vocabulary        int     `json:"vocabulary"`
	Length            int     `json:"length"`
	Volume            float64 `json:"volume"`
	LOC               int     `json:"loc"`
	Index             float64 `json:"index"`
}

func failingMaintainability(values []MaintainabilityResult, threshold float64) []MaintainabilityResult {
	if threshold <= 0 {
		return nil
	}
	result := []MaintainabilityResult{}
	for _, value := range values {
		if value.Index < threshold {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Index != result[j].Index {
			return result[i].Index < result[j].Index
		}
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Range.Start.Offset < result[j].Range.Start.Offset
	})
	return result
}
