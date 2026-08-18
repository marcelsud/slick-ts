package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNodeAnalyzerUsesTypeScriptDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"strict":true},"include":["src"]}`)
	writeTestFile(t, filepath.Join(root, "src", "main.ts"), `const value: string = 42;`)
	compiler, err := filepath.Abs(filepath.Join("..", "..", "node_modules", "typescript", "lib", "typescript.js"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLICK_TYPESCRIPT_PATH", compiler)

	result := (NodeAnalyzer{}).Analyze(context.Background(), filepath.Join(root, "tsconfig.json"))
	if result.Failure != nil {
		t.Fatalf("unexpected failure: %+v", result.Failure)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics: %+v", len(result.Diagnostics), result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != 2322 || diagnostic.Path != "src/main.ts" || diagnostic.Range == nil || diagnostic.Range.Start.Line != 1 || diagnostic.Range.Start.Column != 7 {
		t.Fatalf("unexpected diagnostic: %+v", diagnostic)
	}
}

func TestNodeAnalyzerBuildsOperationalSummaries(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "tsconfig.json"), `{
		"compilerOptions":{"strict":true,"target":"ES2022","lib":["ES2022","DOM"]},
		"files":["main.ts"]
	}`)
	writeTestFile(t, filepath.Join(root, "main.ts"), `class NetworkError extends Error {}
async function request(): Promise<void> {
	await fetch("https://example.com");
	throw new NetworkError("failed");
}
async function caller(): Promise<void> {
	await request();
}`)
	compiler, err := filepath.Abs(filepath.Join("..", "..", "node_modules", "typescript", "lib", "typescript.js"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLICK_TYPESCRIPT_PATH", compiler)

	result := (NodeAnalyzer{}).Analyze(context.Background(), filepath.Join(root, "tsconfig.json"))
	if result.Failure != nil || len(result.Diagnostics) != 0 {
		t.Fatalf("analysis failed: failure=%+v diagnostics=%+v", result.Failure, result.Diagnostics)
	}
	request := summaryNamed(t, result.Summaries, "main.ts::request")
	if request.Execution != ExecutionAsynchronous || len(request.Errors) != 1 || request.Errors[0].Name != "NetworkError" || len(request.Effects) != 1 || request.Effects[0].Name != "network" {
		t.Fatalf("unexpected request summary: %+v", request)
	}
	caller := summaryNamed(t, result.Summaries, "main.ts::caller")
	if len(caller.Errors) != 1 || caller.Errors[0].Name != "NetworkError" || len(caller.Effects) != 1 || caller.Effects[0].Name != "network" {
		t.Fatalf("unexpected caller summary: %+v", caller)
	}
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
