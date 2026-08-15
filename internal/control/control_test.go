package control

import (
	"math"
	"strings"
	"testing"
)

func ptr(v float64) *float64 { return &v }
func fixture() Model {
	return Model{ID: "specific", FamilyID: "oxide", Name: "Oxide", Revision: 2, Status: "approved", Enabled: true, Indexes: map[string]IndexValues{"machine": {"ETCH-02"}, "action": {"OXIDE_ETCH"}, "material": {"OXIDE"}, "chamber": {"B"}}, Parameters: []ParameterDefinition{{Key: "wafer", Description: "Wafer ID", Source: "external", Definition: "waferId"}, {Key: "base_time", Description: "Base time", Unit: "s", Source: "constant", Definition: "54.5"}, {Key: "target", Description: "Target", Unit: "nm", Source: "constant", Definition: "100"}, {Key: "measured", Description: "Measured", Unit: "nm", Source: "sql", Definition: "SELECT value FROM metrology"}, {Key: "correction", Description: "Correction", Unit: "s", Source: "formula", Definition: "0.28 * (target - measured)"}, {Key: "history_max", Unit: "nm", Source: "data-sample", Definition: "MAX(history.thickness_nm)"}, {Key: "recipe_time", Unit: "s", Source: "formula", Definition: "base_time + correction + history_max", LSL: ptr(44), USL: ptr(80)}, {Key: "polish_time", Unit: "s", Source: "formula", Definition: "max(10, recipe_time * 0.4)"}}, DataSamples: []DataSampleDefinition{{Key: "history", Source: "sql", DataSourceID: "metrology", Query: "SELECT thickness_nm FROM history", Limit: 10}}, Outputs: []string{"recipe_time", "polish_time"}}
}

