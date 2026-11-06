package control

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// ExpressionExplanation is a deterministic, review-oriented description of
// an expression parsed with the same grammar used by typed expression execution.
type ExpressionExplanation struct {
	Summary  string                         `json:"summary"`
	Sections []ExpressionExplanationSection `json:"sections"`
}

type ExpressionExplanationSection struct {
	Label string   `json:"label"`
	Lines []string `json:"lines"`
}

func ExplainExpression(expression string) (ExpressionExplanation, error) {
	node, err := parseTypedExpression(expression)
	if err != nil {
		return ExpressionExplanation{}, err
	}
	if call, ok := node.(*ast.CallExpr); ok {
		if function, ok := call.Fun.(*ast.Ident); ok && strings.EqualFold(function.Name, "IF") && len(call.Args) == 3 {
			return ExpressionExplanation{
				Summary: "Choose a result based on the following condition.",
				Sections: []ExpressionExplanationSection{
					{Label: "When", Lines: explainConditionLines(call.Args[0])},
					{Label: "Return", Lines: []string{explainExpressionNode(call.Args[1])}},
					{Label: "Otherwise", Lines: []string{explainExpressionNode(call.Args[2])}},
				},
			}, nil
		}
		if function, ok := call.Fun.(*ast.Ident); ok && (strings.EqualFold(function.Name, "LOOKUP_ONE") || strings.EqualFold(function.Name, "LOOKUP_FIRST")) && len(call.Args) == 3 {
			mode := "the first matching row"
			if strings.EqualFold(function.Name, "LOOKUP_ONE") {
				mode = "exactly one matching row; return an error if several rows match"
			}
			return ExpressionExplanation{
				Summary: "Look up a value from a data sample.",
				Sections: []ExpressionExplanationSection{
					{Label: "Data sample", Lines: []string{explainExpressionNode(call.Args[0])}},
					{Label: "Match", Lines: explainConditionLines(call.Args[1])},
					{Label: "Return", Lines: []string{fmt.Sprintf("%s from %s", explainExpressionNode(call.Args[2]), mode)}},
					{Label: "No match", Lines: []string{"Return NULL."}},
				},
			}, nil
		}
	}
	return ExpressionExplanation{
		Summary: "Calculate the result shown below.",
		Sections: []ExpressionExplanationSection{
			{Label: "Result", Lines: []string{explainExpressionNode(node)}},
		},
	}, nil
}

func explainConditionLines(node ast.Expr) []string {
	if binary, ok := unparenthesized(node).(*ast.BinaryExpr); ok && binary.Op == token.LAND {
		return append(explainConditionLines(binary.X), explainConditionLines(binary.Y)...)
	}
	if binary, ok := unparenthesized(node).(*ast.BinaryExpr); ok && binary.Op == token.LOR {
		return []string{fmt.Sprintf("Either %s, or %s", explainExpressionNode(binary.X), explainExpressionNode(binary.Y))}
	}
	return []string{explainExpressionNode(node)}
}

