package slick_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type apiDocument struct {
	Version int          `json:"version"`
	Command string       `json:"command"`
	Success bool         `json:"success"`
	API     *apiSnapshot `json:"api"`
	Changes []apiChange  `json:"changes"`
	Error   *failure     `json:"error"`
}

type apiSnapshot struct {
	Version   int               `json:"version"`
	Contracts []json.RawMessage `json:"contracts"`
}

type apiChange struct {
	Symbol   string           `json:"symbol"`
	Kind     string           `json:"kind"`
	Breaking bool             `json:"breaking"`
	Detail   string           `json:"detail"`
	Old      *json.RawMessage `json:"old"`
	New      *json.RawMessage `json:"new"`
	Location *sourceLocation  `json:"location"`
}

func TestAPISnapshotAndDiffClassifyBreakingOperationalChanges(t *testing.T) {
	root := apiProject(t, `export function convert(value: string): string { return value; }`)
	snapshotOutput, snapshotErr, snapshotCode := runSlick(t, root, nil, "api", "snapshot", "--json")
	snapshot := decodeAPI(t, snapshotOutput)
	if snapshotCode != 0 || snapshotErr != "" || !snapshot.Success || snapshot.API == nil || snapshot.API.Version != 1 || len(snapshot.API.Contracts) != 1 {
		t.Fatalf("snapshot exit %d, stderr %q, output %+v", snapshotCode, snapshotErr, snapshot)
	}
	first, err := os.ReadFile(filepath.Join(root, "slick-api.json"))
	if err != nil {
		t.Fatal(err)
	}
	runSlick(t, root, nil, "api", "snapshot", "--json")
	second, _ := os.ReadFile(filepath.Join(root, "slick-api.json"))
	if string(first) != string(second) {
		t.Fatal("unchanged snapshots differ")
	}

	writeFile(t, filepath.Join(root, "src", "index.ts"), `export function convert(value: "a"): string {
  console.log(value);
  throw new Error("failed");
}
export function added(): number { return 1; }`)
	diffOutput, diffErr, diffCode := runSlick(t, root, nil, "api", "diff", "--json")
	diff := decodeAPI(t, diffOutput)
	if diffCode != 1 || diffErr != "" || diff.Success || len(diff.Changes) < 4 {
		t.Fatalf("diff exit %d, stderr %q, output %+v", diffCode, diffErr, diff)
	}
	kinds := map[string]bool{}
	for _, change := range diff.Changes {
		if change.Breaking {
			kinds[change.Kind] = true
		}
	}
	if !kinds["removed_or_incompatible_overload"] || !kinds["added_error"] || !kinds["added_effect"] {
		t.Fatalf("breaking changes: %+v", diff.Changes)
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "api", "diff")
	if humanCode != 1 || humanErr != "" || !strings.Contains(human, "breaking") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestAPIDiffTreatsAdditionsAsNonBreakingAndValidatesBaseline(t *testing.T) {
	root := apiProject(t, `export function existing(): number { return 1; }`)
	_, _, code := runSlick(t, root, nil, "api", "snapshot", "--output", "baseline.json")
	if code != 0 {
		t.Fatal("snapshot failed")
	}
	writeFile(t, filepath.Join(root, "src", "index.ts"), `export function existing(): number { return 1; }
export function added(): number { return 2; }`)
	output, stderr, diffCode := runSlick(t, root, nil, "api", "diff", "--json", "--baseline", "baseline.json")
	document := decodeAPI(t, output)
	if diffCode != 0 || stderr != "" || !document.Success || len(document.Changes) != 1 || document.Changes[0].Kind != "added_export" || document.Changes[0].Breaking {
		t.Fatalf("addition exit %d, stderr %q, output %+v", diffCode, stderr, document)
	}

	writeFile(t, filepath.Join(root, "bad.json"), `{"version":99,"contracts":[]}`)
	badOutput, badErr, badCode := runSlick(t, root, nil, "api", "diff", "--json", "--baseline", "bad.json")
	bad := decodeAPI(t, badOutput)
	if badCode != 1 || badErr != "" || bad.Error == nil || bad.Error.Kind != "api_baseline_failure" {
		t.Fatalf("bad baseline exit %d, stderr %q, output %+v", badCode, badErr, bad)
	}
}

func TestAPIDiffIncludesExportedClassMembers(t *testing.T) {
	root := apiProject(t, `export class Service {
  run(value: string): string { return value; }
}`)
	output, _, code := runSlick(t, root, nil, "api", "snapshot", "--json")
	document := decodeAPI(t, output)
	if code != 0 || document.API == nil || len(document.API.Contracts) != 2 {
		t.Fatalf("class snapshot exit %d, output %+v", code, document)
	}
	writeFile(t, filepath.Join(root, "src", "index.ts"), `export class Service {
  run(value: "only"): string { return value; }
}`)
	diffOutput, diffErr, diffCode := runSlick(t, root, nil, "api", "diff", "--json")
	diff := decodeAPI(t, diffOutput)
	if diffCode != 1 || diffErr != "" {
		t.Fatalf("class diff exit %d, stderr %q, output %+v", diffCode, diffErr, diff)
	}
	found := false
	for _, change := range diff.Changes {
		found = found || strings.Contains(change.Symbol, "Service.run") && change.Breaking
	}
	if !found {
		baseline, _ := os.ReadFile(filepath.Join(root, "slick-api.json"))
		t.Fatalf("missing class member break: %+v\nbaseline=%s\ndiff=%s", diff.Changes, baseline, diffOutput)
	}
}
func TestAPISnapshotScopesDefaultExportsToPackageEntry(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"rootDir":"src","outDir":"dist"},"include":["src"]}`, map[string]string{
		"package.json":    `{"exports":"./dist/index.js"}`,
		"src/index.ts":    `export function publicValue(): number { return 1; }`,
		"src/internal.ts": `export function internalValue(): number { return 2; }`,
	})
	output, stderr, code := runSlick(t, root, nil, "api", "snapshot", "--json")
	document := decodeAPI(t, output)
	if code != 0 || stderr != "" || document.API == nil || len(document.API.Contracts) != 1 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
}

func TestAPIDiffUsesStructuralAssignabilityAndReportsCompatibleChanges(t *testing.T) {
	root := apiProject(t, `interface First { value: string }
