package control

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"regexp"
	"sort"
	"strings"
)

var formulaParameterReference = regexp.MustCompile(`@([A-Za-z_][A-Za-z0-9_]*)`)

func normalizeFormulaReferences(expression string) string {
	return formulaParameterReference.ReplaceAllString(expression, "$1")
}

func ExpressionDependencies(expression string) (parameters []string, samples []string, err error) {
	node, err := parser.ParseExpr(normalizeFormulaReferences(expression))
	if err != nil {
		return nil, nil, err
	}
	parameterSet := map[string]bool{}
	sampleSet := map[string]bool{}
	selectorIdentifiers := map[string]bool{}
	ast.Inspect(node, func(current ast.Node) bool {
		switch value := current.(type) {
		case *ast.CallExpr:
			if selector, ok := selectorArgument(value); ok {
				if sample, ok := selector.X.(*ast.Ident); ok {
					sampleSet[sample.Name] = true
				}
			}
		case *ast.Ident:
			parameterSet[value.Name] = true
		case *ast.SelectorExpr:
			if sample, ok := value.X.(*ast.Ident); ok {
				selectorIdentifiers[sample.Name] = true
				selectorIdentifiers[value.Sel.Name] = true
			}
		}
		return true
	})
	for _, function := range []string{"min", "max", "abs", "avg", "median", "range", "stdev", "sum", "count", "first", "nth", "last", "ceil", "cos", "exp", "floor", "log", "pow", "round", "sin", "sqrt", "tan"} {
		delete(parameterSet, function)
		delete(parameterSet, strings.ToUpper(function))
	}
	for name := range selectorIdentifiers {
		delete(parameterSet, name)
	}
	for name := range parameterSet {
		parameters = append(parameters, name)
	}
	for name := range sampleSet {
		samples = append(samples, name)
	}
	sort.Strings(parameters)
	sort.Strings(samples)
	return parameters, samples, nil
}

func firstSelector(call *ast.CallExpr) (*ast.SelectorExpr, bool) {
	if len(call.Args) != 1 {
		return nil, false
	}
	return selectorArgument(call)
}

func selectorArgument(call *ast.CallExpr) (*ast.SelectorExpr, bool) {
	if len(call.Args) == 0 {
		return nil, false
	}
	selector, ok := call.Args[0].(*ast.SelectorExpr)
	return selector, ok
}

func Evaluate(expression string, variables map[string]float64) (float64, error) {
	return EvaluateWithSamples(expression, variables, nil)
}

func EvaluateWithSamples(expression string, variables map[string]float64, samples map[string][]map[string]interface{}) (float64, error) {
	node, err := parser.ParseExpr(normalizeFormulaReferences(expression))
	if err != nil {
		return 0, err
	}
	return evalNode(node, variables, samples)
}

func EvaluateSampleValue(expression string, samples map[string][]map[string]interface{}) (interface{}, error) {
	node, err := parser.ParseExpr(normalizeFormulaReferences(expression))
	if err != nil {
		return nil, err
	}
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return nil, fmt.Errorf("data sample value must use FIRST(sample.column), NTH(sample.column, row), or LAST(sample.column)")
	}
	function, ok := call.Fun.(*ast.Ident)
	name := ""
	if ok {
		name = strings.ToLower(function.Name)
	}
	if !ok || (name != "first" && name != "nth" && name != "last") {
		return nil, fmt.Errorf("data sample value must use FIRST(sample.column), NTH(sample.column, row), or LAST(sample.column)")
	}
	selector, ok := selectorArgument(call)
	if !ok {
		return nil, fmt.Errorf("%s requires a sample.column argument", strings.ToUpper(function.Name))
	}
	sampleID, ok := selector.X.(*ast.Ident)
	if !ok {
		return nil, fmt.Errorf("data sample must use sample.column format")
	}
	rows, exists := samples[sampleID.Name]
	if !exists {
		return nil, fmt.Errorf("unknown data sample %q", sampleID.Name)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("data sample %s has no rows", sampleID.Name)
	}
	index, err := positionalIndex(name, call, len(rows))
	if err != nil {
		if _, validating := rows[0]["__validation__"]; validating && name == "nth" {
			return "VALUE", nil
		}
		return nil, fmt.Errorf("data sample %s: %w", sampleID.Name, err)
	}
	row := rows[index]
	value, exists := row[selector.Sel.Name]
	if !exists {
		if _, validating := row["__validation__"]; validating {
			return "VALUE", nil
		}
		return nil, fmt.Errorf("data sample %s has no column %q", sampleID.Name, selector.Sel.Name)
	}
	return value, nil
}

