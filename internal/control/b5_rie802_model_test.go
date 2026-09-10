package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadB5RIE802Model(t *testing.T) Model {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "models", "b5_rie802_model.json"))
	if err != nil {
		t.Fatal(err)
	}
	var model Model
	if err := json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	model.FamilyID = "b5-rie802-recipe-selection"
	model.ID = model.FamilyID + "-r1"
	if err := ValidateModel(model); err != nil {
		t.Fatalf("B5 RIE802 model is invalid: %v", err)
	}
	graph, err := BuildDependencyGraph(model)
	if err != nil || len(graph.Order) == 0 {
		t.Fatalf("B5 RIE802 dependency graph is invalid: %#v %v", graph, err)
	}
	return model
}

func resolveB5RIE802(t *testing.T, model Model, operation, product string, history []map[string]interface{}) map[string]interface{} {
	t.Helper()
	context := map[string]interface{}{
		"machine": "B5_RIE802", "action": "RECIPE_SELECTION", "material": "WAFER",
		"wafer_id": "WAFER-01", "operation": operation, "product": product,
	}
	values, _, unresolved, err := ResolveModel(model, context, nil,
		map[string][]map[string]interface{}{"wafer_history": history}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved parameters: %#v", unresolved)
	}
	return values
}

func TestB5RIE802WorkbookBranches(t *testing.T) {
	model := loadB5RIE802Model(t)
	tests := []struct {
		name, operation, product, expected string
		history                            []map[string]interface{}
	}{
		{
			name: "BPI190 MG11", operation: "BPI190", product: "700-0016-000",
			expected: "~PM2/PM3_N2O2_4mTIG_4.7%_12_250W_105S~",
			history:  []map[string]interface{}{{"operation": "BPI170", "metric_name": "CD_Average", "measurement": 0.0500}},
		},
		{
			name: "CMS190 M12 equality", operation: "CMS190", product: "700-0055-000",
			expected: "~PM2/PM3_N2O2_4mTIG_5.3%_12_250W_105S~",
			history:  []map[string]interface{}{{"operation": "CMS170", "metric_name": "CD_Average", "measurement": 0.0515}},
		},
		{
			name: "CLU200 confirmed offset", operation: "CLU200", product: "700-0027-000",
			expected: "~FLTR_2%_25_250W_100S~",
			history: []map[string]interface{}{
				{"operation": "CLU170", "metric_name": "YOvl_Average", "measurement": 1.5},
				{"operation": "CLU160", "metric_name": "CD_Average", "measurement": 0.052},
				{"operation": "BNE192", "metric_name": "CD_Average", "measurement": 0.030},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := resolveB5RIE802(t, model, test.operation, test.product, test.history)
			if values["recipe"] != test.expected {
				t.Fatalf("expected %q, got %#v", test.expected, values["recipe"])
			}
		})
	}
}

func TestB5RIE802RejectsUnsupportedOperationAndProduct(t *testing.T) {
	model := loadB5RIE802Model(t)
	context := map[string]interface{}{
		"machine": "B5_RIE802", "action": "RECIPE_SELECTION", "material": "WAFER",
		"wafer_id": "WAFER-01", "operation": "UNKNOWN", "product": "700-0016-000",
	}
	_, _, _, err := ResolveModel(model, context, nil, map[string][]map[string]interface{}{"wafer_history": {}}, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected unsupported operation rejection, got %v", err)
	}
	context["operation"] = "BPI190"
	context["product"] = "UNKNOWN"
	_, _, _, err = ResolveModel(model, context, nil,
		map[string][]map[string]interface{}{"wafer_history": {{"operation": "BPI170", "metric_name": "CD_Average", "measurement": 0.05}}}, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected unsupported product rejection, got %v", err)
	}
}

func TestB5RIE802RejectsWorkbookZeroAsMissingSentinel(t *testing.T) {
	model := loadB5RIE802Model(t)
	context := map[string]interface{}{
		"machine": "B5_RIE802", "action": "RECIPE_SELECTION", "material": "WAFER",
		"wafer_id": "WAFER-01", "operation": "BPI190", "product": "700-0016-000",
	}
	_, _, _, err := ResolveModel(model, context, nil,
		map[string][]map[string]interface{}{"wafer_history": {{"operation": "BPI170", "metric_name": "CD_Average", "measurement": 0.0}}}, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected zero measurement rejection, got %v", err)
	}
}
