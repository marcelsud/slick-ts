package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const apiSnapshotVersion = 1

type APISnapshot struct {
	Version   int              `json:"version"`
	Contracts []SymbolContract `json:"contracts"`
}

type APIChange struct {
	Symbol   string `json:"symbol"`
	Kind     string `json:"kind"`
	Breaking bool   `json:"breaking"`
	Detail   string `json:"detail"`
}

func buildAPISnapshot(analysis Analysis, entries []string) (APISnapshot, error) {
	selected := []SymbolDescription{}
	seen := map[string]struct{}{}
	add := func(description SymbolDescription) {
		if _, ok := seen[description.CanonicalName]; ok {
			return
		}
		seen[description.CanonicalName] = struct{}{}
		selected = append(selected, description)
		if description.Kind == "class" || description.Kind == "namespace" {
			prefix := description.CanonicalName + "."
			for _, member := range analysis.Descriptions {
				if member.Package == nil && strings.HasPrefix(member.CanonicalName, prefix) &&
					(member.Visibility == "public" || member.Visibility == "exported") {
					if _, ok := seen[member.CanonicalName]; !ok {
						seen[member.CanonicalName] = struct{}{}
						selected = append(selected, member)
					}
				}
			}
		}
	}
	if len(entries) > 0 {
		for _, entry := range entries {
			description, alternatives, kind := resolveDescription(entry, analysis.Descriptions)
			if kind != "" {
				return APISnapshot{}, fmt.Errorf("%s %q; alternatives: %v", kind, entry, alternatives)
			}
			if description.Package != nil {
				return APISnapshot{}, fmt.Errorf("entry %q resolves to a dependency symbol", entry)
			}
			add(description)
		}
	} else {
		for _, description := range analysis.Descriptions {
			if description.Package == nil && description.Visibility == "exported" {
				add(description)
			}
		}
	}
	contracts := make([]SymbolContract, len(selected))
	for index, description := range selected {
		contracts[index] = contractFor(description, analysis.Summaries)
	}
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].CanonicalName < contracts[j].CanonicalName })
	return APISnapshot{Version: apiSnapshotVersion, Contracts: contracts}, nil
}

