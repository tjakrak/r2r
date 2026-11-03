package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

type ParameterDefinition struct {
	Key           string              `json:"key"`
	Description   string              `json:"description,omitempty"`
	Unit          string              `json:"unit"`
	DataType      string              `json:"dataType,omitempty"`
	Source        string              `json:"source"`
	DataSourceID  string              `json:"dataSourceId,omitempty"`
	Definition    string              `json:"definition"`
	LSL           *float64            `json:"lsl,omitempty"`
	USL           *float64            `json:"usl,omitempty"`
	LimitAction   string              `json:"limitAction,omitempty"`
	FallbackValue *float64            `json:"fallbackValue,omitempty"`
	Adaptive      *AdaptiveDefinition `json:"adaptive,omitempty"`
	Logic         *LogicDefinition    `json:"logic,omitempty"`
	Lookup        *LookupDefinition   `json:"lookup,omitempty"`
}
type LookupDefinition struct {
	Sample       string `json:"sample"`
	MatchColumn  string `json:"matchColumn"`
	Operator     string `json:"operator"`
	InputSource  string `json:"inputSource"`
	MatchValue   string `json:"matchValue"`
	ReturnColumn string `json:"returnColumn"`
}
type LogicDefinition struct {
	Conditions []LogicCondition `json:"conditions"`
	ElseValue  string           `json:"elseValue"`
}
type LogicCondition struct {
	InputParameter string           `json:"inputParameter"`
	InputSource    string           `json:"inputSource,omitempty"`
	Operator       string           `json:"operator"`
	CompareValue   string           `json:"compareValue"`
	CompareSource  string           `json:"compareSource,omitempty"`
	ResultValue    string           `json:"resultValue"`
	And            []LogicPredicate `json:"and,omitempty"`
	Or             []LogicPredicate `json:"or,omitempty"`
}
type LogicPredicate struct {
	InputParameter string `json:"inputParameter"`
	InputSource    string `json:"inputSource,omitempty"`
	Operator       string `json:"operator"`
	CompareValue   string `json:"compareValue"`
	CompareSource  string `json:"compareSource,omitempty"`
}
type AdaptiveDefinition struct {
	InitialValue         float64              `json:"initialValue"`
	UpdateMethod         string               `json:"updateMethod"`
	UpdateInput          *AdaptiveUpdateInput `json:"updateInput,omitempty"`
	UpdateParameter      string               `json:"updateParameter,omitempty"`
	MeasurementParameter string               `json:"measurementParameter,omitempty"` // Legacy model compatibility.
	Alpha                float64              `json:"alpha,omitempty"`
	UpdateFormula        string               `json:"updateFormula,omitempty"`
	Deadband             *float64             `json:"deadband,omitempty"`
	MaxAdjustment        *float64             `json:"maxAdjustment,omitempty"`
}
type AdaptiveUpdateInput struct {
	Source string `json:"source"`
	Name   string `json:"name"`
}
type DataSampleDefinition struct {
	Key          string                       `json:"key"`
	Description  string                       `json:"description,omitempty"`
	Source       string                       `json:"source"`
	DataSourceID string                       `json:"dataSourceId,omitempty"`
	Query        string                       `json:"query,omitempty"`
	Columns      []DataSampleColumnDefinition `json:"columns,omitempty"`
	Rows         []map[string]interface{}     `json:"rows,omitempty"`
	Limit        int                          `json:"limit"`
}
type DataSampleColumnDefinition struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type ExternalInputDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
}
type FeedbackInputDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
}
type ExecutionPolicy struct {
	MaxUnfinishedRuns int      `json:"maxUnfinishedRuns"`
	PartitionBy       []string `json:"partitionBy,omitempty"`
}
type Model struct {
	ID              string                    `json:"id"`
	FamilyID        string                    `json:"familyId"`
	FolderID        string                    `json:"folderId,omitempty"`
	Name            string                    `json:"name"`
	Owner           string                    `json:"owner"`
	Revision        int                       `json:"revision"`
	Status          string                    `json:"status"`
	ChangeNote      string                    `json:"changeNote"`
	CreatedBy       string                    `json:"createdBy"`
	CreatedAt       string                    `json:"createdAt"`
	SubmittedBy     string                    `json:"submittedBy,omitempty"`
	SubmittedAt     string                    `json:"submittedAt,omitempty"`
	RejectedBy      string                    `json:"rejectedBy,omitempty"`
	RejectedAt      string                    `json:"rejectedAt,omitempty"`
	RejectionReason string                    `json:"rejectionReason,omitempty"`
	ApprovedBy      string                    `json:"approvedBy,omitempty"`
	ApprovedAt      string                    `json:"approvedAt,omitempty"`
	Description     string                    `json:"description"`
	Indexes         map[string]IndexValues    `json:"indexes"`
	ExternalInputs  []ExternalInputDefinition `json:"externalInputs,omitempty"`
	FeedbackInputs  []FeedbackInputDefinition `json:"feedbackInputs,omitempty"`
	ExecutionPolicy ExecutionPolicy           `json:"executionPolicy"`
	Parameters      []ParameterDefinition     `json:"parameters"`
	DataSamples     []DataSampleDefinition    `json:"dataSamples"`
	Outputs         []string                  `json:"outputs"`
	Enabled         bool                      `json:"enabled"`
}

type IndexValues []string

func (values *IndexValues) UnmarshalJSON(data []byte) error {
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err == nil {
		*values = normalizeIndexValues(multiple)
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err != nil {
		return errors.New("index value must be a string or array of strings")
	}
	*values = normalizeIndexValues([]string{single})
	return nil
}

func normalizeIndexValues(values []string) IndexValues {
	normalized := IndexValues{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			normalized = append(normalized, value)
		}
	}
	return normalized
}

type Request struct {
	Indexes        map[string]interface{}              `json:"indexes"`
	ExternalInputs map[string]interface{}              `json:"externalInputs"`
	DataSamples    map[string][]map[string]interface{} `json:"dataSamples"`
}
type OutputValue struct {
	Parameter       string      `json:"parameter"`
	Unit            string      `json:"unit,omitempty"`
	Value           interface{} `json:"value"`
	CalculatedValue interface{} `json:"calculatedValue,omitempty"`
	LSL             *float64    `json:"lsl,omitempty"`
	USL             *float64    `json:"usl,omitempty"`
	BoundLimited    bool        `json:"boundLimited"`
	LimitAction     string      `json:"limitAction,omitempty"`
}

type ParameterLimitError struct {
	Violation OutputValue
	Message   string
}

func (err *ParameterLimitError) Error() string { return err.Message }

var requiredIndexes = []string{"machine", "action", "material"}

var ErrNoMatchingModel = errors.New("no approved control model matches all supplied indexes")

func stringValue(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func numberValue(v interface{}) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case jsonNumber:
		return n.Float64()
	default:
		var value float64
		_, err := fmt.Sscan(fmt.Sprint(v), &value)
		return value, err
	}
}

func booleanValue(v interface{}) (bool, error) {
	if value, ok := v.(bool); ok {
		return value, nil
	}
	switch strings.ToLower(strings.TrimSpace(stringValue(v))) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("value must be true or false")
	}
}

type jsonNumber interface{ Float64() (float64, error) }

func round(v float64, p int) float64 { factor := math.Pow10(p); return math.Round(v*factor) / factor }

