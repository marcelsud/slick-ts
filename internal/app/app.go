package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const documentVersion = 1

type Document struct {
	Version                      int                 `json:"version"`
	Command                      string              `json:"command"`
	Success                      bool                `json:"success"`
	Project                      string              `json:"project,omitempty"`
	Diagnostics                  []Diagnostic        `json:"diagnostics"`
	Contract                     *SymbolContract     `json:"contract,omitempty"`
	Outputs                      []string            `json:"outputs,omitempty"`
	Threshold                    float64             `json:"threshold,omitempty"`
	Coverage                     string              `json:"coverage,omitempty"`
	Functions                    any                 `json:"functions,omitempty"`
	Files                        any                 `json:"files,omitempty"`
	CoverageSummary              *CoverageSummary    `json:"coverageSummary,omitempty"`
	BranchThreshold              float64             `json:"branchThreshold,omitempty"`
	ChangedLineThreshold         float64             `json:"changedLineThreshold,omitempty"`
	UncoveredComplexityThreshold int                 `json:"uncoveredComplexityThreshold,omitempty"`
	Artifacts                    *ArtifactReport     `json:"artifacts,omitempty"`
	MaxTotalBytes                int                 `json:"maxTotalBytes,omitempty"`
	MaxFileBytes                 int                 `json:"maxFileBytes,omitempty"`
	DeniedRuntimeImports         []string            `json:"deniedRuntimeImports,omitempty"`
	DeadCode                     *DeadCodeReport     `json:"deadCode,omitempty"`
	Architecture                 *ArchitectureReport `json:"architecture,omitempty"`
	API                          *APISnapshot        `json:"api,omitempty"`
	Changes                      []APIChange         `json:"changes,omitempty"`
	Baseline                     string              `json:"baseline,omitempty"`
	Duplication                  *DuplicationReport  `json:"duplication,omitempty"`
	Risk                         *RiskReport         `json:"risk,omitempty"`
	Mutation                     *MutationReport     `json:"mutation,omitempty"`
	Bounds                       *BoundsReport       `json:"bounds,omitempty"`
	Error                        *Failure            `json:"error,omitempty"`
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: slick <check|describe|build|crap|complexity|coverage|artifacts|dead-code|architecture|api|duplication|maintainability|risk|mutate|bounds> [options]")
		return 2
	}
	switch args[0] {
	case "check":
		return runCheck(ctx, args, stdout, stderr, analyzer)
	case "describe":
		return runDescribe(ctx, args, stdout, stderr, analyzer)
	case "build":
		return runBuild(ctx, args, stdout, stderr, analyzer)
	case "crap":
		return runCRAP(ctx, args, stdout, stderr, analyzer)
	case "complexity":
		return runComplexity(ctx, args, stdout, stderr, analyzer)
	case "coverage":
		return runCoverage(ctx, args, stdout, stderr, analyzer)
	case "artifacts":
		return runArtifacts(ctx, args, stdout, stderr, analyzer)
	case "dead-code":
		return runDeadCode(ctx, args, stdout, stderr, analyzer)
	case "architecture":
		return runArchitecture(ctx, args, stdout, stderr, analyzer)
	case "api":
		return runAPI(ctx, args, stdout, stderr, analyzer)
	case "duplication":
		return runDuplication(ctx, args, stdout, stderr, analyzer)
	case "maintainability":
		return runMaintainability(ctx, args, stdout, stderr, analyzer)
	case "risk":
		return runRisk(ctx, args, stdout, stderr, analyzer)
	case "mutate":
		return runMutation(ctx, args, stdout, stderr, analyzer)
	case "bounds":
		return runBounds(ctx, args, stdout, stderr, analyzer)
	default:
		fmt.Fprintln(stderr, "usage: slick <check|describe|build|crap|complexity|coverage|artifacts|dead-code|architecture|api|duplication|maintainability|risk|mutate|bounds> [options]")
		return 2
	}
}

