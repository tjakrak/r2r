package main

import (
	"archive/zip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"r2r/internal/control"
	"r2r/internal/store"
)

const executionExportLimit = 2000

func parseRunSearch(r *http.Request) (store.RunSearch, error) {
	query := r.URL.Query()
	from, err := time.Parse(time.RFC3339, query.Get("from"))
	if err != nil {
		return store.RunSearch{}, fmt.Errorf("from must be an ISO-8601 timestamp")
	}
	to, err := time.Parse(time.RFC3339, query.Get("to"))
	if err != nil {
		return store.RunSearch{}, fmt.Errorf("to must be an ISO-8601 timestamp")
	}
	if !from.Before(to) || to.Sub(from) > 366*24*time.Hour {
		return store.RunSearch{}, fmt.Errorf("choose a date range of 366 days or less")
	}
	status := strings.TrimSpace(query.Get("status"))
	switch status {
	case "", "prepared", "completed", "run_failed", "run_cancelled":
	default:
		return store.RunSearch{}, fmt.Errorf("invalid run status")
	}
	ids := query["familyId"]
	if len(ids) > 100 {
		return store.RunSearch{}, fmt.Errorf("select at most 100 models")
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return store.RunSearch{}, fmt.Errorf("empty model family identifier")
		}
	}
	page := 1
	if query.Has("page") {
		page, err = strconv.Atoi(query.Get("page"))
		if err != nil || page < 1 || page > 100000 {
			return store.RunSearch{}, fmt.Errorf("invalid page")
		}
	}
	return store.RunSearch{From: from, To: to, Status: status, FamilyIDs: ids,
		FolderID: strings.TrimSpace(query.Get("folderId")), Offset: (page - 1) * 50, Limit: 50}, nil
}

