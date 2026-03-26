package rules

import (
	"testing"
)

// this test file might feel redundant since it doesnt touch business logic
// but it can help avoid regressions on schema change.

func TestGetValidationSchema_ReturnsDraft202012Schema(t *testing.T) {
	schema := GetValidationSchema()
	if len(schema) == 0 {
		t.Fatalf("expected embedded schema to be loaded")
	}

	rawDraft, ok := schema["$schema"]
	if !ok {
		t.Fatalf("expected $schema in root")
	}

	draft, ok := rawDraft.(string)
	if !ok || draft != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("expected draft 2020-12, got %v", rawDraft)
	}
}

func TestGetValidationSchema_EnumsMatchRuleDefinitions(t *testing.T) {
	schema := GetValidationSchema()

	defs, ok := schema["$defs"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected $defs object")
	}

	condition, ok := defs["condition"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected $defs.condition object")
	}

	properties, ok := condition["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected condition.properties object")
	}

	fieldProp, ok := properties["field"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected field schema object")
	}

	operatorProp, ok := properties["operator"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected operator schema object")
	}

	gotFields := mustEnumStrings(t, fieldProp["enum"])
	gotOperators := mustEnumStrings(t, operatorProp["enum"])

	expectedFields := uniqueStrings(append(GetStringFields(), GetNumericFields()...))
	expectedOperators := uniqueStrings(append(GetStringOperators(), GetNumericOperators()...))

	assertSameStringSet(t, "fields", gotFields, expectedFields)
	assertSameStringSet(t, "operators", gotOperators, expectedOperators)
}

func TestGetValidationSchema_ConditionHasCoreProperties(t *testing.T) {
	schema := GetValidationSchema()

	defs, ok := schema["$defs"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected $defs object")
	}

	condition, ok := defs["condition"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected $defs.condition object")
	}

	properties, ok := condition["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected condition.properties object")
	}

	expectedProps := []string{"field", "operator", "value", "values", "min_value", "max_value", "case_sensitive"}
	for _, prop := range expectedProps {
		if _, found := properties[prop]; !found {
			t.Fatalf("expected condition property %q in schema", prop)
		}
	}
}

func mustEnumStrings(t *testing.T, raw interface{}) []string {
	t.Helper()

	items, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("expected enum array, got %T", raw)
	}

	result := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			t.Fatalf("expected enum string item, got %T", item)
		}
		result = append(result, value)
	}

	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func assertSameStringSet(t *testing.T, label string, got, expected []string) {
	t.Helper()

	gotSet := make(map[string]struct{}, len(got))
	for _, value := range got {
		gotSet[value] = struct{}{}
	}

	expectedSet := make(map[string]struct{}, len(expected))
	for _, value := range expected {
		expectedSet[value] = struct{}{}
	}

	if len(gotSet) != len(expectedSet) {
		t.Fatalf("%s count mismatch: expected %d got %d", label, len(expectedSet), len(gotSet))
	}

	for value := range expectedSet {
		if _, ok := gotSet[value]; !ok {
			t.Fatalf("%s missing value %q", label, value)
		}
	}

	for value := range gotSet {
		if _, ok := expectedSet[value]; !ok {
			t.Fatalf("%s has unexpected value %q", label, value)
		}
	}
}
