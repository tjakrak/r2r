package control

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

func parseTypedExpression(expression string) (ast.Expr, error) {
	normalized := lookupBooleanWord.ReplaceAllStringFunc(expression, func(value string) string {
		if strings.EqualFold(strings.TrimSpace(value), "AND") {
			return " && "
		}
		return " || "
	})
	normalized = lookupReference.ReplaceAllString(normalized, "ref__$1")
	return parser.ParseExpr(normalized)
}

func expressionText(node ast.Expr) (string, error) {
	var buffer bytes.Buffer
	if err := format.Node(&buffer, token.NewFileSet(), node); err != nil {
		return "", err
	}
	return strings.ReplaceAll(buffer.String(), "ref__", "@"), nil
}

func EvaluateTypedExpression(expression, expectedType string, model Model, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (interface{}, error) {
	node, err := parseTypedExpression(expression)
	if err != nil {
		return nil, err
	}
	value, err := evalTypedNode(node, model, context, values, samples)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	if actual := runtimeValueType(value); actual != expectedType {
		return nil, fmt.Errorf("expression returns %s, but parameter result is %s", typeLabel(actual), typeLabel(expectedType))
	}
	return value, nil
}

func evalTypedNode(node ast.Expr, model Model, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (interface{}, error) {
	switch n := node.(type) {
	case *ast.ParenExpr:
		return evalTypedNode(n.X, model, context, values, samples)
	case *ast.BasicLit:
		switch n.Kind {
		case token.STRING:
			return strconv.Unquote(n.Value)
		case token.INT, token.FLOAT:
			return strconv.ParseFloat(n.Value, 64)
		}
	case *ast.Ident:
		if strings.EqualFold(n.Name, "true") {
			return true, nil
		}
		if strings.EqualFold(n.Name, "false") {
			return false, nil
		}
		if strings.HasPrefix(n.Name, "ref__") {
			name := strings.TrimPrefix(n.Name, "ref__")
			if value, ok := values[name]; ok {
				return value, nil
			}
			if value, ok := context[name]; ok {
				return value, nil
			}
			return nil, fmt.Errorf("unknown reference @%s", name)
		}
	case *ast.UnaryExpr:
		value, err := evalTypedNode(n.X, model, context, values, samples)
		if err != nil {
			return nil, err
		}
		switch n.Op {
		case token.NOT:
			boolean, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("! requires a boolean operand")
			}
			return !boolean, nil
		case token.ADD, token.SUB:
			number, err := numberValue(value)
			if err != nil {
				return nil, fmt.Errorf("%s requires a numerical operand", n.Op)
			}
			if n.Op == token.SUB {
				number = -number
			}
			return number, nil
		}
	case *ast.BinaryExpr:
		left, err := evalTypedNode(n.X, model, context, values, samples)
		if err != nil {
			return nil, err
		}
		right, err := evalTypedNode(n.Y, model, context, values, samples)
		if err != nil {
			return nil, err
		}
		return evalTypedBinary(n.Op, left, right)
	case *ast.CallExpr:
		function, ok := n.Fun.(*ast.Ident)
		if !ok {
			break
		}
		name := strings.ToUpper(function.Name)
		if name == "IF" {
			if len(n.Args) != 3 {
				return nil, fmt.Errorf("IF requires condition, result, and else result")
			}
			condition, err := evalTypedNode(n.Args[0], model, context, values, samples)
			if err != nil {
				return nil, err
			}
			matched, ok := condition.(bool)
			if !ok {
				return nil, fmt.Errorf("IF condition must be boolean")
			}
			if matched {
				return evalTypedNode(n.Args[1], model, context, values, samples)
			}
			return evalTypedNode(n.Args[2], model, context, values, samples)
		}
		if name == "IS_NULL" {
			if len(n.Args) != 1 {
				return nil, fmt.Errorf("IS_NULL requires one value")
			}
			value, err := evalTypedNode(n.Args[0], model, context, values, samples)
			return value == nil, err
		}
		if name == "LOOKUP_ONE" || name == "LOOKUP_FIRST" {
			text, err := expressionText(n)
			if err != nil {
				return nil, err
			}
			resultType, err := LookupFormulaType(text, model.DataSamples)
			if err != nil {
				return nil, err
			}
			return EvaluateLookupFormula(text, resultType, model.DataSamples, context, values, samples)
		}
		if name == "CONTAINS" || name == "MATCHES" {
			return evaluateSampleMembership(name, n, model, context, values, samples)
		}
		if name == "SUBSTR" || name == "REPLACE" || name == "LOWER" || name == "UPPER" || name == "TRIM" || name == "CONCAT" || name == "LEN" {
			return evalStringCall(name, n.Args, model, context, values, samples)
		}
		text, err := expressionText(n)
		if err != nil {
			return nil, err
		}
		if name == "FIRST" || name == "NTH" || name == "LAST" {
			return EvaluateSampleValue(text, samples)
		}
		return EvaluateWithSamples(text, numericValues(valuesAndContext(values, context)), samples)
	}
	return nil, fmt.Errorf("unsupported expression")
}

func TypedExpressionDependencies(expression string) (references, samples []string, err error) {
	node, err := parseTypedExpression(expression)
	if err != nil {
		return nil, nil, err
	}
	referenceSet, sampleSet := map[string]bool{}, map[string]bool{}
	ast.Inspect(node, func(current ast.Node) bool {
		switch value := current.(type) {
		case *ast.Ident:
			if strings.HasPrefix(value.Name, "ref__") {
				referenceSet[strings.TrimPrefix(value.Name, "ref__")] = true
			}
		case *ast.CallExpr:
			function, _ := value.Fun.(*ast.Ident)
			if function == nil {
				break
			}
			name := strings.ToUpper(function.Name)
			if (name == "LOOKUP_ONE" || name == "LOOKUP_FIRST") && len(value.Args) > 0 {
				if sample, ok := value.Args[0].(*ast.Ident); ok {
					sampleSet[strings.TrimPrefix(sample.Name, "ref__")] = true
					delete(referenceSet, strings.TrimPrefix(sample.Name, "ref__"))
				}
			} else if name == "CONTAINS" || name == "MATCHES" || name == "FIRST" || name == "NTH" || name == "LAST" || name == "MIN" || name == "MAX" || name == "AVG" || name == "MEDIAN" || name == "RANGE" || name == "STDEV" || name == "SUM" || name == "COUNT" {
				if selector, ok := selectorArgument(value); ok {
					if sample, ok := selector.X.(*ast.Ident); ok {
						sampleSet[strings.TrimPrefix(sample.Name, "ref__")] = true
						delete(referenceSet, strings.TrimPrefix(sample.Name, "ref__"))
					}
				}
			}
		}
		return true
	})
	for name := range sampleSet {
		delete(referenceSet, name)
	}
	for name := range referenceSet {
		references = append(references, name)
	}
	for name := range sampleSet {
		samples = append(samples, name)
	}
	return references, samples, nil
}

func valuesAndContext(values, context map[string]interface{}) map[string]interface{} {
	all := make(map[string]interface{}, len(values)+len(context))
	for key, value := range context {
		all[key] = value
	}
	for key, value := range values {
		all[key] = value
	}
	return all
}

func evalStringCall(name string, arguments []ast.Expr, model Model, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (interface{}, error) {
	resolved := make([]interface{}, 0, len(arguments))
	for _, argument := range arguments {
		value, err := evalTypedNode(argument, model, context, values, samples)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, value)
	}
	textArg := func(index int) (string, error) {
		if index >= len(resolved) {
			return "", fmt.Errorf("%s has too few arguments", name)
		}
		value, ok := resolved[index].(string)
		if !ok {
			return "", fmt.Errorf("%s requires text arguments", name)
		}
		return value, nil
	}
	switch name {
	case "LOWER", "UPPER", "TRIM", "LEN":
		if len(resolved) != 1 {
			return nil, fmt.Errorf("%s requires one argument", name)
		}
		text, err := textArg(0)
		if err != nil {
			return nil, err
		}
		if name == "LOWER" {
			return strings.ToLower(text), nil
		}
		if name == "UPPER" {
			return strings.ToUpper(text), nil
		}
		if name == "TRIM" {
			return strings.TrimSpace(text), nil
		}
		return float64(len([]rune(text))), nil
	case "REPLACE":
		if len(resolved) != 3 {
			return nil, fmt.Errorf("REPLACE requires text, old text, and new text")
		}
		text, err := textArg(0)
		if err != nil {
			return nil, err
		}
		old, err := textArg(1)
		if err != nil {
			return nil, err
		}
		replacement, err := textArg(2)
		if err != nil {
			return nil, err
		}
		return strings.ReplaceAll(text, old, replacement), nil
	case "CONCAT":
		if len(resolved) < 2 {
			return nil, fmt.Errorf("CONCAT requires at least two arguments")
		}
		var builder strings.Builder
		for index := range resolved {
			text, err := textArg(index)
			if err != nil {
				return nil, err
			}
			builder.WriteString(text)
		}
		return builder.String(), nil
	case "SUBSTR":
		if len(resolved) != 3 {
			return nil, fmt.Errorf("SUBSTR requires text, start, and length")
		}
		text, err := textArg(0)
		if err != nil {
			return nil, err
		}
		start, err := numberValue(resolved[1])
		if err != nil {
			return nil, fmt.Errorf("SUBSTR start must be numerical")
		}
		length, err := numberValue(resolved[2])
		if err != nil {
			return nil, fmt.Errorf("SUBSTR length must be numerical")
		}
		runes := []rune(text)
		first, count := int(start)-1, int(length)
		if first < 0 || count < 0 || first > len(runes) {
			return nil, fmt.Errorf("SUBSTR range is invalid")
		}
		end := first + count
		if end > len(runes) {
			end = len(runes)
		}
		return string(runes[first:end]), nil
	}
	return nil, fmt.Errorf("unsupported string function %s", name)
}

func evaluateSampleMembership(name string, call *ast.CallExpr, model Model, context, values map[string]interface{}, samples map[string][]map[string]interface{}) (interface{}, error) {
	if len(call.Args) != 2 {
		return nil, fmt.Errorf("%s requires sample.column and text", name)
	}
	selector, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok {
		return nil, fmt.Errorf("%s first argument must use @sample.column", name)
	}
	sample, ok := selector.X.(*ast.Ident)
	if !ok {
		return nil, fmt.Errorf("%s first argument must use @sample.column", name)
	}
	sampleName := strings.TrimPrefix(sample.Name, "ref__")
	needleValue, err := evalTypedNode(call.Args[1], model, context, values, samples)
	if err != nil {
		return nil, err
	}
	needle, ok := needleValue.(string)
	if !ok {
		return nil, fmt.Errorf("%s search value must be text", name)
	}
	var pattern *regexp.Regexp
	if name == "MATCHES" {
		pattern, err = regexp.Compile(needle)
		if err != nil {
			return nil, fmt.Errorf("MATCHES pattern is invalid: %w", err)
		}
	}
	rows, exists := samples[sampleName]
	if !exists {
		return nil, fmt.Errorf("unknown data sample %q", sampleName)
	}
	for _, row := range rows {
		raw, exists := row[selector.Sel.Name]
		if !exists {
			return nil, fmt.Errorf("data sample %s has no column %q", sampleName, selector.Sel.Name)
		}
		if raw == nil {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%s requires a text column", name)
		}
		if (name == "CONTAINS" && strings.EqualFold(text, needle)) || (name == "MATCHES" && pattern.MatchString(text)) {
			return true, nil
		}
	}
	return false, nil
}

func evalTypedBinary(op token.Token, left, right interface{}) (interface{}, error) {
	if op == token.LAND || op == token.LOR {
		l, lok := left.(bool)
		r, rok := right.(bool)
		if !lok || !rok {
			return nil, fmt.Errorf("%s requires boolean operands", op)
		}
		if op == token.LAND {
			return l && r, nil
		}
		return l || r, nil
	}
	// NULL only participates in IS_NULL. Ordinary comparisons with it do not match.
	if left == nil || right == nil {
		switch op {
		case token.EQL, token.NEQ, token.GTR, token.GEQ, token.LSS, token.LEQ:
			return false, nil
		}
	}
	if op == token.EQL || op == token.NEQ {
		if runtimeValueType(left) != runtimeValueType(right) {
			return nil, fmt.Errorf("%s operands must have the same type", op)
		}
		equal := fmt.Sprint(left) == fmt.Sprint(right)
		if op == token.NEQ {
			equal = !equal
		}
		return equal, nil
	}
	l, lerr := numberValue(left)
	r, rerr := numberValue(right)
	if lerr != nil || rerr != nil {
		return nil, fmt.Errorf("%s requires numerical operands", op)
	}
	switch op {
	case token.ADD:
		return l + r, nil
	case token.SUB:
		return l - r, nil
	case token.MUL:
		return l * r, nil
	case token.QUO:
		if r == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return l / r, nil
	case token.GTR:
		return l > r, nil
	case token.GEQ:
		return l >= r, nil
	case token.LSS:
		return l < r, nil
	case token.LEQ:
		return l <= r, nil
	}
	return nil, fmt.Errorf("unsupported operator %s", op)
}

func runtimeValueType(value interface{}) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case string:
		return "string"
	}
	if _, err := numberValue(value); err == nil {
		return "number"
	}
	return "unknown"
}