func TestSelectsActiveRevision(t *testing.T) {
	model := fixture()
	draft := model
	draft.ID = "draft"
	draft.Revision = 3
	draft.Status = "draft"
	selected, _, err := Select([]Model{model, draft}, map[string]interface{}{"machine": "etch-02", "action": "OXIDE_ETCH", "material": "OXIDE", "chamber": "B"})
	if err != nil || selected.ID != "specific" {
		t.Fatalf("got %s %v", selected.ID, err)
	}
}
func TestSelectMatchesAnyAcceptedIndexValueAndPrefersNarrowerModel(t *testing.T) {
	broad := fixture()
	broad.ID = "broad"
	broad.Name = "All chambers"
	broad.Indexes["chamber"] = IndexValues{"1", "2", "3", "4"}
	narrow := fixture()
	narrow.ID = "narrow"
	narrow.Name = "Chamber four"
	narrow.Indexes["chamber"] = IndexValues{"4"}
	context := map[string]interface{}{"machine": "ETCH-02", "action": "OXIDE_ETCH", "material": "OXIDE", "chamber": "4"}
	selected, _, err := Select([]Model{broad, narrow}, context)
	if err != nil || selected.ID != "narrow" {
		t.Fatalf("expected narrower chamber model, got %s %v", selected.ID, err)
	}
	context["chamber"] = "2"
	selected, _, err = Select([]Model{broad, narrow}, context)
	if err != nil || selected.ID != "broad" {
		t.Fatalf("expected broad chamber model, got %s %v", selected.ID, err)
	}
}
func TestSelectRejectsEquallySpecificOverlappingModels(t *testing.T) {
	first := fixture()
	first.Name = "First"
	first.Indexes["chamber"] = IndexValues{"1", "2"}
	second := fixture()
	second.Name = "Second"
	second.Indexes["chamber"] = IndexValues{"2", "3"}
	_, _, err := Select([]Model{first, second}, map[string]interface{}{"machine": "ETCH-02", "action": "OXIDE_ETCH", "material": "OXIDE", "chamber": "2"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous routing") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}
func TestResolvesExternalConstantSQLAndFormula(t *testing.T) {
	samples := map[string][]map[string]interface{}{"history": {{"thickness_nm": 2.0}, {"thickness_nm": 3.0}}}
	values, sql, err := ResolveParameters(fixture(), map[string]interface{}{"waferId": "W-42"}, map[string]interface{}{"measured": 96.2}, samples, nil)
	if err != nil || len(sql) != 0 {
		t.Fatal(err)
	}
	if values["wafer"] != "W-42" || math.Abs(values["correction"].(float64)-1.064) > 1e-9 {
		t.Fatalf("unexpected values: %+v", values)
	}
	outputs, err := EvaluateOutputs(fixture(), values)
	if err != nil || math.Abs(outputs[0].Value.(float64)-58.564) > 1e-9 || math.Abs(outputs[1].Value.(float64)-23.4256) > 1e-9 {
		t.Fatalf("unexpected outputs: %+v %v", outputs, err)
	}
}

func TestOutputLimitActions(t *testing.T) {
	model := fixture()
	model.Outputs = []string{"recipe_time"}
	setAction := func(action string, fallback *float64) {
		for index := range model.Parameters {
			if model.Parameters[index].Key == "recipe_time" {
				model.Parameters[index].LimitAction = action
				model.Parameters[index].FallbackValue = fallback
			}
		}
	}
	setAction("clamp", nil)
	outputs, err := EvaluateOutputs(model, map[string]interface{}{"recipe_time": 90.0})
	if err != nil || outputs[0].Value != 80.0 || outputs[0].CalculatedValue != 90.0 {
		t.Fatalf("clamp action failed: %+v %v", outputs, err)
	}
	setAction("fallback", ptr(55))
	outputs, err = EvaluateOutputs(model, map[string]interface{}{"recipe_time": 90.0})
	if err != nil || outputs[0].Value != 55.0 || outputs[0].CalculatedValue != 90.0 {
		t.Fatalf("fallback action failed: %+v %v", outputs, err)
	}
	setAction("reject", nil)
	outputs, err = EvaluateOutputs(model, map[string]interface{}{"recipe_time": 90.0})
	if err != nil || !outputs[0].BoundLimited || outputs[0].LimitAction != "reject" {
		t.Fatalf("reject action was not reported: %+v %v", outputs, err)
	}
}

func TestParameterLimitsApplyBeforeDependentParameters(t *testing.T) {
	model := Model{
		FamilyID: "parameter-limits",
		Indexes: map[string]IndexValues{
			"machine": {"M1"}, "action": {"A1"}, "material": {"P1"},
		},
		Parameters: []ParameterDefinition{
			{Key: "internal", Source: "expression", Definition: "90", DataType: "number", USL: ptr(80), LimitAction: "clamp"},
			{Key: "result", Source: "expression", Definition: "@internal * 2", DataType: "number"},
		},
		Outputs: []string{"result"},
	}
	limits := map[string]OutputValue{}
	values, _, unresolved, err := ResolveModelWithLimitCollector(model, map[string]interface{}{}, nil, nil, nil, nil, nil, func(result OutputValue) {
		limits[result.Parameter] = result
	})
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("resolve clamped parameter: %v %+v", err, unresolved)
	}
	if values["internal"] != 80.0 || values["result"] != 160.0 {
		t.Fatalf("dependent parameter did not use clamped value: %+v", values)
	}
	if limits["internal"].CalculatedValue != 90.0 || limits["internal"].Value != 80.0 {
		t.Fatalf("clamp metadata was not preserved: %+v", limits["internal"])
	}

	model.Parameters[0].LimitAction = "reject"
	if _, _, _, err := ResolveModel(model, map[string]interface{}{}, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("internal parameter outside USL should reject even when it is not an output")
	}
}

func TestExternalInputsAreTypedAndAvailableToParameters(t *testing.T) {
	model := fixture()
	model.ExternalInputs = []ExternalInputDefinition{
		{Name: "operation", Type: "string"},
		{Name: "target", Type: "number"},
	}
	model.Parameters = []ParameterDefinition{
		{Key: "adjusted_target", Source: "formula", Definition: "target + 2"},
		{Key: "route_value", Source: "logic", Logic: &LogicDefinition{
			Conditions: []LogicCondition{{
				InputParameter: "operation",
				Operator:       "equals",
				CompareValue:   "POLISH",
				ResultValue:    "adjusted_target",
				And: []LogicPredicate{{
					InputSource:    "index",
					InputParameter: "chamber",
					Operator:       "equals",
					CompareValue:   "B",
				}},
			}},
			ElseValue: "0",
		}},
	}
	model.DataSamples = nil
	model.Outputs = []string{"route_value"}

	if err := ValidateModel(model); err != nil {
		t.Fatalf("expected valid typed external inputs: %v", err)
	}
	values, _, err := ResolveParameters(model, map[string]interface{}{
		"operation": "POLISH",
		"target":    10.0,
		"chamber":   "B",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("resolve typed external inputs: %v", err)
	}
	if values["route_value"] != 12.0 {
		t.Fatalf("expected route_value 12, got %#v", values["route_value"])
	}

	_, _, err = ResolveParameters(model, map[string]interface{}{
		"operation": "POLISH",
		"target":    "not-a-number",
		"chamber":   "B",
	}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "target must be a number") {
		t.Fatalf("expected numeric type error, got %v", err)
	}
}

func TestLogicConditionsSupportIndexesAndDataSamples(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "by_index", Source: "logic", Logic: &LogicDefinition{
			Conditions: []LogicCondition{{
				InputSource:    "index",
				InputParameter: "chamber",
				Operator:       "equals",
				CompareValue:   "B",
				ResultValue:    "1",
			}},
			ElseValue: "0",
		}},
		{Key: "by_sample", Source: "logic", Logic: &LogicDefinition{
			Conditions: []LogicCondition{{
				InputSource:    "data-sample",
				InputParameter: "MAX(history.thickness_nm)",
				Operator:       "greater-than",
				CompareValue:   "100",
				ResultValue:    "2",
			}},
			ElseValue: "0",
		}},
	}
	model.Outputs = []string{"by_index", "by_sample"}
	if err := ValidateModel(model); err != nil {
		t.Fatalf("expected valid index and sample conditions: %v", err)
	}
	values, _, err := ResolveParameters(
		model,
		map[string]interface{}{"machine": "ETCH-02", "action": "OXIDE_ETCH", "material": "OXIDE", "chamber": "B"},
		nil,
		map[string][]map[string]interface{}{"history": {{"thickness_nm": 101.0}}},
		nil,
	)
	if err != nil {
		t.Fatalf("resolve index and sample conditions: %v", err)
	}
	if values["by_index"] != 1.0 || values["by_sample"] != 2.0 {
		t.Fatalf("unexpected logic values: %#v", values)
	}
}

func TestTextParametersAndNumericFormulaValidation(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "operation_name", DataType: "string", Source: "constant", Definition: "OXIDE_ETCH"},
		{Key: "etch_recipe", DataType: "string", Source: "constant", Definition: "ETCH_RECIPE"},
		{Key: "route", DataType: "string", Source: "logic", Logic: &LogicDefinition{
			Conditions: []LogicCondition{{
				InputParameter: "operation_name",
				Operator:       "equals",
				CompareValue:   "OXIDE_ETCH",
				ResultValue:    "etch_recipe",
			}},
			ElseValue: "DEFAULT_RECIPE",
		}},
	}
	model.DataSamples = nil
	model.Outputs = []string{"route"}
	if err := ValidateModel(model); err != nil {
		t.Fatalf("validate text parameters: %v", err)
	}
	values, _, err := ResolveParameters(model, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("resolve text parameters: %v", err)
	}
	if values["route"] != "ETCH_RECIPE" {
		t.Fatalf("unexpected text logic result: %#v", values["route"])
	}

	model.Parameters[2].Logic.Conditions[0].Operator = "greater-than"
	if err := ValidateModel(model); err == nil || !strings.Contains(err.Error(), "with text") {
		t.Fatalf("expected text comparison operator error, got %v", err)
	}
	model.Parameters[2].Logic.Conditions[0].Operator = "equals"
	model.Parameters = append(model.Parameters, ParameterDefinition{
		Key:        "invalid_formula",
		DataType:   "number",
		Source:     "formula",
		Definition: "operation_name + 1",
	})
	if err := ValidateModel(model); err == nil || !strings.Contains(err.Error(), "non-numeric") {
		t.Fatalf("expected non-numeric formula reference error, got %v", err)
	}
}
func TestUnresolvedSQLIsReturnedForConnector(t *testing.T) {
	_, sql, err := ResolveParameters(fixture(), map[string]interface{}{"waferId": "W-42"}, nil, nil, nil)
	if err != nil || len(sql) != 1 || sql[0].Key != "measured" {
		t.Fatalf("%v %v", sql, err)
	}
}
func TestSQLResolverReceivesPreviouslyDeclaredParameters(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "target", Source: "constant", Definition: "100"},
		{Key: "wafer", Source: "external", Definition: "waferId"},
		{Key: "measured", Source: "sql", DataSourceID: "metrology", Definition: "SELECT value WHERE wafer = @wafer AND target = @target"},
	}
	model.Outputs = []string{"measured"}
	values, unresolved, err := ResolveParametersWithSQL(model, map[string]interface{}{"waferId": "W-42"}, nil, nil, nil, func(parameter ParameterDefinition, resolved map[string]interface{}) (interface{}, error) {
		if parameter.Key != "measured" || resolved["target"] != 100.0 || resolved["wafer"] != "W-42" {
			t.Fatalf("resolver received unexpected parameters: %+v", resolved)
		}
		return 97.5, nil
	})
	if err != nil || len(unresolved) != 0 || values["measured"] != 97.5 {
		t.Fatalf("unexpected SQL resolution: %+v %+v %v", values, unresolved, err)
	}
}
func TestFormulaCanReferenceLaterParameterThroughDependencyOrder(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{{Key: "first", Source: "formula", Definition: "second + 1"}, {Key: "second", Source: "constant", Definition: "2"}}
	model.DataSamples = nil
	model.Outputs = []string{"first"}
	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
	values, _, err := ResolveParameters(model, nil, nil, nil, nil)
	if err != nil || values["first"] != 3.0 {
		t.Fatalf("unexpected dependency resolution: %+v %v", values, err)
	}
}
func TestDataSampleCanUseParameterAndParameterCanUseSample(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "wafer_id", Source: "external", Definition: "waferId"},
		{Key: "maximum", Source: "data-sample", Definition: "MAX(history.thickness_nm)"},
	}
	model.DataSamples = []DataSampleDefinition{{Key: "history", Source: "sql", DataSourceID: "metrology", Query: "SELECT thickness_nm WHERE wafer_id = @wafer_id", Limit: 10}}
	model.Outputs = []string{"maximum"}
	values, samples, _, err := ResolveModel(model, map[string]interface{}{"waferId": "W-42"}, nil, nil, nil, nil, func(sample DataSampleDefinition, resolved map[string]interface{}) ([]map[string]interface{}, error) {
		if resolved["wafer_id"] != "W-42" {
			t.Fatalf("sample resolver did not receive external parameter: %+v", resolved)
		}
		return []map[string]interface{}{{"thickness_nm": 98.0}, {"thickness_nm": 101.5}}, nil
	})
	if err != nil || values["maximum"] != 101.5 || len(samples["history"]) != 2 {
		t.Fatalf("unexpected graph resolution: %+v %+v %v", values, samples, err)
	}
}
func TestDependencyGraphRejectsParameterDataSampleCycle(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{{Key: "threshold", Source: "data-sample", Definition: "MAX(history.thickness_nm)"}}
	model.DataSamples = []DataSampleDefinition{{Key: "history", Source: "sql", DataSourceID: "metrology", Query: "SELECT thickness_nm WHERE thickness_nm > @threshold", Limit: 10}}
	model.Outputs = []string{"threshold"}
	_, err := BuildDependencyGraph(model)
	if err == nil || !strings.Contains(err.Error(), "circular dependency") || !strings.Contains(err.Error(), `parameter "threshold"`) || !strings.Contains(err.Error(), `data sample "history"`) {
		t.Fatalf("expected readable cycle error, got %v", err)
	}
}
func TestEmptyDependencyGraphUsesEmptyCollections(t *testing.T) {
	graph, err := BuildDependencyGraph(Model{})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Nodes == nil || graph.Edges == nil || graph.Order == nil {
		t.Fatalf("empty graph collections must not be nil: %+v", graph)
	}
}
func TestCorrectionDependencyEdgesAreAccurate(t *testing.T) {
	graph, err := BuildDependencyGraph(fixture())
	if err != nil {
		t.Fatal(err)
	}
	incoming := map[string]bool{}
	for _, edge := range graph.Edges {
		if edge.To == "parameter:correction" {
			incoming[edge.From] = true
		}
	}
	if !incoming["parameter:target"] || !incoming["parameter:measured"] || len(incoming) != 2 {
		t.Fatalf("correction has incorrect dependencies: %+v", incoming)
	}
	if incoming["parameter:operations"] {
		t.Fatal("correction must not depend on operations")
	}
}

