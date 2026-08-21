package app

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

//go:embed analyzer.mjs
var analyzerSource string

//go:embed operational.mjs
var operationalSource string

//go:embed packages.mjs
var packagesSource string

//go:embed strict.mjs
var strictSource string

//go:embed describe.mjs
var describeSource string

//go:embed build.mjs
var buildSource string

//go:embed crap.mjs
var crapSource string

//go:embed complexity.mjs
var complexitySource string

//go:embed coverage.mjs
var coverageSource string

//go:embed artifacts.mjs
var artifactsSource string

//go:embed deadcode.mjs
var deadCodeSource string

//go:embed architecture.mjs
var architectureSource string

//go:embed duplication.mjs
var duplicationSource string

//go:embed maintainability.mjs
var maintainabilitySource string

//go:embed risk.mjs
var riskSource string

//go:embed mutation.mjs
var mutationSource string

//go:embed bounds.mjs
var boundsSource string

type analyzerResponse struct {
	Diagnostics     []Diagnostic            `json:"diagnostics"`
	Graph           []operationalNode       `json:"graph"`
	Descriptions    []SymbolDescription     `json:"descriptions"`
	Outputs         []BuildOutput           `json:"outputs"`
	CRAP            []CRAPResult            `json:"crap"`
	Complexity      []ComplexityResult      `json:"complexity"`
	Coverage        *CoverageReport         `json:"coverageReport,omitempty"`
	Artifacts       *ArtifactReport         `json:"artifacts,omitempty"`
	DeadCode        *DeadCodeReport         `json:"deadCode,omitempty"`
	Architecture    *ArchitectureReport     `json:"architecture,omitempty"`
	Duplication     *DuplicationReport      `json:"duplication,omitempty"`
	Maintainability []MaintainabilityResult `json:"maintainability"`
	RiskInputs      []RiskInput             `json:"riskInputs"`
	Mutants         []MutationCandidate     `json:"mutants"`
	Bounds          *BoundsReport           `json:"bounds,omitempty"`
	Cache           CacheStats              `json:"cache"`
	Failure         *Failure                `json:"failure,omitempty"`
}

type NodeAnalyzer struct{}