func diffAPI(oldSnapshot, current APISnapshot) []APIChange {
	changes := []APIChange{}
	oldByName := map[string]SymbolContract{}
	newByName := map[string]SymbolContract{}
	for _, contract := range oldSnapshot.Contracts {
		oldByName[contract.CanonicalName] = contract
	}
	for _, contract := range current.Contracts {
		newByName[contract.CanonicalName] = contract
	}
	for name, oldContract := range oldByName {
		newContract, ok := newByName[name]
		if !ok {
			changes = append(changes, APIChange{Symbol: name, Kind: "removed_export", Breaking: true, Detail: "export was removed"})
			continue
		}
		if visibilityRank(newContract.Visibility) < visibilityRank(oldContract.Visibility) {
			changes = append(changes, APIChange{Symbol: name, Kind: "reduced_visibility", Breaking: true, Detail: oldContract.Visibility + " -> " + newContract.Visibility})
		}
		oldAliases := map[string]struct{}{}
		for _, alias := range oldContract.Aliases {
			oldAliases[alias] = struct{}{}
		}
		newAliases := map[string]struct{}{}
		for _, alias := range newContract.Aliases {
			newAliases[alias] = struct{}{}
		}
		for alias := range oldAliases {
			if _, ok := newAliases[alias]; !ok {
				changes = append(changes, APIChange{Symbol: name, Kind: "removed_alias", Breaking: true, Detail: alias})
			}
		}
		changes = append(changes, signatureChanges(name, oldContract, newContract)...)
		changes = append(changes, addedFactChanges(name, "error", oldContract.Errors, newContract.Errors)...)
		changes = append(changes, addedFactChanges(name, "effect", oldContract.Effects, newContract.Effects)...)
		if oldContract.Completeness == "complete" && newContract.Completeness == "partial" {
			changes = append(changes, APIChange{Symbol: name, Kind: "became_partial", Breaking: true, Detail: "contract completeness changed from complete to partial"})
		}
	}
	for name := range newByName {
		if _, ok := oldByName[name]; !ok {
			changes = append(changes, APIChange{Symbol: name, Kind: "added_export", Detail: "export was added"})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Symbol != changes[j].Symbol {
			return changes[i].Symbol < changes[j].Symbol
		}
		if changes[i].Breaking != changes[j].Breaking {
			return changes[i].Breaking
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}

func visibilityRank(value string) int {
	switch value {
	case "exported":
		return 4
	case "public":
		return 3
	case "protected":
		return 2
	case "local":
		return 1
	default:
		return 0
	}
}

func signatureChanges(symbol string, oldContract, newContract SymbolContract) []APIChange {
	changes := []APIChange{}
	oldSignatures := oldContract.Signatures
	newSignatures := newContract.Signatures
	if len(oldSignatures) == 0 && oldContract.Return != nil {
		oldSignatures = []SignatureDescription{{TypeParameters: oldContract.TypeParameters, Parameters: oldContract.Parameters, Return: *oldContract.Return}}
	}
	if len(newSignatures) == 0 && newContract.Return != nil {
		newSignatures = []SignatureDescription{{TypeParameters: newContract.TypeParameters, Parameters: newContract.Parameters, Return: *newContract.Return}}
	}
	for _, oldSignature := range oldSignatures {
		compatible := false
		for _, newSignature := range newSignatures {
			if signatureCompatible(oldSignature, newSignature) {
				compatible = true
				break
			}
		}
		if !compatible {
			encoded, _ := json.Marshal(oldSignature)
			changes = append(changes, APIChange{Symbol: symbol, Kind: "removed_or_incompatible_overload", Breaking: true, Detail: string(encoded)})
		}
	}
	return changes
}

func signatureCompatible(oldSignature, newSignature SignatureDescription) bool {
	if len(oldSignature.TypeParameters) != len(newSignature.TypeParameters) {
		return false
	}
	for index := range oldSignature.TypeParameters {
		oldParameter, newParameter := oldSignature.TypeParameters[index], newSignature.TypeParameters[index]
		oldConstraint, _ := json.Marshal(oldParameter.Constraint)
		newConstraint, _ := json.Marshal(newParameter.Constraint)
		oldDefault, _ := json.Marshal(oldParameter.Default)
		newDefault, _ := json.Marshal(newParameter.Default)
		if string(oldConstraint) != string(newConstraint) || string(oldDefault) != string(newDefault) {
			return false
		}
	}
	oldRequired := requiredParameters(oldSignature.Parameters)
	newRequired := requiredParameters(newSignature.Parameters)
	if newRequired > oldRequired || len(newSignature.Parameters) < oldRequired {
		return false
	}
	for index := 0; index < len(oldSignature.Parameters) && index < len(newSignature.Parameters); index++ {
		if !typeAccepts(newSignature.Parameters[index].Type, oldSignature.Parameters[index].Type) {
			return false
		}
	}
	return typeAccepts(oldSignature.Return, newSignature.Return)
}

func requiredParameters(values []ParameterDescription) int {
	count := 0
	for _, value := range values {
		if !value.Optional && !value.Rest {
			count++
		}
	}
	return count
}

func typeAccepts(target, value TypeDescription) bool {
	targetJSON, _ := json.Marshal(target)
	valueJSON, _ := json.Marshal(value)
	if string(targetJSON) == string(valueJSON) {
		return true
	}
	if target.Kind == "primitive" && (target.Name == "unknown" || target.Name == "any") {
		return true
	}
	if value.Kind == "primitive" && value.Name == "never" {
		return true
	}
	if target.Kind == "primitive" && value.Kind == "literal" && target.Name == value.Name {
		return true
	}
	if value.Kind == "union" {
		for _, member := range value.Members {
			if !typeAccepts(target, member) {
				return false
			}
		}
		return true
	}
	if target.Kind == "union" {
		for _, member := range target.Members {
			if typeAccepts(member, value) {
				return true
			}
		}
	}
	if target.Kind == "array" && value.Kind == "array" && target.Element != nil && value.Element != nil {
		return typeAccepts(*target.Element, *value.Element)
	}
	if target.Kind == "tuple" && value.Kind == "tuple" && len(target.Elements) == len(value.Elements) {
		for index := range target.Elements {
			if !typeAccepts(target.Elements[index], value.Elements[index]) {
				return false
			}
		}
		return true
	}
	if target.Kind == "reference" && value.Kind == "reference" && target.Name == value.Name && len(target.Arguments) == len(value.Arguments) {
		for index := range target.Arguments {
			if !typeAccepts(target.Arguments[index], value.Arguments[index]) {
				return false
			}
		}
		return true
	}
	if target.Kind == "object" && value.Kind == "object" {
		valueProperties := map[string]PropertyDescription{}
		for _, property := range value.Properties {
			valueProperties[property.Name] = property
		}
		for _, property := range target.Properties {
			candidate, ok := valueProperties[property.Name]
			if !ok {
				if property.Optional {
					continue
				}
				return false
			}
			if !typeAccepts(property.Type, candidate.Type) {
				return false
			}
		}
		return true
	}
	if target.Kind == "callable" && value.Kind == "callable" && target.Return != nil && value.Return != nil {
		if len(target.Parameters) != len(value.Parameters) {
			return false
		}
		for index := range target.Parameters {
			if !typeAccepts(value.Parameters[index].Type, target.Parameters[index].Type) {
				return false
			}
		}
		return typeAccepts(*target.Return, *value.Return)
	}
	return false
}

func addedFactChanges(symbol, kind string, oldFacts, newFacts []OperationalFact) []APIChange {
	old := map[string]struct{}{}
	for _, fact := range oldFacts {
		old[fact.Name] = struct{}{}
	}
	changes := []APIChange{}
	for _, fact := range newFacts {
		if _, ok := old[fact.Name]; !ok {
			changes = append(changes, APIChange{Symbol: symbol, Kind: "added_" + kind, Breaking: true, Detail: fact.Name})
		}
	}
	return changes
}
