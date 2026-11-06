package control

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type DependencyNode struct {
	ID         string               `json:"id"`
	Key        string               `json:"key"`
	Kind       string               `json:"kind"`
	Properties []DependencyProperty `json:"properties"`
}

type DependencyProperty struct {
	Label string      `json:"label"`
	Value interface{} `json:"value"`
}

type DependencyEdge struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Reasons []string `json:"reasons,omitempty"`
}

type DependencyGraph struct {
	Nodes []DependencyNode `json:"nodes"`
	Edges []DependencyEdge `json:"edges"`
	Order []string         `json:"order"`
}

var namedSQLReference = regexp.MustCompile(`@[A-Za-z_][A-Za-z0-9_]*`)

func SQLReferences(query string) []string {
	seen := map[string]bool{}
	references := []string{}
	for _, location := range namedSQLReference.FindAllStringIndex(query, -1) {
		if location[0] > 0 && query[location[0]-1] == '@' {
			continue
		}
		name := query[location[0]+1 : location[1]]
		normalized := strings.ToLower(name)
		if !seen[normalized] {
			seen[normalized] = true
			references = append(references, name)
		}
	}
	return references
}

func BuildDependencyGraph(model Model) (DependencyGraph, error) {
	graph := DependencyGraph{
		Nodes: []DependencyNode{},
		Edges: []DependencyEdge{},
		Order: []string{},
	}
	parameterIDs := map[string]string{}
	sampleIDs := map[string]string{}
	externalIDs := map[string]string{}
	feedbackIDs := map[string]string{}
	indexIDs := map[string]string{}
	for key, values := range model.Indexes {
		indexIDs[strings.ToLower(key)] = "index:" + key
		graph.Nodes = append(graph.Nodes, DependencyNode{
			ID:   "index:" + key,
			Key:  key,
			Kind: "index",
			Properties: []DependencyProperty{
				{Label: "Accepted values", Value: []string(values)},
			},
		})
	}
	for _, input := range model.ExternalInputs {
		key := strings.TrimSpace(input.Name)
		normalized := strings.ToLower(key)
		if key == "" {
			return graph, fmt.Errorf("external input name is required")
		}
		if _, exists := externalIDs[normalized]; exists {
			return graph, fmt.Errorf("duplicate external input name: %s", key)
		}
		externalIDs[normalized] = "external:" + key
		properties := []DependencyProperty{{Label: "Type", Value: input.Type}}
		if input.Description != "" {
			properties = append(properties, DependencyProperty{Label: "Description", Value: input.Description})
		}
		graph.Nodes = append(graph.Nodes, DependencyNode{ID: "external:" + key, Key: key, Kind: "external", Properties: properties})
	}
	for _, input := range model.FeedbackInputs {
		key := strings.TrimSpace(input.Name)
		feedbackIDs[strings.ToLower(key)] = "feedback:" + key
		properties := []DependencyProperty{
			{Label: "Type", Value: input.Type},
			{Label: "Required", Value: input.Required},
		}
		if input.Description != "" {
			properties = append(properties, DependencyProperty{Label: "Description", Value: input.Description})
		}
		graph.Nodes = append(graph.Nodes, DependencyNode{
			ID: "feedback:" + key, Key: key, Kind: "feedback", Properties: properties,
		})
	}
	for _, parameter := range model.Parameters {
		key := strings.TrimSpace(parameter.Key)
		normalized := strings.ToLower(key)
		if key == "" {
			return graph, fmt.Errorf("parameter name is required")
		}
		if _, exists := parameterIDs[normalized]; exists {
			return graph, fmt.Errorf("duplicate parameter name: %s", key)
		}
		if _, exists := externalIDs[normalized]; exists {
			return graph, fmt.Errorf("name %q is used by both an external input and a parameter", key)
		}
		parameterIDs[normalized] = "parameter:" + key
		properties := []DependencyProperty{
			{Label: "Source", Value: parameter.Source},
			{Label: "Data type", Value: parameterDataType(parameter)},
		}
		if parameter.Description != "" {
			properties = append(properties, DependencyProperty{Label: "Description", Value: parameter.Description})
		}
		if parameter.Definition != "" {
			properties = append(properties, DependencyProperty{Label: "Definition", Value: parameter.Definition})
		}
		if parameter.Unit != "" {
			properties = append(properties, DependencyProperty{Label: "Unit", Value: parameter.Unit})
		}
		if parameter.Adaptive != nil {
			updateInput := adaptiveUpdateInput(parameter.Adaptive)
			properties = append(properties,
				DependencyProperty{Label: "Update method", Value: parameter.Adaptive.UpdateMethod},
				DependencyProperty{Label: "Update input", Value: updateInput.Source + "." + updateInput.Name},
			)
		}
		if parameter.Source == "logic" && parameter.Logic != nil {
			properties = append(properties,
				DependencyProperty{Label: "Conditions", Value: len(parameter.Logic.Conditions)},
				DependencyProperty{Label: "Logic rules", Value: formatLogicRules(*parameter.Logic)},
			)
		}
		if parameter.Source == "lookup" && parameter.Lookup != nil {
			properties = append(properties,
				DependencyProperty{Label: "Sample", Value: parameter.Lookup.Sample},
				DependencyProperty{Label: "Match", Value: parameter.Lookup.MatchColumn + " " + parameter.Lookup.Operator},
				DependencyProperty{Label: "Return column", Value: parameter.Lookup.ReturnColumn},
			)
		}
		graph.Nodes = append(graph.Nodes, DependencyNode{ID: "parameter:" + key, Key: key, Kind: "parameter", Properties: properties})
	}
	for _, sample := range model.DataSamples {
		key := strings.TrimSpace(sample.Key)
		normalized := strings.ToLower(key)
		if key == "" {
			return graph, fmt.Errorf("data sample name is required")
		}
		if _, exists := sampleIDs[normalized]; exists {
			return graph, fmt.Errorf("duplicate data sample name: %s", key)
		}
		if _, exists := parameterIDs[normalized]; exists {
			return graph, fmt.Errorf("name %q is used by both a parameter and a data sample", key)
		}
		sampleIDs[normalized] = "sample:" + key
		properties := []DependencyProperty{
			{Label: "Source", Value: sample.Source},
			{Label: "Record limit", Value: sample.Limit},
		}
		if sample.Source == "static" {
			properties = append(properties,
				DependencyProperty{Label: "Rows", Value: len(sample.Rows)},
			)
		} else {
			properties = append(properties,
				DependencyProperty{Label: "Data source", Value: sample.DataSourceID},
				DependencyProperty{Label: "Query", Value: sample.Query},
			)
		}
		if len(sample.Columns) > 0 {
			label := "Columns"
			if sample.Source != "static" {
				label = "Expected columns"
			}
			columns := make([]string, 0, len(sample.Columns))
			for _, column := range sample.Columns {
				columns = append(columns, fmt.Sprintf("%s (%s)", column.Name, typeLabel(column.Type)))
			}
			properties = append(properties, DependencyProperty{Label: label, Value: columns})
		}
		if sample.Description != "" {
			properties = append(properties, DependencyProperty{Label: "Description", Value: sample.Description})
		}
		graph.Nodes = append(graph.Nodes, DependencyNode{ID: "sample:" + key, Key: key, Kind: "sample", Properties: properties})
	}

	dependencies := map[string][]string{}
	declaredTypes := map[string]string{}
	for _, input := range model.ExternalInputs {
		declaredTypes[input.Name] = input.Type
	}
	for _, parameter := range model.Parameters {
		declaredTypes[parameter.Key] = parameterDataType(parameter)
	}
	edgeReasons := map[string][]string{}
	addReason := func(from, to, reason string) {
		key := from + "\x00" + to
		edgeReasons[key] = appendUnique(edgeReasons[key], reason)
	}
	addParameter := func(target, reference string) error {
		reference = strings.TrimPrefix(strings.TrimSpace(reference), "@")
		id, exists := parameterIDs[strings.ToLower(reference)]
		if exists {
			dependencies[target] = appendUnique(dependencies[target], id)
			return nil
		}
		id, exists = externalIDs[strings.ToLower(reference)]
		if exists {
			dependencies[target] = appendUnique(dependencies[target], id)
			return nil
		}
		return fmt.Errorf("%s references unknown parameter or external input %q", displayNode(target), reference)
	}
	addSQLReference := func(target, reference, reason string) error {
		if id, exists := indexIDs[strings.ToLower(reference)]; exists {
			dependencies[target] = appendUnique(dependencies[target], id)
			addReason(id, target, reason+" @"+reference)
			return nil
		}
		if err := addParameter(target, reference); err != nil {
			return err
		}
		if id := parameterIDs[strings.ToLower(reference)]; id != "" {
			addReason(id, target, reason+" @"+reference)
		} else if id := externalIDs[strings.ToLower(reference)]; id != "" {
			addReason(id, target, reason+" @"+reference)
		}
		return nil
	}
	addSample := func(target, reference string) error {
		id, exists := sampleIDs[strings.ToLower(reference)]
		if !exists {
			return fmt.Errorf("%s references unknown data sample %q", displayNode(target), reference)
		}
		dependencies[target] = appendUnique(dependencies[target], id)
		return nil
	}
	addExpression := func(target, expression string) error {
		parameters, samples, err := ExpressionDependencies(expression)
		if err != nil {
			return fmt.Errorf("%s has an invalid expression: %w", displayNode(target), err)
		}
		for _, reference := range parameters {
			if err := addParameter(target, reference); err != nil {
				return err
			}
		}
		for _, reference := range samples {
			if err := addSample(target, reference); err != nil {
				return err
			}
		}
		return nil
	}
	addLogicOperand := func(target, source, value string, legacyParameter bool) error {
		if source == "" {
			if !legacyParameter {
				return nil
			}
			source = "parameter"
		}
		switch source {
		case "constant":
			return nil
		case "index":
			value = strings.TrimPrefix(strings.TrimSpace(value), "@")
			id, exists := indexIDs[strings.ToLower(value)]
			if !exists {
				return fmt.Errorf("%s references unknown index %q", displayNode(target), value)
			}
			dependencies[target] = appendUnique(dependencies[target], id)
			return nil
		case "data-sample":
			return addExpression(target, value)
		default:
			return addParameter(target, value)
		}
	}
	addLogicResult := func(target, expression string) error {
		reference := strings.TrimPrefix(strings.TrimSpace(expression), "@")
		if id, exists := indexIDs[strings.ToLower(reference)]; exists {
			dependencies[target] = appendUnique(dependencies[target], id)
			return nil
		}
		if _, exists := parameterIDs[strings.ToLower(reference)]; exists {
			return addParameter(target, reference)
		}
		if _, exists := externalIDs[strings.ToLower(reference)]; exists {
			return addParameter(target, reference)
		}
		if strings.HasPrefix(strings.TrimSpace(expression), "@") && strings.Contains(expression, "(") {
			return addExpression(target, expression)
		}
		return nil
	}

	for _, parameter := range model.Parameters {
		target := parameterIDs[strings.ToLower(parameter.Key)]
		switch parameter.Source {
		case "expression":
			references, sampleReferences, expressionErr := TypedExpressionDependencies(parameter.Definition)
			if expressionErr != nil {
				return graph, fmt.Errorf("invalid definition for %s: %w", parameter.Key, expressionErr)
			}
			for _, reference := range references {
				if err := addParameter(target, reference); err != nil {
					if id, exists := indexIDs[strings.ToLower(reference)]; exists {
						dependencies[target] = appendUnique(dependencies[target], id)
						addReason(id, target, "Expression reference @"+reference)
					} else {
						return graph, err
					}
				} else if id := parameterIDs[strings.ToLower(reference)]; id != "" {
					addReason(id, target, "Expression reference @"+reference)
				} else if id := externalIDs[strings.ToLower(reference)]; id != "" {
					addReason(id, target, "Expression reference @"+reference)
				}
			}
			for _, sample := range sampleReferences {
				if err := addSample(target, sample); err != nil {
					return graph, err
				}
				addReason(sampleIDs[strings.ToLower(sample)], target, "Expression data sample @"+sample)
			}
		case "sql":
			for _, reference := range SQLReferences(parameter.Definition) {
				if err := addSQLReference(target, reference, "SQL query parameter"); err != nil {
					return graph, err
				}
			}
		case "constant", "formula", "data-sample":
			if parameter.Source == "formula" && IsLookupFormula(parameter.Definition) {
				if lookupErr := ValidateLookupFormula(parameter.Definition, parameterDataType(parameter), model.DataSamples, declaredTypes, model.Indexes); lookupErr != nil {
					return graph, fmt.Errorf("invalid definition for %s: %w", parameter.Key, lookupErr)
				}
				sample, references, lookupErr := LookupFormulaDependencies(parameter.Definition)
				if lookupErr != nil {
					return graph, lookupErr
				}
				if err := addSample(target, sample); err != nil {
					return graph, err
				}
				for _, reference := range references {
					if err := addParameter(target, reference); err != nil {
						if id, exists := indexIDs[strings.ToLower(reference)]; exists {
							dependencies[target] = appendUnique(dependencies[target], id)
						} else {
							return graph, err
						}
					}
				}
			} else if parameterDataType(parameter) == "number" || parameter.Source == "data-sample" {
				if err := addExpression(target, parameter.Definition); err != nil {
					return graph, err
				}
			}
		case "logic":
			if parameter.Logic != nil {
				for conditionIndex, condition := range parameter.Logic.Conditions {
					predicates := append([]LogicPredicate{{
						InputParameter: condition.InputParameter,
						InputSource:    condition.InputSource,
						Operator:       condition.Operator,
						CompareValue:   condition.CompareValue,
						CompareSource:  condition.CompareSource,
					}}, condition.And...)
					predicates = append(predicates, condition.Or...)
					for predicateIndex, predicate := range predicates {
						clause := "IF"
						if predicateIndex > 0 && predicateIndex <= len(condition.And) {
							clause = "AND"
						} else if predicateIndex > len(condition.And) {
							clause = "OR"
						}
						if err := addLogicOperand(target, predicate.InputSource, predicate.InputParameter, true); err != nil {
							return graph, err
						}
						recordLogicOperandReasons(addReason, target, predicate.InputSource, predicate.InputParameter, fmt.Sprintf("Condition %d %s input", conditionIndex+1, clause), parameterIDs, externalIDs, indexIDs, sampleIDs)
						if err := addLogicOperand(target, predicate.CompareSource, predicate.CompareValue, false); err != nil {
							return graph, err
						}
						recordLogicOperandReasons(addReason, target, predicate.CompareSource, predicate.CompareValue, fmt.Sprintf("Condition %d %s comparison", conditionIndex+1, clause), parameterIDs, externalIDs, indexIDs, sampleIDs)
					}
					if parameterDataType(parameter) == "number" {
						if err := addExpression(target, condition.ResultValue); err != nil {
							return graph, err
						}
					} else {
						if err := addLogicResult(target, condition.ResultValue); err != nil {
							return graph, err
						}
					}
					recordLogicResultReasons(addReason, target, condition.ResultValue, fmt.Sprintf("Condition %d result", conditionIndex+1), parameterIDs, externalIDs, indexIDs, sampleIDs)
				}
				if parameterDataType(parameter) == "number" {
					if err := addExpression(target, parameter.Logic.ElseValue); err != nil {
						return graph, err
					}
				} else {
					if err := addLogicResult(target, parameter.Logic.ElseValue); err != nil {
						return graph, err
					}
				}
				recordLogicResultReasons(addReason, target, parameter.Logic.ElseValue, "Else result", parameterIDs, externalIDs, indexIDs, sampleIDs)
			}
		case "lookup":
			if parameter.Lookup != nil {
				if err := addSample(target, parameter.Lookup.Sample); err != nil {
					return graph, err
				}
				if parameter.Lookup.InputSource == "parameter" || parameter.Lookup.InputSource == "external" {
					if err := addParameter(target, parameter.Lookup.MatchValue); err != nil {
						return graph, err
					}
				} else if parameter.Lookup.InputSource == "index" {
					id, exists := indexIDs[strings.ToLower(parameter.Lookup.MatchValue)]
					if !exists {
						return graph, fmt.Errorf("%s references unknown index %q", displayNode(target), parameter.Lookup.MatchValue)
					}
					dependencies[target] = appendUnique(dependencies[target], id)
				}
			}
		}
		if parameter.Adaptive != nil {
			updateInput := adaptiveUpdateInput(parameter.Adaptive)
			if updateInput.Source == "post-run-feedback" {
				id, exists := feedbackIDs[strings.ToLower(updateInput.Name)]
				if !exists {
					return graph, fmt.Errorf("%s references unknown post-run feedback %q", displayNode(target), updateInput.Name)
				}
				dependencies[target] = appendUnique(dependencies[target], id)
				addReason(id, target, "Adaptive update input")
			} else if err := addParameter(target, updateInput.Name); err != nil {
				return graph, err
			} else if id := parameterIDs[strings.ToLower(updateInput.Name)]; id != "" {
				addReason(id, target, "Adaptive update input")
			} else if id := externalIDs[strings.ToLower(updateInput.Name)]; id != "" {
				addReason(id, target, "Adaptive update input")
			}
		}
	}
	for _, sample := range model.DataSamples {
		target := sampleIDs[strings.ToLower(sample.Key)]
		if sample.Source != "sql" {
			continue
		}
		for _, reference := range SQLReferences(sample.Query) {
			if err := addSQLReference(target, reference, "Data sample SQL parameter"); err != nil {
				return graph, err
			}
		}
	}

	for target, required := range dependencies {
		for _, dependency := range required {
			graph.Edges = append(graph.Edges, DependencyEdge{From: dependency, To: target, Reasons: edgeReasons[dependency+"\x00"+target]})
		}
	}
	sort.Slice(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].From == graph.Edges[j].From {
			return graph.Edges[i].To < graph.Edges[j].To
		}
		return graph.Edges[i].From < graph.Edges[j].From
	})
	order, err := topologicalOrder(graph.Nodes, dependencies)
	if err != nil {
		return graph, err
	}
	graph.Order = order
	return graph, nil
}

