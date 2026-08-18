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

type analyzerResponse struct {
	Diagnostics  []Diagnostic        `json:"diagnostics"`
	Graph        []operationalNode   `json:"graph"`
	Descriptions []SymbolDescription `json:"descriptions"`
	Cache        CacheStats          `json:"cache"`
	Failure      *Failure            `json:"failure,omitempty"`
}

type NodeAnalyzer struct{}

func (NodeAnalyzer) Analyze(ctx context.Context, config string) Analysis {
	node, err := exec.LookPath("node")
	if err != nil {
		return failed("missing_toolchain", "Node.js was not found in PATH")
	}

	cmd := exec.CommandContext(ctx, node, "--input-type=module", "--eval", packagesSource+"\n"+operationalSource+"\n"+strictSource+"\n"+describeSource+"\n"+analyzerSource)
	cmd.Env = append(os.Environ(), "SLICK_CONFIG_PATH="+config)
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
		Cache:        response.Cache,
		Failure:      response.Failure,
	}
}

func failed(kind, message string) Analysis {
	return Analysis{
		Diagnostics:  []Diagnostic{},
		Summaries:    []OperationalSummary{},
		Descriptions: []SymbolDescription{},
		Failure:      &Failure{Kind: kind, Message: message},
	}
}
