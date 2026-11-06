package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"

	"r2r/internal/control"
	"r2r/internal/store"
)

func TestExecutionArchiveIncludesRunValues(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	run := store.RunRecord{RunID: "run-1", FamilyID: "etch", ModelID: "etch-r2", ModelRevision: 2,
		Status: "completed", PreparedAt: "2026-10-01T10:00:00Z", Context: map[string]interface{}{"machine": "M1"},
		Parameters: map[string]interface{}{"time": 2.5}, Feedback: map[string]interface{}{"measured": 3.1},
		Outputs:     []control.OutputValue{{Parameter: "recipe", Value: "R1"}},
		DataSamples: map[string][]map[string]interface{}{"history": {{"cd": 3.1}}}}
	model := control.Model{DataSamples: []control.DataSampleDefinition{{Key: "history", Source: "sql", DataSourceID: "source-1", Query: "SELECT cd FROM history"}}}
	if err := writeExecutionArchive(archive, []store.RunRecord{run}, map[string]control.Model{"etch-r2": model}, true); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 6 {
		t.Fatalf("expected six CSVs for one selected family, got %d", len(reader.File))
	}
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(stream).ReadAll()
		stream.Close()
		if err != nil {
			t.Fatalf("%s: %v", file.Name, err)
		}
		if len(rows) != 2 {
			t.Fatalf("%s should include one data row, got %d", file.Name, len(rows))
		}
		if rows[1][0] != "run-1" {
			t.Fatalf("%s has wrong run: %v", file.Name, rows[1])
		}
		if file.Name == "parameters_wide.csv" && (rows[0][4] != "parameter.time" || rows[1][4] != "2.5") {
			t.Fatalf("wide parameters are incorrect: %v", rows)
		}
	}
}

func TestExecutionArchiveOmitsWideParametersForMultipleFamilies(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	if err := writeExecutionArchive(archive, []store.RunRecord{{RunID: "run-1", FamilyID: "etch", Parameters: map[string]interface{}{"time": 2.5}}}, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 5 {
		t.Fatalf("expected five standard CSVs, got %d", len(reader.File))
	}
}

func TestParseRunSearchRejectsInvalidDates(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/runs?from=bad&to=2026-10-02T00:00:00Z", nil)
	if _, err := parseRunSearch(request); err == nil || !strings.Contains(err.Error(), "from") {
		t.Fatalf("expected date validation error, got %v", err)
	}
}

func TestExportCSVPreservesNegativeNumbersAndEscapesFormulas(t *testing.T) {
	if csvSafe("-1.5") != "-1.5" {
		t.Fatal("negative numeric values must remain numeric")
	}
	if csvSafe("=HYPERLINK(\"bad\")") != "'=HYPERLINK(\"bad\")" {
		t.Fatal("spreadsheet formula should be escaped")
	}
}
