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
	"sort"
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
	Outputs     []string        `json:"outputs,omitempty"`
	Error       *Failure        `json:"error,omitempty"`
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, analyzer Analyzer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: slick <check|describe|build> [options]")
		return 2
	}
	switch args[0] {
	case "check":
		return runCheck(ctx, args, stdout, stderr, analyzer)
	case "describe":
		return runDescribe(ctx, args, stdout, stderr, analyzer)
	case "build":
		return runBuild(ctx, args, stdout, stderr, analyzer)
	default:
		fmt.Fprintln(stderr, "usage: slick <check|describe|build> [options]")
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

	analysis := analyzer.Analyze(ctx, AnalyzeRequest{Config: config})
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
	stage, err := os.MkdirTemp(projectRoot, ".slick-build-")
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
		_ = os.Chmod(temporaryName, 0o644)

		backup := ""
		if info, err := os.Stat(final); err == nil {
			if info.IsDir() {
				_ = os.Remove(temporaryName)
				rollback()
				return fmt.Errorf("output path %q is a directory", final)
			}
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
		} else if !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(temporaryName)
			rollback()
			return fmt.Errorf("inspect output: %w", err)
		}
		if err := os.Rename(temporaryName, final); err != nil {
			if backup != "" {
				_ = os.Rename(backup, final)
			}
			rollback()
			return fmt.Errorf("install output: %w", err)
		}
		installed = append(installed, installedOutput{final: final, backup: backup})
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
}

func writeContractField(output io.Writer, name string, value any) {
	encoded, _ := json.Marshal(value)
	fmt.Fprintf(output, "%s: %s\n", name, encoded)
}
