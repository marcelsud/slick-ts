package app

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSummarizePropagatesFactsThroughRecursiveGraph(t *testing.T) {
	source := testProvenance("src/main.ts::source", 10)
	nodes := []operationalNode{
		{
			Symbol:    "src/main.ts::source",
			Execution: ExecutionSynchronous,
			Errors: []directError{{OperationalFact: OperationalFact{
				Name: "ConcreteError", Provenance: []Provenance{source},
			}}},
			Effects: []OperationalFact{{Name: "network", Provenance: []Provenance{source}}},
		},
		{Symbol: "src/main.ts::left", Execution: ExecutionSynchronous, Calls: []callEdge{{Target: "src/main.ts::right"}}},
		{Symbol: "src/main.ts::right", Execution: ExecutionSynchronous, Calls: []callEdge{{Target: "src/main.ts::left"}, {Target: "src/main.ts::source"}}},
	}

	summaries := summarize(nodes)
	left := summaryNamed(t, summaries, "src/main.ts::left")
	if !reflect.DeepEqual(left.Errors, []OperationalFact{{Name: "ConcreteError", Provenance: []Provenance{source}}}) {
		t.Fatalf("unexpected errors: %+v", left.Errors)
	}
	if !reflect.DeepEqual(left.Effects, []OperationalFact{{Name: "network", Provenance: []Provenance{source}}}) {
		t.Fatalf("unexpected effects: %+v", left.Effects)
	}
}

func TestSummarizeAppliesCatchPoliciesOnlyToErrors(t *testing.T) {
	source := testProvenance("src/main.ts::source", 10)
	nodes := []operationalNode{
		{
			Symbol: "src/main.ts::source",
			Errors: []directError{
				{OperationalFact: OperationalFact{Name: "HandledError", Provenance: []Provenance{source}}},
				{OperationalFact: OperationalFact{Name: "OtherError", Provenance: []Provenance{source}}},
			},
			Effects: []OperationalFact{{Name: "io", Provenance: []Provenance{source}}},
		},
		{
			Symbol: "src/main.ts::caller",
			Calls: []callEdge{{
				Target:   "src/main.ts::source",
				Policies: []errorPolicy{{Mode: "except", Names: []string{"HandledError"}}},
			}},
		},
	}

	caller := summaryNamed(t, summarize(nodes), "src/main.ts::caller")
	if len(caller.Errors) != 1 || caller.Errors[0].Name != "OtherError" {
		t.Fatalf("unexpected errors: %+v", caller.Errors)
	}
	if len(caller.Effects) != 1 || caller.Effects[0].Name != "io" {
		t.Fatalf("catch removed effect: %+v", caller.Effects)
	}
}

func TestSummarizeIsDeterministic(t *testing.T) {
	source := testProvenance("src/main.ts::source", 10)
	nodes := []operationalNode{
		{Symbol: "src/main.ts::z", Effects: []OperationalFact{{Name: "time", Provenance: []Provenance{source}}}},
		{Symbol: "src/main.ts::a", Calls: []callEdge{{Target: "src/main.ts::z"}}},
	}
	first, err := json.Marshal(summarize(nodes))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(summarize([]operationalNode{nodes[1], nodes[0]}))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("nondeterministic summaries:\n%s\n%s", first, second)
	}
}

func summaryNamed(t *testing.T, summaries []OperationalSummary, name string) OperationalSummary {
	t.Helper()
	for _, summary := range summaries {
		if summary.Symbol == name {
			return summary
		}
	}
	t.Fatalf("missing summary %q", name)
	return OperationalSummary{}
}

func testProvenance(symbol string, offset int) Provenance {
	return Provenance{
		Symbol: symbol,
		Path:   "src/main.ts",
		Range: Range{
			Start: Position{Line: 1, Column: offset + 1, Offset: offset},
			End:   Position{Line: 1, Column: offset + 2, Offset: offset + 1},
		},
	}
}