func TestLogicResultReferencesCreateDependencyEdgesRegardlessOfDeclarationOrder(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "route", DataType: "string", Source: "logic", Logic: &LogicDefinition{
			Conditions: []LogicCondition{{
				InputSource:    "index",
				InputParameter: "action",
				Operator:       "equals",
				CompareValue:   "OXIDE_ETCH",
				ResultValue:    "@etch_recipe",
			}},
			ElseValue: "@default_recipe",
		}},
		{Key: "etch_recipe", DataType: "string", Source: "constant", Definition: "ETCH_RECIPE"},
		{Key: "default_recipe", DataType: "string", Source: "constant", Definition: "DEFAULT_RECIPE"},
	}
	model.DataSamples = nil
	model.Outputs = []string{"route"}
	graph, err := BuildDependencyGraph(model)
	if err != nil {
		t.Fatal(err)
	}
	incoming := map[string]bool{}
	for _, edge := range graph.Edges {
		if edge.To == "parameter:route" {
			incoming[edge.From] = true
		}
	}
	if !incoming["parameter:etch_recipe"] || !incoming["parameter:default_recipe"] {
		t.Fatalf("logic result dependencies are missing from graph: %+v", incoming)
	}
	values, _, err := ResolveParameters(model, map[string]interface{}{"action": "OXIDE_ETCH"}, nil, nil, nil)
	if err != nil || values["route"] != "ETCH_RECIPE" {
		t.Fatalf("logic result did not resolve through dependency order: %+v %v", values, err)
	}
}

