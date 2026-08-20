package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const apiSnapshotVersion = 1

type APISnapshot struct {
	Version   int              `json:"version"`
	Contracts []SymbolContract `json:"contracts"`
}

type APIChange struct {
	Symbol   string          `json:"symbol"`
	Kind     string          `json:"kind"`
	Breaking bool            `json:"breaking"`
	Detail   string          `json:"detail"`
	Old      *SymbolContract `json:"old,omitempty"`
	New      *SymbolContract `json:"new,omitempty"`
	Location *SourceLocation `json:"location,omitempty"`
}

func buildAPISnapshot(analysis Analysis, entries []string, projectRoot string) (APISnapshot, error) {
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
		entryPaths := apiEntryPaths(projectRoot, analysis.Descriptions)
		for _, description := range analysis.Descriptions {
			if description.Package == nil && description.Visibility == "exported" &&
				(len(entryPaths) == 0 || entryPaths[description.Location.Path]) {
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
func apiEntryPaths(projectRoot string, descriptions []SymbolDescription) map[string]bool {
	content, err := os.ReadFile(filepath.Join(projectRoot, "package.json"))
	if err != nil {
		return nil
	}
	var packageJSON map[string]any
	if json.Unmarshal(content, &packageJSON) != nil {
		return nil
	}
	targets := []string{}
	var collect func(any)
	collect = func(value any) {
		switch current := value.(type) {
		case string:
			targets = append(targets, current)
		case []any:
			for _, item := range current {
				collect(item)
			}
		case map[string]any:
			for _, item := range current {
				collect(item)
			}
		}
	}
	if value, ok := packageJSON["exports"]; ok {
		collect(value)
	} else if value, ok := packageJSON["module"]; ok {
		collect(value)
	} else if value, ok := packageJSON["main"]; ok {
		collect(value)
	}
	result := map[string]bool{}
	for _, target := range targets {
		relative := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(target, "./")))
		withoutExtension := strings.TrimSuffix(relative, filepath.Ext(relative))
		candidates := []string{withoutExtension}
		if strings.HasPrefix(withoutExtension, "dist/") {
			candidates = append(candidates, "src/"+strings.TrimPrefix(withoutExtension, "dist/"))
		}
		for _, description := range descriptions {
			pathWithoutExtension := strings.TrimSuffix(description.Location.Path, filepath.Ext(description.Location.Path))
			for _, candidate := range candidates {
				if pathWithoutExtension == candidate {
					result[description.Location.Path] = true
				}
			}
		}
	}
	return result
}

func diffAPI(oldSnapshot, current APISnapshot, assignable func(TypeDescription, TypeDescription) bool) []APIChange {
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
		changes = append(changes, signatureChanges(name, oldContract, newContract, assignable)...)
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
	for index := range changes {
		if oldContract, ok := oldByName[changes[index].Symbol]; ok {
			copy := oldContract
			changes[index].Old = &copy
		}
		if newContract, ok := newByName[changes[index].Symbol]; ok {
			copy := newContract
			changes[index].New = &copy
			location := newContract.Location
			changes[index].Location = &location
		} else if changes[index].Old != nil {
			location := changes[index].Old.Location
			changes[index].Location = &location
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

func signatureChanges(symbol string, oldContract, newContract SymbolContract, assignable func(TypeDescription, TypeDescription) bool) []APIChange {
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
		var matched SignatureDescription
		for _, newSignature := range newSignatures {
			if signatureCompatible(oldSignature, newSignature, assignable) {
				compatible = true
				matched = newSignature
				break
			}
		}
		if !compatible {
			encoded, _ := json.Marshal(oldSignature)
			changes = append(changes, APIChange{Symbol: symbol, Kind: "removed_or_incompatible_overload", Breaking: true, Detail: string(encoded)})
			continue
		}
		oldJSON, _ := json.Marshal(oldSignature)
		newJSON, _ := json.Marshal(matched)
		if string(oldJSON) != string(newJSON) {
			changes = append(changes, APIChange{Symbol: symbol, Kind: "compatible_signature_change", Detail: string(oldJSON) + " -> " + string(newJSON)})
		}
	}
	return changes
}

func signatureCompatible(oldSignature, newSignature SignatureDescription, assignable func(TypeDescription, TypeDescription) bool) bool {
	if len(oldSignature.TypeParameters) != len(newSignature.TypeParameters) {
		return false
	}
	for index := range oldSignature.TypeParameters {
		oldParameter, newParameter := oldSignature.TypeParameters[index], newSignature.TypeParameters[index]
		if oldParameter.Constraint == nil && newParameter.Constraint != nil {
			return false
		}
		if oldParameter.Constraint != nil && newParameter.Constraint != nil &&
			!assignable(*newParameter.Constraint, *oldParameter.Constraint) {
			return false
		}
		if oldParameter.Default == nil != (newParameter.Default == nil) {
			return false
		}
		if oldParameter.Default != nil && !assignable(*oldParameter.Default, *newParameter.Default) {
			return false
		}
	}
	oldRequired := requiredParameters(oldSignature.Parameters)
	newRequired := requiredParameters(newSignature.Parameters)
	if newRequired > oldRequired || len(newSignature.Parameters) < oldRequired {
		return false
	}
	for index := 0; index < len(oldSignature.Parameters) && index < len(newSignature.Parameters); index++ {
		if !assignable(newSignature.Parameters[index].Type, oldSignature.Parameters[index].Type) {
			return false
		}
	}
	return assignable(oldSignature.Return, newSignature.Return)
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

type tsTypeAssigner struct {
	ctx    context.Context
	config string
	cache  map[string]bool
	err    error
}

const apiAssignabilityScript = `
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
const config = path.resolve(process.argv[1]);
const input = JSON.parse(fs.readFileSync(0, "utf8"));
const require = createRequire(config);
const compilerPath = process.env.SLICK_TYPESCRIPT_PATH
  ? path.resolve(process.env.SLICK_TYPESCRIPT_PATH)
  : require.resolve("typescript", { paths: [path.dirname(config)] });
const ts = require(compilerPath);
const options = { strict: true, noEmit: true, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.NodeNext, moduleResolution: ts.ModuleResolutionKind.NodeNext, skipLibCheck: true };
const fileName = path.join(path.dirname(config), ".slick-api-assignability.ts");
const source = "type Target = " + input.target + ";\ntype Value = " + input.value + ";\ndeclare const value: Value;\nconst check: Target = value;\n";
const host = ts.createCompilerHost(options);
const originalGetSourceFile = host.getSourceFile;
host.fileExists = (name) => path.resolve(name) === path.resolve(fileName) || ts.sys.fileExists(name);
host.readFile = (name) => path.resolve(name) === path.resolve(fileName) ? source : ts.sys.readFile(name);
host.getSourceFile = (name, languageVersion, onError, shouldCreateNewSourceFile) =>
  path.resolve(name) === path.resolve(fileName)
    ? ts.createSourceFile(fileName, source, languageVersion, true)
    : originalGetSourceFile(name, languageVersion, onError, shouldCreateNewSourceFile);
const program = ts.createProgram([fileName], options, host);
const failed = ts.getPreEmitDiagnostics(program).some((diagnostic) =>
  diagnostic.category === ts.DiagnosticCategory.Error &&
  diagnostic.file && path.resolve(diagnostic.file.fileName) === path.resolve(fileName));
process.stdout.write(failed ? "false" : "true");
`

func newTSTypeAssigner(ctx context.Context, config string) *tsTypeAssigner {
	return &tsTypeAssigner{ctx: ctx, config: config, cache: map[string]bool{}}
}

func (assigner *tsTypeAssigner) assignable(target, value TypeDescription) bool {
	if assigner.err != nil {
		return false
	}
	targetSource, valueSource := renderTypeScriptType(target), renderTypeScriptType(value)
	key := targetSource + "\x00" + valueSource
	if result, ok := assigner.cache[key]; ok {
		return result
	}
	input, _ := json.Marshal(map[string]string{"target": targetSource, "value": valueSource})
	node, err := exec.LookPath("node")
	if err != nil {
		assigner.err = err
		return false
	}
	command := exec.CommandContext(assigner.ctx, node, "--input-type=module", "--eval", apiAssignabilityScript, assigner.config)
	command.Stdin = bytes.NewReader(input)
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		assigner.err = fmt.Errorf("TypeScript assignability: %w: %s", err, strings.TrimSpace(string(output)))
		return false
	}
	result := strings.TrimSpace(string(output)) == "true"
	assigner.cache[key] = result
	return result
}

func renderTypeScriptType(value TypeDescription) string {
	switch value.Kind {
	case "primitive":
		if value.Name == "" {
			return "unknown"
		}
		return value.Name
	case "literal":
		if value.Name == "string" {
			encoded, _ := json.Marshal(value.Value)
			return string(encoded)
		}
		if value.Value == "" {
			return value.Name
		}
		return value.Value
	case "union":
		parts := make([]string, len(value.Members))
		for index, member := range value.Members {
			parts[index] = renderTypeScriptType(member)
		}
		return "(" + strings.Join(parts, " | ") + ")"
	case "intersection":
		parts := make([]string, len(value.Members))
		for index, member := range value.Members {
			parts[index] = renderTypeScriptType(member)
		}
		return "(" + strings.Join(parts, " & ") + ")"
	case "array":
		if value.Element == nil {
			return "unknown[]"
		}
		return "Array<" + renderTypeScriptType(*value.Element) + ">"
	case "tuple":
		parts := make([]string, len(value.Elements))
		for index, element := range value.Elements {
			parts[index] = renderTypeScriptType(element)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case "object":
		properties := make([]string, len(value.Properties))
		for index, property := range value.Properties {
			encoded, _ := json.Marshal(property.Name)
			optional := ""
			if property.Optional {
				optional = "?"
			}
			properties[index] = string(encoded) + optional + ": " + renderTypeScriptType(property.Type)
		}
		return "{ " + strings.Join(properties, "; ") + " }"
	case "callable":
		parameters := make([]string, len(value.Parameters))
		for index, parameter := range value.Parameters {
			optional := ""
			if parameter.Optional {
				optional = "?"
			}
			parameters[index] = fmt.Sprintf("p%d%s: %s", index, optional, renderTypeScriptType(parameter.Type))
		}
		return "((" + strings.Join(parameters, ", ") + ") => " + func() string {
			if value.Return == nil {
				return "unknown"
			}
			return renderTypeScriptType(*value.Return)
		}() + ")"
	case "reference":
		if value.Name == "" || strings.ContainsAny(value.Name, "\"' ") {
			return "unknown"
		}
		if len(value.Arguments) == 0 {
			return value.Name
		}
		arguments := make([]string, len(value.Arguments))
		for index, argument := range value.Arguments {
			arguments[index] = renderTypeScriptType(argument)
		}
		return value.Name + "<" + strings.Join(arguments, ", ") + ">"
	default:
		return "unknown"
	}
}
