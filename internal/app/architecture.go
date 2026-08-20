package app

type ArchitectureModule struct {
	Path   string `json:"path"`
	Layer  string `json:"layer"`
	FanIn  int    `json:"fanIn"`
	FanOut int    `json:"fanOut"`
}

type ArchitectureEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	TypeOnly bool   `json:"typeOnly"`
	Kind     string `json:"kind"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

type ArchitectureCycle struct {
	Modules []string `json:"modules"`
}

type ArchitectureViolation struct {
	Kind        string `json:"kind"`
	Source      string `json:"source,omitempty"`
	Target      string `json:"target,omitempty"`
	SourceLayer string `json:"sourceLayer,omitempty"`
	TargetLayer string `json:"targetLayer,omitempty"`
	Path        string `json:"path,omitempty"`
	Actual      int    `json:"actual,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Line        int    `json:"line,omitempty"`
	Column      int    `json:"column,omitempty"`
}

type ArchitectureUnknown struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
	Line   int    `json:"line"`
}

type ArchitectureReport struct {
	Modules    []ArchitectureModule    `json:"modules"`
	Edges      []ArchitectureEdge      `json:"edges"`
	Cycles     []ArchitectureCycle     `json:"cycles"`
	Violations []ArchitectureViolation `json:"violations"`
	Unresolved []ArchitectureUnknown   `json:"unresolved"`
}
