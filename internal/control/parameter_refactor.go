package control

import (
	"errors"
	"fmt"
	"strings"
)

// ParameterUsage identifies one model location that references a parameter.
type ParameterUsage struct {
	Section        string `json:"section"`
	Location       string `json:"location"`
	Detail         string `json:"detail"`
	ParameterIndex *int   `json:"parameterIndex,omitempty"`
}

// FindParameterUsages returns structured references without matching similarly
// prefixed names such as @temperature when searching for @temp.
func FindParameterUsages(model Model, name string) []ParameterUsage {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	usages := []ParameterUsage{}
	add := func(section, location, detail string, parameterIndex *int) {
		usages = append(usages, ParameterUsage{Section: section, Location: location, Detail: detail, ParameterIndex: parameterIndex})
	}
	inspectPredicate := func(predicate LogicPredicate, owner string, index, rule int, suffix string) {
		if strings.EqualFold(predicate.InputSource, "parameter") && equalName(predicate.InputParameter, name) {
			add("parameter", owner, fmt.Sprintf("Rule %d%s uses it as the comparison input.", rule+1, suffix), intPointer(index))
		}
		if strings.EqualFold(predicate.CompareSource, "parameter") && equalName(predicate.CompareValue, name) {
			add("parameter", owner, fmt.Sprintf("Rule %d%s compares against it.", rule+1, suffix), intPointer(index))
		}
	}
	for index, parameter := range model.Parameters {
		if containsReference(parameter.Definition, name) {
			add("parameter", parameter.Key, "Expression or SQL references @"+name+".", intPointer(index))
		}
		if parameter.Logic != nil {
			for rule, condition := range parameter.Logic.Conditions {
				inspectPredicate(LogicPredicate{InputParameter: condition.InputParameter, InputSource: condition.InputSource, Operator: condition.Operator, CompareValue: condition.CompareValue, CompareSource: condition.CompareSource}, parameter.Key, index, rule, "")
				for predicateIndex, predicate := range condition.And {
					inspectPredicate(predicate, parameter.Key, index, rule, fmt.Sprintf(", AND %d", predicateIndex+1))
				}
				for predicateIndex, predicate := range condition.Or {
					inspectPredicate(predicate, parameter.Key, index, rule, fmt.Sprintf(", OR %d", predicateIndex+1))
				}
				if containsReference(condition.ResultValue, name) {
					add("parameter", parameter.Key, fmt.Sprintf("Rule %d result references @%s.", rule+1, name), intPointer(index))
				}
			}
			if containsReference(parameter.Logic.ElseValue, name) {
				add("parameter", parameter.Key, "Else result references @"+name+".", intPointer(index))
			}
		}
		if parameter.Lookup != nil && strings.EqualFold(parameter.Lookup.InputSource, "parameter") && equalName(parameter.Lookup.MatchValue, name) {
			add("parameter", parameter.Key, "Lookup filter uses it as the comparison value.", intPointer(index))
		}
		if parameter.Adaptive != nil {
			adaptive := parameter.Adaptive
			if (adaptive.UpdateInput != nil && strings.EqualFold(adaptive.UpdateInput.Source, "parameter") && equalName(adaptive.UpdateInput.Name, name)) || equalName(adaptive.UpdateParameter, name) || equalName(adaptive.MeasurementParameter, name) {
				add("parameter", parameter.Key, "Adaptive update uses this parameter.", intPointer(index))
			}
			if containsReference(adaptive.UpdateFormula, name) {
				add("parameter", parameter.Key, "Adaptive update formula references @"+name+".", intPointer(index))
			}
		}
	}
	for _, sample := range model.DataSamples {
		if containsReference(sample.Query, name) {
			add("data-sample", sample.Key, "SQL query references @"+name+".", nil)
		}
	}
	for _, output := range model.Outputs {
		if equalName(output, name) {
			add("outputs", "Model outputs", "Returned to the calling application.", nil)
		}
	}
	for _, field := range model.ExecutionPolicy.PartitionBy {
		if equalName(field, name) {
			add("partition", "Run partition", "Used to partition unfinished runs and adaptive state.", nil)
		}
	}
	return usages
}

