package common

import (
	"strings"
	"testing"
)

func TestNormalizeCodexStrictSchemaPreservesBytesAndPropertyOrder(t *testing.T) {
	raw := `{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"integer"},"nested":{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}},"required":["z","a","nested"],"additionalProperties":false}`
	normalized, err := NormalizeCodexStrictSchema(raw)
	if err != nil {
		t.Fatalf("NormalizeCodexStrictSchema: %v", err)
	}
	if normalized != raw {
		t.Fatalf("schema bytes changed:\n got %s\nwant %s", normalized, raw)
	}
}

func TestNormalizeCodexStrictSchemaPreservesNullableObject(t *testing.T) {
	raw := `{"type":["object","null"],"properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`
	normalized, err := NormalizeCodexStrictSchema(raw)
	if err != nil {
		t.Fatalf("NormalizeCodexStrictSchema: %v", err)
	}
	if normalized != raw {
		t.Fatalf("nullable schema changed: %s", normalized)
	}
}

func TestNormalizeCodexStrictSchemaRejectsOptionalFields(t *testing.T) {
	_, err := NormalizeCodexStrictSchema(`{"type":"object","properties":{"required":{"type":"string"},"optional":{"type":"string"}},"required":["required"],"additionalProperties":false}`)
	if err == nil || !strings.Contains(err.Error(), "optional") {
		t.Fatalf("error = %v, want optional-field rejection", err)
	}
}

func TestNormalizeCodexStrictSchemaRejectsUnsupportedComposition(t *testing.T) {
	arrayKeywords := []string{"oneOf", "allOf"}
	for _, keyword := range arrayKeywords {
		raw := `{"type":"object","properties":{"value":{"` + keyword + `":[{"type":"string"}]}},"required":["value"],"additionalProperties":false}`
		if _, err := NormalizeCodexStrictSchema(raw); err == nil {
			t.Fatalf("%s schema accepted", keyword)
		}
	}
	objectKeywords := []string{"not", "dependentRequired", "dependentSchemas", "if", "then", "else", "contains", "patternProperties", "propertyNames"}
	for _, keyword := range objectKeywords {
		raw := `{"type":"object","properties":{"value":{"` + keyword + `":{"type":"string"}}},"required":["value"],"additionalProperties":false}`
		if _, err := NormalizeCodexStrictSchema(raw); err == nil {
			t.Fatalf("%s schema accepted", keyword)
		}
	}
}

func TestNormalizeCodexStrictSchemaRejectsRootCompositions(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","anyOf":[{"type":"object","properties":{},"required":[],"additionalProperties":false}],"properties":{},"required":[],"additionalProperties":false}`,
		`{"type":"object","enum":[{}],"properties":{},"required":[],"additionalProperties":false}`,
		`{"type":"object","const":{},"properties":{},"required":[],"additionalProperties":false}`,
	} {
		if _, err := NormalizeCodexStrictSchema(raw); err == nil {
			t.Fatalf("root composition accepted: %s", raw)
		}
	}
}

func TestNormalizeCodexStrictSchemaPreservesNestedAnyOf(t *testing.T) {
	raw := `{"type":"object","properties":{"value":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["value"],"additionalProperties":false}`
	if normalized, err := NormalizeCodexStrictSchema(raw); err != nil || normalized != raw {
		t.Fatalf("nested anyOf changed: normalized=%s error=%v", normalized, err)
	}
}

func TestNormalizeCodexStrictSchemaValidatesPrefixItems(t *testing.T) {
	raw := `{"type":"object","properties":{"values":{"type":"array","prefixItems":[{"type":"object","properties":{},"required":[],"additionalProperties":false,"propertyNames":{"pattern":"^x-"}}],"items":{"type":"string"}}},"required":["values"],"additionalProperties":false}`
	if _, err := NormalizeCodexStrictSchema(raw); err == nil {
		t.Fatal("unsupported keyword inside prefixItems was accepted")
	}
}

func TestNormalizeCodexStrictSchemaRejectsInvalidRequiredSets(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","properties":{"value":{"type":"string"}},"required":["value","ghost"],"additionalProperties":false}`,
		`{"type":"object","properties":{"value":{"type":"string"}},"required":["value","value"],"additionalProperties":false}`,
		`{"type":"object","properties":{"value":{"type":"string"}},"required":["value",1],"additionalProperties":false}`,
	} {
		if _, err := NormalizeCodexStrictSchema(raw); err == nil {
			t.Fatalf("schema accepted: %s", raw)
		}
	}
}

func TestNormalizeCodexStrictSchemaRejectsScalarSchemaNodes(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","properties":{"value":42},"required":["value"],"additionalProperties":false}`,
		`{"type":"object","properties":{"value":true},"required":["value"],"additionalProperties":false}`,
	} {
		if _, err := NormalizeCodexStrictSchema(raw); err == nil {
			t.Fatalf("schema accepted: %s", raw)
		}
	}
}

func TestNormalizeCodexStrictSchemaRejectsUnboundedObjects(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object"}`,
		`{"type":["object","null"]}`,
		`{"type":"object","properties":{},"required":[]}`,
	} {
		if _, err := NormalizeCodexStrictSchema(raw); err == nil {
			t.Fatalf("schema accepted: %s", raw)
		}
	}
}
