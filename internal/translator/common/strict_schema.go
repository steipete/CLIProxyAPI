package common

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// NormalizeCodexStrictSchema validates that a schema already satisfies the
// lossless Codex strict-schema subset. The original bytes are returned so
// property ordering remains exactly as the Anthropic caller supplied it.
// Constraint keywords (minLength, pattern, minimum, minItems, uniqueItems,
// format) intentionally pass through: the live Codex endpoint accepts them
// in tool schemas (verified 2026-07-23), so rejecting them here would fail
// valid requests.
func NormalizeCodexStrictSchema(raw string) (string, error) {
	if !gjson.Valid(raw) {
		return "", fmt.Errorf("invalid JSON schema")
	}
	root := gjson.Parse(raw)
	for _, keyword := range []string{"anyOf", "enum", "const"} {
		if root.Get(keyword).Exists() {
			return "", fmt.Errorf("$.%s is not supported at the root of Codex strict schemas", keyword)
		}
	}
	if err := validateCodexStrictSchemaValue(root, "$"); err != nil {
		return "", err
	}
	return raw, nil
}

func validateCodexStrictSchemaValue(schema gjson.Result, path string) error {
	if schema.IsArray() {
		var firstErr error
		schema.ForEach(func(index, child gjson.Result) bool {
			firstErr = validateCodexStrictSchemaValue(child, fmt.Sprintf("%s[%s]", path, index.Raw))
			return firstErr == nil
		})
		return firstErr
	}
	if !schema.IsObject() {
		return fmt.Errorf("%s must be a JSON Schema object", path)
	}
	for _, keyword := range []string{"oneOf", "allOf", "not", "dependentRequired", "dependentSchemas", "if", "then", "else", "contains", "patternProperties", "propertyNames"} {
		if schema.Get(keyword).Exists() {
			return fmt.Errorf("%s.%s is not supported by Codex strict schemas", path, keyword)
		}
	}

	if schemaDeclaresObject(schema) {
		properties := schema.Get("properties")
		if !properties.IsObject() {
			return fmt.Errorf("%s.properties is required for Codex strict object schemas", path)
		}
		if additional := schema.Get("additionalProperties"); additional.Type != gjson.False {
			return fmt.Errorf("%s.additionalProperties must be false for Codex strict object schemas", path)
		}
		propertyNames := map[string]struct{}{}
		properties.ForEach(func(name, _ gjson.Result) bool {
			propertyNames[name.String()] = struct{}{}
			return true
		})
		required := map[string]struct{}{}
		requiredResult := schema.Get("required")
		if !requiredResult.IsArray() {
			return fmt.Errorf("%s.required is required for Codex strict object schemas", path)
		}
		var requiredErr error
		requiredResult.ForEach(func(_, value gjson.Result) bool {
			if value.Type != gjson.String {
				requiredErr = fmt.Errorf("%s.required entries must be unique property names", path)
				return false
			}
			name := value.String()
			if _, duplicate := required[name]; duplicate {
				requiredErr = fmt.Errorf("%s.required contains duplicate property %q", path, name)
				return false
			}
			if _, exists := propertyNames[name]; !exists {
				requiredErr = fmt.Errorf("%s.required contains unknown property %q", path, name)
				return false
			}
			required[name] = struct{}{}
			return true
		})
		if requiredErr != nil {
			return requiredErr
		}
		var firstErr error
		properties.ForEach(func(name, child gjson.Result) bool {
			if _, ok := required[name.String()]; !ok {
				firstErr = fmt.Errorf("%s.properties.%s is optional and cannot be represented by Codex strict schemas", path, name.String())
				return false
			}
			firstErr = validateCodexStrictSchemaValue(child, path+".properties."+name.String())
			return firstErr == nil
		})
		if firstErr != nil {
			return firstErr
		}
	}

	var firstErr error
	for _, key := range []string{"items", "prefixItems", "anyOf", "$defs", "definitions"} {
		child := schema.Get(key)
		if !child.Exists() {
			continue
		}
		if key == "$defs" || key == "definitions" {
			child.ForEach(func(name, definition gjson.Result) bool {
				firstErr = validateCodexStrictSchemaValue(definition, path+"."+key+"."+name.String())
				return firstErr == nil
			})
		} else {
			firstErr = validateCodexStrictSchemaValue(child, path+"."+key)
		}
		if firstErr != nil {
			return firstErr
		}
	}
	return nil
}

func schemaDeclaresObject(schema gjson.Result) bool {
	if schema.Get("properties").Exists() {
		return true
	}
	typeResult := schema.Get("type")
	if typeResult.Type == gjson.String {
		return strings.EqualFold(typeResult.String(), "object")
	}
	if typeResult.IsArray() {
		found := false
		typeResult.ForEach(func(_, value gjson.Result) bool {
			found = found || strings.EqualFold(value.String(), "object")
			return !found
		})
		return found
	}
	return false
}