func (NodeAnalyzer) Analyze(ctx context.Context, request AnalyzeRequest) Analysis {
	node, err := exec.LookPath("node")
	if err != nil {
		return failed("missing_toolchain", "Node.js was not found in PATH")
	}

	script, err := os.CreateTemp("", "slick-analyzer-*.mjs")
	if err != nil {
		return failed("analyzer_failure", "create analyzer script: "+err.Error())
	}
	scriptName := script.Name()
	defer os.Remove(scriptName)
	source := packagesSource + "\n" + operationalSource + "\n" + strictSource + "\n" +
		describeSource + "\n" + buildSource + "\n" + complexitySource + "\n" +
		crapSource + "\n" + coverageSource + "\n" + artifactsSource + "\n" +
		deadCodeSource + "\n" + architectureSource + "\n" + duplicationSource + "\n" +
		maintainabilitySource + "\n" + riskSource + "\n" + mutationSource + "\n" +
		boundsSource + "\n" + analyzerSource
	if _, err := script.WriteString(source); err != nil {
		script.Close()
		return failed("analyzer_failure", "write analyzer script: "+err.Error())
	}
	if err := script.Close(); err != nil {
		return failed("analyzer_failure", "close analyzer script: "+err.Error())
	}
	cmd := exec.CommandContext(ctx, node, scriptName)
	cmd.Env = make([]string, 0, len(os.Environ())+3)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "SLICK_CONFIG_PATH=") &&
			!strings.HasPrefix(variable, "SLICK_DESCRIPTIONS=") &&
			!strings.HasPrefix(variable, "SLICK_EMIT_ROOT=") &&
			!strings.HasPrefix(variable, "SLICK_COVERAGE_PATH=") &&
			!strings.HasPrefix(variable, "SLICK_COVERAGE_QUALITY=") &&
			!strings.HasPrefix(variable, "SLICK_ARTIFACTS=") &&
			!strings.HasPrefix(variable, "SLICK_DEAD_CODE=") &&
			!strings.HasPrefix(variable, "SLICK_DEAD_ENTRIES=") &&
			!strings.HasPrefix(variable, "SLICK_ARCHITECTURE=") &&
			!strings.HasPrefix(variable, "SLICK_ARCHITECTURE_CONFIG=") &&
			!strings.HasPrefix(variable, "SLICK_DUPLICATION=") &&
			!strings.HasPrefix(variable, "SLICK_MIN_CLONE_NODES=") &&
			!strings.HasPrefix(variable, "SLICK_MIN_CLONE_OCCURRENCES=") &&
			!strings.HasPrefix(variable, "SLICK_MAINTAINABILITY=") &&
			!strings.HasPrefix(variable, "SLICK_RISK=") &&
			!strings.HasPrefix(variable, "SLICK_MUTATION=") &&
			!strings.HasPrefix(variable, "SLICK_BOUNDS=") &&
			!strings.HasPrefix(variable, "SLICK_BOUNDS_CONFIG=") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "SLICK_CONFIG_PATH="+request.Config)
	if request.NeedDescriptions || request.DeadCode || request.Bounds {
		cmd.Env = append(cmd.Env, "SLICK_DESCRIPTIONS=1")
	}
	if request.EmitRoot != "" {
		cmd.Env = append(cmd.Env, "SLICK_EMIT_ROOT="+request.EmitRoot)
	}
	if request.CoveragePath != "" {
		cmd.Env = append(cmd.Env, "SLICK_COVERAGE_PATH="+request.CoveragePath)
	}
	if request.CoverageQuality {
		cmd.Env = append(cmd.Env, "SLICK_COVERAGE_QUALITY=1")
	}
	if request.Artifacts {
		cmd.Env = append(cmd.Env, "SLICK_ARTIFACTS=1")
	}
	if request.DeadCode {
		cmd.Env = append(cmd.Env, "SLICK_DEAD_CODE=1")
		entries, _ := json.Marshal(request.Entries)
		cmd.Env = append(cmd.Env, "SLICK_DEAD_ENTRIES="+string(entries))
	}
	if request.Architecture {
		cmd.Env = append(cmd.Env, "SLICK_ARCHITECTURE=1", "SLICK_ARCHITECTURE_CONFIG="+request.ArchitectureConfig)
	}
	if request.Duplication {
		cmd.Env = append(cmd.Env,
			"SLICK_DUPLICATION=1",
			"SLICK_MIN_CLONE_NODES="+strconv.Itoa(request.MinCloneNodes),
			"SLICK_MIN_CLONE_OCCURRENCES="+strconv.Itoa(request.MinCloneOccurrences),
		)
	}
	if request.Maintainability {
		cmd.Env = append(cmd.Env, "SLICK_MAINTAINABILITY=1")
	}
	if request.Risk {
		cmd.Env = append(cmd.Env, "SLICK_RISK=1")
	}
	if request.Mutation {
		cmd.Env = append(cmd.Env, "SLICK_MUTATION=1")
	}
	if request.Bounds {
		cmd.Env = append(cmd.Env, "SLICK_BOUNDS=1", "SLICK_BOUNDS_CONFIG="+request.BoundsConfig)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return failed("interrupted", ctx.Err().Error())
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return failed("analyzer_failure", message)
	}

	var response analyzerResponse
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return failed("analyzer_failure", "invalid analyzer response: "+err.Error())
	}
	if response.Diagnostics == nil {
		response.Diagnostics = []Diagnostic{}
	}
	return Analysis{
		Diagnostics:     response.Diagnostics,
		Summaries:       summarize(response.Graph),
		Descriptions:    response.Descriptions,
		Outputs:         response.Outputs,
		CRAP:            response.CRAP,
		Complexity:      response.Complexity,
		Coverage:        response.Coverage,
		Artifacts:       response.Artifacts,
		DeadCode:        response.DeadCode,
		Architecture:    response.Architecture,
		Duplication:     response.Duplication,
		Maintainability: response.Maintainability,
		RiskInputs:      response.RiskInputs,
		Mutants:         response.Mutants,
		Bounds:          response.Bounds,
		Cache:           response.Cache,
		Failure:         response.Failure,
	}
}

func failed(kind, message string) Analysis {
	return Analysis{
		Diagnostics:     []Diagnostic{},
		Summaries:       []OperationalSummary{},
		Descriptions:    []SymbolDescription{},
		Outputs:         []BuildOutput{},
		CRAP:            []CRAPResult{},
		Complexity:      []ComplexityResult{},
		Coverage:        nil,
		Artifacts:       nil,
		DeadCode:        nil,
		Architecture:    nil,
		Duplication:     nil,
		Maintainability: []MaintainabilityResult{},
		RiskInputs:      []RiskInput{},
		Mutants:         []MutationCandidate{},
		Bounds:          nil,
		Failure:         &Failure{Kind: kind, Message: message},
	}
}