func formatLogicRules(logic LogicDefinition) []string {
	rules := []string{}
	for index, condition := range logic.Conditions {
		prefix := "IF"
		if index > 0 {
			prefix = "ELSE IF"
		}
		rules = append(rules, fmt.Sprintf("%s %s %s %s", prefix, condition.InputParameter, condition.Operator, condition.CompareValue))
		for _, predicate := range condition.And {
			rules = append(rules, fmt.Sprintf("AND %s %s %s", predicate.InputParameter, predicate.Operator, predicate.CompareValue))
		}
		for _, predicate := range condition.Or {
			rules = append(rules, fmt.Sprintf("OR %s %s %s", predicate.InputParameter, predicate.Operator, predicate.CompareValue))
		}
		rules = append(rules, "THEN "+condition.ResultValue)
	}
	return append(rules, "ELSE "+logic.ElseValue)
}

func recordLogicOperandReasons(add func(string, string, string), target, source, value, reason string, parameters, externals, indexes, samples map[string]string) {
	if source == "" {
		source = "parameter"
	}
	reference := strings.TrimPrefix(strings.TrimSpace(value), "@")
	switch source {
	case "constant":
		return
	case "index":
		if id := indexes[strings.ToLower(reference)]; id != "" {
			add(id, target, reason)
		}
	case "data-sample":
		parameterRefs, sampleRefs, err := ExpressionDependencies(value)
		if err != nil {
			return
		}
		for _, item := range parameterRefs {
			if id := parameters[strings.ToLower(item)]; id != "" {
				add(id, target, reason)
			} else if id := externals[strings.ToLower(item)]; id != "" {
				add(id, target, reason)
			}
		}
		for _, item := range sampleRefs {
			if id := samples[strings.ToLower(item)]; id != "" {
				add(id, target, reason)
			}
		}
	default:
		if id := parameters[strings.ToLower(reference)]; id != "" {
			add(id, target, reason)
		} else if id := externals[strings.ToLower(reference)]; id != "" {
			add(id, target, reason)
		}
	}
}

