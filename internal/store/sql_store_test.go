package store

import (
	"encoding/json"
	"r2r/internal/control"
	"strings"
	"testing"
)

func TestEmptySQLConfigSerializesCollectionsAsArrays(t *testing.T) {
	encoded, err := json.Marshal(emptySQLConfig())
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	for _, field := range []string{"sources", "inputDefinitions", "models"} {
		if !strings.Contains(value, `"`+field+`":[]`) {
			t.Fatalf("expected %s to serialize as an empty array: %s", field, value)
		}
	}
}

func TestParameterMetadataUsesNormalizedProductionSourceTypes(t *testing.T) {
	model := control.Model{
		Indexes:        map[string]control.IndexValues{"machine": {"M1"}},
		ExternalInputs: []control.ExternalInputDefinition{{Name: "wafer_id", Type: "string"}},
		Parameters: []control.ParameterDefinition{
			{Key: "expression_value", Source: "formula", Unit: "nm"},
			{Key: "sql_value", Source: "sql"},
			{Key: "adaptive_value", Source: "expression", Adaptive: &control.AdaptiveDefinition{}},
		},
	}
	tests := []struct {
		name, kind, source string
	}{
		{"machine", "index", "input"},
		{"wafer_id", "external", "input"},
		{"expression_value", "calculated", "expression"},
		{"sql_value", "calculated", "sql"},
		{"adaptive_value", "calculated", "adaptive"},
	}
	for _, test := range tests {
		kind, source, _ := parameterMetadata(model, test.name)
		if kind != test.kind || source != test.source {
			t.Fatalf("metadata for %s = %s/%s, want %s/%s", test.name, kind, source, test.kind, test.source)
		}
	}
}
