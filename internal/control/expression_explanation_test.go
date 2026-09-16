package control

import "testing"

func TestExplainConditionalExpression(t *testing.T) {
	explanation, err := ExplainExpression(`IF(@latest_cd > 1 AND IS_NULL(@pdi), ROUND(@base_time + @adjustment, 2), 0)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(explanation.Sections) != 3 || explanation.Sections[0].Label != "When" || len(explanation.Sections[0].Lines) != 2 {
		t.Fatalf("unexpected explanation: %#v", explanation)
	}
	if got := explanation.Sections[1].Lines[0]; got != "round (@base_time + @adjustment) to 2 decimal places" {
		t.Fatalf("unexpected return explanation %q", got)
	}
}

func TestExplainMathKeepsSymbolsAndPrecedence(t *testing.T) {
	explanation, err := ExplainExpression(`ROUND(@base_time + @adjustment * 0.15, 2)`)
	if err != nil {
		t.Fatal(err)
	}
	want := "round (@base_time + @adjustment × 0.15) to 2 decimal places"
	if got := explanation.Sections[0].Lines[0]; got != want {
		t.Fatalf("unexpected mathematical explanation:\n got: %q\nwant: %q", got, want)
	}
}

func TestExplainLookupExpression(t *testing.T) {
	explanation, err := ExplainExpression(`LOOKUP_FIRST(@wafer_history, @wafer_history.wafer_id == @wafer_id, @wafer_history.cd)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(explanation.Sections) != 4 || explanation.Sections[3].Lines[0] != "Return NULL." {
		t.Fatalf("unexpected lookup explanation: %#v", explanation)
	}
}

func TestExplainNestedLookupDoesNotRepeatReturn(t *testing.T) {
	explanation, err := ExplainExpression(`IF(@use_primary, @history_primary, IF(@history_primary == 0, LOOKUP_ONE(@missing_measurement_error, @missing_measurement_error.code == 0, @missing_measurement_error.value), @history_primary))`)
	if err != nil {
		t.Fatal(err)
	}
	want := "if @history_primary equals 0, use @missing_measurement_error.value from the only matching row in @missing_measurement_error where @missing_measurement_error.code equals 0; otherwise use @history_primary"
	if got := explanation.Sections[2].Lines[0]; got != want {
		t.Fatalf("unexpected nested explanation:\n got: %q\nwant: %q", got, want)
	}
}
