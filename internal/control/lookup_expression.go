package control

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

var lookupBooleanWord = regexp.MustCompile(`(?i)\s+(AND|OR)\s+`)
var lookupReference = regexp.MustCompile(`@([A-Za-z_][A-Za-z0-9_]*)`)

func parseLookupExpression(expression string) (*ast.CallExpr, string, error) {
	normalized := lookupBooleanWord.ReplaceAllStringFunc(expression, func(value string) string {
		if strings.EqualFold(strings.TrimSpace(value), "AND") {
			return " && "
		}
		return " || "
	})
	normalized = lookupReference.ReplaceAllString(normalized, "ref__$1")
	node, err := parser.ParseExpr(normalized)
	if err != nil {
		return nil, "", err
	}
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return nil, "", fmt.Errorf("lookup formula must call LOOKUP_ONE or LOOKUP_FIRST")
	}
	function, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil, "", fmt.Errorf("invalid lookup function")
	}
	name := strings.ToUpper(function.Name)
	if name != "LOOKUP_ONE" && name != "LOOKUP_FIRST" {
		return nil, "", fmt.Errorf("unsupported lookup function %s", function.Name)
	}
	if len(call.Args) != 3 {
		return nil, "", fmt.Errorf("%s requires sample, filter, and return column", name)
	}
	return call, name, nil
}

func IsLookupFormula(expression string) bool {
	upper := strings.ToUpper(strings.TrimSpace(expression))
	return strings.HasPrefix(upper, "LOOKUP_ONE(") || strings.HasPrefix(upper, "LOOKUP_FIRST(")
}

func LookupFormulaType(expression string, definitions []DataSampleDefinition) (string, error) {
	call, _, err := parseLookupExpression(expression)
	if err != nil {
		return "", err
	}
	sampleName, returnName, err := lookupNames(call)
	if err != nil {
		return "", err
	}
	definition := findSampleDefinition(sampleName, definitions)
	if definition == nil {
		return "", fmt.Errorf("unknown data sample %q", sampleName)
	}
	for _, column := range definition.Columns {
		if strings.EqualFold(column.Name, returnName) {
			return column.Type, nil
		}
	}
	return "", fmt.Errorf("data sample %s has no return column %q", sampleName, returnName)
}

func ValidateLookupFormula(expression, expectedResultType string, definitions []DataSampleDefinition, referenceTypes map[string]string, indexes map[string]IndexValues) error {
	call, _, err := parseLookupExpression(expression)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`(?i)^\s*LOOKUP_(?:ONE|FIRST)\s*\(\s*@`).MatchString(expression) {
		return fmt.Errorf("lookup data sample must use an @ reference")
	}
	sampleName, returnName, err := lookupNames(call)
	if err != nil {
		return err
	}
	definition := findSampleDefinition(sampleName, definitions)
	if definition == nil {
		return fmt.Errorf("unknown data sample %q", sampleName)
	}
	if len(definition.Columns) == 0 {
		return fmt.Errorf("data sample %s needs a declared schema for lookup", sampleName)
	}
	columnTypes := map[string]string{}
	for _, column := range definition.Columns {
		columnTypes[strings.ToLower(column.Name)] = column.Type
	}
	returnType, exists := columnTypes[strings.ToLower(returnName)]
	if !exists {
		return fmt.Errorf("data sample %s has no return column %q", sampleName, returnName)
	}
	if returnType != expectedResultType {
		return fmt.Errorf("lookup return column %s is %s, but parameter result is %s", returnName, returnType, expectedResultType)
	}
	if err := validateLookupFilter(call.Args[1], columnTypes, referenceTypes, indexes); err != nil {
		return fmt.Errorf("lookup filter: %w", err)
	}
	return nil
}

