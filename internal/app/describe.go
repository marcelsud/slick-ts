package app

import (
	"sort"
	"strings"
)

type TypeDescription struct {
	Kind       string                 `json:"kind"`
	Name       string                 `json:"name,omitempty"`
	Value      string                 `json:"value,omitempty"`
	Members    []TypeDescription      `json:"members,omitempty"`
	Element    *TypeDescription       `json:"element,omitempty"`
	Elements   []TypeDescription      `json:"elements,omitempty"`
	Arguments  []TypeDescription      `json:"arguments,omitempty"`
	Properties []PropertyDescription  `json:"properties,omitempty"`
	Parameters []ParameterDescription `json:"parameters,omitempty"`
	Return     *TypeDescription       `json:"return,omitempty"`
}

type PropertyDescription struct {
	Name     string          `json:"name"`
	Optional bool            `json:"optional"`
	Readonly bool            `json:"readonly"`
	Type     TypeDescription `json:"type"`
}

type ParameterDescription struct {
	Name     string          `json:"name"`
	Optional bool            `json:"optional"`
	Rest     bool            `json:"rest"`
	Type     TypeDescription `json:"type"`
}

type TypeParameterDescription struct {
	Name       string           `json:"name"`
	Constraint *TypeDescription `json:"constraint,omitempty"`
	Default    *TypeDescription `json:"default,omitempty"`
}
type SignatureDescription struct {
	TypeParameters []TypeParameterDescription `json:"typeParameters"`
	Parameters     []ParameterDescription     `json:"parameters"`
	Return         TypeDescription            `json:"return"`
}

type SymbolDescription struct {
	CanonicalName  string                     `json:"canonicalName"`
	Name           string                     `json:"name"`
	Kind           string                     `json:"kind"`
	Visibility     string                     `json:"visibility"`
	Documentation  string                     `json:"documentation"`
	Aliases        []string                   `json:"aliases"`
	Location       SourceLocation             `json:"location"`
	TypeParameters []TypeParameterDescription `json:"typeParameters"`
	Parameters     []ParameterDescription     `json:"parameters"`
	Return         *TypeDescription           `json:"return,omitempty"`
	Signatures     []SignatureDescription     `json:"signatures"`
	Members        []string                   `json:"members"`
	Package        *PackageIdentity           `json:"package,omitempty"`
}

type SymbolContract struct {
	SymbolDescription
	Execution    Execution         `json:"execution,omitempty"`
	Errors       []OperationalFact `json:"errors"`
	Effects      []OperationalFact `json:"effects"`
	Completeness string            `json:"completeness"`
	Unresolved   []UnresolvedLeaf  `json:"unresolved"`
}

func contractFor(description SymbolDescription, summaries []OperationalSummary) SymbolContract {
	contract := SymbolContract{
		SymbolDescription: description,
		Errors:            []OperationalFact{},
		Effects:           []OperationalFact{},
		Completeness:      "complete",
		Unresolved:        []UnresolvedLeaf{},
	}
	for _, summary := range summaries {
		if summary.Symbol != description.CanonicalName {
			continue
		}
		contract.Execution = summary.Execution
		contract.Errors = summary.Errors
		contract.Effects = summary.Effects
		contract.Unresolved = summary.Unresolved
		if len(summary.Unresolved) > 0 {
			contract.Completeness = "partial"
		}
		break
	}
	return contract
}

func resolveDescription(query string, descriptions []SymbolDescription) (SymbolDescription, []string, string) {
	matches := make([]SymbolDescription, 0, 1)
	for _, description := range descriptions {
		if description.CanonicalName == query {
			return description, nil, ""
		}
		for _, alias := range description.Aliases {
			if alias == query {
				matches = append(matches, description)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil, ""
	}
	if len(matches) > 1 {
		alternatives := make([]string, len(matches))
		for index, match := range matches {
			alternatives[index] = match.CanonicalName
		}
		sort.Strings(alternatives)
		return SymbolDescription{}, alternatives, "ambiguous_symbol"
	}

	needle := strings.ToLower(query)
	alternatives := make([]string, 0, 5)
	for _, description := range descriptions {
		if strings.Contains(strings.ToLower(description.CanonicalName), needle) ||
			strings.Contains(strings.ToLower(description.Name), needle) {
			alternatives = append(alternatives, description.CanonicalName)
		}
	}
	sort.Strings(alternatives)
	if len(alternatives) > 5 {
		alternatives = alternatives[:5]
	}
	return SymbolDescription{}, alternatives, "unknown_symbol"
}