func Select(models []Model, context map[string]interface{}) (Model, string, error) {
	for _, key := range requiredIndexes {
		if strings.TrimSpace(stringValue(context[key])) == "" {
			return Model{}, "", fmt.Errorf("missing required context index: %s", key)
		}
	}
	candidates := []Model{}
	for _, model := range models {
		if !model.Enabled || model.Status != "approved" {
			continue
		}
		matches := true
		for key, accepted := range model.Indexes {
			actual := stringValue(context[key])
			matched := false
			for _, expected := range accepted {
				if (expected == "*" && strings.TrimSpace(actual) != "") || strings.EqualFold(actual, expected) {
					matched = true
					break
				}
			}
			if !matched {
				matches = false
				break
			}
		}
		if matches {
			candidates = append(candidates, model)
		}
	}
	if len(candidates) == 0 {
		return Model{}, "", ErrNoMatchingModel
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftKeys, leftValues := modelSpecificity(candidates[i])
		rightKeys, rightValues := modelSpecificity(candidates[j])
		if leftKeys != rightKeys {
			return leftKeys > rightKeys
		}
		return leftValues < rightValues
	})
	if len(candidates) > 1 {
		firstKeys, firstValues := modelSpecificity(candidates[0])
		secondKeys, secondValues := modelSpecificity(candidates[1])
		if firstKeys == secondKeys && firstValues == secondValues {
			return Model{}, "", fmt.Errorf("ambiguous routing: models %q and %q have equal matching specificity", candidates[0].Name, candidates[1].Name)
		}
	}
	model := candidates[0]
	keys := make([]string, 0, len(model.Indexes))
	for key := range model.Indexes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, key := range keys {
		parts = append(parts, key+"="+stringValue(context[key]))
	}
	return model, "Matched indexes: " + strings.Join(parts, ", "), nil
}

func modelSpecificity(model Model) (int, int) {
	exactIndexes := 0
	totalValues := 0
	for _, values := range model.Indexes {
		if len(values) == 1 && values[0] == "*" {
			continue
		}
		exactIndexes++
		totalValues += len(values)
	}
	return exactIndexes, totalValues
}

func HasAmbiguousRouting(left, right Model) bool {
	leftKeys, leftValues := modelSpecificity(left)
	rightKeys, rightValues := modelSpecificity(right)
	if leftKeys != rightKeys || leftValues != rightValues {
		return false
	}
	for key, leftAccepted := range left.Indexes {
		rightAccepted, shared := right.Indexes[key]
		if !shared {
			continue
		}
		overlaps := false
		for _, leftValue := range leftAccepted {
			for _, rightValue := range rightAccepted {
				if leftValue == "*" || rightValue == "*" || strings.EqualFold(leftValue, rightValue) {
					overlaps = true
					break
				}
			}
		}
		if !overlaps {
			return false
		}
	}
	return true
}

type SQLParameterResolver func(parameter ParameterDefinition, resolved map[string]interface{}) (interface{}, error)
type DataSampleResolver func(sample DataSampleDefinition, resolved map[string]interface{}) ([]map[string]interface{}, error)

func ResolveParameters(model Model, context, supplied map[string]interface{}, samples map[string][]map[string]interface{}, adaptiveValues map[string]float64) (map[string]interface{}, []ParameterDefinition, error) {
	return ResolveParametersWithSQL(model, context, supplied, samples, adaptiveValues, nil)
}

func ResolveParametersWithSQL(model Model, context, supplied map[string]interface{}, samples map[string][]map[string]interface{}, adaptiveValues map[string]float64, resolver SQLParameterResolver) (map[string]interface{}, []ParameterDefinition, error) {
	values, _, unresolved, err := ResolveModel(model, context, supplied, samples, adaptiveValues, resolver, nil)
	return values, unresolved, err
}

func ResolveModel(model Model, context, supplied map[string]interface{}, suppliedSamples map[string][]map[string]interface{}, adaptiveValues map[string]float64, sqlResolver SQLParameterResolver, sampleResolver DataSampleResolver) (map[string]interface{}, map[string][]map[string]interface{}, []ParameterDefinition, error) {
	return ResolveModelWithLimitCollector(model, context, supplied, suppliedSamples, adaptiveValues, sqlResolver, sampleResolver, nil)
}