export function use(value: First): string | number { return value.value; }
export function generic<T extends "a">(value: T): T { return value; }`)
	_, _, code := runSlick(t, root, nil, "api", "snapshot")
	if code != 0 {
		t.Fatal("snapshot failed")
	}
	writeFile(t, filepath.Join(root, "src", "index.ts"), `interface Second { value: string }
export function use(value: Second): string { return value.value; }
export function generic<T extends string>(value: T): T { return value; }`)
	output, stderr, diffCode := runSlick(t, root, nil, "api", "diff", "--json")
	document := decodeAPI(t, output)
	if diffCode != 0 || stderr != "" || !document.Success || len(document.Changes) != 2 {
		t.Fatalf("exit %d, stderr %q, output %+v", diffCode, stderr, document)
	}
	for _, change := range document.Changes {
		if change.Kind != "compatible_signature_change" || change.Breaking || change.Old == nil || change.New == nil || change.Location == nil {
			t.Fatalf("invalid compatible change: %+v", change)
		}
	}
}

func apiProject(t *testing.T, source string) string {
	t.Helper()

	return project(t, `{"compilerOptions":{"strict":true,"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","lib":["ES2022","DOM"]},"include":["src"]}`, map[string]string{
		"package.json": `{"type":"module","exports":"./dist/index.js"}`,
		"src/index.ts": source,
	})
}

func decodeAPI(t *testing.T, output string) apiDocument {
	t.Helper()
	var document apiDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || !strings.HasPrefix(document.Command, "api ") {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