func TestLogicComparisonReferencesCreateDependencyEdgesAndResolve(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "is_match", DataType: "boolean", Source: "logic", Logic: &LogicDefinition{
			Conditions: []LogicCondition{{
				InputSource: "parameter", InputParameter: "actual", Operator: "equals",
				CompareSource: "parameter", CompareValue: "@expected", ResultValue: "true",
			}}, ElseValue: "false",
		}},
		{Key: "actual", DataType: "string", Source: "constant", Definition: "ETCH"},
		{Key: "expected", DataType: "string", Source: "constant", Definition: "ETCH"},
	}
	model.DataSamples = nil
	model.Outputs = []string{"is_match"}
	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
	graph, err := BuildDependencyGraph(model)
	if err != nil {
		t.Fatal(err)
	}
	incoming := map[string]bool{}
	reasons := map[string][]string{}
	for _, edge := range graph.Edges {
		if edge.To == "parameter:is_match" {
			incoming[edge.From] = true
			reasons[edge.From] = edge.Reasons
		}
	}
	if !incoming["parameter:actual"] || !incoming["parameter:expected"] {
		t.Fatalf("logic operand dependencies are missing from graph: %+v", incoming)
	}
	if len(reasons["parameter:actual"]) == 0 || len(reasons["parameter:expected"]) == 0 {
		t.Fatalf("logic dependency reasons are missing: %+v", reasons)
	}
	values, _, err := ResolveParameters(model, nil, nil, nil, nil)
	if err != nil || values["is_match"] != true {
		t.Fatalf("logic reference comparison failed: %+v %v", values, err)
	}
}

func TestLogicResultCanReferenceModelIndex(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{{
		Key: "selected_action", DataType: "string", Source: "logic",
		Logic: &LogicDefinition{Conditions: []LogicCondition{{
			InputSource: "index", InputParameter: "chamber", Operator: "equals", CompareValue: "B", ResultValue: "@action",
		}}, ElseValue: "NONE"},
	}}
	model.DataSamples = nil
	model.Outputs = []string{"selected_action"}
	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
	values, _, err := ResolveParameters(model, map[string]interface{}{"action": "OXIDE_ETCH", "chamber": "B"}, nil, nil, nil)
	if err != nil || values["selected_action"] != "OXIDE_ETCH" {
		t.Fatalf("logic index result failed: %+v %v", values, err)
	}
}

