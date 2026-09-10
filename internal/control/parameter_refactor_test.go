package control

import (
	"strings"
	"testing"
)

func refactorFixture() Model {
	return Model{
		ID:       "refactor-model",
		FamilyID: "refactor-family",
		Name:     "Refactor model",
		Indexes: map[string]IndexValues{
			"machine":  {"M1"},
			"action":   {"CALCULATE"},
			"material": {"PRODUCT"},
		},
		Parameters: []ParameterDefinition{
			{Key: "temp", Source: "expression", Definition: "2", DataType: "number"},
			{Key: "temperature", Source: "expression", Definition: "3", DataType: "number"},
			{Key: "result", Source: "expression", Definition: `@temp + @temperature + LEN("@temp")`, DataType: "number"},
		},
		Outputs: []string{"temp", "result"},
	}
}

func TestRenameParameterUpdatesExactTokensAndOutputs(t *testing.T) {
	model := refactorFixture()
	updated, usages, err := RenameParameter(model, "temp", "process_temp")
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 2 {
		t.Fatalf("expected expression and output usages, got %#v", usages)
	}
	if updated.Parameters[0].Key != "process_temp" || updated.Outputs[0] != "process_temp" {
		t.Fatalf("parameter or output was not renamed: %#v %#v", updated.Parameters, updated.Outputs)
	}
	if actual := updated.Parameters[2].Definition; actual != `@process_temp + @temperature + LEN("@temp")` {
		t.Fatalf("unexpected rewritten expression %q", actual)
	}
}

func TestRenameParameterRejectsInvalidResult(t *testing.T) {
	model := refactorFixture()
	model.Outputs = []string{"missing_output"}
	_, _, err := RenameParameter(model, "temp", "process_temp")
	if err == nil || !strings.Contains(err.Error(), "renamed model is invalid") {
		t.Fatalf("expected complete model validation failure, got %v", err)
	}
}
