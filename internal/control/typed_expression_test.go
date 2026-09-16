package control

import (
	"strings"
	"testing"
)

func expressionTestModel() Model {
	return Model{
		Indexes:        map[string]IndexValues{"machine": {"M1"}, "action": {"run"}, "material": {"Si"}},
		ExternalInputs: []ExternalInputDefinition{{Name: "temperature", Type: "number"}, {Name: "rework", Type: "boolean"}},
		Parameters:     []ParameterDefinition{{Key: "base", DataType: "number", Source: "expression", Definition: "2"}},
		DataSamples:    []DataSampleDefinition{{Key: "rules", Source: "static", Columns: []DataSampleColumnDefinition{{Name: "threshold", Type: "number"}, {Name: "recipe", Type: "string"}}}},
	}
}

func TestTypedExpressionSupportsConditionsReferencesAndLookups(t *testing.T) {
	model := expressionTestModel()
	rows := map[string][]map[string]interface{}{"rules": {{"threshold": float64(2), "recipe": "A"}}}
	value, err := EvaluateTypedExpression(`IF(@rework, LOOKUP_ONE(@rules, @rules.threshold == @base, @rules.recipe), "standard")`, "string", model,
		map[string]interface{}{"rework": true}, map[string]interface{}{"base": float64(2)}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if value != "A" {
		t.Fatalf("expected A, got %#v", value)
	}
}

func TestTypedExpressionValidationAndDependencies(t *testing.T) {
	model := expressionTestModel()
	refs := map[string]string{"temperature": "number", "rework": "boolean", "base": "number"}
	if err := ValidateTypedExpression(`IF(@temperature > FIRST(@rules.threshold), @base * 3, 0)`, "number", model, refs); err != nil {
		t.Fatal(err)
	}
	dependencies, samples, err := TypedExpressionDependencies(`IF(@temperature > FIRST(@rules.threshold), @base, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependencies) != 2 || len(samples) != 1 || samples[0] != "rules" {
		t.Fatalf("unexpected dependencies: %#v %#v", dependencies, samples)
	}
	if err := ValidateTypedExpression(`IF(@rework, "yes", 0)`, "string", model, refs); err == nil {
		t.Fatal("expected mismatched result types to fail")
	}
}

func TestTypedExpressionSupportsAggregateMathAndStringFunctions(t *testing.T) {
	model := expressionTestModel()
	rows := map[string][]map[string]interface{}{"rules": {
		{"threshold": float64(1), "recipe": " A "},
		{"threshold": float64(3), "recipe": "B"},
		{"threshold": float64(8), "recipe": "C"},
	}}
	value, err := EvaluateTypedExpression(`MEDIAN(@rules.threshold) + RANGE(@rules.threshold)`, "number", model, nil, nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	if value != float64(10) {
		t.Fatalf("expected 10, got %#v", value)
	}
	value, err = EvaluateTypedExpression(`UPPER(TRIM(FIRST(@rules.recipe)))`, "string", model, nil, nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	if value != "A" {
		t.Fatalf("expected A, got %#v", value)
	}
	value, err = EvaluateTypedExpression(`SQRT(POW(3, 2) + POW(4, 2))`, "number", model, nil, nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	if value != float64(5) {
		t.Fatalf("expected 5, got %#v", value)
	}
	value, err = EvaluateTypedExpression(`ROUND(12.3456, 2)`, "number", model, nil, nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	if value != 12.35 {
		t.Fatalf("expected 12.35, got %#v", value)
	}
	value, err = EvaluateTypedExpression(`ROUND(126, -1)`, "number", model, nil, nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	if value != float64(130) {
		t.Fatalf("expected 130, got %#v", value)
	}
	if err := ValidateTypedExpression(`ROUND(12.3, 2, 1)`, "number", model, nil); err == nil {
		t.Fatal("expected ROUND with three arguments to fail validation")
	}
	if _, err := EvaluateTypedExpression(`ROUND(12.3, 1.5)`, "number", model, nil, nil, rows); err == nil || !strings.Contains(err.Error(), "whole number") {
		t.Fatalf("expected non-integer decimal places to fail, got %v", err)
	}
}

func TestValidateModelAcceptsIfComparisonAgainstSQLParameter(t *testing.T) {
	model := Model{
		ID: "test-r1", FamilyID: "test", Name: "Test", Indexes: map[string]IndexValues{"machine": {"M1"}, "action": {"run"}, "material": {"Si"}},
		ExecutionPolicy: ExecutionPolicy{MaxUnfinishedRuns: 1},
		Parameters: []ParameterDefinition{
			{Key: "latest_cd", DataType: "number", Source: "sql", DataSourceID: "source", Definition: "SELECT 1"},
			{Key: "test", DataType: "number", Source: "expression", Definition: "IF(@latest_cd > 1, 3, 0)"},
		},
		Outputs: []string{"test"},
	}
	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
}

func TestExpressionDependencyEdgesIncludeDetails(t *testing.T) {
	model := expressionTestModel()
	model.Parameters = append(model.Parameters, ParameterDefinition{Key: "result", DataType: "number", Source: "expression", Definition: "IF(@temperature > 1, @base + FIRST(@rules.threshold), 0)"})
	graph, err := BuildDependencyGraph(model)
	if err != nil {
		t.Fatal(err)
	}
	details := map[string][]string{}
	for _, edge := range graph.Edges {
		if edge.To == "parameter:result" {
			details[edge.From] = edge.Reasons
		}
	}
	for _, dependency := range []string{"external:temperature", "parameter:base", "sample:rules"} {
		if len(details[dependency]) == 0 {
			t.Fatalf("missing details for %s: %#v", dependency, details)
		}
	}
}

func TestDataSampleContainsUsesExactCaseInsensitiveMembership(t *testing.T) {
	model := expressionTestModel()
	model.ExternalInputs = append(model.ExternalInputs, ExternalInputDefinition{Name: "search", Type: "string"})
	rows := map[string][]map[string]interface{}{"rules": {
		{"threshold": float64(1), "recipe": "RECIPE_A"},
		{"threshold": float64(2), "recipe": "RECIPE_B"},
	}}
	value, err := EvaluateTypedExpression(`CONTAINS(@rules.recipe, @search)`, "boolean", model, map[string]interface{}{"search": "recipe_b"}, nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	if value != true {
		t.Fatalf("expected exact case-insensitive match, got %#v", value)
	}
	value, err = EvaluateTypedExpression(`CONTAINS(@rules.recipe, @search)`, "boolean", model, map[string]interface{}{"search": "cipe_b"}, nil, rows)
	if err != nil || value != false {
		t.Fatalf("partial text must not satisfy CONTAINS, got %#v, %v", value, err)
	}
	refs := map[string]string{"search": "string"}
	if err := ValidateTypedExpression(`CONTAINS(@rules.recipe, @search)`, "boolean", model, refs); err != nil {
		t.Fatal(err)
	}
	dependencies, samples, err := TypedExpressionDependencies(`CONTAINS(@rules.recipe, @search)`)
	if err != nil || len(dependencies) != 1 || dependencies[0] != "search" || len(samples) != 1 || samples[0] != "rules" {
		t.Fatalf("unexpected dependencies: %#v %#v %v", dependencies, samples, err)
	}
}

func TestDataSampleMatchesUsesRegularExpression(t *testing.T) {
	model := expressionTestModel()
	rows := map[string][]map[string]interface{}{"rules": {
		{"threshold": float64(1), "recipe": "RECIPE_A"},
		{"threshold": float64(2), "recipe": "RECIPE_B"},
	}}
	value, err := EvaluateTypedExpression(`MATCHES(@rules.recipe, "^RECIPE_[AB]$")`, "boolean", model, nil, nil, rows)
	if err != nil || value != true {
		t.Fatalf("expected regex match, got %#v, %v", value, err)
	}
	if _, err := EvaluateTypedExpression(`MATCHES(@rules.recipe, "[")`, "boolean", model, nil, nil, rows); err == nil || !strings.Contains(err.Error(), "pattern is invalid") {
		t.Fatalf("expected invalid regex error, got %v", err)
	}
}

func TestNullableLookupCanBeHandledWithIsNull(t *testing.T) {
	model := Model{
		ID: "nullable", FamilyID: "nullable", Name: "Nullable lookup",
		Indexes: map[string]IndexValues{"machine": {"M1"}, "action": {"run"}, "material": {"Si"}},
		Parameters: []ParameterDefinition{
			{Key: "optional_recipe", DataType: "string", Source: "expression", Definition: `LOOKUP_FIRST(@rules, @rules.operation == "MISSING", @rules.recipe)`},
			{Key: "recipe", DataType: "string", Source: "expression", Definition: `IF(IS_NULL(@optional_recipe), "DEFAULT", @optional_recipe)`},
		},
		DataSamples: []DataSampleDefinition{{
			Key: "rules", Source: "static", Limit: 10,
			Columns: []DataSampleColumnDefinition{{Name: "operation", Type: "string"}, {Name: "recipe", Type: "string"}},
			Rows:    []map[string]interface{}{{"operation": "OTHER", "recipe": "R1"}},
		}},
		Outputs: []string{"optional_recipe", "recipe"},
	}
	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
	values, _, unresolved, err := ResolveModel(model, map[string]interface{}{}, nil, nil, nil, nil, nil)
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("resolve nullable lookup: %v, unresolved=%v", err, unresolved)
	}
	if value, exists := values["optional_recipe"]; !exists || value != nil {
		t.Fatalf("nullable parameter must be resolved as null, got %#v", values)
	}
	if values["recipe"] != "DEFAULT" {
		t.Fatalf("IS_NULL fallback was not selected: %#v", values)
	}
	outputs, err := EvaluateResolvedOutputs(model, values, nil)
	if err != nil || outputs[0].Value != nil {
		t.Fatalf("nullable output was not preserved: %#v, %v", outputs, err)
	}
}

func TestDataSampleNullCells(t *testing.T) {
	model := expressionTestModel()
	rows := map[string][]map[string]interface{}{"rules": {
		{"threshold": nil, "recipe": nil},
		{"threshold": float64(2), "recipe": "RECIPE_B"},
	}}

	value, err := EvaluateTypedExpression(`IS_NULL(FIRST(@rules.recipe))`, "boolean", model, nil, nil, rows)
	if err != nil || value != true {
		t.Fatalf("expected FIRST to preserve NULL, got %#v, %v", value, err)
	}
	value, err = EvaluateTypedExpression(`CONTAINS(@rules.recipe, "RECIPE_B")`, "boolean", model, nil, nil, rows)
	if err != nil || value != true {
		t.Fatalf("expected membership to skip NULL, got %#v, %v", value, err)
	}
	value, err = EvaluateTypedExpression(`LOOKUP_FIRST(@rules, @rules.threshold > 1, @rules.recipe)`, "string", model, nil, nil, rows)
	if err != nil || value != "RECIPE_B" {
		t.Fatalf("expected lookup comparison to skip NULL, got %#v, %v", value, err)
	}

	count, err := EvaluateWithSamples(`COUNT(@rules.threshold)`, nil, rows)
	if err != nil || count != float64(1) {
		t.Fatalf("expected COUNT to ignore NULL, got %#v, %v", count, err)
	}
	average, err := EvaluateWithSamples(`AVG(@rules.threshold)`, nil, rows)
	if err != nil || average != float64(2) {
		t.Fatalf("expected AVG to ignore NULL, got %#v, %v", average, err)
	}
}

func TestDeclaredDataSampleSchemasAcceptNullCells(t *testing.T) {
	sample := DataSampleDefinition{
		Key: "nullable_sample", Source: "static", Limit: 10,
		Columns: []DataSampleColumnDefinition{
			{Name: "number_value", Type: "number"},
			{Name: "text_value", Type: "string"},
			{Name: "boolean_value", Type: "boolean"},
		},
		Rows: []map[string]interface{}{{"number_value": nil, "text_value": nil, "boolean_value": nil}},
	}
	if err := validateStaticDataSample(sample); err != nil {
		t.Fatalf("static NULL cells should validate: %v", err)
	}
	if _, err := validateResolvedDataSampleRows(sample, sample.Rows); err != nil {
		t.Fatalf("resolved SQL NULL cells should validate: %v", err)
	}
}
