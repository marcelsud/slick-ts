package app

import "testing"

func TestSignatureCompatibilityIsReflexive(t *testing.T) {
	result := TypeDescription{Kind: "reference", Name: "Promise", Arguments: []TypeDescription{{
		Kind: "object",
		Name: "Service",
		Properties: []PropertyDescription{{
			Name: "run",
			Type: TypeDescription{Kind: "callable", Parameters: []ParameterDescription{{
				Name: "callback",
				Type: TypeDescription{Kind: "callable", Return: &TypeDescription{Kind: "reference", Name: "type"}},
			}}, Return: &TypeDescription{Kind: "reference", Name: "Service"}},
		}},
	}}}
	signature := SignatureDescription{Return: result}
	if !signatureCompatible(signature, signature, func(TypeDescription, TypeDescription) bool { return false }) {
		t.Fatal("an unchanged signature must be compatible with itself")
	}
}

func TestSignatureChangesPreferExactOverload(t *testing.T) {
	stringType := TypeDescription{Kind: "primitive", Name: "string"}
	broad := SignatureDescription{
		Parameters: []ParameterDescription{{Name: "value", Type: stringType}},
		Return:     stringType,
	}
	narrow := SignatureDescription{
		Parameters: []ParameterDescription{{Name: "value", Type: TypeDescription{Kind: "literal", Name: "string", Value: "a"}}},
		Return:     stringType,
	}
	contract := SymbolContract{SymbolDescription: SymbolDescription{Signatures: []SignatureDescription{broad, narrow}}}
	if changes := signatureChanges("f", contract, contract, func(TypeDescription, TypeDescription) bool { return true }); len(changes) != 0 {
		t.Fatalf("unchanged overloads produced changes: %+v", changes)
	}
}