func validateLookupFilter(node ast.Expr, columns, references map[string]string, indexes map[string]IndexValues) error {
	if parenthesized, ok := node.(*ast.ParenExpr); ok {
		return validateLookupFilter(parenthesized.X, columns, references, indexes)
	}
	if call, ok := node.(*ast.CallExpr); ok {
		function, _ := call.Fun.(*ast.Ident)
		if function == nil || !strings.EqualFold(function.Name, "CONTAINS") || len(call.Args) != 2 {
			return fmt.Errorf("filter must use comparisons joined by AND or OR")
		}
		left, err := lookupValueType(call.Args[0], columns, references, indexes)
		if err != nil {
			return err
		}
		right, err := lookupValueType(call.Args[1], columns, references, indexes)
		if err != nil {
			return err
		}
		if left != "string" || right != "string" {
			return fmt.Errorf("CONTAINS requires text operands, got %s and %s", typeLabel(left), typeLabel(right))
		}
		return nil
	}
	binary, ok := node.(*ast.BinaryExpr)
	if !ok {
		return fmt.Errorf("filter must use comparisons joined by AND or OR")
	}
	if binary.Op == token.LAND || binary.Op == token.LOR {
		if err := validateLookupFilter(binary.X, columns, references, indexes); err != nil {
			return err
		}
		return validateLookupFilter(binary.Y, columns, references, indexes)
	}
	left, err := lookupValueType(binary.X, columns, references, indexes)
	if err != nil {
		return err
	}
	right, err := lookupValueType(binary.Y, columns, references, indexes)
	if err != nil {
		return err
	}
	switch binary.Op {
	case token.EQL, token.NEQ:
		if left != right {
			return fmt.Errorf("%s compares %s with %s; equality operands must have the same type", binary.Op, typeLabel(left), typeLabel(right))
		}
	case token.GTR, token.GEQ, token.LSS, token.LEQ:
		if left != "number" || right != "number" {
			return fmt.Errorf("operator %s requires numerical operands, got %s and %s", binary.Op, typeLabel(left), typeLabel(right))
		}
	default:
		return fmt.Errorf("unsupported comparison operator %s", binary.Op)
	}
	return nil
}

func lookupValueType(node ast.Expr, columns, references map[string]string, indexes map[string]IndexValues) (string, error) {
	if parenthesized, ok := node.(*ast.ParenExpr); ok {
		return lookupValueType(parenthesized.X, columns, references, indexes)
	}
	switch value := node.(type) {
	case *ast.Ident:
		if strings.HasPrefix(value.Name, "ref__") {
			name := strings.TrimPrefix(value.Name, "ref__")
			if dataType, exists := references[name]; exists {
				return dataType, nil
			}
			if _, exists := indexes[name]; exists {
				return "string", nil
			}
			return "", fmt.Errorf("unknown reference @%s", name)
		}
		if strings.EqualFold(value.Name, "true") || strings.EqualFold(value.Name, "false") {
			return "boolean", nil
		}
		if dataType, exists := columns[strings.ToLower(value.Name)]; exists {
			return dataType, nil
		}
		return "", fmt.Errorf("unknown column %q", value.Name)
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			return "string", nil
		}
		if value.Kind == token.INT || value.Kind == token.FLOAT {
			return "number", nil
		}
		return "", fmt.Errorf("unsupported literal %s", value.Value)
	default:
		return "", fmt.Errorf("unsupported filter operand")
	}
}

func typeLabel(dataType string) string {
	if dataType == "string" {
		return "text"
	}
	return dataType
}

func LookupFormulaDependencies(expression string) (string, []string, error) {
	call, _, err := parseLookupExpression(expression)
	if err != nil {
		return "", nil, err
	}
	sample, _, err := lookupNames(call)
	if err != nil {
		return "", nil, err
	}
	seen := map[string]bool{}
	ast.Inspect(call.Args[1], func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok && strings.HasPrefix(identifier.Name, "ref__") {
			seen[strings.TrimPrefix(identifier.Name, "ref__")] = true
		}
		return true
	})
	references := []string{}
	for reference := range seen {
		references = append(references, reference)
	}
	return sample, references, nil
}

func EvaluateLookupFormula(expression, resultType string, definitions []DataSampleDefinition, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (interface{}, error) {
	referenceTypes := map[string]string{}
	for name, value := range values {
		if dataType := runtimeLookupType(value); dataType != "" {
			referenceTypes[name] = dataType
		}
	}
	for name, value := range context {
		if dataType := runtimeLookupType(value); dataType != "" {
			referenceTypes[name] = dataType
		}
	}
	if err := ValidateLookupFormula(expression, resultType, definitions, referenceTypes, map[string]IndexValues{}); err != nil {
		return nil, err
	}
	call, mode, err := parseLookupExpression(expression)
	if err != nil {
		return nil, err
	}
	sampleName, returnName, err := lookupNames(call)
	if err != nil {
		return nil, err
	}
	definition := findSampleDefinition(sampleName, definitions)
	if definition == nil {
		return nil, fmt.Errorf("unknown data sample %q", sampleName)
	}
	returnType, err := LookupFormulaType(expression, definitions)
	if err != nil {
		return nil, err
	}
	if returnType != resultType {
		return nil, fmt.Errorf("lookup return column uses %s but parameter uses %s", returnType, resultType)
	}
	canonical := map[string]string{}
	for _, column := range definition.Columns {
		canonical[strings.ToLower(column.Name)] = column.Name
	}
	matches := []interface{}{}
	for _, row := range samples[definition.Key] {
		matched, evalErr := evalLookupFilter(call.Args[1], row, canonical, context, values)
		if evalErr != nil {
			return nil, evalErr
		}
		if matched {
			matches = append(matches, row[canonical[strings.ToLower(returnName)]])
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("lookup found no matching row in data sample %s", sampleName)
	}
	if mode == "LOOKUP_ONE" && len(matches) != 1 {
		return nil, fmt.Errorf("lookup found %d matching rows in data sample %s", len(matches), sampleName)
	}
	return matches[0], nil
}

func runtimeLookupType(value interface{}) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case string:
		return "string"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return "number"
	default:
		return ""
	}
}

