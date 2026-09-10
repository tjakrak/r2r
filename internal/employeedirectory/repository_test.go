package employeedirectory

import "testing"

func TestRecordDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		record   Record
		expected string
	}{
		{name: "full name", record: Record{FirstName: "Ryan", LastName: "Tjakrakartadinata", LoginID: "ryan.t"}, expected: "Ryan Tjakrakartadinata"},
		{name: "first name only", record: Record{FirstName: "Ryan", LoginID: "ryan.t"}, expected: "Ryan"},
		{name: "login fallback", record: Record{LoginID: "ryan.t"}, expected: "ryan.t"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := test.record.DisplayName(); actual != test.expected {
				t.Fatalf("DisplayName() = %q, want %q", actual, test.expected)
			}
		})
	}
}