func ValidateTypedExpression(expression, expectedType string, model Model, referenceTypes map[string]string) error {
	node, err := parseTypedExpression(expression)
	if err != nil {
		return err
	}
	actual, err := inferTypedNode(node, model, referenceTypes)
	if err != nil {
		return err
	}
	if actual != expectedType {
		return fmt.Errorf("expression returns %s, but parameter result is %s", typeLabel(actual), typeLabel(expectedType))
	}
	return nil
}

func inferTypedNode(node ast.Expr, model Model, refs map[string]string) (string, error) {
	switch n := node.(type) {
	case *ast.ParenExpr:
		return inferTypedNode(n.X, model, refs)
	case *ast.BasicLit:
		if n.Kind == token.STRING {
			return "string", nil
		}
		if n.Kind == token.INT || n.Kind == token.FLOAT {
			return "number", nil
		}
	case *ast.Ident:
		if strings.EqualFold(n.Name, "true") || strings.EqualFold(n.Name, "false") {
			return "boolean", nil
		}
		if strings.HasPrefix(n.Name, "ref__") {
			name := strings.TrimPrefix(n.Name, "ref__")
			if typ, ok := refs[name]; ok {
				return typ, nil
			}
			if _, ok := model.Indexes[name]; ok {
				return "string", nil
			}
			return "", fmt.Errorf("unknown reference @%s", name)
		}
	case *ast.UnaryExpr:
		typ, err := inferTypedNode(n.X, model, refs)
		if err != nil {
			return "", err
		}
		if n.Op == token.NOT && typ == "boolean" {
			return "boolean", nil
		}
		if (n.Op == token.ADD || n.Op == token.SUB) && typ == "number" {
			return "number", nil
		}
		return "", fmt.Errorf("operator %s cannot be used with %s", n.Op, typeLabel(typ))
	case *ast.BinaryExpr:
		left, err := inferTypedNode(n.X, model, refs)
		if err != nil {
			return "", err
		}
		right, err := inferTypedNode(n.Y, model, refs)
		if err != nil {
			return "", err
		}
		switch n.Op {
		case token.LAND, token.LOR:
			if left == "boolean" && right == "boolean" {
				return "boolean", nil
			}
		case token.EQL, token.NEQ:
			if left == right {
				return "boolean", nil
			}
		case token.GTR, token.GEQ, token.LSS, token.LEQ:
			if left == "number" && right == "number" {
				return "boolean", nil
			}
		case token.ADD, token.SUB, token.MUL, token.QUO:
			if left == "number" && right == "number" {
				return "number", nil
			}
		}
		return "", fmt.Errorf("operator %s cannot combine %s and %s", n.Op, typeLabel(left), typeLabel(right))
	case *ast.CallExpr:
		function, ok := n.Fun.(*ast.Ident)
		if !ok {
			break
		}
		name := strings.ToUpper(function.Name)
		if name == "IF" {
			if len(n.Args) != 3 {
				return "", fmt.Errorf("IF requires condition, result, and else result")
			}
			condition, err := inferTypedNode(n.Args[0], model, refs)
			if err != nil {
				return "", err
			}
			if condition != "boolean" {
				return "", fmt.Errorf("IF condition must be boolean")
			}
			left, err := inferTypedNode(n.Args[1], model, refs)
			if err != nil {
				return "", err
			}
			right, err := inferTypedNode(n.Args[2], model, refs)
			if err != nil {
				return "", err
			}
			if left != right {
				return "", fmt.Errorf("IF results must have the same type")
			}
			return left, nil
		}
		if name == "IS_NULL" {
			if len(n.Args) != 1 {
				return "", fmt.Errorf("IS_NULL requires one value")
			}
			if _, err := inferTypedNode(n.Args[0], model, refs); err != nil {
				return "", err
			}
			return "boolean", nil
		}
		text, err := expressionText(n)
		if err != nil {
			return "", err
		}
		if name == "CONTAINS" || name == "MATCHES" {
			if len(n.Args) != 2 {
				return "", fmt.Errorf("%s requires sample.column and text", name)
			}
			if _, err := SampleExpressionType(text, model.DataSamples); err != nil {
				return "", err
			}
			valueType, err := inferTypedNode(n.Args[1], model, refs)
			if err != nil {
				return "", err
			}
			if valueType != "string" {
				return "", fmt.Errorf("%s search value must be text", name)
			}
			return "boolean", nil
		}
		if name == "LOOKUP_ONE" || name == "LOOKUP_FIRST" {
			typ, err := LookupFormulaType(text, model.DataSamples)
			if err != nil {
				return "", err
			}
			if err := ValidateLookupFormula(text, typ, model.DataSamples, refs, model.Indexes); err != nil {
				return "", err
			}
			return typ, nil
		}
		if name == "FIRST" || name == "NTH" || name == "LAST" || name == "AVG" || name == "MEDIAN" || name == "RANGE" || name == "STDEV" || name == "SUM" || name == "COUNT" || ((name == "MIN" || name == "MAX") && len(n.Args) > 0 && isSelector(n.Args[0])) {
			return SampleExpressionType(text, model.DataSamples)
		}
		if name == "ABS" || name == "CEIL" || name == "COS" || name == "EXP" || name == "FLOOR" || name == "LOG" || name == "ROUND" || name == "SIN" || name == "SQRT" || name == "TAN" || name == "POW" || name == "MIN" || name == "MAX" {
			if len(n.Args) == 0 {
				return "", fmt.Errorf("%s needs at least one numerical argument", name)
			}
			if name == "ROUND" && len(n.Args) != 1 && len(n.Args) != 2 {
				return "", fmt.Errorf("ROUND requires a value and an optional number of decimal places")
			}
			for _, argument := range n.Args {
				typ, err := inferTypedNode(argument, model, refs)
				if err != nil {
					return "", err
				}
				if typ != "number" {
					return "", fmt.Errorf("%s requires numerical arguments", name)
				}
			}
			return "number", nil
		}
		if name == "SUBSTR" || name == "REPLACE" || name == "LOWER" || name == "UPPER" || name == "TRIM" || name == "CONCAT" || name == "LEN" {
			expected := map[string]int{"SUBSTR": 3, "REPLACE": 3, "LOWER": 1, "UPPER": 1, "TRIM": 1, "LEN": 1}
			if count, fixed := expected[name]; fixed && len(n.Args) != count {
				return "", fmt.Errorf("%s requires %d arguments", name, count)
			}
			if name == "CONCAT" && len(n.Args) < 2 {
				return "", fmt.Errorf("CONCAT requires at least two arguments")
			}
			for index, argument := range n.Args {
				typ, err := inferTypedNode(argument, model, refs)
				if err != nil {
					return "", err
				}
				numerical := name == "SUBSTR" && index > 0
				if (numerical && typ != "number") || (!numerical && typ != "string") {
					return "", fmt.Errorf("%s argument %d has the wrong type", name, index+1)
				}
			}
			if name == "LEN" {
				return "number", nil
			}
			return "string", nil
		}
	}
	return "", fmt.Errorf("unsupported expression")
}

func isSelector(node ast.Expr) bool { _, ok := node.(*ast.SelectorExpr); return ok }
