package app

import (
	"sort"
	"strconv"
)

type Execution string

const (
	ExecutionSynchronous  Execution = "synchronous"
	ExecutionAsynchronous Execution = "asynchronous"
)

type SourceLocation struct {
	Path  string `json:"path"`
	Range Range  `json:"range"`
}

type Provenance struct {
	Symbol string `json:"symbol"`
	Path   string `json:"path"`
	Range  Range  `json:"range"`
}

type OperationalFact struct {
	Name       string       `json:"name"`
	Type       string       `json:"type,omitempty"`
	Timing     Execution    `json:"timing,omitempty"`
	Provenance []Provenance `json:"provenance"`
}

type UnresolvedLeaf struct {
	Symbol     string       `json:"symbol"`
	Reason     string       `json:"reason"`
	Provenance []Provenance `json:"provenance"`
}

type OperationalSummary struct {
	Symbol     string            `json:"symbol"`
	Execution  Execution         `json:"execution"`
	Location   SourceLocation    `json:"location"`
	Errors     []OperationalFact `json:"errors"`
	Effects    []OperationalFact `json:"effects"`
	Unresolved []UnresolvedLeaf  `json:"unresolved"`
}

type errorPolicy struct {
	Mode  string   `json:"mode"`
	Types []string `json:"types,omitempty"`
}

type directError struct {
	OperationalFact
	Supertypes []string      `json:"supertypes,omitempty"`
	Policies   []errorPolicy `json:"policies,omitempty"`
}

type callEdge struct {
	Target               string        `json:"target"`
	Provenance           []Provenance  `json:"provenance"`
	Policies             []errorPolicy `json:"policies,omitempty"`
	PropagateAsyncErrors bool          `json:"propagateAsyncErrors,omitempty"`
}

type operationalNode struct {
	Symbol        string            `json:"symbol"`
	Execution     Execution         `json:"execution"`
	AsyncBoundary bool              `json:"asyncBoundary,omitempty"`
	Location      SourceLocation    `json:"location"`
	Errors        []directError     `json:"errors"`
	Effects       []OperationalFact `json:"effects"`
	Unresolved    []UnresolvedLeaf  `json:"unresolved"`
	Calls         []callEdge        `json:"calls"`
}

type factSet map[string]map[string]Provenance

type errorEntry struct {
	fact       OperationalFact
	supertypes []string
	provenance map[string]Provenance
}

type errorSet map[string]errorEntry

type unresolvedSet map[string]struct {
	leaf       UnresolvedLeaf
	provenance map[string]Provenance
}

type mutableSummary struct {
	node       operationalNode
	errors     errorSet
	effects    factSet
	unresolved unresolvedSet
}

func summarize(nodes []operationalNode) []OperationalSummary {
	sortedNodes := append([]operationalNode(nil), nodes...)
	sort.Slice(sortedNodes, func(i, j int) bool { return sortedNodes[i].Symbol < sortedNodes[j].Symbol })

	bySymbol := make(map[string]*mutableSummary, len(sortedNodes))
	for _, node := range sortedNodes {
		summary := &mutableSummary{
			node:       node,
			errors:     errorSet{},
			effects:    factSet{},
			unresolved: unresolvedSet{},
		}
		for _, fact := range node.Errors {
			if allowsError(errorType(fact.OperationalFact), fact.Supertypes, fact.Policies) {
				mergeDirectError(summary.errors, fact)
			}
		}
		for _, fact := range node.Effects {
			mergeFact(summary.effects, fact)
		}
		for _, leaf := range node.Unresolved {
			mergeUnresolved(summary.unresolved, leaf)
		}
		bySymbol[node.Symbol] = summary
	}

	for changed := true; changed; {
		changed = false
		for _, node := range sortedNodes {
			current := bySymbol[node.Symbol]
			for _, call := range node.Calls {
				callee, ok := bySymbol[call.Target]
				if !ok {
					changed = mergeUnresolved(current.unresolved, UnresolvedLeaf{
						Symbol:     call.Target,
						Reason:     "missing_summary",
						Provenance: call.Provenance,
					}) || changed
					continue
				}
				for _, entry := range callee.errors {
					if entry.fact.Timing == ExecutionAsynchronous && !call.PropagateAsyncErrors {
						continue
					}
					if allowsError(errorType(entry.fact), entry.supertypes, call.Policies) {
						incoming := entry
						if current.node.AsyncBoundary && incoming.fact.Timing == ExecutionSynchronous {
							incoming.fact.Timing = ExecutionAsynchronous
						}
						changed = mergeError(current.errors, incoming) || changed
					}
				}
				for _, fact := range facts(callee.effects) {
					changed = mergeFact(current.effects, fact) || changed
				}
				for _, leaf := range unresolved(callee.unresolved) {
					changed = mergeUnresolved(current.unresolved, leaf) || changed
				}
			}
		}
	}

	result := make([]OperationalSummary, 0, len(sortedNodes))
	for _, node := range sortedNodes {
		summary := bySymbol[node.Symbol]
		result = append(result, OperationalSummary{
			Symbol:     node.Symbol,
			Execution:  node.Execution,
			Location:   node.Location,
			Errors:     errorFacts(summary.errors),
			Effects:    facts(summary.effects),
			Unresolved: unresolved(summary.unresolved),
		})
	}
	return result
}