func SampleExpressionType(expression string, definitions []DataSampleDefinition) (string, error) {
	node, err := parser.ParseExpr(normalizeFormulaReferences(expression))
	if err != nil {
		return "", err
	}
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return "", fmt.Errorf("data sample expression must call a supported function")
	}
	function, ok := call.Fun.(*ast.Ident)
	if !ok {
		return "", fmt.Errorf("invalid data sample function")
	}
	selector, ok := selectorArgument(call)
	if !ok {
		return "", fmt.Errorf("data sample expression requires sample.column")
	}
	sampleID, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", fmt.Errorf("data sample must use sample.column format")
	}
	var definition *DataSampleDefinition
	for index := range definitions {
		if strings.EqualFold(definitions[index].Key, sampleID.Name) {
			definition = &definitions[index]
			break
		}
	}
	if definition == nil {
		return "", fmt.Errorf("unknown data sample %q", sampleID.Name)
	}
	columnType := "number"
	if len(definition.Columns) > 0 {
		columnType = ""
		for _, column := range definition.Columns {
			if strings.EqualFold(column.Name, selector.Sel.Name) {
				columnType = column.Type
				break
			}
		}
		if columnType == "" {
			return "", fmt.Errorf("data sample %s has no declared column %q", definition.Key, selector.Sel.Name)
		}
	}
	name := strings.ToLower(function.Name)
	switch name {
	case "first", "nth", "last":
		return columnType, nil
	case "contains":
		if columnType != "string" {
			return "", fmt.Errorf("CONTAINS requires a text column")
		}
		return "boolean", nil
	case "count":
		return "number", nil
	case "min", "max", "avg", "median", "range", "stdev", "sum":
		if columnType != "number" {
			return "", fmt.Errorf("%s requires a numerical column", strings.ToUpper(name))
		}
		return "number", nil
	default:
		return "", fmt.Errorf("function %q is not a supported data sample expression", function.Name)
	}
}
func evalNode(node ast.Expr, vars map[string]float64, samples map[string][]map[string]interface{}) (float64, error) {
	switch n := node.(type) {
	case *ast.BasicLit:
		if n.Kind != token.INT && n.Kind != token.FLOAT {
			return 0, fmt.Errorf("only numeric literals are allowed")
		}
		var value float64
		_, err := fmt.Sscan(n.Value, &value)
		return value, err
	case *ast.Ident:
		value, ok := vars[n.Name]
		if !ok {
			return 0, fmt.Errorf("unknown variable %q", n.Name)
		}
		return value, nil
	case *ast.ParenExpr:
		return evalNode(n.X, vars, samples)
	case *ast.UnaryExpr:
		value, err := evalNode(n.X, vars, samples)
		if err != nil {
			return 0, err
		}
		if n.Op == token.SUB {
			return -value, nil
		}
		if n.Op == token.ADD {
			return value, nil
		}
		return 0, fmt.Errorf("operator %s is not allowed", n.Op)
	case *ast.BinaryExpr:
		left, err := evalNode(n.X, vars, samples)
		if err != nil {
			return 0, err
		}
		right, err := evalNode(n.Y, vars, samples)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		}
		return 0, fmt.Errorf("operator %s is not allowed", n.Op)
	case *ast.CallExpr:
		id, ok := n.Fun.(*ast.Ident)
		if !ok {
			return 0, fmt.Errorf("invalid function")
		}
		name := strings.ToLower(id.Name)
		if name == "nth" {
			selector, ok := selectorArgument(n)
			if !ok {
				return 0, fmt.Errorf("NTH requires sample.column and row arguments")
			}
			value, err := nthSampleValue(selector, n, samples)
			if err != nil {
				return 0, err
			}
			number, err := numberValue(value)
			if err != nil {
				return 0, fmt.Errorf("NTH data sample value is non-numeric")
			}
			return number, nil
		}
		if len(n.Args) == 1 {
			if selector, ok := n.Args[0].(*ast.SelectorExpr); ok {
				return aggregateSample(name, selector, samples)
			}
		}
		args := []float64{}
		for _, arg := range n.Args {
			value, err := evalNode(arg, vars, samples)
			if err != nil {
				return 0, err
			}
			args = append(args, value)
		}
		switch name {
		case "min":
			if len(args) != 2 {
				return 0, fmt.Errorf("min requires two arguments")
			}
			return math.Min(args[0], args[1]), nil
		case "max":
			if len(args) != 2 {
				return 0, fmt.Errorf("max requires two arguments")
			}
			return math.Max(args[0], args[1]), nil
		case "abs":
			if len(args) != 1 {
				return 0, fmt.Errorf("abs requires one argument")
			}
			return math.Abs(args[0]), nil
		case "ceil", "cos", "exp", "floor", "log", "round", "sin", "sqrt", "tan":
			if len(args) != 1 {
				return 0, fmt.Errorf("%s requires one argument", name)
			}
			switch name {
			case "ceil":
				return math.Ceil(args[0]), nil
			case "cos":
				return math.Cos(args[0]), nil
			case "exp":
				return math.Exp(args[0]), nil
			case "floor":
				return math.Floor(args[0]), nil
			case "log":
				return math.Log(args[0]), nil
			case "round":
				return math.Round(args[0]), nil
			case "sin":
				return math.Sin(args[0]), nil
			case "sqrt":
				return math.Sqrt(args[0]), nil
			default:
				return math.Tan(args[0]), nil
			}
		case "pow":
			if len(args) != 2 {
				return 0, fmt.Errorf("pow requires two arguments")
			}
			return math.Pow(args[0], args[1]), nil
		}
		return 0, fmt.Errorf("function %q is not allowed", id.Name)
	}
	return 0, fmt.Errorf("expression element is not allowed")
}

