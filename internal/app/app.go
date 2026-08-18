package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const documentVersion = 1

type Document struct {
	Version     int             `json:"version"`
	Command     string          `json:"command"`
	Success     bool            `json:"success"`
	Project     string          `json:"project,omitempty"`
	Diagnostics []Diagnostic    `json:"diagnostics"`
	Contract    *SymbolContract `json:"contract,omitempty"`
	Error       *Failure        `json:"error,omitempty"`
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: slick <check|describe> [options]")
		return 2
	}
	switch args[0] {
	case "check":
		return runCheck(ctx, args, stdout, stderr, analyzer)
	case "describe":
		return runDescribe(ctx, args, stdout, stderr, analyzer)
	default:
		fmt.Fprintln(stderr, "usage: slick <check|describe> [options]")
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

	analysis := analyzer.Analyze(ctx, config)
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

	analysis := analyzer.Analyze(ctx, config)
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
	if doc.Error != nil {
		fmt.Fprintf(stderr, "slick: %s: %s\n", doc.Error.Kind, doc.Error.Message)
		for _, alternative := range doc.Error.Alternatives {
			fmt.Fprintln(stderr, "  ", alternative)
		}
	}
}

func writeContract(output io.Writer, contract SymbolContract) {
	fmt.Fprintf(output, "%s (%s, %s)\n", contract.CanonicalName, contract.Kind, contract.Visibility)
	fmt.Fprintf(output, "source: %s:%d:%d\n", contract.Location.Path, contract.Location.Range.Start.Line, contract.Location.Range.Start.Column)
	if contract.Documentation != "" {
		fmt.Fprintln(output, "documentation:", contract.Documentation)
	}
	if len(contract.TypeParameters) > 0 {
		value, _ := json.Marshal(contract.TypeParameters)
		fmt.Fprintln(output, "type parameters:", string(value))
	}
	if len(contract.Parameters) > 0 {
		value, _ := json.Marshal(contract.Parameters)
		fmt.Fprintln(output, "parameters:", string(value))
	}
	if contract.Return != nil {
		value, _ := json.Marshal(contract.Return)
		fmt.Fprintln(output, "return:", string(value))
	}
	if contract.Execution != "" {
		fmt.Fprintln(output, "execution:", contract.Execution)
	}
	for _, fact := range contract.Errors {
		fmt.Fprintln(output, "error:", fact.Name)
	}
	for _, fact := range contract.Effects {
		fmt.Fprintln(output, "effect:", fact.Name)
	}
	fmt.Fprintln(output, "completeness:", contract.Completeness)
	for _, leaf := range contract.Unresolved {
		fmt.Fprintf(output, "unresolved: %s (%s)\n", leaf.Symbol, leaf.Reason)
	}
}