func allowsError(typeID string, supertypes []string, policies []errorPolicy) bool {
	allowed := true
	for _, policy := range policies {
		matches := contains(policy.Types, typeID)
		for _, supertype := range supertypes {
			matches = matches || contains(policy.Types, supertype)
		}
		switch policy.Mode {
		case "none":
			allowed = false
		case "except":
			if matches {
				allowed = false
			}
		case "only":
			if !matches {
				allowed = false
			}
		}
	}
	return allowed
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func errorType(fact OperationalFact) string {
	if fact.Type != "" {
		return fact.Type
	}
	return fact.Name
}

func errorIdentity(fact OperationalFact) string {
	return errorType(fact) + "\x00" + string(fact.Timing)
}

func mergeDirectError(set errorSet, direct directError) bool {
	return mergeError(set, errorEntry{
		fact:       direct.OperationalFact,
		supertypes: direct.Supertypes,
		provenance: provenanceMap(direct.Provenance),
	})
}

func mergeError(set errorSet, incoming errorEntry) bool {
	key := errorIdentity(incoming.fact)
	entry, ok := set[key]
	if !ok {
		entry = errorEntry{
			fact: OperationalFact{
				Name:   incoming.fact.Name,
				Type:   incoming.fact.Type,
				Timing: incoming.fact.Timing,
			},
			supertypes: append([]string(nil), incoming.supertypes...),
			provenance: map[string]Provenance{},
		}
	}
	changed := !ok
	for provenanceKey, source := range incoming.provenance {
		if _, exists := entry.provenance[provenanceKey]; !exists {
			entry.provenance[provenanceKey] = source
			changed = true
		}
	}
	set[key] = entry
	return changed
}

func provenanceMap(values []Provenance) map[string]Provenance {
	result := make(map[string]Provenance, len(values))
	for _, source := range values {
		result[provenanceKey(source)] = source
	}
	return result
}

func mergeFact(set factSet, fact OperationalFact) bool {
	provenance, ok := set[fact.Name]
	if !ok {
		provenance = map[string]Provenance{}
		set[fact.Name] = provenance
	}
	changed := !ok
	for _, source := range fact.Provenance {
		key := provenanceKey(source)
		if _, exists := provenance[key]; !exists {
			provenance[key] = source
			changed = true
		}
	}
	return changed
}

func mergeUnresolved(set unresolvedSet, leaf UnresolvedLeaf) bool {
	key := leaf.Symbol + "\x00" + leaf.Reason
	entry, ok := set[key]
	if !ok {
		entry = struct {
			leaf       UnresolvedLeaf
			provenance map[string]Provenance
		}{leaf: UnresolvedLeaf{Symbol: leaf.Symbol, Reason: leaf.Reason}, provenance: map[string]Provenance{}}
	}
	changed := !ok
	for _, source := range leaf.Provenance {
		provenanceKey := provenanceKey(source)
		if _, exists := entry.provenance[provenanceKey]; !exists {
			entry.provenance[provenanceKey] = source
			changed = true
		}
	}
	set[key] = entry
	return changed
}

func errorFacts(set errorSet) []OperationalFact {
	types := make([]string, 0, len(set))
	for typeID := range set {
		types = append(types, typeID)
	}
	sort.Strings(types)
	result := make([]OperationalFact, 0, len(types))
	for _, typeID := range types {
		entry := set[typeID]
		entry.fact.Provenance = sortedProvenance(entry.provenance)
		result = append(result, entry.fact)
	}
	return result
}

func facts(set factSet) []OperationalFact {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]OperationalFact, 0, len(names))
	for _, name := range names {
		result = append(result, OperationalFact{Name: name, Provenance: sortedProvenance(set[name])})
	}
	return result
}

func unresolved(set unresolvedSet) []UnresolvedLeaf {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]UnresolvedLeaf, 0, len(keys))
	for _, key := range keys {
		entry := set[key]
		entry.leaf.Provenance = sortedProvenance(entry.provenance)
		result = append(result, entry.leaf)
	}
	return result
}

func sortedProvenance(set map[string]Provenance) []Provenance {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Provenance, 0, len(keys))
	for _, key := range keys {
		result = append(result, set[key])
	}
	return result
}

func provenanceKey(source Provenance) string {
	return source.Path + "\x00" + source.Symbol + "\x00" + strconv.Itoa(source.Range.Start.Offset) + "\x00" + strconv.Itoa(source.Range.End.Offset)
}