func explainExpressionNode(node ast.Expr) string {
	switch value := node.(type) {
	case *ast.ParenExpr:
		return explainExpressionNode(value.X)
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			if text, err := strconv.Unquote(value.Value); err == nil {
				return strconv.Quote(text)
			}
		}
		return value.Value
	case *ast.Ident:
		if strings.HasPrefix(value.Name, "ref__") {
			return "@" + strings.TrimPrefix(value.Name, "ref__")
		}
		return value.Name
	case *ast.SelectorExpr:
		return explainExpressionNode(value.X) + "." + value.Sel.Name
	case *ast.UnaryExpr:
		switch value.Op {
		case token.NOT:
			return "not " + explainExpressionNode(value.X)
		case token.SUB:
			return "negative " + explainExpressionNode(value.X)
		default:
			return explainExpressionNode(value.X)
		}
	case *ast.BinaryExpr:
		if isArithmeticOperator(value.Op) {
			return formatMathExpression(value, 0)
		}
		left, right := explainExpressionNode(value.X), explainExpressionNode(value.Y)
		operators := map[token.Token]string{
			token.EQL: "equals", token.NEQ: "does not equal", token.GTR: "is greater than",
			token.GEQ: "is greater than or equal to", token.LSS: "is less than", token.LEQ: "is less than or equal to",
			token.LAND: "and", token.LOR: "or",
		}
		if operator, ok := operators[value.Op]; ok {
			return fmt.Sprintf("%s %s %s", left, operator, right)
		}
	case *ast.CallExpr:
		function, ok := value.Fun.(*ast.Ident)
		if !ok {
			break
		}
		name := strings.ToUpper(function.Name)
		arguments := make([]string, len(value.Args))
		for index, argument := range value.Args {
			arguments[index] = explainExpressionNode(argument)
			if binary, ok := unparenthesized(argument).(*ast.BinaryExpr); ok && isArithmeticOperator(binary.Op) {
				arguments[index] = "(" + formatMathExpression(argument, 0) + ")"
			}
		}
		switch name {
		case "IF":
			if len(arguments) == 3 {
				return fmt.Sprintf("if %s, use %s; otherwise use %s", arguments[0], arguments[1], arguments[2])
			}
		case "IS_NULL":
			if len(arguments) == 1 {
				return arguments[0] + " is NULL"
			}
		case "ROUND":
			if len(arguments) == 1 {
				return "round " + arguments[0] + " to the nearest integer"
			}
			if len(arguments) == 2 {
				return fmt.Sprintf("round %s to %s decimal places", arguments[0], arguments[1])
			}
		case "LOOKUP_ONE", "LOOKUP_FIRST":
			if len(arguments) == 3 {
				mode := "the first matching row"
				if name == "LOOKUP_ONE" {
					mode = "the only matching row"
				}
				return fmt.Sprintf("%s from %s in %s where %s", arguments[2], mode, arguments[0], arguments[1])
			}
		case "FIRST":
			if len(arguments) == 1 {
				return "the first value of " + arguments[0]
			}
		case "LAST":
			if len(arguments) == 1 {
				return "the last value of " + arguments[0]
			}
		case "NTH":
			if len(arguments) == 2 {
				return fmt.Sprintf("row %s of %s", arguments[1], arguments[0])
			}
		case "CONTAINS":
			if len(arguments) == 2 {
				return fmt.Sprintf("%s contains the exact value %s", arguments[0], arguments[1])
			}
		case "MATCHES":
			if len(arguments) == 2 {
				return fmt.Sprintf("%s matches the regular expression %s", arguments[0], arguments[1])
			}
		case "AVG":
			return describeSingleArgument("the average of", arguments)
		case "MAX":
			return describeSingleArgument("the maximum of", arguments)
		case "MEDIAN":
			return describeSingleArgument("the median of", arguments)
		case "MIN":
			return describeSingleArgument("the minimum of", arguments)
		case "RANGE":
			return describeSingleArgument("the range of", arguments)
		case "STDEV":
			return describeSingleArgument("the standard deviation of", arguments)
		case "SUM":
			return describeSingleArgument("the sum of", arguments)
		case "COUNT":
			return describeSingleArgument("the number of non-NULL values in", arguments)
		case "ABS":
			return describeSingleArgument("the absolute value of", arguments)
		case "CEIL":
			return describeSingleArgument("round up", arguments)
		case "FLOOR":
			return describeSingleArgument("round down", arguments)
		case "SQRT":
			return describeSingleArgument("the square root of", arguments)
		case "LOWER":
			return describeSingleArgument("lowercase", arguments)
		case "UPPER":
			return describeSingleArgument("uppercase", arguments)
		case "TRIM":
			return describeSingleArgument("trimmed", arguments)
		case "LEN":
			return describeSingleArgument("the length of", arguments)
		case "POW":
			if len(arguments) == 2 {
				return fmt.Sprintf("%s raised to the power of %s", arguments[0], arguments[1])
			}
		case "CONCAT":
			return "join " + strings.Join(arguments, ", ")
		case "TO_TEXT":
			if len(arguments) == 2 {
				return fmt.Sprintf("format %s as text with %s decimal places", arguments[0], arguments[1])
			}
			return describeSingleArgument("convert to text", arguments)
		case "TO_NUMBER":
			return describeSingleArgument("convert to a number", arguments)
		case "REPLACE":
			if len(arguments) == 3 {
				return fmt.Sprintf("replace %s with %s in %s", arguments[1], arguments[2], arguments[0])
			}
		case "SUBSTR":
			if len(arguments) == 3 {
				return fmt.Sprintf("%s characters from %s, starting at position %s", arguments[2], arguments[0], arguments[1])
			}
		}
		return fmt.Sprintf("%s of %s", strings.ToLower(name), strings.Join(arguments, ", "))
	}
	if text, err := expressionText(node); err == nil {
		return text
	}
	return "the expression result"
}

func isArithmeticOperator(operator token.Token) bool {
	return operator == token.ADD || operator == token.SUB || operator == token.MUL || operator == token.QUO
}

func arithmeticPrecedence(operator token.Token) int {
	if operator == token.MUL || operator == token.QUO {
		return 2
	}
	return 1
}

func formatMathExpression(node ast.Expr, parentPrecedence int) string {
	switch value := node.(type) {
	case *ast.ParenExpr:
		return "(" + formatMathExpression(value.X, 0) + ")"
	case *ast.BinaryExpr:
		if !isArithmeticOperator(value.Op) {
			return explainExpressionNode(value)
		}
		precedence := arithmeticPrecedence(value.Op)
		operator := map[token.Token]string{
			token.ADD: "+",
			token.SUB: "−",
			token.MUL: "×",
			token.QUO: "÷",
		}[value.Op]
		text := fmt.Sprintf(
			"%s %s %s",
			formatMathExpression(value.X, precedence),
			operator,
			formatMathExpression(value.Y, precedence+1),
		)
		if precedence < parentPrecedence {
			return "(" + text + ")"
		}
		return text
	case *ast.UnaryExpr:
		if value.Op == token.SUB {
			return "−" + formatMathExpression(value.X, 3)
		}
	}
	return explainExpressionNode(node)
}

func describeSingleArgument(prefix string, arguments []string) string {
	if len(arguments) == 1 {
		return prefix + " " + arguments[0]
	}
	return prefix + " " + strings.Join(arguments, ", ")
}

func unparenthesized(node ast.Expr) ast.Expr {
	for {
		parenthesized, ok := node.(*ast.ParenExpr)
		if !ok {
			return node
		}
		node = parenthesized.X
	}
}
