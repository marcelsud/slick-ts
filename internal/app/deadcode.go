package app

type DeadCodeItem struct {
	Symbol string `json:"symbol"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Range  Range  `json:"range"`
	Module string `json:"module"`
}

type DeadCodeUnknown struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type DeadCodeReport struct {
	Entries     []string          `json:"entries"`
	Unreachable []DeadCodeItem    `json:"unreachable"`
	Unknown     []DeadCodeUnknown `json:"unknown"`
}
