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

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
