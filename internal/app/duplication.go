package app

type CloneOccurrence struct {
	Path  string `json:"path"`
	Range Range  `json:"range"`
}

type CloneGroup struct {
	Fingerprint string            `json:"fingerprint"`
	Nodes       int               `json:"nodes"`
	Occurrences []CloneOccurrence `json:"occurrences"`
}

type DuplicationReport struct {
	MinNodes       int          `json:"minNodes"`
	MinOccurrences int          `json:"minOccurrences"`
	Clones         []CloneGroup `json:"clones"`
}
