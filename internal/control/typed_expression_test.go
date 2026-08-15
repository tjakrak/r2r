package control

import "testing"

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
	value, err := EvaluateTypedExpression(`IF(@rework, LOOKUP_ONE(@rules, threshold == @base, recipe), "standard")`, "string", model,
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

func TestDataSampleContainsReturnsBoolean(t *testing.T) {
	model := expressionTestModel()
	model.ExternalInputs = append(model.ExternalInputs, ExternalInputDefinition{Name: "search", Type: "string"})
	rows := map[string][]map[string]interface{}{"rules": {
		{"threshold": float64(1), "recipe": "RECIPE_A"},
		{"threshold": float64(2), "recipe": "RECIPE_B"},
	}}
	value, err := EvaluateTypedExpression(`CONTAINS(@rules.recipe, @search)`, "boolean", model, map[string]interface{}{"search": "cipe_b"}, nil, rows)
	if err != nil {
		t.Fatal(err)
	}
	if value != true {
		t.Fatalf("expected case-insensitive match, got %#v", value)
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