func TestValidateModelRejectsNamesAcrossCategoriesCaseInsensitively(t *testing.T) {
	model := fixture()
	for index := range model.Parameters {
		if model.Parameters[index].Source == "sql" {
			model.Parameters[index].DataSourceID = "metrology"
		}
	}
	model.DataSamples = append(model.DataSamples, DataSampleDefinition{
		Key: "MACHINE", Source: "sql", DataSourceID: "metrology",
		Query: "SELECT value FROM history", Limit: 10,
	})
	err := ValidateModel(model)
	if err == nil || !strings.Contains(err.Error(), "conflicts with an existing index") {
		t.Fatalf("expected cross-category name conflict, got %v", err)
	}
}

func TestDataSampleAggregates(t *testing.T) {
	samples := map[string][]map[string]interface{}{"sample1": {{"thickness_nm": 2.0}, {"thickness_nm": 7.0}, {"thickness_nm": 4.0}}}
	value, err := EvaluateWithSamples("MAX(sample1.thickness_nm) + AVG(sample1.thickness_nm) + NTH(sample1.thickness_nm, 2)", nil, samples)
	if err != nil || math.Abs(value-(7+13.0/3+7)) > 1e-9 {
		t.Fatalf("got %g %v", value, err)
	}
	if _, err := EvaluateWithSamples("NTH(sample1.thickness_nm, 2)", nil, map[string][]map[string]interface{}{"sample1": {{"thickness_nm": 2.0}}}); err == nil || !strings.Contains(err.Error(), "row 2 is unavailable") {
		t.Fatalf("expected unavailable-row error, got %v", err)
	}
}

