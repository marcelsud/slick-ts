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

type NodeAnalyzer struct{}

func (NodeAnalyzer) Analyze(ctx context.Context, config string) Analysis {
	node, err := exec.LookPath("node")
	if err != nil {
		return failed("missing_toolchain", "Node.js was not found in PATH")
	}

	cmd := exec.CommandContext(ctx, node, "--input-type=module", "--eval", analyzerSource)
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

	var result Analysis
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return failed("analyzer_failure", "invalid analyzer response: "+err.Error())
	}
	if result.Diagnostics == nil {
		result.Diagnostics = []Diagnostic{}
	}
	return result
}

func failed(kind, message string) Analysis {
	return Analysis{Diagnostics: []Diagnostic{}, Failure: &Failure{Kind: kind, Message: message}}
}