func (s *server) executions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	filter, err := parseRunSearch(r)
	if err != nil {
		write(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.store.SearchRuns(filter)
	if err != nil {
		write(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	write(w, http.StatusOK, result)
}

func (s *server) exportExecutions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	filter, err := parseRunSearch(r)
	if err != nil {
		write(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	filter.Offset, filter.Limit = 0, 1
	count, err := s.store.SearchRuns(filter)
	if err != nil {
		write(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if count.Total > executionExportLimit {
		write(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "More than 2,000 runs match. Narrow the date, folder, model, or status filters before exporting."})
		return
	}
	filter.Limit = executionExportLimit
	result, err := s.store.SearchRuns(filter)
	if err != nil {
		write(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	current, _ := s.currentUser(r)
	includeWideParameters := len(filter.FamilyIDs) == 1
	_ = s.store.RecordAudit(actorID(current), "runs.exported", "success", "run_export", "filtered", r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), map[string]interface{}{"runCount": result.Total, "from": filter.From.Format(time.RFC3339), "to": filter.To.Format(time.RFC3339), "status": filter.Status, "familyIds": filter.FamilyIDs, "folderId": filter.FolderID, "wideParameters": includeWideParameters})
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="r2r-executions-%s.zip"`, time.Now().UTC().Format("20060102-150405")))
	archive := zip.NewWriter(w)
	defer archive.Close()
	models := map[string]control.Model{}
	for _, model := range s.store.Config().Models {
		models[model.ID] = model
	}
	if err := writeExecutionArchive(archive, result.Runs, models, includeWideParameters); err != nil {
		operationalLog("ERROR", "execution_export_failed", map[string]interface{}{"error": err.Error()})
	}
}

func csvJSON(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(encoded)
}

func csvSafe(value string) string {
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		if _, err := strconv.ParseFloat(value, 64); err == nil {
			return value
		}
	}
	if value != "" && strings.ContainsRune("=+-@\t\r\n", rune(value[0])) {
		return "'" + value
	}
	return value
}

func writeArchiveCSV(archive *zip.Writer, name string, header []string, rows [][]string) error {
	file, err := archive.Create(name)
	if err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		for index := range row {
			row[index] = csvSafe(row[index])
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func sortedRunKeys(values map[string]interface{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func wideParameterValue(value interface{}) string {
	if value == nil {
		return "NULL"
	}
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	default:
		return csvJSON(value)
	}
}

func writeExecutionArchive(archive *zip.Writer, runs []store.RunRecord, models map[string]control.Model, includeWideParameters bool) error {
	var runRows, parameterRows, outputRows, feedbackRows, sampleRows [][]string
	for _, run := range runs {
		runRows = append(runRows, []string{run.RunID, run.FamilyID, run.ModelID, strconv.Itoa(run.ModelRevision), run.Status,
			run.Disposition, run.PreparedAt, run.CompletedAt, run.RequestID, run.FailureCode, run.FailureMessage,
			csvJSON(run.Context), csvJSON(run.AdaptiveUpdates)})
		for _, key := range sortedRunKeys(run.Parameters) {
			parameterRows = append(parameterRows, []string{run.RunID, key, csvJSON(run.Parameters[key])})
		}
		for ordinal, output := range run.Outputs {
			lsl, usl := "", ""
			if output.LSL != nil {
				lsl = strconv.FormatFloat(*output.LSL, 'g', -1, 64)
			}
			if output.USL != nil {
				usl = strconv.FormatFloat(*output.USL, 'g', -1, 64)
			}
			outputRows = append(outputRows, []string{run.RunID, strconv.Itoa(ordinal), output.Parameter, csvJSON(output.Value),
				csvJSON(output.CalculatedValue), output.Unit, lsl, usl, strconv.FormatBool(output.BoundLimited), output.LimitAction})
		}
		for _, key := range sortedRunKeys(run.Feedback) {
			feedbackRows = append(feedbackRows, []string{run.RunID, key, csvJSON(run.Feedback[key])})
		}
		model := models[run.ModelID]
		for _, name := range sortedRunKeysOfSamples(run.DataSamples) {
			rows := run.DataSamples[name]
			source, sourceID, query := "", "", ""
			for _, sample := range model.DataSamples {
				if sample.Key == name {
					source, sourceID, query = sample.Source, sample.DataSourceID, sample.Query
					break
				}
			}
			sampleRows = append(sampleRows, []string{run.RunID, name, source, sourceID, query, strconv.Itoa(len(rows)), csvJSON(rows)})
		}
	}
	if err := writeArchiveCSV(archive, "runs.csv", []string{"run_id", "family_id", "model_id", "model_revision", "status", "disposition", "prepared_at_utc", "completed_at_utc", "request_id", "failure_code", "failure_message", "context_json", "adaptive_updates_json"}, runRows); err != nil {
		return err
	}
	if err := writeArchiveCSV(archive, "parameter_values.csv", []string{"run_id", "parameter_name", "value_json"}, parameterRows); err != nil {
		return err
	}
	if includeWideParameters {
		columnNames := make(map[string]bool)
		for _, run := range runs {
			for name := range run.Parameters {
				columnNames[name] = true
			}
		}
		orderedNames := make([]string, 0, len(columnNames))
		for name := range columnNames {
			orderedNames = append(orderedNames, name)
		}
		sort.Strings(orderedNames)
		header := []string{"run_id", "model_revision", "status", "prepared_at_utc"}
		for _, name := range orderedNames {
			header = append(header, "parameter."+name)
		}
		wideRows := make([][]string, 0, len(runs))
		for _, run := range runs {
			row := []string{run.RunID, strconv.Itoa(run.ModelRevision), run.Status, run.PreparedAt}
			for _, name := range orderedNames {
				value, exists := run.Parameters[name]
				if exists {
					row = append(row, wideParameterValue(value))
				} else {
					row = append(row, "")
				}
			}
			wideRows = append(wideRows, row)
		}
		if err := writeArchiveCSV(archive, "parameters_wide.csv", header, wideRows); err != nil {
			return err
		}
	}
	if err := writeArchiveCSV(archive, "outputs.csv", []string{"run_id", "output_ordinal", "parameter_name", "value_json", "calculated_value_json", "unit", "lsl", "usl", "out_of_limit", "limit_action"}, outputRows); err != nil {
		return err
	}
	if err := writeArchiveCSV(archive, "feedback_values.csv", []string{"run_id", "feedback_name", "value_json"}, feedbackRows); err != nil {
		return err
	}
	return writeArchiveCSV(archive, "data_samples.csv", []string{"run_id", "sample_name", "source", "data_source_id", "parameterized_query_text", "row_count", "snapshot_json"}, sampleRows)
}

func sortedRunKeysOfSamples(values map[string][]map[string]interface{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