func positionalIndex(function string, call *ast.CallExpr, rowCount int) (int, error) {
	switch function {
	case "first":
		if len(call.Args) != 1 {
			return 0, fmt.Errorf("FIRST requires one sample.column argument")
		}
		return 0, nil
	case "last":
		if len(call.Args) != 1 {
			return 0, fmt.Errorf("LAST requires one sample.column argument")
		}
		return rowCount - 1, nil
	case "nth":
		if len(call.Args) != 2 {
			return 0, fmt.Errorf("NTH requires sample.column and row arguments")
		}
		literal, ok := call.Args[1].(*ast.BasicLit)
		if !ok || literal.Kind != token.INT {
			return 0, fmt.Errorf("NTH row must be a positive integer")
		}
		var position int
		if _, err := fmt.Sscan(literal.Value, &position); err != nil || position < 1 {
			return 0, fmt.Errorf("NTH row must be a positive integer")
		}
		if position > rowCount {
			return 0, fmt.Errorf("row %d is unavailable; sample has %d rows", position, rowCount)
		}
		return position - 1, nil
	default:
		return 0, fmt.Errorf("unsupported positional function %q", function)
	}
}

func nthSampleValue(selector *ast.SelectorExpr, call *ast.CallExpr, samples map[string][]map[string]interface{}) (interface{}, error) {
	sampleID, ok := selector.X.(*ast.Ident)
	if !ok {
		return nil, fmt.Errorf("data sample must use sample.column format")
	}
	rows, exists := samples[sampleID.Name]
	if !exists {
		return nil, fmt.Errorf("unknown data sample %q", sampleID.Name)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("data sample %s has no rows", sampleID.Name)
	}
	index, err := positionalIndex("nth", call, len(rows))
	if err != nil {
		if _, validating := rows[0]["__validation__"]; validating {
			return 1.0, nil
		}
		return nil, fmt.Errorf("data sample %s: %w", sampleID.Name, err)
	}
	value, exists := rows[index][selector.Sel.Name]
	if !exists {
		if _, validating := rows[index]["__validation__"]; validating {
			return 1.0, nil
		}
		return nil, fmt.Errorf("data sample %s has no column %q", sampleID.Name, selector.Sel.Name)
	}
	return value, nil
}

func aggregateSample(function string, selector *ast.SelectorExpr, samples map[string][]map[string]interface{}) (float64, error) {
	sampleID, ok := selector.X.(*ast.Ident)
	if !ok {
		return 0, fmt.Errorf("data sample must use sample.column format")
	}
	rows, ok := samples[sampleID.Name]
	if !ok {
		return 0, fmt.Errorf("unknown data sample %q", sampleID.Name)
	}
	values := []float64{}
	for _, row := range rows {
		raw, exists := row[selector.Sel.Name]
		if !exists {
			if _, validating := row["__validation__"]; validating {
				values = append(values, 1)
				continue
			}
			return 0, fmt.Errorf("data sample %s has no column %q", sampleID.Name, selector.Sel.Name)
		}
		value, err := numberValue(raw)
		if err != nil {
			return 0, fmt.Errorf("data sample %s.%s contains a non-numeric value", sampleID.Name, selector.Sel.Name)
		}
		values = append(values, value)
	}
	if function == "count" {
		return float64(len(rows)), nil
	}
	if len(values) == 0 {
		return 0, fmt.Errorf("data sample %s has no rows", sampleID.Name)
	}
	result := values[0]
	switch function {
	case "first":
		return values[0], nil
	case "last":
		return values[len(values)-1], nil
	case "max":
		for _, value := range values[1:] {
			result = math.Max(result, value)
		}
	case "min":
		for _, value := range values[1:] {
			result = math.Min(result, value)
		}
	case "sum", "avg":
		for _, value := range values[1:] {
			result += value
		}
		if function == "avg" {
			result /= float64(len(values))
		}
	case "median":
		sorted := append([]float64(nil), values...)
		sort.Float64s(sorted)
		middle := len(sorted) / 2
		if len(sorted)%2 == 0 {
			result = (sorted[middle-1] + sorted[middle]) / 2
		} else {
			result = sorted[middle]
		}
	case "range":
		minimum, maximum := values[0], values[0]
		for _, value := range values[1:] {
			minimum = math.Min(minimum, value)
			maximum = math.Max(maximum, value)
		}
		result = maximum - minimum
	case "stdev":
		mean := 0.0
		for _, value := range values {
			mean += value
		}
		mean /= float64(len(values))
		variance := 0.0
		for _, value := range values {
			difference := value - mean
			variance += difference * difference
		}
		result = math.Sqrt(variance / float64(len(values)))
	default:
		return 0, fmt.Errorf("function %q is not a data sample aggregate", function)
	}
	return result, nil
}
