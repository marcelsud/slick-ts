package slick_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type riskDocument struct {
	Version int         `json:"version"`
	Command string      `json:"command"`
	Success bool        `json:"success"`
	Risk    *riskReport `json:"risk"`
	Error   *failure    `json:"error"`
}

type riskReport struct {
	Base      string             `json:"base"`
	History   string             `json:"history"`
	Threshold float64            `json:"threshold"`
	Weights   map[string]float64 `json:"weights"`
	Results   []riskResult       `json:"results"`
}

type riskResult struct {
	Symbol           string   `json:"symbol"`
	ChangedLineCount int      `json:"changedLineCount"`
	CommitCount      int      `json:"commitCount"`
	ChurnLines       int      `json:"churnLines"`
	AuthorCount      int      `json:"authorCount"`
	Complexity       int      `json:"complexity"`
	Coverage         *float64 `json:"coverage"`
	FanIn            int      `json:"fanIn"`
	Missing          []string `json:"missing"`
	Score            float64  `json:"score"`
}

func TestRiskRanksChangedFunctionsAndKeepsMissingCoverageVisible(t *testing.T) {
	root, base := riskProject(t)
	output, stderr, code := runSlick(t, root, nil, "risk", "--json", "--base", base)
	document := decodeRisk(t, output)
	if code != 0 || stderr != "" || !document.Success || document.Risk == nil || len(document.Risk.Results) != 1 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	result := document.Risk.Results[0]
	if !strings.Contains(result.Symbol, "changed") || result.ChangedLineCount == 0 || result.CommitCount == 0 || result.ChurnLines == 0 || result.AuthorCount != 1 || result.Complexity < 3 || len(result.Missing) == 0 || result.Missing[0] != "coverage" || result.Score <= 0 {
		t.Fatalf("risk result: %+v", result)
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "risk", "--base", base)
	if humanCode != 0 || humanErr != "" || !strings.Contains(human, "risk") || !strings.Contains(human, "missing coverage") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestRiskWeightsAndThresholdAreExplicitAndInvalidBaseIsStructured(t *testing.T) {
	root, base := riskProject(t)
	config := map[string]any{"threshold": 1, "weights": map[string]float64{"complexity": 1}}
	content, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(root, "risk.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	output, stderr, code := runSlick(t, root, nil, "risk", "--json", "--base", base, "--config", "risk.json")
	document := decodeRisk(t, output)
	if code != 1 || stderr != "" || document.Success || document.Risk == nil || document.Risk.Threshold != 1 || document.Risk.Weights["complexity"] != 1 || len(document.Risk.Results) != 1 {
		t.Fatalf("threshold exit %d, stderr %q, output %+v", code, stderr, document)
	}

	badOutput, badErr, badCode := runSlick(t, root, nil, "risk", "--json", "--base", "missing-ref")
	bad := decodeRisk(t, badOutput)
	if badCode != 1 || badErr != "" || bad.Error == nil || bad.Error.Kind != "git_failure" {
		t.Fatalf("bad base exit %d, stderr %q, output %+v", badCode, badErr, bad)
	}
}

func TestRiskFollowsFileRenames(t *testing.T) {
	root, base := riskProject(t)
	runGit(t, root, "mv", "src/main.ts", "src/renamed.ts")
	runGit(t, root, "commit", "-m", "rename")
	output, stderr, code := runSlick(t, root, nil, "risk", "--json", "--base", base)
	document := decodeRisk(t, output)
	if code != 0 || stderr != "" || document.Risk == nil || len(document.Risk.Results) != 2 {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
	for _, result := range document.Risk.Results {
		if result.CommitCount < 3 {
			t.Fatalf("rename history was truncated: %+v", result)
		}
	}
}

func riskProject(t *testing.T) (string, string) {
	t.Helper()
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function changed(value: number): number { return value; }
export function untouched(): number { return 1; }`,
	})
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "risk@example.test")
	runGit(t, root, "config", "user.name", "Risk Test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "base")
	base := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(root, "src", "main.ts"), `export function changed(value: number): number {
  if (value > 10) return value * 2;
  if (value > 5) return value + 1;
  return value;
}
export function untouched(): number { return 1; }`)
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "change")
	return root, base
}

func decodeRisk(t *testing.T, output string) riskDocument {
	t.Helper()
	var document riskDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "risk" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