func lookupNames(call *ast.CallExpr) (string, string, error) {
	sample, sampleOK := call.Args[0].(*ast.Ident)
	result, resultOK := call.Args[2].(*ast.Ident)
	if !sampleOK || !resultOK {
		return "", "", fmt.Errorf("lookup sample and return column must be names")
	}
	return strings.TrimPrefix(sample.Name, "ref__"), result.Name, nil
}

func findSampleDefinition(name string, definitions []DataSampleDefinition) *DataSampleDefinition {
	for index := range definitions {
		if strings.EqualFold(definitions[index].Key, name) {
			return &definitions[index]
		}
	}
	return nil
}

func evalLookupFilter(node ast.Expr, row map[string]interface{}, columns map[string]string, context, values map[string]interface{}) (bool, error) {
	binary, ok := node.(*ast.BinaryExpr)
	if !ok {
		call, callOK := node.(*ast.CallExpr)
		if callOK {
			function, _ := call.Fun.(*ast.Ident)
			if function != nil && strings.EqualFold(function.Name, "CONTAINS") && len(call.Args) == 2 {
				left, leftErr := lookupValue(call.Args[0], row, columns, context, values)
				right, rightErr := lookupValue(call.Args[1], row, columns, context, values)
				if leftErr != nil {
					return false, leftErr
				}
				if rightErr != nil {
					return false, rightErr
				}
				return strings.Contains(strings.ToLower(stringValue(left)), strings.ToLower(stringValue(right))), nil
			}
		}
		return false, fmt.Errorf("lookup filter must use comparisons joined by AND or OR")
	}
	if binary.Op == token.LAND || binary.Op == token.LOR {
		left, err := evalLookupFilter(binary.X, row, columns, context, values)
		if err != nil {
			return false, err
		}
		right, err := evalLookupFilter(binary.Y, row, columns, context, values)
		if err != nil {
			return false, err
		}
		if binary.Op == token.LAND {
			return left && right, nil
		}
		return left || right, nil
	}
	left, err := lookupValue(binary.X, row, columns, context, values)
	if err != nil {
		return false, err
	}
	right, err := lookupValue(binary.Y, row, columns, context, values)
	if err != nil {
		return false, err
	}
	switch binary.Op {
	case token.EQL:
		return compareLogicValue(left, "equals", right)
	case token.NEQ:
		return compareLogicValue(left, "not-equals", right)
	case token.GTR:
		return compareLogicValue(left, "greater-than", right)
	case token.GEQ:
		return compareLogicValue(left, "greater-or-equal", right)
	case token.LSS:
		return compareLogicValue(left, "less-than", right)
	case token.LEQ:
		return compareLogicValue(left, "less-or-equal", right)
	default:
		return false, fmt.Errorf("unsupported lookup comparison %s", binary.Op)
	}
}

func lookupValue(node ast.Expr, row map[string]interface{}, columns map[string]string, context, values map[string]interface{}) (interface{}, error) {
	switch value := node.(type) {
	case *ast.Ident:
		if strings.HasPrefix(value.Name, "ref__") {
			name := strings.TrimPrefix(value.Name, "ref__")
			if resolved, exists := values[name]; exists {
				return resolved, nil
			}
			if resolved, exists := context[name]; exists {
				return resolved, nil
			}
			return nil, fmt.Errorf("lookup reference %q is unavailable", name)
		}
		if strings.EqualFold(value.Name, "true") {
			return true, nil
		}
		if strings.EqualFold(value.Name, "false") {
			return false, nil
		}
		name, exists := columns[strings.ToLower(value.Name)]
		if !exists {
			return nil, fmt.Errorf("unknown lookup column %q", value.Name)
		}
		return row[name], nil
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			return strings.Trim(value.Value, `"`), nil
		}
		return numberValue(value.Value)
	default:
		return nil, fmt.Errorf("unsupported lookup filter value")
	}
}