func ResolveModelWithLimitCollector(model Model, context, supplied map[string]interface{}, suppliedSamples map[string][]map[string]interface{}, adaptiveValues map[string]float64, sqlResolver SQLParameterResolver, sampleResolver DataSampleResolver, collectLimit func(OutputValue)) (map[string]interface{}, map[string][]map[string]interface{}, []ParameterDefinition, error) {
	graph, err := BuildDependencyGraph(model)
	if err != nil {
		return nil, nil, nil, err
	}
	values := map[string]interface{}{}
	samples := map[string][]map[string]interface{}{}
	unresolved := []ParameterDefinition{}
	resolvedNodes := map[string]bool{}
	parameters := map[string]ParameterDefinition{}
	dataSamples := map[string]DataSampleDefinition{}
	for _, parameter := range model.Parameters {
		parameters["parameter:"+parameter.Key] = parameter
	}
	for _, sample := range model.DataSamples {
		dataSamples["sample:"+sample.Key] = sample
	}
	for _, input := range model.ExternalInputs {
		raw, exists := context[input.Name]
		if !exists {
			return nil, nil, nil, fmt.Errorf("missing required external input %q", input.Name)
		}
		switch input.Type {
		case "number":
			value, valueErr := numberValue(raw)
			if valueErr != nil {
				return nil, nil, nil, fmt.Errorf("external input %s must be a number", input.Name)
			}
			values[input.Name] = value
		case "string":
			if _, ok := raw.(string); !ok {
				return nil, nil, nil, fmt.Errorf("external input %s must be text", input.Name)
			}
			values[input.Name] = raw
		case "boolean":
			value, valueErr := booleanValue(raw)
			if valueErr != nil {
				return nil, nil, nil, fmt.Errorf("external input %s must be true or false", input.Name)
			}
			values[input.Name] = value
		default:
			return nil, nil, nil, fmt.Errorf("external input %s has invalid type %q", input.Name, input.Type)
		}
		resolvedNodes["external:"+input.Name] = true
	}
	dependencies := map[string][]string{}
	for _, edge := range graph.Edges {
		dependencies[edge.To] = append(dependencies[edge.To], edge.From)
	}
	for _, nodeID := range graph.Order {
		if strings.HasPrefix(nodeID, "external:") {
			continue
		}
		if strings.HasPrefix(nodeID, "feedback:") {
			resolvedNodes[nodeID] = true
			continue
		}
		if strings.HasPrefix(nodeID, "index:") {
			resolvedNodes[nodeID] = true
			continue
		}
		blocked := false
		for _, dependency := range dependencies[nodeID] {
			if !resolvedNodes[dependency] {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		if sample, exists := dataSamples[nodeID]; exists {
			if rows, supplied := suppliedSamples[sample.Key]; supplied {
				if len(rows) > sample.Limit {
					rows = rows[:sample.Limit]
				}
				rows, resolveErr := validateResolvedDataSampleRows(sample, rows)
				if resolveErr != nil {
					return nil, nil, nil, resolveErr
				}
				samples[sample.Key] = rows
				resolvedNodes[nodeID] = true
			} else if sample.Source == "static" {
				rows := append([]map[string]interface{}{}, sample.Rows...)
				if len(rows) > sample.Limit {
					rows = rows[:sample.Limit]
				}
				rows, resolveErr := validateResolvedDataSampleRows(sample, rows)
				if resolveErr != nil {
					return nil, nil, nil, resolveErr
				}
				samples[sample.Key] = rows
				resolvedNodes[nodeID] = true
			} else if sampleResolver != nil {
				rows, resolveErr := sampleResolver(sample, values)
				if resolveErr != nil {
					return nil, nil, nil, resolveErr
				}
				if len(rows) > sample.Limit {
					rows = rows[:sample.Limit]
				}
				rows, validationErr := validateResolvedDataSampleRows(sample, rows)
				if validationErr != nil {
					return nil, nil, nil, validationErr
				}
				samples[sample.Key] = rows
				resolvedNodes[nodeID] = true
			}
			continue
		}
		parameter := parameters[nodeID]
		var value interface{}
		var resolveErr error
		if parameter.Adaptive != nil {
			if adaptiveValue, exists := adaptiveValues[parameter.Key]; exists {
				value = adaptiveValue
			} else {
				value = parameter.Adaptive.InitialValue
			}
		} else {
			switch parameter.Source {
			case "expression":
				value, resolveErr = EvaluateTypedExpression(parameter.Definition, parameterDataType(parameter), model, context, values, samples)
			case "sql":
				raw, exists := supplied[parameter.Key]
				if exists {
					value = raw
				} else if sqlResolver != nil {
					value, resolveErr = sqlResolver(parameter, values)
				} else {
					unresolved = append(unresolved, parameter)
					continue
				}
			case "external":
				externalValue, exists := context[parameter.Definition]
				if !exists {
					return nil, nil, nil, fmt.Errorf("parameter %s: missing external context value %q", parameter.Key, parameter.Definition)
				}
				value = externalValue
			case "constant":
				if parameterDataType(parameter) == "string" {
					value = parameter.Definition
				} else if parameterDataType(parameter) == "boolean" {
					value, resolveErr = booleanValue(parameter.Definition)
				} else {
					value, resolveErr = EvaluateWithSamples(parameter.Definition, numericValues(values), samples)
				}
			case "formula":
				if IsLookupFormula(parameter.Definition) {
					value, resolveErr = EvaluateLookupFormula(parameter.Definition, parameterDataType(parameter), model.DataSamples, context, values, samples)
				} else {
					value, resolveErr = EvaluateWithSamples(parameter.Definition, numericValues(values), samples)
				}
			case "data-sample":
				if parameterDataType(parameter) != "number" {
					value, resolveErr = EvaluateSampleValue(parameter.Definition, samples)
				} else {
					value, resolveErr = EvaluateWithSamples(parameter.Definition, numericValues(values), samples)
				}
			case "logic":
				if parameter.Logic == nil {
					return nil, nil, nil, fmt.Errorf("logic parameter %s has no logic settings", parameter.Key)
				}
				expression := parameter.Logic.ElseValue
				for _, condition := range parameter.Logic.Conditions {
					andPredicates := append([]LogicPredicate{{
						InputParameter: condition.InputParameter,
						InputSource:    condition.InputSource,
						Operator:       condition.Operator,
						CompareValue:   condition.CompareValue,
						CompareSource:  condition.CompareSource,
					}}, condition.And...)
					allMatch := true
					for _, predicate := range andPredicates {
						matches, predicateErr := evaluateLogicPredicate(predicate, context, values, samples)
						if predicateErr != nil {
							return nil, nil, nil, fmt.Errorf("logic parameter %s: %w", parameter.Key, predicateErr)
						}
						if !matches {
							allMatch = false
							break
						}
					}
					anyOrMatch := false
					for _, predicate := range condition.Or {
						matches, predicateErr := evaluateLogicPredicate(predicate, context, values, samples)
						if predicateErr != nil {
							return nil, nil, nil, fmt.Errorf("logic parameter %s: %w", parameter.Key, predicateErr)
						}
						if matches {
							anyOrMatch = true
							break
						}
					}
					if allMatch || anyOrMatch {
						expression = condition.ResultValue
						break
					}
				}
				value, resolveErr = resolveLogicResult(expression, parameterDataType(parameter), context, values, samples)
			case "lookup":
				if parameter.Lookup == nil {
					resolveErr = fmt.Errorf("lookup settings are missing")
				} else {
					value, resolveErr = evaluateLookup(*parameter.Lookup, parameterDataType(parameter), model.DataSamples, context, values, samples)
				}
			default:
				resolveErr = fmt.Errorf("unsupported parameter source %s", parameter.Source)
			}
		}
		if resolveErr != nil {
			return nil, nil, nil, fmt.Errorf("parameter %s: %w", parameter.Key, resolveErr)
		}
		if value == nil {
			values[parameter.Key] = nil
			resolvedNodes[nodeID] = true
			continue
		}
		if parameter.DataType == "" && parameter.Source == "external" {
			// Legacy external parameters inherit the caller's already-declared input type.
		} else if parameterDataType(parameter) == "number" {
			value, resolveErr = numberValue(value)
			if resolveErr != nil {
				return nil, nil, nil, fmt.Errorf("parameter %s must resolve to a number", parameter.Key)
			}
		} else if parameterDataType(parameter) == "boolean" {
			value, resolveErr = booleanValue(value)
			if resolveErr != nil {
				return nil, nil, nil, fmt.Errorf("parameter %s must resolve to true or false", parameter.Key)
			}
		} else if _, ok := value.(string); !ok {
			return nil, nil, nil, fmt.Errorf("parameter %s must resolve to text", parameter.Key)
		}
		value, limitResult, limited, limitErr := applyParameterLimits(parameter, value)
		if limitErr != nil {
			return nil, nil, nil, limitErr
		}
		if limited && collectLimit != nil {
			collectLimit(limitResult)
		}
		values[parameter.Key] = value
		resolvedNodes[nodeID] = true
	}
	return values, samples, unresolved, nil
}

func applyParameterLimits(parameter ParameterDefinition, value interface{}) (interface{}, OutputValue, bool, error) {
	result := OutputValue{Parameter: parameter.Key, Unit: parameter.Unit, Value: value, LSL: parameter.LSL, USL: parameter.USL}
	if value == nil {
		return nil, result, false, nil
	}
	if parameter.LSL == nil && parameter.USL == nil {
		return value, result, false, nil
	}
	numeric, err := numberValue(value)
	if err != nil {
		return value, result, false, fmt.Errorf("parameter %s limits require a numerical value", parameter.Key)
	}
	below := parameter.LSL != nil && numeric < *parameter.LSL
	above := parameter.USL != nil && numeric > *parameter.USL
	if !below && !above {
		return value, result, false, nil
	}
	action := parameter.LimitAction
	if action == "" {
		action = "warn"
	}
	result.BoundLimited = true
	result.LimitAction = action
	switch action {
	case "reject":
		if below {
			message := fmt.Sprintf("parameter %q value %v is below LSL %v", parameter.Key, value, *parameter.LSL)
			return value, result, true, &ParameterLimitError{Violation: result, Message: message}
		}
		message := fmt.Sprintf("parameter %q value %v is above USL %v", parameter.Key, value, *parameter.USL)
		return value, result, true, &ParameterLimitError{Violation: result, Message: message}
	case "clamp":
		result.CalculatedValue = value
		if below {
			value = *parameter.LSL
		} else {
			value = *parameter.USL
		}
		result.Value = value
	case "fallback":
		if parameter.FallbackValue == nil {
			return value, result, true, fmt.Errorf("parameter %s fallback action requires a fallback value", parameter.Key)
		}
		result.CalculatedValue = value
		value = *parameter.FallbackValue
		result.Value = value
	case "warn":
		// Preserve the resolved value for downstream calculations.
	default:
		return value, result, true, fmt.Errorf("parameter %s has unsupported limit action %q", parameter.Key, action)
	}
	return value, result, true, nil
}

func EvaluateResolvedOutputs(model Model, values map[string]interface{}, limits map[string]OutputValue) ([]OutputValue, error) {
	definitions := map[string]ParameterDefinition{}
	for _, parameter := range model.Parameters {
		definitions[parameter.Key] = parameter
	}
	results := make([]OutputValue, 0, len(model.Outputs))
	for _, key := range model.Outputs {
		value, exists := values[key]
		if !exists {
			return nil, fmt.Errorf("output parameter %s was not resolved", key)
		}
		if limited, exists := limits[key]; exists {
			limited.Value = value
			results = append(results, limited)
			continue
		}
		parameter := definitions[key]
		results = append(results, OutputValue{Parameter: key, Unit: parameter.Unit, Value: value, LSL: parameter.LSL, USL: parameter.USL})
	}
	return results, nil
}

func evaluateLookup(definition LookupDefinition, resultType string, sampleDefinitions []DataSampleDefinition, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (interface{}, error) {
	var sampleDefinition *DataSampleDefinition
	for index := range sampleDefinitions {
		if strings.EqualFold(sampleDefinitions[index].Key, definition.Sample) {
			sampleDefinition = &sampleDefinitions[index]
			break
		}
	}
	if sampleDefinition == nil {
		return nil, fmt.Errorf("unknown data sample %q", definition.Sample)
	}
	columnTypes := map[string]string{}
	canonicalColumns := map[string]string{}
	for _, column := range sampleDefinition.Columns {
		columnTypes[strings.ToLower(column.Name)] = column.Type
		canonicalColumns[strings.ToLower(column.Name)] = column.Name
	}
	matchType, matchExists := columnTypes[strings.ToLower(definition.MatchColumn)]
	returnType, returnExists := columnTypes[strings.ToLower(definition.ReturnColumn)]
	if !matchExists || !returnExists {
		return nil, errors.New("lookup columns are not present in the declared sample schema")
	}
	if returnType != resultType {
		return nil, fmt.Errorf("lookup return column type %s does not match parameter type %s", returnType, resultType)
	}
	var expected interface{}
	switch definition.InputSource {
	case "parameter", "external":
		expected = values[definition.MatchValue]
	case "index":
		expected = context[definition.MatchValue]
	case "constant":
		expected = definition.MatchValue
	default:
		return nil, fmt.Errorf("unsupported lookup input source %q", definition.InputSource)
	}
	if expected == nil {
		return nil, fmt.Errorf("lookup match input %q is unavailable", definition.MatchValue)
	}
	if matchType == "number" {
		converted, err := numberValue(expected)
		if err != nil {
			return nil, errors.New("lookup match value must be a number")
		}
		expected = converted
	} else if matchType == "boolean" {
		converted, err := booleanValue(expected)
		if err != nil {
			return nil, errors.New("lookup match value must be boolean")
		}
		expected = converted
	} else if _, ok := expected.(string); !ok {
		return nil, errors.New("lookup match value must be text")
	}
	matchName := canonicalColumns[strings.ToLower(definition.MatchColumn)]
	returnName := canonicalColumns[strings.ToLower(definition.ReturnColumn)]
	matches := []interface{}{}
	for _, row := range samples[sampleDefinition.Key] {
		actual := row[matchName]
		if actual == nil {
			continue
		}
		matched := false
		switch definition.Operator {
		case "equals":
			if matchType == "number" {
				left, leftErr := numberValue(actual)
				right, rightErr := numberValue(expected)
				matched = leftErr == nil && rightErr == nil && left == right
			} else if matchType == "boolean" {
				left, leftErr := booleanValue(actual)
				right, rightErr := booleanValue(expected)
				matched = leftErr == nil && rightErr == nil && left == right
			} else {
				matched = strings.EqualFold(strings.TrimSpace(stringValue(actual)), strings.TrimSpace(stringValue(expected)))
			}
		case "contains":
			if matchType != "string" {
				return nil, errors.New("lookup contains operator requires a text match column")
			}
			matched = strings.Contains(strings.ToLower(stringValue(actual)), strings.ToLower(stringValue(expected)))
		default:
			return nil, fmt.Errorf("unsupported lookup operator %q", definition.Operator)
		}
		if matched {
			matches = append(matches, row[returnName])
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("lookup found %d matching rows in data sample %s", len(matches), definition.Sample)
	}
	return matches[0], nil
}

func resolveLogicResult(expression, dataType string, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (interface{}, error) {
	reference := strings.TrimPrefix(strings.TrimSpace(expression), "@")
	referenced, exists := values[reference]
	if !exists {
		referenced, exists = context[reference]
	}
	if exists {
		if referenced == nil {
			return nil, nil
		}
		switch dataType {
		case "number":
			value, err := numberValue(referenced)
			if err != nil {
				return nil, errors.New("referenced result parameter must be a number")
			}
			return value, nil
		case "boolean":
			value, err := booleanValue(referenced)
			if err != nil {
				return nil, errors.New("referenced result parameter must be boolean")
			}
			return value, nil
		case "string":
			if value, ok := referenced.(string); ok {
				return value, nil
			}
			return nil, errors.New("referenced result parameter must be text")
		}
	}
	switch dataType {
	case "number":
		return EvaluateWithSamples(expression, numericValues(values), samples)
	case "boolean":
		if value, err := EvaluateSampleValue(expression, samples); err == nil {
			return booleanValue(value)
		}
		return booleanValue(expression)
	default:
		if value, err := EvaluateSampleValue(expression, samples); err == nil {
			if text, ok := value.(string); ok {
				return text, nil
			}
			return nil, errors.New("data sample result must be text")
		}
		return expression, nil
	}
}

func parameterDataType(parameter ParameterDefinition) string {
	if parameter.DataType == "" {
		return "number"
	}
	return parameter.DataType
}

func evaluateLogicPredicate(predicate LogicPredicate, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (bool, error) {
	inputSource := predicate.InputSource
	if inputSource == "" {
		inputSource = "parameter"
	}
	input, exists, err := resolveLogicOperand(inputSource, predicate.InputParameter, context, values, samples)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, fmt.Errorf("condition input %s is unavailable", predicate.InputParameter)
	}
	expected, expectedExists, err := resolveLogicOperand(predicate.CompareSource, predicate.CompareValue, context, values, samples)
	if err != nil {
		return false, err
	}
	if !expectedExists {
		return false, fmt.Errorf("comparison input %s is unavailable", predicate.CompareValue)
	}
	return compareLogicValue(input, predicate.Operator, expected)
}

func resolveLogicOperand(source, name string, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (interface{}, bool, error) {
	switch source {
	case "constant":
		return name, true, nil
	case "index":
		value, exists := context[strings.TrimPrefix(strings.TrimSpace(name), "@")]
		return value, exists, nil
	case "external", "parameter":
		value, exists := values[strings.TrimPrefix(strings.TrimSpace(name), "@")]
		return value, exists, nil
	case "":
		return name, true, nil
	case "data-sample":
		value, err := EvaluateSampleValue(name, samples)
		if err != nil {
			value, err = EvaluateWithSamples(name, numericValues(values), samples)
		}
		if err != nil {
			return nil, false, fmt.Errorf("invalid data sample expression: %w", err)
		}
		return value, true, nil
	default:
		return nil, false, fmt.Errorf("invalid condition source %q", source)
	}
}

func compareLogicValue(input interface{}, operator string, expected interface{}) (bool, error) {
	if input == nil || expected == nil {
		return false, nil
	}
	switch operator {
	case "equals":
		return strings.EqualFold(strings.TrimSpace(stringValue(input)), strings.TrimSpace(stringValue(expected))), nil
	case "not-equals":
		return !strings.EqualFold(strings.TrimSpace(stringValue(input)), strings.TrimSpace(stringValue(expected))), nil
	case "greater-than", "greater-or-equal", "less-than", "less-or-equal":
		left, err := numberValue(input)
		if err != nil {
			return false, errors.New("numeric comparison requires a numeric input")
		}
		right, err := numberValue(expected)
		if err != nil {
			return false, errors.New("numeric comparison requires a numeric comparison value")
		}
		switch operator {
		case "greater-than":
			return left > right, nil
		case "greater-or-equal":
			return left >= right, nil
		case "less-than":
			return left < right, nil
		default:
			return left <= right, nil
		}
	default:
		return false, fmt.Errorf("unsupported comparison operator %s", operator)
	}
}

func CalculateAdaptiveUpdates(model Model, values map[string]interface{}) (map[string]float64, error) {
	return CalculateAdaptiveUpdatesForSource(model, values, "")
}

func CalculateAdaptiveUpdatesForSource(model Model, values map[string]interface{}, source string) (map[string]float64, error) {
	updates := map[string]float64{}
	variables := numericValues(values)
	for _, parameter := range model.Parameters {
		if parameter.Adaptive == nil {
			continue
		}
		definition := parameter.Adaptive
		updateInput := adaptiveUpdateInput(definition)
		if source != "" && updateInput.Source != source {
			continue
		}
		current, ok := variables[parameter.Key]
		if !ok {
			return nil, fmt.Errorf("adaptive parameter %s has no numeric current value", parameter.Key)
		}
		var next float64
		var err error
		switch definition.UpdateMethod {
		case "ewma":
			measurement, exists := variables[updateInput.Name]
			if !exists {
				return nil, fmt.Errorf("adaptive parameter %s: update input %s is unavailable", parameter.Key, updateInput.Name)
			}
			next = definition.Alpha*measurement + (1-definition.Alpha)*current
		case "formula":
			formulaVariables := numericValues(values)
			formulaVariables["previous_value"] = current
			formulaVariables["current_value"] = current
			next, err = Evaluate(definition.UpdateFormula, formulaVariables)
		default:
			err = fmt.Errorf("unsupported update method %s", definition.UpdateMethod)
		}
		if err != nil {
			return nil, fmt.Errorf("adaptive parameter %s: %w", parameter.Key, err)
		}
		adjustment := next - current
		if definition.Deadband != nil && math.Abs(adjustment) <= *definition.Deadband {
			next = current
		} else if definition.MaxAdjustment != nil && math.Abs(adjustment) > *definition.MaxAdjustment {
			next = current + math.Copysign(*definition.MaxAdjustment, adjustment)
		}
		updates[parameter.Key] = next
	}
	return updates, nil
}

func adaptiveUpdateParameter(definition *AdaptiveDefinition) string {
	return adaptiveUpdateInput(definition).Name
}

func adaptiveUpdateInput(definition *AdaptiveDefinition) AdaptiveUpdateInput {
	if definition.UpdateInput != nil {
		return *definition.UpdateInput
	}
	if definition.UpdateParameter != "" {
		return AdaptiveUpdateInput{Source: "parameter", Name: definition.UpdateParameter}
	}
	return AdaptiveUpdateInput{Source: "parameter", Name: definition.MeasurementParameter}
}

func referencesMissingSample(expression string, definitions []DataSampleDefinition, samples map[string][]map[string]interface{}) bool {
	for _, definition := range definitions {
		if strings.Contains(expression, definition.Key+".") {
			if _, exists := samples[definition.Key]; !exists {
				return true
			}
		}
	}
	return false
}

func EvaluateOutputs(model Model, values map[string]interface{}) ([]OutputValue, error) {
	definitions := map[string]ParameterDefinition{}
	for _, parameter := range model.Parameters {
		definitions[parameter.Key] = parameter
	}
	results := make([]OutputValue, 0, len(model.Outputs))
	for _, key := range model.Outputs {
		value, exists := values[key]
		if !exists {
			return nil, fmt.Errorf("output parameter %s was not resolved", key)
		}
		parameter := definitions[key]
		limited := false
		action := parameter.LimitAction
		if action == "" {
			action = "warn"
		}
		calculatedValue := value
		if numeric, err := numberValue(value); err == nil {
			if parameter.LSL != nil && numeric < *parameter.LSL {
				limited = true
				if action == "clamp" {
					value = *parameter.LSL
				}
			}
			if parameter.USL != nil && numeric > *parameter.USL {
				limited = true
				if action == "clamp" {
					value = *parameter.USL
				}
			}
			if limited && action == "fallback" {
				value = *parameter.FallbackValue
			}
		}
		output := OutputValue{Parameter: key, Unit: parameter.Unit, Value: value, LSL: parameter.LSL, USL: parameter.USL, BoundLimited: limited, LimitAction: action}
		if limited && (action == "clamp" || action == "fallback") {
			output.CalculatedValue = calculatedValue
		}
		results = append(results, output)
	}
	return results, nil
}

func ValidateModel(model Model) error {
	for _, key := range requiredIndexes {
		if len(model.Indexes[key]) == 0 {
			return fmt.Errorf("%s index is required", key)
		}
	}
	for key, accepted := range model.Indexes {
		if len(accepted) == 0 {
			return fmt.Errorf("%s index needs at least one accepted value", key)
		}
		if len(accepted) > 1 {
			for _, value := range accepted {
				if value == "*" {
					return fmt.Errorf("%s index wildcard * cannot be combined with other accepted values", key)
				}
			}
		}
	}
	if model.Name == "" || model.FamilyID == "" {
		return errors.New("model name and internal family identifier are required")
	}
	if len(model.Parameters) == 0 {
		return errors.New("at least one processing parameter is required")
	}
	if len(model.Outputs) == 0 {
		return errors.New("at least one output definition is required")
	}
	if model.ExecutionPolicy.MaxUnfinishedRuns < 0 {
		return errors.New("maximum unfinished runs cannot be negative")
	}
	partitionSeen := map[string]bool{}
	for _, field := range model.ExecutionPolicy.PartitionBy {
		field = strings.TrimSpace(field)
		if field == "" {
			return errors.New("unfinished-run partition fields cannot be empty")
		}
		if partitionSeen[strings.ToLower(field)] {
			return fmt.Errorf("duplicate unfinished-run partition field %s", field)
		}
		partitionSeen[strings.ToLower(field)] = true
		if _, exists := model.Indexes[field]; exists {
			continue
		}
		found := false
		for _, input := range model.ExternalInputs {
			if input.Name == field {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unfinished-run partition references unknown index or external input %s", field)
		}
	}
	names := map[string]string{}
	registerName := func(raw, category string) error {
		name := strings.TrimSpace(raw)
		if name == "" {
			return fmt.Errorf("every %s needs a name", category)
		}
		normalized := strings.ToLower(name)
		if existing, found := names[normalized]; found {
			return fmt.Errorf("%s name %q conflicts with an existing %s", category, name, existing)
		}
		names[normalized] = category
		return nil
	}
	for key := range model.Indexes {
		if err := registerName(key, "index"); err != nil {
			return err
		}
	}
	feedbackTypes := map[string]string{}
	feedbackRequired := map[string]bool{}
	for _, input := range model.FeedbackInputs {
		if strings.TrimSpace(input.Name) == "" || (input.Type != "string" && input.Type != "number" && input.Type != "boolean") {
			return errors.New("every feedback input needs a name and valid data type")
		}
		if err := registerName(input.Name, "feedback input"); err != nil {
			return err
		}
		feedbackTypes[input.Name] = input.Type
		feedbackRequired[input.Name] = input.Required
	}
	if _, err := BuildDependencyGraph(model); err != nil {
		return err
	}
	seen := map[string]bool{}
	declared := map[string]bool{}
	valueTypes := map[string]string{}
	variables := map[string]float64{}
	for _, input := range model.ExternalInputs {
		name := strings.TrimSpace(input.Name)
		if input.Type != "string" && input.Type != "number" && input.Type != "boolean" {
			return fmt.Errorf("external input %s must use text, number, or boolean type", name)
		}
		if err := registerName(name, "external input"); err != nil {
			return err
		}
		seen[name] = true
		declared[name] = true
		valueTypes[name] = input.Type
		if input.Type == "number" {
			variables[name] = 1
		}
	}
	for _, parameter := range model.Parameters {
		if parameterDataType(parameter) == "number" {
			variables[parameter.Key] = 1
		}
		declared[parameter.Key] = true
		valueTypes[parameter.Key] = parameterDataType(parameter)
	}
	for _, parameter := range model.Parameters {
		if parameter.Key == "" || (parameter.Source != "logic" && parameter.Source != "lookup" && parameter.Definition == "") {
			return errors.New("every parameter needs a key and source value")
		}
		dataType := parameterDataType(parameter)
		if dataType != "string" && dataType != "number" && dataType != "boolean" {
			return fmt.Errorf("parameter %s must use text, number, or boolean type", parameter.Key)
		}
		if dataType != "number" && ((parameter.Source == "formula" && !IsLookupFormula(parameter.Definition)) || parameter.Adaptive != nil) {
			return fmt.Errorf("%s parameter %s cannot use %s as its value source", dataType, parameter.Key, parameter.Source)
		}
		if (dataType != "number" || parameter.Source == "constant") && (parameter.LSL != nil || parameter.USL != nil) {
			return fmt.Errorf("parameter %s cannot use specification limits", parameter.Key)
		}
		if parameter.LSL != nil && parameter.USL != nil && *parameter.LSL > *parameter.USL {
			return fmt.Errorf("parameter %s LSL cannot exceed USL", parameter.Key)
		}
		if parameter.LSL != nil || parameter.USL != nil {
			action := parameter.LimitAction
			if action == "" {
				action = "warn"
			}
			if action != "warn" && action != "clamp" && action != "reject" && action != "fallback" {
				return fmt.Errorf("parameter %s has an invalid out-of-limit action", parameter.Key)
			}
			if action == "fallback" && parameter.FallbackValue == nil {
				return fmt.Errorf("parameter %s needs a fallback value", parameter.Key)
			}
		}
		if err := registerName(parameter.Key, "parameter"); err != nil {
			return err
		}
		if parameter.Source != "expression" && parameter.Source != "sql" && parameter.Source != "constant" && parameter.Source != "formula" && parameter.Source != "external" && parameter.Source != "data-sample" && parameter.Source != "logic" && parameter.Source != "lookup" {
			return fmt.Errorf("invalid source %q for parameter %s; use expression or sql", parameter.Source, parameter.Key)
		}
		if parameter.Source == "sql" && parameter.DataSourceID == "" {
			return fmt.Errorf("SQL parameter %s needs a configured data source", parameter.Key)
		}
		if parameter.Source == "expression" {
			if err := ValidateTypedExpression(parameter.Definition, dataType, model, valueTypes); err != nil {
				return fmt.Errorf("invalid definition for %s: %w", parameter.Key, err)
			}
		}
		if parameter.Source == "data-sample" && dataType != "number" {
			if _, err := EvaluateSampleValue(parameter.Definition, validationSamples(model.DataSamples)); err != nil {
				return fmt.Errorf("invalid definition for %s: %w", parameter.Key, err)
			}
		}
		if (parameter.Source == "constant" && dataType == "number") || parameter.Source == "formula" || (parameter.Source == "data-sample" && dataType == "number") {
			lookupFormula := parameter.Source == "formula" && IsLookupFormula(parameter.Definition)
			if lookupFormula {
				if lookupErr := ValidateLookupFormula(parameter.Definition, dataType, model.DataSamples, valueTypes, model.Indexes); lookupErr != nil {
					return fmt.Errorf("invalid definition for %s: %w", parameter.Key, lookupErr)
				}
			} else if parameter.Source == "formula" {
				references, _, dependencyErr := ExpressionDependencies(parameter.Definition)
				if dependencyErr != nil {
					return fmt.Errorf("invalid definition for %s: %w", parameter.Key, dependencyErr)
				}
				for _, reference := range references {
					if _, numeric := variables[reference]; !numeric {
						return fmt.Errorf("formula parameter %s references non-numeric value %s", parameter.Key, reference)
					}
				}
			}
			if !lookupFormula {
				if _, err := EvaluateWithSamples(parameter.Definition, variables, validationSamples(model.DataSamples)); err != nil {
					return fmt.Errorf("invalid definition for %s: %w", parameter.Key, err)
				}
			}
		}
		if parameter.Adaptive != nil {
			if parameter.Source != "constant" && parameter.Source != "expression" {
				return fmt.Errorf("adaptive parameter %s must use an initial expression value", parameter.Key)
			}
			updateInput := adaptiveUpdateInput(parameter.Adaptive)
			if updateInput.Source != "parameter" && updateInput.Source != "post-run-feedback" {
				return fmt.Errorf("adaptive parameter %s has an invalid update input source", parameter.Key)
			}
			if updateInput.Source == "post-run-feedback" && !feedbackRequired[updateInput.Name] {
				return fmt.Errorf("adaptive parameter %s requires post-run feedback %s to be required", parameter.Key, updateInput.Name)
			}
			if parameter.Adaptive.UpdateMethod == "ewma" {
				validInput := false
				switch updateInput.Source {
				case "parameter":
					_, validInput = variables[updateInput.Name]
				case "post-run-feedback":
					validInput = feedbackTypes[updateInput.Name] == "number" && feedbackRequired[updateInput.Name]
				}
				if !validInput || parameter.Adaptive.Alpha <= 0 || parameter.Adaptive.Alpha > 1 {
					return fmt.Errorf("adaptive parameter %s needs a defined numeric update input and alpha between 0 and 1", parameter.Key)
				}
			}
			if parameter.Adaptive.UpdateMethod == "formula" {
				formulaVariables := make(map[string]float64, len(variables)+2)
				for key, value := range variables {
					formulaVariables[key] = value
				}
				formulaVariables["previous_value"] = 1
				formulaVariables["current_value"] = 1
				for name, dataType := range feedbackTypes {
					if dataType == "number" {
						formulaVariables[name] = 1
					}
				}
				if _, err := Evaluate(parameter.Adaptive.UpdateFormula, formulaVariables); err != nil {
					return fmt.Errorf("adaptive parameter %s has an invalid update formula: %w", parameter.Key, err)
				}
			}
			if parameter.Adaptive.Deadband != nil && *parameter.Adaptive.Deadband < 0 {
				return fmt.Errorf("adaptive parameter %s deadband cannot be negative", parameter.Key)
			}
			if parameter.Adaptive.MaxAdjustment != nil && *parameter.Adaptive.MaxAdjustment <= 0 {
				return fmt.Errorf("adaptive parameter %s maximum adjustment must be greater than zero", parameter.Key)
			}
		}
		if parameter.Source == "logic" {
			if parameter.Logic == nil || len(parameter.Logic.Conditions) == 0 {
				return fmt.Errorf("logic parameter %s needs at least one condition", parameter.Key)
			}
			validOperator := map[string]bool{"equals": true, "not-equals": true, "greater-than": true, "greater-or-equal": true, "less-than": true, "less-or-equal": true}
			for index, condition := range parameter.Logic.Conditions {
				predicates := append([]LogicPredicate{{
					InputParameter: condition.InputParameter,
					InputSource:    condition.InputSource,
					Operator:       condition.Operator,
					CompareValue:   condition.CompareValue,
					CompareSource:  condition.CompareSource,
				}}, condition.And...)
				predicates = append(predicates, condition.Or...)
				for predicateIndex, predicate := range predicates {
					inputSource := predicate.InputSource
					if inputSource == "" {
						inputSource = "parameter"
					}
					sourceType, sourceValid := logicOperandType(inputSource, predicate.InputParameter, model, valueTypes)
					if !sourceValid || !validOperator[predicate.Operator] || predicate.CompareValue == "" {
						return fmt.Errorf("logic parameter %s condition %d clause %d is incomplete", parameter.Key, index+1, predicateIndex+1)
					}
					compareSource := predicate.CompareSource
					if compareSource == "" || compareSource == "constant" {
						if sourceType == "number" {
							if _, err := numberValue(predicate.CompareValue); err != nil {
								return fmt.Errorf("logic parameter %s condition %d comparison must be numeric", parameter.Key, index+1)
							}
						} else if sourceType == "boolean" {
							if _, err := booleanValue(predicate.CompareValue); err != nil {
								return fmt.Errorf("logic parameter %s condition %d comparison must be boolean", parameter.Key, index+1)
							}
						}
					} else {
						compareType, compareValid := logicOperandType(compareSource, predicate.CompareValue, model, valueTypes)
						if !compareValid || compareType != sourceType {
							return fmt.Errorf("logic parameter %s condition %d comparison must reference a %s value", parameter.Key, index+1, sourceType)
						}
					}
					if sourceType != "number" && predicate.Operator != "equals" && predicate.Operator != "not-equals" {
						typeLabel := sourceType
						if sourceType == "string" {
							typeLabel = "text"
						}
						return fmt.Errorf("logic parameter %s condition %d cannot use %s with %s", parameter.Key, index+1, predicate.Operator, typeLabel)
					}
				}
				if condition.ResultValue == "" {
					return fmt.Errorf("logic parameter %s condition %d is incomplete", parameter.Key, index+1)
				}
				if dataType == "number" {
					if _, err := EvaluateWithSamples(condition.ResultValue, variables, validationSamples(model.DataSamples)); err != nil {
						return fmt.Errorf("logic parameter %s condition %d has an invalid result: %w", parameter.Key, index+1, err)
					}
				} else if resultType, referencesValue := logicResultReferenceType(condition.ResultValue, model, valueTypes); referencesValue {
					if resultType != dataType {
						return fmt.Errorf("logic parameter %s condition %d result references a %s value", parameter.Key, index+1, resultType)
					}
				} else if dataType == "boolean" {
					if _, err := booleanValue(condition.ResultValue); err != nil {
						return fmt.Errorf("logic parameter %s condition %d result must be true, false, or a boolean parameter", parameter.Key, index+1)
					}
				}
			}
			if parameter.Logic.ElseValue == "" {
				return fmt.Errorf("logic parameter %s needs an else value", parameter.Key)
			}
			if dataType == "number" {
				if _, err := EvaluateWithSamples(parameter.Logic.ElseValue, variables, validationSamples(model.DataSamples)); err != nil {
					return fmt.Errorf("logic parameter %s has an invalid else value: %w", parameter.Key, err)
				}
			} else if resultType, referencesValue := logicResultReferenceType(parameter.Logic.ElseValue, model, valueTypes); referencesValue {
				if resultType != dataType {
					return fmt.Errorf("logic parameter %s else result references a %s value", parameter.Key, resultType)
				}
			} else if dataType == "boolean" {
				if _, err := booleanValue(parameter.Logic.ElseValue); err != nil {
					return fmt.Errorf("logic parameter %s else result must be true, false, or a boolean parameter", parameter.Key)
				}
			}
		}
		if parameter.Source == "lookup" {
			if err := validateLookup(model, parameter, valueTypes); err != nil {
				return err
			}
		}
		seen[parameter.Key] = true
	}
	for _, sample := range model.DataSamples {
		if sample.Key == "" || (sample.Source != "sql" && sample.Source != "static") {
			return errors.New("every data sample needs a key and valid source")
		}
		if err := registerName(sample.Key, "data sample"); err != nil {
			return err
		}
		if sample.Source == "sql" {
			if sample.DataSourceID == "" {
				return fmt.Errorf("data sample %s needs a configured data source", sample.Key)
			}
			if strings.TrimSpace(sample.Query) == "" {
				return fmt.Errorf("data sample %s needs a SQL query", sample.Key)
			}
			if err := validateDataSampleColumns(sample); err != nil {
				return err
			}
		} else {
			if err := validateStaticDataSample(sample); err != nil {
				return err
			}
		}
		if sample.Limit < 1 {
			return fmt.Errorf("data sample %s needs a positive record limit", sample.Key)
		}
	}
	outputSeen := map[string]bool{}
	for _, output := range model.Outputs {
		if !seen[output] {
			return fmt.Errorf("output references unknown parameter: %s", output)
		}
		if outputSeen[output] {
			return fmt.Errorf("duplicate output parameter: %s", output)
		}
		outputSeen[output] = true
	}
	return nil
}

func logicOperandType(source, name string, model Model, valueTypes map[string]string) (string, bool) {
	switch source {
	case "index":
		_, exists := model.Indexes[strings.TrimPrefix(strings.TrimSpace(name), "@")]
		return "string", exists
	case "external", "parameter":
		dataType, exists := valueTypes[strings.TrimPrefix(strings.TrimSpace(name), "@")]
		return dataType, exists
	case "data-sample":
		dataType, err := SampleExpressionType(name, model.DataSamples)
		return dataType, err == nil
	default:
		return "", false
	}
}

func logicResultReferenceType(expression string, model Model, valueTypes map[string]string) (string, bool) {
	reference := strings.TrimPrefix(strings.TrimSpace(expression), "@")
	if dataType, exists := valueTypes[reference]; exists {
		return dataType, true
	}
	if _, exists := model.Indexes[reference]; exists {
		return "string", true
	}
	dataType, err := SampleExpressionType(expression, model.DataSamples)
	return dataType, err == nil
}

func validateLookup(model Model, parameter ParameterDefinition, valueTypes map[string]string) error {
	if parameter.Lookup == nil {
		return fmt.Errorf("lookup parameter %s needs lookup settings", parameter.Key)
	}
	lookup := parameter.Lookup
	var sample *DataSampleDefinition
	for index := range model.DataSamples {
		if strings.EqualFold(model.DataSamples[index].Key, lookup.Sample) {
			sample = &model.DataSamples[index]
			break
		}
	}
	if sample == nil || len(sample.Columns) == 0 {
		return fmt.Errorf("lookup parameter %s needs a data sample with a declared schema", parameter.Key)
	}
	columnTypes := map[string]string{}
	for _, column := range sample.Columns {
		columnTypes[strings.ToLower(column.Name)] = column.Type
	}
	matchType, matchExists := columnTypes[strings.ToLower(lookup.MatchColumn)]
	returnType, returnExists := columnTypes[strings.ToLower(lookup.ReturnColumn)]
	if !matchExists || !returnExists || lookup.MatchValue == "" {
		return fmt.Errorf("lookup parameter %s has incomplete column or match settings", parameter.Key)
	}
	if returnType != parameterDataType(parameter) {
		return fmt.Errorf("lookup parameter %s return column must use %s type", parameter.Key, parameterDataType(parameter))
	}
	if lookup.Operator != "equals" && lookup.Operator != "contains" {
		return fmt.Errorf("lookup parameter %s has an invalid operator", parameter.Key)
	}
	if lookup.Operator == "contains" && matchType != "string" {
		return fmt.Errorf("lookup parameter %s contains operator requires a text match column", parameter.Key)
	}
	switch lookup.InputSource {
	case "constant":
		if matchType == "number" {
			if _, err := numberValue(lookup.MatchValue); err != nil {
				return fmt.Errorf("lookup parameter %s constant must be a number", parameter.Key)
			}
		} else if matchType == "boolean" {
			if _, err := booleanValue(lookup.MatchValue); err != nil {
				return fmt.Errorf("lookup parameter %s constant must be boolean", parameter.Key)
			}
		}
	case "parameter", "external":
		if valueTypes[lookup.MatchValue] != matchType {
			return fmt.Errorf("lookup parameter %s match input must use %s type", parameter.Key, matchType)
		}
	case "index":
		if _, exists := model.Indexes[lookup.MatchValue]; !exists || matchType != "string" {
			return fmt.Errorf("lookup parameter %s index match requires a text column and declared index", parameter.Key)
		}
	default:
		return fmt.Errorf("lookup parameter %s has an invalid input source", parameter.Key)
	}
	return nil
}

func validateStaticDataSample(sample DataSampleDefinition) error {
	if len(sample.Columns) == 0 {
		return fmt.Errorf("static data sample %s needs at least one column", sample.Key)
	}
	if len(sample.Rows) == 0 {
		return fmt.Errorf("static data sample %s needs at least one row", sample.Key)
	}
	if err := validateDataSampleColumns(sample); err != nil {
		return err
	}
	columns := map[string]string{}
	for _, column := range sample.Columns {
		columns[strings.ToLower(strings.TrimSpace(column.Name))] = column.Type
	}
	for rowIndex, row := range sample.Rows {
		if len(row) != len(columns) {
			return fmt.Errorf("static data sample %s row %d must contain every declared column", sample.Key, rowIndex+1)
		}
		for name, raw := range row {
			dataType, exists := columns[strings.ToLower(name)]
			if !exists {
				return fmt.Errorf("static data sample %s row %d contains unknown column %s", sample.Key, rowIndex+1, name)
			}
			if raw == nil {
				continue
			}
			switch dataType {
			case "number":
				if _, err := numberValue(raw); err != nil {
					return fmt.Errorf("static data sample %s row %d column %s must be a number", sample.Key, rowIndex+1, name)
				}
			case "boolean":
				if _, err := booleanValue(raw); err != nil {
					return fmt.Errorf("static data sample %s row %d column %s must be boolean", sample.Key, rowIndex+1, name)
				}
			case "string":
				if _, ok := raw.(string); !ok {
					return fmt.Errorf("static data sample %s row %d column %s must be text", sample.Key, rowIndex+1, name)
				}
			}
		}
	}
	return nil
}

func validateDataSampleColumns(sample DataSampleDefinition) error {
	seen := map[string]bool{}
	for _, column := range sample.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" || (column.Type != "string" && column.Type != "number" && column.Type != "boolean") {
			return fmt.Errorf("data sample %s has an invalid declared column", sample.Key)
		}
		normalized := strings.ToLower(name)
		if seen[normalized] {
			return fmt.Errorf("data sample %s has duplicate column %s", sample.Key, name)
		}
		seen[normalized] = true
	}
	return nil
}

func validateResolvedDataSampleRows(sample DataSampleDefinition, rows []map[string]interface{}) ([]map[string]interface{}, error) {
	if len(sample.Columns) == 0 {
		return rows, nil
	}
	validated := make([]map[string]interface{}, 0, len(rows))
	for rowIndex, row := range rows {
		next := make(map[string]interface{}, len(row))
		for key, value := range row {
			next[key] = value
		}
		for _, column := range sample.Columns {
			var raw interface{}
			found := false
			for key, value := range row {
				if strings.EqualFold(key, column.Name) {
					raw, found = value, true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("data sample %s row %d has no declared column %q", sample.Key, rowIndex+1, column.Name)
			}
			if raw == nil {
				next[column.Name] = nil
				continue
			}
			switch column.Type {
			case "number":
				value, err := numberValue(raw)
				if err != nil {
					return nil, fmt.Errorf("data sample %s row %d column %s must be a number", sample.Key, rowIndex+1, column.Name)
				}
				next[column.Name] = value
			case "boolean":
				value, err := booleanValue(raw)
				if err != nil {
					return nil, fmt.Errorf("data sample %s row %d column %s must be boolean", sample.Key, rowIndex+1, column.Name)
				}
				next[column.Name] = value
			case "string":
				if bytes, ok := raw.([]byte); ok {
					next[column.Name] = string(bytes)
				} else if value, ok := raw.(string); ok {
					next[column.Name] = value
				} else {
					return nil, fmt.Errorf("data sample %s row %d column %s must be text", sample.Key, rowIndex+1, column.Name)
				}
			}
		}
		validated = append(validated, next)
	}
	return validated, nil
}

func numericValues(values map[string]interface{}) map[string]float64 {
	numeric := map[string]float64{}
	for key, raw := range values {
		if value, err := numberValue(raw); err == nil {
			numeric[key] = value
		}
	}
	return numeric
}

func validationSamples(definitions []DataSampleDefinition) map[string][]map[string]interface{} {
	samples := map[string][]map[string]interface{}{}
	for _, definition := range definitions {
		samples[definition.Key] = []map[string]interface{}{
			{"__validation__": 1.0},
			{"__validation__": 1.0},
		}
	}
	return samples
}
