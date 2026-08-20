package slick_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndToEndCheckDescribeBuildAndRun(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("testdata", "e2e"))); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("npm", "install", "--ignore-scripts", "--no-audit", "--no-fund")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install fixture dependencies: %v\n%s", err, output)
	}

	firstCheck, firstErr, firstCode := runSlick(t, root, nil, "check", "--json")
	secondCheck, secondErr, secondCode := runSlick(t, root, nil, "check", "--json")
	if firstCode != 0 || secondCode != 0 || firstErr != "" || secondErr != "" || firstCheck != secondCheck || !decodeOutput(t, firstCheck).Success {
		t.Fatalf("check codes %d/%d, stderr %q/%q, output:\n%s\n%s", firstCode, secondCode, firstErr, secondErr, firstCheck, secondCheck)
	}

	request := runDescribe(t, root, "src/main.ts::request")
	if len(request.Contract.Effects) != 1 || request.Contract.Effects[0].Name != "network" || request.Contract.Completeness != "complete" {
		t.Fatalf("network contract: %+v", request.Contract)
	}
	failure := runDescribe(t, root, "src/main.ts::fail")
	if len(failure.Contract.Errors) != 1 || failure.Contract.Errors[0].Name != "DependencyError" {
		t.Fatalf("error contract: %+v", failure.Contract)
	}
	unresolved := runDescribe(t, root, "unresolvedCall")
	if unresolved.Contract.Completeness != "partial" || len(unresolved.Contract.Unresolved) != 1 {
		t.Fatalf("unresolved contract: %+v", unresolved.Contract)
	}
	leaf := unresolved.Contract.Unresolved[0]
	if leaf.Symbol != "declarationOnly" || leaf.Reason != "declaration_only" ||
		leaf.Package == nil || leaf.Package.Name != "declaration-only" ||
		leaf.Package.Version != "4.5.6" || leaf.Package.Export != "." ||
		leaf.Package.Declaration != "packages/declaration-only/index.d.ts" ||
		len(leaf.Provenance) != 1 {
		t.Fatalf("unresolved leaf identity: %+v", leaf)
	}
	evidence := leaf.Provenance[0]
	if evidence.Symbol != "packages/declaration-only/index.d.ts::declarationOnly" ||
		evidence.Path != "packages/declaration-only/index.d.ts" ||
		evidence.Range.Start.Line != 1 || evidence.Range.Start.Column != 25 || evidence.Range.Start.Offset != 24 ||
		evidence.Range.End.Line != 1 || evidence.Range.End.Column != 40 || evidence.Range.End.Offset != 39 {
		t.Fatalf("unresolved leaf evidence: %+v", evidence)
	}

	buildOutput, buildErr, buildCode := runSlick(t, root, nil, "build", "--json")
	build := decodeBuild(t, buildOutput)
	if buildCode != 0 || buildErr != "" || !build.Success || len(build.Outputs) != 6 {
		t.Fatalf("build exit %d, stderr %q, output %+v", buildCode, buildErr, build)
	}
	command = exec.Command("node", "--enable-source-maps", filepath.Join(root, "dist", "main.js"))
	runtimeOutput, err := command.CombinedOutput()
	if err != nil || string(runtimeOutput) != "hello slick\n" {
		t.Fatalf("run emitted fixture: err=%v output=%q", err, runtimeOutput)
	}

	for _, output := range build.Outputs {
		if !strings.HasSuffix(output, ".js") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(output)))
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(content))
		if strings.Contains(lower, "effect runtime") || strings.Contains(lower, `from "effect"`) || strings.Contains(lower, `from 'effect'`) {
			t.Fatalf("%s contains generated Effect code:\n%s", output, content)
		}
	}
}
