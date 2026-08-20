package app

type BoundValues struct {
	TimeoutMS      int `json:"timeoutMs"`
	MaxAttempts    int `json:"maxAttempts"`
	MaxItems       int `json:"maxItems"`
	MaxConcurrency int `json:"maxConcurrency"`
}

type BoundUnknown struct {
	Reason string `json:"reason"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type BoundResult struct {
	Symbol  string         `json:"symbol"`
	Bounds  map[string]int `json:"bounds"`
	Limits  map[string]int `json:"limits"`
	Unknown []BoundUnknown `json:"unknown"`
}

type BoundViolation struct {
	Symbol    string `json:"symbol"`
	Dimension string `json:"dimension"`
	Actual    int    `json:"actual"`
	Limit     int    `json:"limit"`
}

type BoundsReport struct {
	Results    []BoundResult    `json:"results"`
	Violations []BoundViolation `json:"violations"`
}