func TestStaticDataSampleResolvesWithoutDatabase(t *testing.T) {
	model := fixture()
	model.DataSamples = []DataSampleDefinition{{
		Key:    "limits",
		Source: "static",
		Columns: []DataSampleColumnDefinition{
			{Name: "target", Type: "number"},
			{Name: "enabled", Type: "boolean"},
			{Name: "operation", Type: "string"},
		},
		Rows: []map[string]interface{}{
			{"target": 95.0, "enabled": true, "operation": "ETCH"},
			{"target": 105.0, "enabled": false, "operation": "POLISH"},
		},
		Limit: 10,
	}}
	model.Parameters = []ParameterDefinition{
		{Key: "maximum", Source: "data-sample", Definition: "MAX(limits.target)"},
		{Key: "second_operation", DataType: "string", Source: "data-sample", Definition: "NTH(limits.operation, 2)"},
		{Key: "first_enabled", DataType: "boolean", Source: "data-sample", Definition: "FIRST(limits.enabled)"},
	}
	model.Outputs = []string{"maximum", "second_operation", "first_enabled"}
	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
	values, samples, _, err := ResolveModel(model, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if values["maximum"] != 105.0 || values["second_operation"] != "POLISH" || values["first_enabled"] != true {
		t.Fatalf("unexpected static sample values: %+v", values)
	}
	if len(samples["limits"]) != 2 {
		t.Fatalf("static rows were not resolved: %+v", samples)
	}
}

func TestStaticDataSampleValidatesColumnsAndTypes(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{{Key: "result", Source: "constant", Definition: "1"}}
	model.Outputs = []string{"result"}
	model.DataSamples = []DataSampleDefinition{{
		Key:     "limits",
		Source:  "static",
		Columns: []DataSampleColumnDefinition{{Name: "target", Type: "number"}},
		Rows:    []map[string]interface{}{{"target": "not-a-number"}},
		Limit:   10,
	}}
	if err := ValidateModel(model); err == nil || !strings.Contains(err.Error(), "must be a number") {
		t.Fatalf("expected static cell type error, got %v", err)
	}
}

func TestDeclaredSQLSampleSchemaValidatesAndNormalizesRows(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{{Key: "maximum", Source: "data-sample", Definition: "MAX(history.target)"}}
	model.DataSamples = []DataSampleDefinition{{
		Key: "history", Source: "sql", DataSourceID: "metrology", Query: "SELECT target, enabled FROM history", Limit: 10,
		Columns: []DataSampleColumnDefinition{{Name: "target", Type: "number"}, {Name: "enabled", Type: "boolean"}},
	}}
	model.Outputs = []string{"maximum"}
	values, samples, _, err := ResolveModel(model, nil, nil, map[string][]map[string]interface{}{
		"history": {{"TARGET": 7, "enabled": "true"}},
	}, nil, nil, nil)
	if err != nil || values["maximum"] != 7.0 || samples["history"][0]["enabled"] != true {
		t.Fatalf("declared SQL schema was not applied: %+v %+v %v", values, samples, err)
	}
	_, _, _, err = ResolveModel(model, nil, nil, map[string][]map[string]interface{}{
		"history": {{"target": "bad", "enabled": true}},
	}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "must be a number") {
		t.Fatalf("expected SQL sample type error, got %v", err)
	}
}

func TestTypedLookupUsesDeclaredSchemaAndDependencyOrder(t *testing.T) {
	model := fixture()
	model.DataSamples = []DataSampleDefinition{{
		Key: "recipes", Source: "static", Limit: 10,
		Columns: []DataSampleColumnDefinition{{Name: "material", Type: "string"}, {Name: "recipe", Type: "string"}},
		Rows:    []map[string]interface{}{{"material": "OXIDE", "recipe": "RCP-01"}, {"material": "NITRIDE", "recipe": "RCP-02"}},
	}}
	model.Parameters = []ParameterDefinition{
		{Key: "selected_recipe", DataType: "string", Source: "lookup", Lookup: &LookupDefinition{
			Sample: "recipes", MatchColumn: "material", Operator: "contains", InputSource: "parameter", MatchValue: "requested_material", ReturnColumn: "recipe",
		}},
		{Key: "requested_material", DataType: "string", Source: "constant", Definition: "oxide"},
	}
	model.Outputs = []string{"selected_recipe"}
	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
	graph, err := BuildDependencyGraph(model)
	if err != nil {
		t.Fatal(err)
	}
	incoming := map[string]bool{}
	for _, edge := range graph.Edges {
		if edge.To == "parameter:selected_recipe" {
			incoming[edge.From] = true
		}
	}
	if !incoming["sample:recipes"] || !incoming["parameter:requested_material"] {
		t.Fatalf("lookup dependencies missing: %+v", incoming)
	}
	values, _, err := ResolveParameters(model, nil, nil, nil, nil)
	if err != nil || values["selected_recipe"] != "RCP-01" {
		t.Fatalf("lookup failed: %+v %v", values, err)
	}
}

func TestTypedLookupRejectsAmbiguousMatches(t *testing.T) {
	model := fixture()
	model.DataSamples = []DataSampleDefinition{{
		Key: "recipes", Source: "static", Limit: 10,
		Columns: []DataSampleColumnDefinition{{Name: "material", Type: "string"}, {Name: "target", Type: "number"}},
		Rows:    []map[string]interface{}{{"material": "OXIDE-A", "target": 1.0}, {"material": "OXIDE-B", "target": 2.0}},
	}}
	model.Parameters = []ParameterDefinition{{Key: "target", Source: "lookup", Lookup: &LookupDefinition{
		Sample: "recipes", MatchColumn: "material", Operator: "contains", InputSource: "constant", MatchValue: "OXIDE", ReturnColumn: "target",
	}}}
	model.Outputs = []string{"target"}
	_, _, err := ResolveParameters(model, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "2 matching rows") {
		t.Fatalf("expected ambiguous lookup error, got %v", err)
	}
}

func TestTextParameterCanReadPositionalDataSampleRows(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "first_operation", DataType: "string", Source: "data-sample", Definition: "FIRST(history.operation_name)"},
		{Key: "second_operation", DataType: "string", Source: "data-sample", Definition: "NTH(history.operation_name, 2)"},
		{Key: "last_operation", DataType: "string", Source: "data-sample", Definition: "LAST(history.operation_name)"},
	}
	model.Outputs = []string{"first_operation", "second_operation", "last_operation"}

	values, _, _, err := ResolveModel(model, nil, nil, nil, nil, nil, func(sample DataSampleDefinition, resolved map[string]interface{}) ([]map[string]interface{}, error) {
		return []map[string]interface{}{
			{"operation_name": "COARSE_POLISH"},
			{"operation_name": "INTERMEDIATE_POLISH"},
			{"operation_name": "FINAL_POLISH"},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if values["first_operation"] != "COARSE_POLISH" || values["second_operation"] != "INTERMEDIATE_POLISH" || values["last_operation"] != "FINAL_POLISH" {
		t.Fatalf("unexpected text sample values: %+v", values)
	}
}

func TestBooleanParametersAndSampleCondition(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "enabled", DataType: "boolean", Source: "constant", Definition: "true"},
		{Key: "second_passed", DataType: "boolean", Source: "data-sample", Definition: "NTH(history.passed, 2)"},
		{Key: "route_enabled", DataType: "boolean", Source: "logic", Logic: &LogicDefinition{
			Conditions: []LogicCondition{{
				InputSource:    "data-sample",
				InputParameter: "NTH(history.passed, 2)",
				Operator:       "equals",
				CompareValue:   "true",
				ResultValue:    "enabled",
			}},
			ElseValue: "false",
		}},
	}
	model.Outputs = []string{"enabled", "second_passed", "route_enabled"}
	values, _, _, err := ResolveModel(model, nil, nil, nil, nil, nil, func(sample DataSampleDefinition, resolved map[string]interface{}) ([]map[string]interface{}, error) {
		return []map[string]interface{}{{"passed": false}, {"passed": true}, {"passed": false}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range model.Outputs {
		if values[key] != true {
			t.Fatalf("expected %s=true, got %#v", key, values[key])
		}
	}
}

func TestTextDataSampleValueReportsMissingData(t *testing.T) {
	if _, err := EvaluateSampleValue("FIRST(history.operation_name)", map[string][]map[string]interface{}{"history": {}}); err == nil || !strings.Contains(err.Error(), "has no rows") {
		t.Fatalf("expected empty sample error, got %v", err)
	}
	if _, err := EvaluateSampleValue("FIRST(history.operation_name)", map[string][]map[string]interface{}{"history": {{"other_column": "value"}}}); err == nil || !strings.Contains(err.Error(), `has no column "operation_name"`) {
		t.Fatalf("expected missing column error, got %v", err)
	}
}

func TestAdaptiveEWMAUsesPersistedOrInitialValue(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{{Key: "measurement", Source: "constant", Definition: "7"}, {Key: "offset", Source: "constant", Definition: "3", Adaptive: &AdaptiveDefinition{InitialValue: 3, UpdateMethod: "ewma", UpdateParameter: "measurement", Alpha: .25}}}
	model.Outputs = []string{"offset"}
	values, _, err := ResolveParameters(model, nil, nil, nil, nil)
	if err != nil || values["offset"] != 3.0 {
		t.Fatalf("unexpected initial state: %+v %v", values, err)
	}
	updates, err := CalculateAdaptiveUpdates(model, values)
	if err != nil || updates["offset"] != 4 {
		t.Fatalf("unexpected update: %+v %v", updates, err)
	}
	values, _, err = ResolveParameters(model, nil, nil, nil, map[string]float64{"offset": 4})
	if err != nil || values["offset"] != 4.0 {
		t.Fatalf("unexpected persisted state: %+v %v", values, err)
	}
}

func TestAdaptiveDeadbandAndMaximumAdjustment(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "measurement", Source: "constant", Definition: "7"},
		{Key: "offset", Source: "constant", Definition: "3", Adaptive: &AdaptiveDefinition{
			InitialValue: 3, UpdateMethod: "ewma", UpdateParameter: "measurement", Alpha: .25,
			Deadband: ptr(1), MaxAdjustment: ptr(.5),
		}},
	}
	values, _, err := ResolveParameters(model, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	updates, err := CalculateAdaptiveUpdates(model, values)
	if err != nil {
		t.Fatal(err)
	}
	if updates["offset"] != 3 {
		t.Fatalf("expected the one-unit proposal to remain inside the deadband, got %+v", updates)
	}

	model.Parameters[1].Adaptive.Deadband = ptr(.25)
	updates, err = CalculateAdaptiveUpdates(model, values)
	if err != nil {
		t.Fatal(err)
	}
	if updates["offset"] != 3.5 {
		t.Fatalf("expected maximum adjustment to limit the update to 0.5, got %+v", updates)
	}
}

func TestAdaptiveDeadbandAndMaximumAdjustmentValidation(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "measurement", Source: "constant", Definition: "7"},
		{Key: "offset", Source: "constant", Definition: "3", Adaptive: &AdaptiveDefinition{
			InitialValue: 3, UpdateMethod: "ewma", UpdateParameter: "measurement", Alpha: .25,
			Deadband: ptr(-1), MaxAdjustment: ptr(0),
		}},
	}
	if err := ValidateModel(model); err == nil || !strings.Contains(err.Error(), "deadband cannot be negative") {
		t.Fatalf("expected deadband validation error, got %v", err)
	}
	model.Parameters[1].Adaptive.Deadband = ptr(0)
	if err := ValidateModel(model); err == nil || !strings.Contains(err.Error(), "maximum adjustment must be greater than zero") {
		t.Fatalf("expected maximum-adjustment validation error, got %v", err)
	}
}
func TestAdaptiveUpdateCanReevaluateDependentOutputDuringPrepare(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "measurement", Source: "constant", Definition: "7"},
		{Key: "offset", Source: "constant", Definition: "3", Adaptive: &AdaptiveDefinition{
			InitialValue: 3, UpdateMethod: "ewma", UpdateParameter: "measurement", Alpha: .25,
		}},
		{Key: "recipe", Source: "formula", Definition: "offset * 2"},
	}
	model.Outputs = []string{"recipe"}
	values, samples, _, err := ResolveModel(model, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	updates, err := CalculateAdaptiveUpdates(model, values)
	if err != nil {
		t.Fatal(err)
	}
	values, _, _, err = ResolveModel(model, nil, values, samples, updates, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if values["offset"] != 4.0 || values["recipe"] != 8.0 {
		t.Fatalf("adaptive update was not applied before dependent output: %+v", values)
	}
}
func TestValidatePostRunFeedbackAdaptiveInputMustBeRequired(t *testing.T) {
	model := Model{
		ID: "polish-r1", FamilyID: "polish", Name: "Polish",
		Indexes: map[string]IndexValues{
			"machine": {"P1"}, "action": {"POLISH"}, "material": {"OXIDE"},
		},
		FeedbackInputs: []FeedbackInputDefinition{{Name: "removal", Type: "number"}},
		Parameters: []ParameterDefinition{{
			Key: "offset", Source: "constant", Definition: "3",
			Adaptive: &AdaptiveDefinition{
				InitialValue: 3, UpdateMethod: "ewma", Alpha: .5,
				UpdateInput: &AdaptiveUpdateInput{Source: "post-run-feedback", Name: "removal"},
			},
		}},
		Outputs: []string{"offset"},
	}
	if err := ValidateModel(model); err == nil || !strings.Contains(err.Error(), "to be required") {
		t.Fatalf("expected required feedback validation, got %v", err)
	}
	model.FeedbackInputs[0].Required = true
	if err := ValidateModel(model); err != nil {
		t.Fatalf("valid post-run adaptive input was rejected: %v", err)
	}
}
func TestConditionalLogicSelectsThenAndElseValues(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{{Key: "operations", Source: "external", Definition: "Operations"}, {Key: "parameter_1", Source: "logic", Logic: &LogicDefinition{Conditions: []LogicCondition{{InputParameter: "operations", Operator: "equals", CompareValue: "OXIDE_ETCH", ResultValue: "1"}, {InputParameter: "operations", Operator: "equals", CompareValue: "POLISH", ResultValue: "1.5"}}, ElseValue: "2"}}}
	model.Outputs = []string{"parameter_1"}
	values, _, err := ResolveParameters(model, map[string]interface{}{"Operations": "OXIDE_ETCH"}, nil, nil, nil)
	if err != nil || values["parameter_1"] != 1.0 {
		t.Fatalf("expected then value 1, got %+v %v", values, err)
	}
	values, _, err = ResolveParameters(model, map[string]interface{}{"Operations": "OTHER"}, nil, nil, nil)
	if err != nil || values["parameter_1"] != 2.0 {
		t.Fatalf("expected else value 2, got %+v %v", values, err)
	}
}

func TestConditionalLogicSupportsOrConditions(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{
		{Key: "operation", DataType: "string", Source: "external", Definition: "Operation"},
		{Key: "route", DataType: "string", Source: "logic", Logic: &LogicDefinition{
			Conditions: []LogicCondition{{
				InputParameter: "operation",
				Operator:       "equals",
				CompareValue:   "POLISH",
				ResultValue:    "RUN",
				Or: []LogicPredicate{{
					InputParameter: "operation",
					Operator:       "equals",
					CompareValue:   "OXIDE_ETCH",
				}},
			}},
			ElseValue: "HOLD",
		}},
	}
	model.Outputs = []string{"route"}

	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
	values, _, err := ResolveParameters(model, map[string]interface{}{"Operation": "OXIDE_ETCH"}, nil, nil, nil)
	if err != nil || values["route"] != "RUN" {
		t.Fatalf("expected OR condition to select RUN, got %+v %v", values, err)
	}
	values, _, err = ResolveParameters(model, map[string]interface{}{"Operation": "CLEAN"}, nil, nil, nil)
	if err != nil || values["route"] != "HOLD" {
		t.Fatalf("expected unmatched rule to select HOLD, got %+v %v", values, err)
	}
}

func TestRestrictedExpression(t *testing.T) {
	value, err := Evaluate("max(2, abs(@error) * @gain)", map[string]float64{"error": -4, "gain": .5})
	if err != nil || value != 2 {
		t.Fatalf("got %g %v", value, err)
	}
	if _, err := Evaluate("unknown(1)", nil); err == nil {
		t.Fatal("expected forbidden function error")
	}
}

func TestLookupFormulaSupportsMultipleColumns(t *testing.T) {
	model := fixture()
	model.Parameters = []ParameterDefinition{{
		Key: "recipe", DataType: "string", Source: "formula",
		Definition: `LOOKUP_ONE(@recipes, colA == "A" AND colB == @requested_b, colC)`,
	}}
	model.ExternalInputs = append(model.ExternalInputs, ExternalInputDefinition{Name: "requested_b", Type: "string"})
	model.DataSamples = []DataSampleDefinition{{
		Key: "recipes", Source: "static", Limit: 10,
		Columns: []DataSampleColumnDefinition{{Name: "colA", Type: "string"}, {Name: "colB", Type: "string"}, {Name: "colC", Type: "string"}},
		Rows:    []map[string]interface{}{{"colA": "A", "colB": "B", "colC": "RESULT"}, {"colA": "A", "colB": "C", "colC": "OTHER"}},
	}}
	model.Outputs = []string{"recipe"}
	if err := ValidateModel(model); err != nil {
		t.Fatal(err)
	}
	values, _, err := ResolveParameters(model, map[string]interface{}{"requested_b": "B", "machine": "ETCH-02", "action": "OXIDE_ETCH", "material": "OXIDE", "chamber": "B"}, nil, nil, nil)
	if err != nil || values["recipe"] != "RESULT" {
		t.Fatalf("lookup formula failed: %+v %v", values, err)
	}
}

func TestLookupFormulaValidationUsesDeclaredTypes(t *testing.T) {
	definitions := []DataSampleDefinition{{
		Key: "ranges", Source: "static", Limit: 10,
		Columns: []DataSampleColumnDefinition{{Name: "minimum", Type: "number"}, {Name: "label", Type: "string"}, {Name: "enabled", Type: "boolean"}},
		Rows:    []map[string]interface{}{{"minimum": 1.0, "label": "A", "enabled": true}},
	}}
	references := map[string]string{"lower": "number", "text_value": "string"}
	indexes := map[string]IndexValues{"chamber": {"A", "B"}}
	tests := []struct{ name, expression, resultType, errorText string }{
		{"valid numerical range", `LOOKUP_ONE(@ranges, minimum > @lower AND minimum < 2, label)`, "string", ""},
		{"text ordering", `LOOKUP_ONE(@ranges, label > "A", label)`, "string", "requires numerical operands"},
		{"mixed equality", `LOOKUP_ONE(@ranges, minimum == "1", label)`, "string", "same type"},
		{"boolean ordering", `LOOKUP_ONE(@ranges, enabled < true, label)`, "string", "requires numerical operands"},
		{"wrong reference type", `LOOKUP_ONE(@ranges, minimum > @text_value, label)`, "string", "got number and text"},
		{"unknown reference", `LOOKUP_ONE(@ranges, minimum > @missing, label)`, "string", "unknown reference @missing"},
		{"wrong return type", `LOOKUP_ONE(@ranges, minimum > 0, label)`, "number", "parameter result is number"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateLookupFormula(test.expression, test.resultType, definitions, references, indexes)
			if test.errorText == "" && err != nil {
				t.Fatalf("expected valid formula: %v", err)
			}
			if test.errorText != "" && (err == nil || !strings.Contains(err.Error(), test.errorText)) {
				t.Fatalf("expected %q, got %v", test.errorText, err)
			}
		})
	}
}
