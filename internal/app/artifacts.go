package app

type RuntimeImport struct {
	Specifier string `json:"specifier"`
	Package   string `json:"package"`
	Builtin   bool   `json:"builtin"`
	Kind      string `json:"kind"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
}

type ArtifactFile struct {
	Path    string          `json:"path"`
	Staged  string          `json:"staged,omitempty"`
	Bytes   int             `json:"bytes"`
	Imports []RuntimeImport `json:"imports"`
}

type ArtifactViolation struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Package string `json:"package,omitempty"`
	Actual  int    `json:"actual,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type ArtifactReport struct {
	TotalBytes int                 `json:"totalBytes"`
	Files      []ArtifactFile      `json:"files"`
	Violations []ArtifactViolation `json:"violations"`
}

type stringList []string

func (values *stringList) String() string {
	if values == nil {
		return ""
	}
	result := ""
	for index, value := range *values {
		if index > 0 {
			result += ","
		}
		result += value
	}
	return result
}

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}