func recordLogicResultReasons(add func(string, string, string), target, expression, reason string, parameters, externals, indexes, samples map[string]string) {
	reference := strings.TrimPrefix(strings.TrimSpace(expression), "@")
	if id := parameters[strings.ToLower(reference)]; id != "" {
		add(id, target, reason)
		return
	}
	if id := externals[strings.ToLower(reference)]; id != "" {
		add(id, target, reason)
		return
	}
	if id := indexes[strings.ToLower(reference)]; id != "" {
		add(id, target, reason)
		return
	}
	parameterRefs, sampleRefs, err := ExpressionDependencies(expression)
	if err != nil {
		return
	}
	for _, item := range parameterRefs {
		if id := parameters[strings.ToLower(item)]; id != "" {
			add(id, target, reason)
		} else if id := externals[strings.ToLower(item)]; id != "" {
			add(id, target, reason)
		}
	}
	for _, item := range sampleRefs {
		if id := samples[strings.ToLower(item)]; id != "" {
			add(id, target, reason)
		}
	}
}

func topologicalOrder(nodes []DependencyNode, dependencies map[string][]string) ([]string, error) {
	state := map[string]int{}
	order := []string{}
	stack := []string{}
	var visit func(string) error
	visit = func(node string) error {
		if state[node] == 2 {
			return nil
		}
		if state[node] == 1 {
			start := 0
			for index, item := range stack {
				if item == node {
					start = index
					break
				}
			}
			path := append(append([]string{}, stack[start:]...), node)
			for index := range path {
				path[index] = displayNode(path[index])
			}
			return fmt.Errorf("circular dependency detected: %s", strings.Join(path, " -> "))
		}
		state[node] = 1
		stack = append(stack, node)
		for _, dependency := range dependencies[node] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = 2
		order = append(order, node)
		return nil
	}
	for _, node := range nodes {
		if err := visit(node.ID); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func displayNode(id string) string {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		return id
	}
	if parts[0] == "sample" {
		return fmt.Sprintf("data sample %q", parts[1])
	}
	return fmt.Sprintf("parameter %q", parts[1])
}
