package app

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
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

type analyzerResponse struct {
	Diagnostics  []Diagnostic        `json:"diagnostics"`
	Graph        []operationalNode   `json:"graph"`
	Descriptions []SymbolDescription `json:"descriptions"`
	Outputs      []BuildOutput       `json:"outputs"`
	CRAP         []CRAPResult        `json:"crap"`
	Cache        CacheStats          `json:"cache"`
	Failure      *Failure            `json:"failure,omitempty"`
}

type NodeAnalyzer struct{}

func (NodeAnalyzer) Analyze(ctx context.Context, request AnalyzeRequest) Analysis {
	node, err := exec.LookPath("node")
	if err != nil {
		return failed("missing_toolchain", "Node.js was not found in PATH")
	}

	cmd := exec.CommandContext(ctx, node, "--input-type=module", "--eval", packagesSource+"\n"+operationalSource+"\n"+strictSource+"\n"+describeSource+"\n"+buildSource+"\n"+crapSource+"\n"+analyzerSource)
	cmd.Env = make([]string, 0, len(os.Environ())+3)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "SLICK_CONFIG_PATH=") &&
			!strings.HasPrefix(variable, "SLICK_EMIT_ROOT=") &&
			!strings.HasPrefix(variable, "SLICK_COVERAGE_PATH=") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "SLICK_CONFIG_PATH="+request.Config)
	if request.EmitRoot != "" {
		cmd.Env = append(cmd.Env, "SLICK_EMIT_ROOT="+request.EmitRoot)
	}
	if request.CoveragePath != "" {
		cmd.Env = append(cmd.Env, "SLICK_COVERAGE_PATH="+request.CoveragePath)
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
		Diagnostics:  response.Diagnostics,
		Summaries:    summarize(response.Graph),
		Descriptions: response.Descriptions,
		Outputs:      response.Outputs,
		CRAP:         response.CRAP,
		Cache:        response.Cache,
		Failure:      response.Failure,
	}
}

func failed(kind, message string) Analysis {
	return Analysis{
		Diagnostics:  []Diagnostic{},
		Summaries:    []OperationalSummary{},
		Descriptions: []SymbolDescription{},
		Outputs:      []BuildOutput{},
		CRAP:         []CRAPResult{},
		Failure:      &Failure{Kind: kind, Message: message},
	}
}