func runCheck(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick check [--json] [path]")
		}
		return 2
	}
	path := "."
	if flags.NArg() == 1 {
		path = flags.Arg(0)
	}

	config, err := findConfig(path)
	if err != nil {
		doc := Document{
			Version:     documentVersion,
			Command:     "check",
			Diagnostics: []Diagnostic{},
			Error:       &Failure{Kind: "missing_configuration", Message: err.Error()},
		}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}

	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:     documentVersion,
		Command:     "check",
		Success:     analysis.Failure == nil && !hasErrors(analysis.Diagnostics),
		Project:     "tsconfig.json",
		Diagnostics: analysis.Diagnostics,
		Error:       analysis.Failure,
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runDescribe(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick describe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() < 1 || flags.NArg() > 2 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick describe [--json] <symbol> [path]")
		}
		return 2
	}
	query := flags.Arg(0)
	path := "."
	if flags.NArg() == 2 {
		path = flags.Arg(1)
	}
	config, err := findConfig(path)
	if err != nil {
		doc := Document{
			Version:     documentVersion,
			Command:     "describe",
			Diagnostics: []Diagnostic{},
			Error:       &Failure{Kind: "missing_configuration", Message: err.Error()},
		}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}

	boundsConfig := filepath.Join(filepath.Dir(config), "slick.contracts.json")
	_, boundsErr := os.Stat(boundsConfig)
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, Bounds: boundsErr == nil, BoundsConfig: boundsConfig})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:     documentVersion,
		Command:     "describe",
		Project:     "tsconfig.json",
		Diagnostics: analysis.Diagnostics,
		Error:       analysis.Failure,
	}
	if analysis.Failure == nil {
		description, alternatives, kind := resolveDescription(query, analysis.Descriptions)
		if kind == "" {
			contract := contractFor(description, analysis.Summaries)
			if analysis.Bounds != nil {
				for index := range analysis.Bounds.Results {
					if analysis.Bounds.Results[index].Symbol == contract.CanonicalName {
						contract.Bounds = &analysis.Bounds.Results[index]
						break
					}
				}
			}
			doc.Contract = &contract
			doc.Success = true
		} else {
			message := fmt.Sprintf("symbol %q was not found", query)
			if kind == "ambiguous_symbol" {
				message = fmt.Sprintf("symbol %q is ambiguous", query)
			}
			doc.Error = &Failure{Kind: kind, Message: message, Alternatives: alternatives}
		}
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}
func runBuild(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick build [--json] [path]")
		}
		return 2
	}
	path := "."
	if flags.NArg() == 1 {
		path = flags.Arg(0)
	}
	config, err := findConfig(path)
	if err != nil {
		doc := Document{
			Version:     documentVersion,
			Command:     "build",
			Diagnostics: []Diagnostic{},
			Error:       &Failure{Kind: "missing_configuration", Message: err.Error()},
		}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	projectRoot := filepath.Dir(config)
	stage, err := os.MkdirTemp("", "slick-build-*")
	if err != nil {
		doc := Document{
			Version:     documentVersion,
			Command:     "build",
			Project:     "tsconfig.json",
			Diagnostics: []Diagnostic{},
			Error:       &Failure{Kind: "emit_failure", Message: err.Error()},
		}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	defer os.RemoveAll(stage)

	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, EmitRoot: stage})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:     documentVersion,
		Command:     "build",
		Project:     "tsconfig.json",
		Diagnostics: analysis.Diagnostics,
		Error:       analysis.Failure,
	}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) {
		if err := installOutputs(ctx, stage, analysis.Outputs); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return 130
			}
			doc.Error = &Failure{Kind: "emit_failure", Message: err.Error()}
		} else {
			doc.Success = true
			doc.Outputs = displayOutputPaths(projectRoot, analysis.Outputs)
		}
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runCRAP(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick crap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	coverageFlag := flags.String("coverage", "coverage/coverage-final.json", "read Istanbul coverage JSON")
	threshold := flags.Float64("threshold", 30, "maximum allowed CRAP score")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 ||
		*threshold < 0 || math.IsNaN(*threshold) || math.IsInf(*threshold, 0) {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick crap [--json] [--coverage file] [--threshold score] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{
			Version:     documentVersion,
			Command:     "crap",
			Diagnostics: []Diagnostic{},
			Threshold:   *threshold,
			Error:       &Failure{Kind: "missing_configuration", Message: err.Error()},
		}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	projectRoot := filepath.Dir(config)
	coveragePath := *coverageFlag
	if !filepath.IsAbs(coveragePath) {
		coveragePath = filepath.Join(projectRoot, coveragePath)
	}
	coveragePath = filepath.Clean(coveragePath)
	displayCoverage, err := filepath.Rel(projectRoot, coveragePath)
	if err != nil {
		displayCoverage = coveragePath
	}

	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, CoveragePath: coveragePath})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:     documentVersion,
		Command:     "crap",
		Project:     "tsconfig.json",
		Diagnostics: analysis.Diagnostics,
		Threshold:   *threshold,
		Coverage:    filepath.ToSlash(displayCoverage),
		Error:       analysis.Failure,
	}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) {
		doc.Functions = analysis.CRAP
		doc.Success = len(failingCRAP(analysis.CRAP, *threshold)) == 0
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runComplexity(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick complexity", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	threshold := flags.Int("threshold", 10, "maximum allowed cyclomatic complexity")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 || *threshold < 0 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick complexity [--json] [--threshold 10] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{
			Version:     documentVersion,
			Command:     "complexity",
			Diagnostics: []Diagnostic{},
			Threshold:   float64(*threshold),
			Error:       &Failure{Kind: "missing_configuration", Message: err.Error()},
		}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}

	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:     documentVersion,
		Command:     "complexity",
		Project:     "tsconfig.json",
		Diagnostics: analysis.Diagnostics,
		Threshold:   float64(*threshold),
		Error:       analysis.Failure,
	}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) {
		functions := analysis.Complexity
		if functions == nil {
			functions = []ComplexityResult{}
		}
		doc.Functions = functions
		doc.Success = len(failingComplexity(functions, *threshold)) == 0
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runCoverage(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick coverage", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	coverageFlag := flags.String("coverage", "coverage/coverage-final.json", "read Istanbul coverage JSON")
	base := flags.String("base", "", "compare changed lines against this Git ref")
	branchThreshold := flags.Float64("branch-threshold", 80, "minimum branch coverage percentage")
	changedThreshold := flags.Float64("changed-line-threshold", 90, "minimum changed-line coverage percentage")
	uncoveredThreshold := flags.Int("uncovered-complexity-threshold", 10, "maximum uncovered decisions per function")
	invalidPercent := func(value float64) bool {
		return value < 0 || value > 100 || math.IsNaN(value) || math.IsInf(value, 0)
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 ||
		invalidPercent(*branchThreshold) || invalidPercent(*changedThreshold) || *uncoveredThreshold < 0 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick coverage --coverage file [--json] [--base ref] [--branch-threshold 80] [--changed-line-threshold 90] [--uncovered-complexity-threshold 10] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{
			Version:     documentVersion,
			Command:     "coverage",
			Diagnostics: []Diagnostic{},
			Error:       &Failure{Kind: "missing_configuration", Message: err.Error()},
		}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	projectRoot := filepath.Dir(config)
	coveragePath := *coverageFlag
	if !filepath.IsAbs(coveragePath) {
		coveragePath = filepath.Join(projectRoot, coveragePath)
	}
	coveragePath = filepath.Clean(coveragePath)
	changed, err := gitChangedLines(ctx, projectRoot, *base)
	if err != nil {
		doc := Document{
			Version:     documentVersion,
			Command:     "coverage",
			Project:     "tsconfig.json",
			Diagnostics: []Diagnostic{},
			Error:       &Failure{Kind: "git_failure", Message: err.Error()},
		}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, CoveragePath: coveragePath, CoverageQuality: true})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	displayCoverage, relErr := filepath.Rel(projectRoot, coveragePath)
	if relErr != nil {
		displayCoverage = coveragePath
	}
	doc := Document{
		Version:                      documentVersion,
		Command:                      "coverage",
		Project:                      "tsconfig.json",
		Diagnostics:                  analysis.Diagnostics,
		Coverage:                     filepath.ToSlash(displayCoverage),
		BranchThreshold:              *branchThreshold,
		ChangedLineThreshold:         *changedThreshold,
		UncoveredComplexityThreshold: *uncoveredThreshold,
		Error:                        analysis.Failure,
	}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) && analysis.Coverage != nil {
		summary := summarizeCoverage(*analysis.Coverage, changed)
		doc.CoverageSummary = &summary
		doc.Files = analysis.Coverage.Files
		functions := append([]CoverageFunction(nil), analysis.Coverage.Functions...)
		sortCoverageFunctions(functions)
		doc.Functions = functions
		passed := summary.BranchPercent >= *branchThreshold && summary.ChangedLinePercent >= *changedThreshold
		for _, file := range analysis.Coverage.Files {
			if file.State == "missing" ||
				file.BranchTotal > 0 && percent(file.BranchCovered, file.BranchTotal) < *branchThreshold {
				passed = false
			}
		}
		for _, function := range functions {
			if function.UncoveredDecisions > *uncoveredThreshold {
				passed = false
			}
		}
		doc.Success = passed
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runArtifacts(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick artifacts", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	maxTotal := flags.Int("max-total-bytes", 0, "maximum total emitted bytes; zero disables the limit")
	maxFile := flags.Int("max-file-bytes", 0, "maximum bytes per emitted file; zero disables the limit")
	var denied stringList
	flags.Var(&denied, "deny-runtime-import", "reject a runtime package import; repeatable")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 || *maxTotal < 0 || *maxFile < 0 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick artifacts [--json] [--max-total-bytes N] [--max-file-bytes N] [--deny-runtime-import package] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "artifacts", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	projectRoot := filepath.Dir(config)
	stage, err := os.MkdirTemp("", "slick-artifacts-*")
	if err != nil {
		doc := Document{Version: documentVersion, Command: "artifacts", Project: "tsconfig.json", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "emit_failure", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	defer os.RemoveAll(stage)
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, EmitRoot: stage, Artifacts: true})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:              documentVersion,
		Command:              "artifacts",
		Project:              "tsconfig.json",
		Diagnostics:          analysis.Diagnostics,
		MaxTotalBytes:        *maxTotal,
		MaxFileBytes:         *maxFile,
		DeniedRuntimeImports: append([]string(nil), denied...),
		Error:                analysis.Failure,
	}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) {
		if analysis.Artifacts == nil {
			doc.Error = &Failure{Kind: "emit_failure", Message: "artifact analyzer returned no report"}
		} else {
			report := *analysis.Artifacts
			report.Violations = []ArtifactViolation{}
			deniedSet := map[string]struct{}{}
			for _, value := range denied {
				deniedSet[value] = struct{}{}
			}
			if *maxTotal > 0 && report.TotalBytes > *maxTotal {
				report.Violations = append(report.Violations, ArtifactViolation{Kind: "total_bytes", Actual: report.TotalBytes, Limit: *maxTotal})
			}
			for index := range report.Files {
				file := &report.Files[index]
				displayPath, relErr := filepath.Rel(projectRoot, file.Path)
				if relErr == nil {
					file.Path = filepath.ToSlash(displayPath)
				} else {
					file.Path = filepath.ToSlash(file.Path)
				}
				file.Staged = ""
				if *maxFile > 0 && file.Bytes > *maxFile {
					report.Violations = append(report.Violations, ArtifactViolation{Kind: "file_bytes", Path: file.Path, Actual: file.Bytes, Limit: *maxFile})
				}
				for _, runtimeImport := range file.Imports {
					if _, rejected := deniedSet[runtimeImport.Package]; rejected {
						report.Violations = append(report.Violations, ArtifactViolation{Kind: "runtime_import", Path: file.Path, Package: runtimeImport.Package})
					}
				}
			}
			doc.Artifacts = &report
			if len(report.Violations) == 0 {
				if err := installOutputs(ctx, stage, analysis.Outputs); err != nil {
					if ctx.Err() != nil || errors.Is(err, context.Canceled) {
						return 130
					}
					doc.Error = &Failure{Kind: "emit_failure", Message: err.Error()}
				} else {
					doc.Success = true
					doc.Outputs = displayOutputPaths(projectRoot, analysis.Outputs)
				}
			}
		}
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runDeadCode(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick dead-code", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	var entries stringList
	flags.Var(&entries, "entry", "authored runtime entry file; repeatable")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick dead-code [--json] [--entry file]... [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "dead-code", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	projectRoot := filepath.Dir(config)
	resolvedEntries := make([]string, len(entries))
	for index, entry := range entries {
		if filepath.IsAbs(entry) {
			resolvedEntries[index] = filepath.Clean(entry)
		} else {
			resolvedEntries[index] = filepath.Join(projectRoot, entry)
		}
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, DeadCode: true, Entries: resolvedEntries})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:     documentVersion,
		Command:     "dead-code",
		Project:     "tsconfig.json",
		Diagnostics: analysis.Diagnostics,
		DeadCode:    analysis.DeadCode,
		Error:       analysis.Failure,
	}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) && analysis.DeadCode != nil {
		doc.Success = len(analysis.DeadCode.Unreachable) == 0 && len(analysis.DeadCode.Unknown) == 0
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runArchitecture(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick architecture", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	configFlag := flags.String("config", "slick.architecture.json", "architecture rule file")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick architecture [--json] [--config slick.architecture.json] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "architecture", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	rulePath := *configFlag
	if !filepath.IsAbs(rulePath) {
		rulePath = filepath.Join(filepath.Dir(config), rulePath)
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, Architecture: true, ArchitectureConfig: filepath.Clean(rulePath)})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:      documentVersion,
		Command:      "architecture",
		Project:      "tsconfig.json",
		Diagnostics:  analysis.Diagnostics,
		Architecture: analysis.Architecture,
		Error:        analysis.Failure,
	}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) && analysis.Architecture != nil {
		doc.Success = len(analysis.Architecture.Violations) == 0 && len(analysis.Architecture.Cycles) == 0 && len(analysis.Architecture.Unresolved) == 0
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runAPI(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	if len(args) < 2 || (args[1] != "snapshot" && args[1] != "diff") {
		fmt.Fprintln(stderr, "usage: slick api <snapshot|diff> [options] [path]")
		return 2
	}
	subcommand := args[1]
	flags := flag.NewFlagSet("slick api "+subcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	var entries stringList
	flags.Var(&entries, "entry", "public contract entry; repeatable")
	outputPath := flags.String("output", "slick-api.json", "snapshot output path")
	baselinePath := flags.String("baseline", "slick-api.json", "baseline snapshot path")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() > 1 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick api "+subcommand+" [--json] [--entry export] [--output file|--baseline file] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "api " + subcommand, Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{
		Version:     documentVersion,
		Command:     "api " + subcommand,
		Project:     "tsconfig.json",
		Diagnostics: analysis.Diagnostics,
		Error:       analysis.Failure,
	}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) {
		snapshot, snapshotErr := buildAPISnapshot(analysis, entries, filepath.Dir(config))
		if snapshotErr != nil {
			doc.Error = &Failure{Kind: "api_snapshot_failure", Message: snapshotErr.Error()}
		} else if subcommand == "snapshot" {
			name := *outputPath
			if !filepath.IsAbs(name) {
				name = filepath.Join(filepath.Dir(config), name)
			}
			if err := writeAPISnapshot(name, snapshot); err != nil {
				doc.Error = &Failure{Kind: "api_snapshot_failure", Message: err.Error()}
			} else {
				doc.API = &snapshot
				doc.Outputs = []string{filepath.ToSlash(*outputPath)}
				doc.Success = true
			}
		} else {
			name := *baselinePath
			if !filepath.IsAbs(name) {
				name = filepath.Join(filepath.Dir(config), name)
			}
			content, readErr := os.ReadFile(name)
			if readErr != nil {
				doc.Error = &Failure{Kind: "api_baseline_failure", Message: readErr.Error()}
			} else {
				var baseline APISnapshot
				if err := json.Unmarshal(content, &baseline); err != nil || baseline.Version != apiSnapshotVersion {
					if err == nil {
						err = fmt.Errorf("unsupported snapshot version %d", baseline.Version)
					}
					doc.Error = &Failure{Kind: "api_baseline_failure", Message: err.Error()}
				} else {
					assigner := newTSTypeAssigner(ctx, config)
					changes := diffAPI(baseline, snapshot, assigner.assignable)
					if assigner.err != nil {
						doc.Error = &Failure{Kind: "api_diff_failure", Message: assigner.err.Error()}
					} else {
						doc.API = &snapshot
						doc.Changes = changes
						doc.Baseline = filepath.ToSlash(*baselinePath)
						doc.Success = true
						for _, change := range changes {
							if change.Breaking {
								doc.Success = false
								break
							}
						}
					}
				}
			}
		}
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func writeAPISnapshot(name string, snapshot APISnapshot) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(name), ".slick-api-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, name)
}

func runDuplication(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick duplication", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	minNodes := flags.Int("min-nodes", 20, "minimum normalized AST nodes")
	minOccurrences := flags.Int("min-occurrences", 2, "minimum clone occurrences")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 || *minNodes < 1 || *minOccurrences < 2 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick duplication [--json] [--min-nodes 20] [--min-occurrences 2] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "duplication", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, Duplication: true, MinCloneNodes: *minNodes, MinCloneOccurrences: *minOccurrences})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{Version: documentVersion, Command: "duplication", Project: "tsconfig.json", Diagnostics: analysis.Diagnostics, Duplication: analysis.Duplication, Error: analysis.Failure}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) && analysis.Duplication != nil {
		doc.Success = len(analysis.Duplication.Clones) == 0
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runMaintainability(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick maintainability", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	threshold := flags.Float64("threshold", 0, "minimum maintainability index; zero reports without failing")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 || *threshold < 0 || *threshold > 100 || math.IsNaN(*threshold) || math.IsInf(*threshold, 0) {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick maintainability [--json] [--threshold 20] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "maintainability", Diagnostics: []Diagnostic{}, Threshold: *threshold, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, Maintainability: true})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{Version: documentVersion, Command: "maintainability", Project: "tsconfig.json", Diagnostics: analysis.Diagnostics, Threshold: *threshold, Functions: analysis.Maintainability, Error: analysis.Failure}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) {
		doc.Success = len(failingMaintainability(analysis.Maintainability, *threshold)) == 0
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runRisk(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick risk", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	base := flags.String("base", "", "Git base revision")
	historyWindow := flags.String("history", "90 days ago", "Git history window")
	coverageFlag := flags.String("coverage", "", "optional Istanbul coverage JSON")
	configFlag := flags.String("config", "slick.risk.json", "risk weights and threshold")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 || *base == "" || *historyWindow == "" {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick risk --base ref [--history 90d] [--coverage file] [--config slick.risk.json] [--json] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "risk", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	projectRoot := filepath.Dir(config)
	riskConfigPath := *configFlag
	if !filepath.IsAbs(riskConfigPath) {
		riskConfigPath = filepath.Join(projectRoot, riskConfigPath)
	}
	riskSettings, err := loadRiskConfig(riskConfigPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "risk", Project: "tsconfig.json", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "risk_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	changed, err := gitChangedLines(ctx, projectRoot, *base)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "risk", Project: "tsconfig.json", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "git_failure", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	coveragePath := *coverageFlag
	if coveragePath != "" && !filepath.IsAbs(coveragePath) {
		coveragePath = filepath.Join(projectRoot, coveragePath)
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, CoveragePath: coveragePath, Risk: true})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	shallowCommand := exec.CommandContext(ctx, "git", "rev-parse", "--is-shallow-repository")
	shallowCommand.Dir = projectRoot
	shallowOutput, shallowErr := shallowCommand.Output()
	shallow := shallowErr == nil && strings.TrimSpace(string(shallowOutput)) == "true"
	results := []RiskResult{}
	historyByPath := map[string]historyStats{}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) {
		for _, input := range analysis.RiskInputs {
			changedFile := changed[input.Path]
			changedCount := 0
			for line := input.Range.Start.Line; line <= input.Range.End.Line; line++ {
				if _, ok := changedFile[line]; ok {
					changedCount++
				}
			}
			if changedCount == 0 {
				continue
			}
			history, ok := historyByPath[input.Path]
			if !ok {
				history, err = gitHistory(ctx, projectRoot, input.Path, *historyWindow)
				if err != nil {
					doc := Document{Version: documentVersion, Command: "risk", Project: "tsconfig.json", Diagnostics: analysis.Diagnostics, Error: &Failure{Kind: "git_failure", Message: err.Error()}}
					writeDocument(stdout, stderr, doc, *jsonOutput)
					return 1
				}
				historyByPath[input.Path] = history
			}
			results = append(results, scoreRisk(input, changedCount, history, riskSettings.Weights, shallow))
		}
		sort.Slice(results, func(i, j int) bool {
			if results[i].Score != results[j].Score {
				return results[i].Score > results[j].Score
			}
			if results[i].Path != results[j].Path {
				return results[i].Path < results[j].Path
			}
			return results[i].Range.Start.Offset < results[j].Range.Start.Offset
		})
	}
	report := &RiskReport{Base: *base, History: *historyWindow, Threshold: riskSettings.Threshold, Weights: riskSettings.Weights, Results: results}
	doc := Document{Version: documentVersion, Command: "risk", Project: "tsconfig.json", Diagnostics: analysis.Diagnostics, Risk: report, Error: analysis.Failure}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) {
		doc.Success = true
		if riskSettings.Threshold > 0 {
			for _, result := range results {
				if result.Score > riskSettings.Threshold {
					doc.Success = false
					break
				}
			}
		}
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runMutation(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	separator := -1
	for index, value := range args {
		if value == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator == len(args)-1 {
		fmt.Fprintln(stderr, "usage: slick mutate [--json] [--timeout 30s] [--max-mutants 200] [--coverage file] [path] -- <test command> [args...]")
		return 2
	}
	flags := flag.NewFlagSet("slick mutate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	timeoutFlag := flags.String("timeout", "30s", "timeout per test run")
	maxMutants := flags.Int("max-mutants", 200, "maximum deterministic mutants")
	coverageFlag := flags.String("coverage", "", "optional Istanbul coverage JSON")
	if err := flags.Parse(args[1:separator]); err != nil || flags.NArg() > 1 || *maxMutants < 1 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick mutate [--json] [--timeout 30s] [--max-mutants 200] [--coverage file] [path] -- <test command> [args...]")
		}
		return 2
	}
	timeout, err := time.ParseDuration(*timeoutFlag)
	if err != nil || timeout <= 0 {
		fmt.Fprintln(stderr, "usage: slick mutate [--json] [--timeout 30s] [--max-mutants 200] [--coverage file] [path] -- <test command> [args...]")
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "mutate", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	projectRoot := filepath.Dir(config)
	coveragePath := *coverageFlag
	if coveragePath != "" && !filepath.IsAbs(coveragePath) {
		coveragePath = filepath.Join(projectRoot, coveragePath)
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, CoveragePath: coveragePath, Mutation: true})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{Version: documentVersion, Command: "mutate", Project: "tsconfig.json", Diagnostics: analysis.Diagnostics, Error: analysis.Failure}
	if analysis.Failure != nil || hasErrors(analysis.Diagnostics) {
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	isolated, err := os.MkdirTemp("", "slick-mutate-*")
	if err != nil {
		doc.Error = &Failure{Kind: "mutation_failure", Message: err.Error()}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	defer os.RemoveAll(isolated)
	if err := copyMutationProject(projectRoot, isolated); err != nil {
		doc.Error = &Failure{Kind: "mutation_failure", Message: err.Error()}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "node_modules")); err == nil {
		if err := os.Symlink(filepath.Join(projectRoot, "node_modules"), filepath.Join(isolated, "node_modules")); err != nil {
			doc.Error = &Failure{Kind: "mutation_failure", Message: err.Error()}
			writeDocument(stdout, stderr, doc, *jsonOutput)
			return 1
		}
	}
	testArgv := append([]string(nil), args[separator+1:]...)
	status, testErr := runTestCommand(ctx, isolated, timeout, testArgv)
	if status != "survived" || testErr != nil {
		if ctx.Err() != nil {
			return 130
		}
		doc.Error = &Failure{Kind: "test_command_failure", Message: "original test command did not pass"}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	candidates := analysis.Mutants
	if len(candidates) > *maxMutants {
		candidates = candidates[:*maxMutants]
	}
	coverageBySymbol := map[string]float64{}
	for _, value := range analysis.CRAP {
		coverageBySymbol[value.Symbol] = value.Coverage
	}
	report := &MutationReport{TestCommand: testArgv, Results: []MutationResult{}}
	isolatedConfig, _ := filepath.Rel(projectRoot, config)
	isolatedConfig = filepath.Join(isolated, isolatedConfig)
	for _, candidate := range candidates {
		result := MutationResult{MutationCandidate: candidate}
		if coveragePath != "" && candidate.Symbol != "" {
			coverage, measured := coverageBySymbol[candidate.Symbol]
			if !measured {
				result.Status = "coverage_unknown"
				report.Results = append(report.Results, result)
				continue
			}
			if coverage == 0 {
				result.Status = "not_covered"
				report.Results = append(report.Results, result)
				continue
			}
		}
		original, err := applyMutation(isolated, candidate)
		if err != nil {
			result.Status = "invalid"
			report.Results = append(report.Results, result)
			continue
		}
		mutantAnalysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: isolatedConfig})
		if ctx.Err() != nil {
			_ = os.WriteFile(filepath.Join(isolated, filepath.FromSlash(candidate.Path)), original, 0o644)
			return 130
		}
		if mutantAnalysis.Failure != nil || hasErrors(mutantAnalysis.Diagnostics) {
			result.Status = "invalid"
		} else {
			status, _ := runTestCommand(ctx, isolated, timeout, testArgv)
			result.Status = status
		}
		_ = os.WriteFile(filepath.Join(isolated, filepath.FromSlash(candidate.Path)), original, 0o644)
		if ctx.Err() != nil {
			return 130
		}
		report.Results = append(report.Results, result)
	}
	finishMutationReport(report)
	doc.Mutation = report
	doc.Success = report.Survived == 0 && report.TimedOut == 0 && report.CoverageUnknown == 0
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

func runBounds(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	flags := flag.NewFlagSet("slick bounds", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "write versioned JSON")
	contracts := flags.String("contracts", "slick.contracts.json", "resource-bound contract file")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 1 {
		if err == nil {
			fmt.Fprintln(stderr, "usage: slick bounds [--json] [--contracts slick.contracts.json] [path]")
		}
		return 2
	}
	projectPath := "."
	if flags.NArg() == 1 {
		projectPath = flags.Arg(0)
	}
	config, err := findConfig(projectPath)
	if err != nil {
		doc := Document{Version: documentVersion, Command: "bounds", Diagnostics: []Diagnostic{}, Error: &Failure{Kind: "missing_configuration", Message: err.Error()}}
		writeDocument(stdout, stderr, doc, *jsonOutput)
		return 1
	}
	contractPath := *contracts
	if !filepath.IsAbs(contractPath) {
		contractPath = filepath.Join(filepath.Dir(config), contractPath)
	}
	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config, Bounds: true, BoundsConfig: filepath.Clean(contractPath)})
	if ctx.Err() != nil || analysis.Failure != nil && analysis.Failure.Kind == "interrupted" {
		return 130
	}
	doc := Document{Version: documentVersion, Command: "bounds", Project: "tsconfig.json", Diagnostics: analysis.Diagnostics, Bounds: analysis.Bounds, Error: analysis.Failure}
	if analysis.Failure == nil && !hasErrors(analysis.Diagnostics) && analysis.Bounds != nil {
		doc.Success = len(analysis.Bounds.Violations) == 0
		for _, result := range analysis.Bounds.Results {
			if len(result.Unknown) > 0 {
				doc.Success = false
				break
			}
		}
	}
	writeDocument(stdout, stderr, doc, *jsonOutput)
	if doc.Success {
		return 0
	}
	return 1
}

type installedOutput struct {
	final  string
	backup string
}

func installOutputs(ctx context.Context, stage string, outputs []BuildOutput) error {
	ordered := append([]BuildOutput(nil), outputs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	installed := make([]installedOutput, 0, len(ordered))
	createdDirectories := []string{}
	seen := map[string]struct{}{}
	rollback := func() {
		for index := len(installed) - 1; index >= 0; index-- {
			output := installed[index]
			_ = os.RemoveAll(output.final)
			if output.backup != "" {
				_ = os.Rename(output.backup, output.final)
			}
		}
		for index := len(createdDirectories) - 1; index >= 0; index-- {
			_ = os.Remove(createdDirectories[index])
		}
	}

	for _, output := range ordered {
		if err := ctx.Err(); err != nil {
			rollback()
			return err
		}
		if !filepath.IsAbs(output.Path) {
			rollback()
			return fmt.Errorf("emit returned non-absolute output path %q", output.Path)
		}
		final := filepath.Clean(output.Path)
		if _, duplicate := seen[final]; duplicate {
			rollback()
			return fmt.Errorf("emit returned duplicate output path %q", final)
		}
		seen[final] = struct{}{}
		staged := filepath.Join(stage, filepath.FromSlash(output.Staged))
		relative, err := filepath.Rel(stage, staged)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			rollback()
			return fmt.Errorf("emit returned invalid staged path %q", output.Staged)
		}
		parent := filepath.Dir(final)
		missing := missingDirectories(parent)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			rollback()
			return fmt.Errorf("create output directory: %w", err)
		}
		createdDirectories = append(createdDirectories, missing...)

		stagedInfo, err := os.Stat(staged)
		if err != nil {
			rollback()
			return fmt.Errorf("inspect staged output: %w", err)
		}
		source, err := os.Open(staged)
		if err != nil {
			rollback()
			return fmt.Errorf("open staged output: %w", err)
		}
		temporary, err := os.CreateTemp(parent, ".slick-output-*")
		if err != nil {
			source.Close()
			rollback()
			return fmt.Errorf("create output file: %w", err)
		}
		temporaryName := temporary.Name()
		_, copyErr := io.Copy(temporary, source)
		closeErr := temporary.Close()
		source.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(temporaryName)
			rollback()
			if copyErr != nil {
				return fmt.Errorf("copy output: %w", copyErr)
			}
			return fmt.Errorf("close output: %w", closeErr)
		}
		if err := ctx.Err(); err != nil {
			_ = os.Remove(temporaryName)
			rollback()
			return err
		}

		mode := stagedInfo.Mode().Perm()
		existing, statErr := os.Lstat(final)
		if statErr == nil {
			if existing.IsDir() {
				_ = os.Remove(temporaryName)
				rollback()
				return fmt.Errorf("output path %q is a directory", final)
			}
			if existing.Mode().IsRegular() {
				mode = existing.Mode().Perm()
			} else if target, err := os.Stat(final); err == nil && target.Mode().IsRegular() {
				mode = target.Mode().Perm()
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			_ = os.Remove(temporaryName)
			rollback()
			return fmt.Errorf("inspect output: %w", statErr)
		}
		if err := os.Chmod(temporaryName, mode); err != nil {
			_ = os.Remove(temporaryName)
			rollback()
			return fmt.Errorf("set output mode: %w", err)
		}

		backup := ""
		if statErr == nil {
			reservation, err := os.CreateTemp(parent, ".slick-backup-*")
			if err != nil {
				_ = os.Remove(temporaryName)
				rollback()
				return fmt.Errorf("reserve output backup: %w", err)
			}
			backup = reservation.Name()
			reservation.Close()
			_ = os.Remove(backup)
			if err := os.Rename(final, backup); err != nil {
				_ = os.Remove(temporaryName)
				rollback()
				return fmt.Errorf("back up output: %w", err)
			}
		}
		if err := ctx.Err(); err != nil {
			_ = os.Remove(temporaryName)
			if backup != "" {
				_ = os.Rename(backup, final)
			}
			rollback()
			return err
		}
		if err := os.Rename(temporaryName, final); err != nil {
			if backup != "" {
				_ = os.Rename(backup, final)
			}
			rollback()
			return fmt.Errorf("install output: %w", err)
		}
		installed = append(installed, installedOutput{final: final, backup: backup})
		if err := ctx.Err(); err != nil {
			rollback()
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		rollback()
		return err
	}
	for _, output := range installed {
		if output.backup != "" {
			_ = os.Remove(output.backup)
		}
	}
	return nil
}

func missingDirectories(path string) []string {
	missing := []string{}
	for current := path; ; current = filepath.Dir(current) {
		if _, err := os.Stat(current); err == nil {
			break
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	sort.Slice(missing, func(i, j int) bool { return len(missing[i]) < len(missing[j]) })
	return missing
}

func displayOutputPaths(projectRoot string, outputs []BuildOutput) []string {
	paths := make([]string, 0, len(outputs))
	for _, output := range outputs {
		value, err := filepath.Rel(projectRoot, output.Path)
		if err != nil {
			value = output.Path
		}
		paths = append(paths, filepath.ToSlash(value))
	}
	sort.Strings(paths)
	return paths
}

func findConfig(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err == nil && !info.IsDir() {
		if filepath.Base(absolute) == "tsconfig.json" {
			return absolute, nil
		}
		absolute = filepath.Dir(absolute)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect %s: %w", path, err)
	}

	for {
		candidate := filepath.Join(absolute, "tsconfig.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			break
		}
		absolute = parent
	}
	return "", fmt.Errorf("no tsconfig.json applies to %s", path)
}

func hasErrors(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Category == "error" {
			return true
		}
	}
	return false
}

func writeDocument(stdout, stderr io.Writer, doc Document, jsonOutput bool) {
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(doc); err != nil {
			fmt.Fprintln(stderr, "slick: write output:", err)
		}
		return
	}
	for _, diagnostic := range doc.Diagnostics {
		location := diagnostic.Path
		if diagnostic.Range != nil {
			location = fmt.Sprintf("%s:%d:%d", location, diagnostic.Range.Start.Line, diagnostic.Range.Start.Column)
		}
		if location != "" {
			location += " - "
		}
		prefix := "TS"
		message := diagnostic.Message
		if diagnostic.Source == "slick" {
			prefix = "SLICK"
			message = diagnostic.Title + ": " + diagnostic.Explanation
			if diagnostic.Fact != "" {
				message += " Fact: " + diagnostic.Fact + "."
			}
			if len(diagnostic.Repairs) > 0 {
				message += " Repair: " + strings.Join(diagnostic.Repairs, "; ") + "."
			}
		}
		fmt.Fprintf(stdout, "%s%s %s%d: %s\n", location, diagnostic.Category, prefix, diagnostic.Code, message)
	}
	if doc.Contract != nil {
		writeContract(stdout, *doc.Contract)
	}
	for _, output := range doc.Outputs {
		fmt.Fprintln(stdout, "emitted:", output)
	}
	if doc.Error == nil {
		switch doc.Command {
		case "crap":
			results, _ := doc.Functions.([]CRAPResult)
			for _, result := range results {
				status := "ok"
				if result.Score > doc.Threshold {
					status = "fail"
				}
				fmt.Fprintf(
					stdout,
					"%s:%d:%d - %s CRAP %.2f (complexity %d, coverage %.1f%%): %s\n",
					result.Path,
					result.Range.Start.Line,
					result.Range.Start.Column,
					status,
					result.Score,
					result.Complexity,
					result.Coverage*100,
					result.Symbol,
				)
			}
		case "complexity":
			results, _ := doc.Functions.([]ComplexityResult)
			for _, result := range results {
				status := "ok"
				if result.Complexity > int(doc.Threshold) {
					status = "fail"
				}
				fmt.Fprintf(
					stdout,
					"%s:%d:%d - %s complexity %d: %s\n",
					result.Path,
					result.Range.Start.Line,
					result.Range.Start.Column,
					status,
					result.Complexity,
					result.Symbol,
				)
			}
		case "coverage":
			if doc.CoverageSummary != nil {
				fmt.Fprintf(stdout, "branch coverage: %.1f%%\n", doc.CoverageSummary.BranchPercent)
				fmt.Fprintf(stdout, "changed-line coverage: %.1f%% (%d/%d)\n", doc.CoverageSummary.ChangedLinePercent, doc.CoverageSummary.ChangedCovered, doc.CoverageSummary.ChangedTotal)
			}
			files, _ := doc.Files.([]CoverageFile)
			for _, file := range files {
				fmt.Fprintf(stdout, "%s - %s, branch coverage %.1f%% (%d/%d)\n", file.Path, file.State, percent(file.BranchCovered, file.BranchTotal), file.BranchCovered, file.BranchTotal)
			}
			functions, _ := doc.Functions.([]CoverageFunction)
			for _, function := range functions {
				if function.UncoveredDecisions > doc.UncoveredComplexityThreshold {
					fmt.Fprintf(stdout, "%s:%d:%d - fail uncovered decisions %d (complexity %d): %s\n", function.Path, function.Range.Start.Line, function.Range.Start.Column, function.UncoveredDecisions, function.Complexity, function.Symbol)
				}
			}
		case "artifacts":
			if doc.Artifacts != nil {
				fmt.Fprintf(stdout, "total emitted bytes: %d\n", doc.Artifacts.TotalBytes)
				for _, file := range doc.Artifacts.Files {
					fmt.Fprintf(stdout, "%s - %d bytes\n", file.Path, file.Bytes)
					for _, runtimeImport := range file.Imports {
						fmt.Fprintf(stdout, "  runtime import %s (%s)\n", runtimeImport.Specifier, runtimeImport.Kind)
					}
				}
				for _, violation := range doc.Artifacts.Violations {
					fmt.Fprintf(stdout, "fail %s: %s%s\n", violation.Kind, violation.Path, violation.Package)
				}
			}
		case "dead-code":
			if doc.DeadCode != nil {
				for _, item := range doc.DeadCode.Unreachable {
					fmt.Fprintf(stdout, "%s:%d:%d - unreachable %s: %s\n", item.Path, item.Range.Start.Line, item.Range.Start.Column, item.Kind, item.Symbol)
				}
				for _, unknown := range doc.DeadCode.Unknown {
					fmt.Fprintf(stdout, "unknown %s: %s\n", unknown.Reason, unknown.Message)
				}
			}
		case "architecture":
			if doc.Architecture != nil {
				for _, module := range doc.Architecture.Modules {
					fmt.Fprintf(stdout, "%s - layer %s, fan-in %d, fan-out %d\n", module.Path, module.Layer, module.FanIn, module.FanOut)
				}
				for _, cycle := range doc.Architecture.Cycles {
					fmt.Fprintf(stdout, "fail cycle: %s\n", strings.Join(cycle.Modules, " -> "))
				}
				for _, violation := range doc.Architecture.Violations {
					fmt.Fprintf(stdout, "fail %s: %s%s\n", violation.Kind, violation.Source, violation.Path)
				}
				for _, unknown := range doc.Architecture.Unresolved {
					fmt.Fprintf(stdout, "unknown %s:%d: %s\n", unknown.Source, unknown.Line, unknown.Reason)
				}
			}
		case "api snapshot":
			if doc.API != nil {
				fmt.Fprintf(stdout, "wrote %d public contracts to %s\n", len(doc.API.Contracts), strings.Join(doc.Outputs, ", "))
			}
		case "api diff":
			for _, change := range doc.Changes {
				status := "info"
				if change.Breaking {
					status = "breaking"
				}
				fmt.Fprintf(stdout, "%s %s %s: %s\n", status, change.Symbol, change.Kind, change.Detail)
			}
		case "duplication":
			if doc.Duplication != nil {
				for _, clone := range doc.Duplication.Clones {
					fmt.Fprintf(stdout, "clone %s - %d nodes, %d occurrences\n", clone.Fingerprint, clone.Nodes, len(clone.Occurrences))
					for _, occurrence := range clone.Occurrences {
						fmt.Fprintf(stdout, "  %s:%d:%d\n", occurrence.Path, occurrence.Range.Start.Line, occurrence.Range.Start.Column)
					}
				}
			}
		case "maintainability":
			results, _ := doc.Functions.([]MaintainabilityResult)
			for _, result := range results {
				status := "ok"
				if doc.Threshold > 0 && result.Index < doc.Threshold {
					status = "fail"
				}
				fmt.Fprintf(stdout, "%s:%d:%d - %s maintainability %.2f (volume %.2f, complexity %d, LOC %d): %s\n", result.Path, result.Range.Start.Line, result.Range.Start.Column, status, result.Index, result.Volume, result.Complexity, result.LOC, result.Symbol)
			}
		case "risk":
			if doc.Risk != nil {
				for _, result := range doc.Risk.Results {
					status := "ok"
					if doc.Risk.Threshold > 0 && result.Score > doc.Risk.Threshold {
						status = "fail"
					}
					fmt.Fprintf(stdout, "%s:%d:%d - %s risk %.2f: %s", result.Path, result.Range.Start.Line, result.Range.Start.Column, status, result.Score, result.Symbol)
					if len(result.Missing) > 0 {
						fmt.Fprintf(stdout, " (missing %s)", strings.Join(result.Missing, ", "))
					}
					fmt.Fprintln(stdout)
				}
			}
		case "mutate":
			if doc.Mutation != nil {
				fmt.Fprintf(stdout, "mutation score: %.1f%% (%d killed, %d survived, %d timed out, %d invalid, %d not covered, %d coverage unknown)\n", doc.Mutation.Score, doc.Mutation.Killed, doc.Mutation.Survived, doc.Mutation.TimedOut, doc.Mutation.Invalid, doc.Mutation.NotCovered, doc.Mutation.CoverageUnknown)
				for _, result := range doc.Mutation.Results {
					fmt.Fprintf(stdout, "%s %s:%d:%d %s\n", result.Status, result.Path, result.Range.Start.Line, result.Range.Start.Column, result.ID)
				}
			}
		case "bounds":
			if doc.Bounds != nil {
				for _, result := range doc.Bounds.Results {
					fmt.Fprintf(stdout, "%s bounds: %v limits: %v\n", result.Symbol, result.Bounds, result.Limits)
					for _, unknown := range result.Unknown {
						fmt.Fprintf(stdout, "  unknown %s at %s:%d:%d\n", unknown.Reason, unknown.Path, unknown.Line, unknown.Column)
					}
				}
				for _, violation := range doc.Bounds.Violations {
					fmt.Fprintf(stdout, "fail %s %s: %d > %d\n", violation.Symbol, violation.Dimension, violation.Actual, violation.Limit)
				}
			}
		}
	}
	if doc.Error != nil {
		fmt.Fprintf(stderr, "slick: %s: %s\n", doc.Error.Kind, doc.Error.Message)
		for _, alternative := range doc.Error.Alternatives {
			fmt.Fprintln(stderr, "  ", alternative)
		}
	}
}

func writeContract(output io.Writer, contract SymbolContract) {
	fmt.Fprintf(output, "%s (%s, %s)\n", contract.CanonicalName, contract.Kind, contract.Visibility)
	fmt.Fprintln(output, "name:", contract.Name)
	writeContractField(output, "location", contract.Location)
	fmt.Fprintln(output, "documentation:", contract.Documentation)
	writeContractField(output, "aliases", contract.Aliases)
	writeContractField(output, "type parameters", contract.TypeParameters)
	writeContractField(output, "parameters", contract.Parameters)
	writeContractField(output, "signatures", contract.Signatures)
	if contract.Return != nil {
		writeContractField(output, "return", contract.Return)
	}
	writeContractField(output, "members", contract.Members)
	if contract.Package != nil {
		writeContractField(output, "package", contract.Package)
	}
	if contract.Execution != "" {
		fmt.Fprintln(output, "execution:", contract.Execution)
	}
	writeContractField(output, "errors", contract.Errors)
	writeContractField(output, "effects", contract.Effects)
	fmt.Fprintln(output, "completeness:", contract.Completeness)
	writeContractField(output, "unresolved", contract.Unresolved)
	if contract.Bounds != nil {
		writeContractField(output, "bounds", contract.Bounds)
	}
}

func writeContractField(output io.Writer, name string, value any) {
	encoded, _ := json.Marshal(value)
	fmt.Fprintf(output, "%s: %s\n", name, encoded)
}