// RenameParameter updates recognized references and validates the complete
// resulting model before returning it.
func RenameParameter(model Model, oldName, newName string) (Model, []ParameterUsage, error) {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return model, nil, errors.New("old and new parameter names are required")
	}
	if !validIdentifier(newName) {
		return model, nil, errors.New("parameter names must begin with a letter or underscore and contain only letters, numbers, and underscores")
	}
	found := false
	for _, parameter := range model.Parameters {
		if equalName(parameter.Key, oldName) {
			found = true
		} else if equalName(parameter.Key, newName) {
			return model, nil, fmt.Errorf("parameter %s already exists", newName)
		}
	}
	if !found {
		return model, nil, fmt.Errorf("parameter %s does not exist", oldName)
	}
	usages := FindParameterUsages(model, oldName)
	for index := range model.Parameters {
		parameter := &model.Parameters[index]
		if equalName(parameter.Key, oldName) {
			parameter.Key = newName
		}
		parameter.Definition = renameReferences(parameter.Definition, oldName, newName)
		if parameter.Logic != nil {
			for conditionIndex := range parameter.Logic.Conditions {
				condition := &parameter.Logic.Conditions[conditionIndex]
				renamePredicateFields(&condition.InputParameter, condition.InputSource, &condition.CompareValue, condition.CompareSource, oldName, newName)
				condition.ResultValue = renameReferences(condition.ResultValue, oldName, newName)
				for predicateIndex := range condition.And {
					predicate := &condition.And[predicateIndex]
					renamePredicateFields(&predicate.InputParameter, predicate.InputSource, &predicate.CompareValue, predicate.CompareSource, oldName, newName)
				}
				for predicateIndex := range condition.Or {
					predicate := &condition.Or[predicateIndex]
					renamePredicateFields(&predicate.InputParameter, predicate.InputSource, &predicate.CompareValue, predicate.CompareSource, oldName, newName)
				}
			}
			parameter.Logic.ElseValue = renameReferences(parameter.Logic.ElseValue, oldName, newName)
		}
		if parameter.Lookup != nil && strings.EqualFold(parameter.Lookup.InputSource, "parameter") && equalName(parameter.Lookup.MatchValue, oldName) {
			parameter.Lookup.MatchValue = newName
		}
		if parameter.Adaptive != nil {
			adaptive := parameter.Adaptive
			if adaptive.UpdateInput != nil && strings.EqualFold(adaptive.UpdateInput.Source, "parameter") && equalName(adaptive.UpdateInput.Name, oldName) {
				adaptive.UpdateInput.Name = newName
			}
			if equalName(adaptive.UpdateParameter, oldName) {
				adaptive.UpdateParameter = newName
			}
			if equalName(adaptive.MeasurementParameter, oldName) {
				adaptive.MeasurementParameter = newName
			}
			adaptive.UpdateFormula = renameReferences(adaptive.UpdateFormula, oldName, newName)
		}
	}
	for index := range model.DataSamples {
		model.DataSamples[index].Query = renameReferences(model.DataSamples[index].Query, oldName, newName)
	}
	for index, output := range model.Outputs {
		if equalName(output, oldName) {
			model.Outputs[index] = newName
		}
	}
	for index, field := range model.ExecutionPolicy.PartitionBy {
		if equalName(field, oldName) {
			model.ExecutionPolicy.PartitionBy[index] = newName
		}
	}
	if err := ValidateModel(model); err != nil {
		return Model{}, usages, fmt.Errorf("renamed model is invalid: %w", err)
	}
	return model, usages, nil
}

func renamePredicateFields(input *string, inputSource string, compare *string, compareSource string, oldName, newName string) {
	if strings.EqualFold(inputSource, "parameter") && equalName(*input, oldName) {
		*input = newName
	}
	if strings.EqualFold(compareSource, "parameter") && equalName(*compare, oldName) {
		*compare = newName
	} else {
		*compare = renameReferences(*compare, oldName, newName)
	}
}

func containsReference(value, name string) bool {
	_, count := rewriteReferences(value, name, name)
	return count > 0
}

func renameReferences(value, oldName, newName string) string {
	result, _ := rewriteReferences(value, oldName, newName)
	return result
}

// rewriteReferences lexes @identifier tokens and skips quoted string literals.
func rewriteReferences(value, oldName, newName string) (string, int) {
	var result strings.Builder
	count := 0
	for index := 0; index < len(value); {
		if value[index] == '\'' || value[index] == '"' {
			quote := value[index]
			start := index
			index++
			for index < len(value) {
				if value[index] == '\\' {
					index += 2
					continue
				}
				if value[index] == quote {
					index++
					if index < len(value) && value[index] == quote {
						index++
						continue
					}
					break
				}
				index++
			}
			result.WriteString(value[start:index])
			continue
		}
		if value[index] == '@' && index+1 < len(value) && identifierStart(rune(value[index+1])) {
			end := index + 2
			for end < len(value) && identifierPart(rune(value[end])) {
				end++
			}
			identifier := value[index+1 : end]
			if equalName(identifier, oldName) {
				result.WriteByte('@')
				result.WriteString(newName)
				count++
			} else {
				result.WriteString(value[index:end])
			}
			index = end
			continue
		}
		result.WriteByte(value[index])
		index++
	}
	return result.String(), count
}

func validIdentifier(value string) bool {
	for index, character := range value {
		if index == 0 && !identifierStart(character) {
			return false
		}
		if index > 0 && !identifierPart(character) {
			return false
		}
	}
	return value != ""
}

func identifierStart(character rune) bool {
	return character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}
func identifierPart(character rune) bool {
	return identifierStart(character) || character >= '0' && character <= '9'
}
func equalName(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}
func intPointer(value int) *int { return &value }
