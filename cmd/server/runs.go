package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"r2r/internal/control"
	"r2r/internal/store"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *server) prepare(w http.ResponseWriter, r *http.Request) {
	s.executeRun(w, r, false, false)
}

func (s *server) simulateProductionRun(w http.ResponseWriter, r *http.Request) {
	s.executeRun(w, r, false, true)
}

func validatePrepareIndexes(indexes map[string]interface{}, definitions []store.InputDefinition) (map[string]interface{}, error) {
	registered := map[string]store.InputDefinition{}
	for _, definition := range definitions {
		registered[strings.ToLower(strings.TrimSpace(definition.Name))] = definition
	}
	normalized := make(map[string]interface{}, len(indexes))
	for rawName, rawValue := range indexes {
		name := strings.TrimSpace(rawName)
		value, ok := rawValue.(string)
		if name == "" || !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("index %q must be a non-empty string", rawName)
		}
		key := strings.ToLower(name)
		definition, exists := registered[key]
		if !exists || definition.Category != "index" || definition.Status != "active" {
			return nil, fmt.Errorf("index %q is not an active registered index", name)
		}
		normalized[definition.Name] = strings.TrimSpace(value)
	}
	return normalized, nil
}

func prepareExecutionContext(model control.Model, indexes, externalInputs map[string]interface{}) (map[string]interface{}, []string, error) {
	context := make(map[string]interface{}, len(indexes)+len(externalInputs))
	ignoredExternalInputs := []string{}
	names := map[string]string{}
	for name, value := range indexes {
		context[name] = value
		names[strings.ToLower(name)] = "index"
	}
	declared := map[string]control.ExternalInputDefinition{}
	for _, input := range model.ExternalInputs {
		declared[strings.ToLower(input.Name)] = input
	}
	for rawName, value := range externalInputs {
		name := strings.TrimSpace(rawName)
		if category, duplicate := names[strings.ToLower(name)]; duplicate {
			return nil, nil, fmt.Errorf("input %q cannot be supplied as both %s and external input", name, category)
		}
		input, exists := declared[strings.ToLower(name)]
		if !exists {
			ignoredExternalInputs = append(ignoredExternalInputs, name)
			continue
		}
		context[input.Name] = value
		names[strings.ToLower(input.Name)] = "external input"
	}
	sort.Strings(ignoredExternalInputs)
	return context, ignoredExternalInputs, nil
}

func (s *server) executeRun(w http.ResponseWriter, r *http.Request, commitAdaptive, dryRun bool) {
	if r.Method != "POST" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req control.Request
	if err := decode(r, &req); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if len(req.DataSamples) > 0 {
		write(w, 422, map[string]string{"error": "dataSamples cannot be supplied to the production prepare API; model data samples come from static definitions or configured SQL sources. Use /api/simulate for caller-supplied test rows"})
		return
	}
	indexes, err := validatePrepareIndexes(req.Indexes, s.store.Config().InputDefinitions)
	if err != nil {
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	model, reason, err := control.Select(s.store.Config().Models, indexes)
	if err != nil {
		if errors.Is(err, control.ErrNoMatchingModel) {
			write(w, http.StatusOK, map[string]interface{}{
				"status":      "no_match",
				"disposition": "no_control_recommendation",
				"outputs":     []control.OutputValue{},
			})
			return
		}
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	context, ignoredExternalInputs, err := prepareExecutionContext(model, indexes, req.ExternalInputs)
	if err != nil {
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	runID := newRunID()
	requestID := r.Header.Get("X-Request-ID")
	recordFailure := func(parameters map[string]interface{}, samples map[string][]map[string]interface{}, outputs []control.OutputValue, failureCode, failureMessage, disposition string) {
		if !dryRun {
			s.recordExecutionFailure(runID, requestID, model, context, parameters, samples, outputs, failureCode, failureMessage, disposition)
		}
	}
	limitResults := map[string]control.OutputValue{}
	collectLimit := func(result control.OutputValue) { limitResults[result.Parameter] = result }
	values, samples, unresolvedSQL, err := control.ResolveModelWithLimitCollector(model, context, nil, nil, s.store.AdaptiveValues(model, context), s.sqlParameterResolver(r.Context()), s.sqlDataSampleResolver(r.Context()), collectLimit)
	if err != nil {
		recordFailure(values, samples, nil, failureCodeForExecutionError(err), err.Error(), "run_rejected")
		if writeParameterLimitRejection(w, runID, model, err, dryRun) {
			return
		}
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	if len(unresolvedSQL) > 0 {
		missing := make([]map[string]string, 0, len(unresolvedSQL))
		for _, parameter := range unresolvedSQL {
			missing = append(missing, map[string]string{"key": parameter.Key, "query": parameter.Definition})
		}
		recordFailure(values, samples, nil, "PARAMETER_EVALUATION_FAILED", "SQL resolver is not configured", "run_rejected")
		write(w, 501, map[string]interface{}{"error": "SQL resolver is not configured", "unresolvedParameters": missing, "controlModel": map[string]interface{}{"id": model.ID, "revision": model.Revision}})
		return
	}
	adaptiveUpdates := map[string]float64{}
	if commitAdaptive || !s.store.HasUnfinishedRun(model, context) {
		adaptiveUpdates, err = control.CalculateAdaptiveUpdatesForSource(model, values, "parameter")
		if err != nil {
			recordFailure(values, samples, nil, "PARAMETER_EVALUATION_FAILED", err.Error(), "run_rejected")
			write(w, 422, map[string]string{"error": err.Error()})
			return
		}
	}
	if len(adaptiveUpdates) > 0 {
		nextAdaptive := s.store.AdaptiveValues(model, context)
		for key, value := range adaptiveUpdates {
			nextAdaptive[key] = value
		}
		limitResults = map[string]control.OutputValue{}
		collectLimit = func(result control.OutputValue) { limitResults[result.Parameter] = result }
		values, samples, unresolvedSQL, err = control.ResolveModelWithLimitCollector(model, context, values, samples, nextAdaptive, nil, nil, collectLimit)
		if err != nil {
			recordFailure(values, samples, nil, failureCodeForExecutionError(err), err.Error(), "run_rejected")
			if writeParameterLimitRejection(w, runID, model, err, dryRun) {
				return
			}
			write(w, 422, map[string]string{"error": err.Error()})
			return
		}
		if len(unresolvedSQL) > 0 {
			recordFailure(values, samples, nil, "PARAMETER_EVALUATION_FAILED", "failed to reevaluate parameters after adaptive update", "run_rejected")
			write(w, 500, map[string]string{"error": "failed to reevaluate parameters after adaptive update"})
			return
		}
	}
	outputs, err := control.EvaluateResolvedOutputs(model, values, limitResults)
	if err != nil {
		recordFailure(values, samples, nil, failureCodeForExecutionError(err), err.Error(), "run_rejected")
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	disposition := "ready"
	if len(limitResults) > 0 {
		disposition = "engineering_review"
	}
	rejected := []control.OutputValue{}
	rejectionReasons := []string{}
	for _, output := range outputs {
		if output.BoundLimited {
			disposition = "engineering_review"
			if output.LimitAction == "reject" {
				rejected = append(rejected, output)
				numeric, _ := strconv.ParseFloat(fmt.Sprint(output.Value), 64)
				switch {
				case output.LSL != nil && numeric < *output.LSL:
					rejectionReasons = append(rejectionReasons,
						fmt.Sprintf("parameter %q value %v is below LSL %v", output.Parameter, output.Value, *output.LSL))
				case output.USL != nil && numeric > *output.USL:
					rejectionReasons = append(rejectionReasons,
						fmt.Sprintf("parameter %q value %v is above USL %v", output.Parameter, output.Value, *output.USL))
				}
			}
		}
	}
	if len(rejected) > 0 {
		failureMessage := "Run is not allowed: " + strings.Join(rejectionReasons, "; ") + "."
		recordFailure(values, samples, outputs, "PARAMETER_LIMIT_REJECTED", failureMessage, "run_rejected")
		response := map[string]interface{}{
			"status":      "rejected",
			"disposition": "run_rejected",
			"error":       failureMessage,
			"controlModel": map[string]interface{}{
				"id": model.ID, "familyId": model.FamilyID, "name": model.Name,
				"revision": model.Revision,
			},
			"violations": rejected,
		}
		if !dryRun {
			response["runId"] = runID
		}
		write(w, 422, response)
		return
	}
	if commitAdaptive {
		if err := s.store.CommitAdaptive(model, context, runID, adaptiveUpdates); err != nil {
			recordFailure(values, samples, outputs, "RUN_PERSISTENCE_FAILED", "failed to persist adaptive state", "run_rejected")
			write(w, 500, map[string]string{"error": "failed to persist adaptive state"})
			return
		}
	}
	if dryRun {
		if err := s.store.CheckPreparedRunLimit(model, context); err != nil {
			write(w, 409, map[string]interface{}{
				"status": "rejected", "disposition": "run_rejected", "error": err.Error(),
				"controlModel": map[string]interface{}{
					"id": model.ID, "familyId": model.FamilyID, "name": model.Name, "revision": model.Revision,
				},
			})
			return
		}
		write(w, 200, map[string]interface{}{
			"requestId": requestID, "status": "simulated",
			"ignoredExternalInputs": ignoredExternalInputs,
			"controlModel": map[string]interface{}{
				"id": model.ID, "familyId": model.FamilyID, "name": model.Name, "revision": model.Revision, "owner": model.Owner,
			},
			"routing": map[string]string{"reason": reason}, "parameters": values,
			"adaptiveUpdates": adaptiveUpdates, "dataSamples": samples, "outputs": outputs,
			"disposition": disposition, "meta": map[string]string{"receivedAt": time.Now().UTC().Format(time.RFC3339)},
		})
		return
	}
	id := runID
	status := "completed"
	if !commitAdaptive {
		status = "prepared"
	}
	result := map[string]interface{}{"id": id, "runId": runID, "requestId": requestID, "status": status, "controlModel": map[string]interface{}{"id": model.ID, "familyId": model.FamilyID, "name": model.Name, "revision": model.Revision, "owner": model.Owner}, "routing": map[string]string{"reason": reason}, "parameters": values, "adaptiveUpdates": adaptiveUpdates, "dataSamples": samples, "outputs": outputs, "ignoredExternalInputs": ignoredExternalInputs, "disposition": disposition, "meta": map[string]string{"receivedAt": time.Now().UTC().Format(time.RFC3339)}}
	record := store.RunRecord{RunID: runID, FamilyID: model.FamilyID, ModelID: model.ID, ModelRevision: model.Revision, Status: status, PreparedAt: time.Now().UTC().Format(time.RFC3339), Context: context, Parameters: values, Outputs: outputs, AdaptiveUpdates: adaptiveUpdates, DataSamples: samples, Disposition: disposition, RequestID: requestID}
	if status != "prepared" {
		record.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	}
	var recordErr error
	var sequence int64
	if commitAdaptive {
		recordErr = s.store.RecordRun(record)
	} else {
		sequence, recordErr = s.store.RecordPreparedRun(record, model, adaptiveUpdates)
	}
	if recordErr != nil {
		statusCode := 500
		if !commitAdaptive {
			statusCode = 409
		}
		write(w, statusCode, map[string]string{"error": recordErr.Error()})
		return
	}
	if sequence > 0 {
		result["sequence"] = sequence
	}
	write(w, 200, result)
}

func (s *server) recordExecutionFailure(runID, requestID string, model control.Model, context, parameters map[string]interface{}, samples map[string][]map[string]interface{}, outputs []control.OutputValue, failureCode, failureMessage, disposition string) {
	if parameters == nil {
		parameters = map[string]interface{}{}
	}
	if samples == nil {
		samples = map[string][]map[string]interface{}{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	record := store.RunRecord{
		RunID: runID, FamilyID: model.FamilyID, ModelID: model.ID, ModelRevision: model.Revision,
		Status: "run_failed", PreparedAt: now, CompletedAt: now, Context: context,
		Parameters: parameters, Outputs: outputs, DataSamples: samples, Disposition: disposition,
		RequestID: requestID, FailureCode: failureCode, FailureMessage: truncateFailureMessage(failureMessage),
	}
	if err := s.store.RecordRun(record); err != nil {
		operationalLog("ERROR", "failed_run_record_write_failed", map[string]interface{}{
			"requestId": requestID, "runId": runID, "error": err.Error(),
		})
	}
}

func failureCodeForExecutionError(err error) string {
	var limitErr *control.ParameterLimitError
	if errors.As(err, &limitErr) {
		return "PARAMETER_LIMIT_REJECTED"
	}
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout") || strings.Contains(message, "deadline exceeded"):
		return "DATABASE_QUERY_TIMEOUT"
	case strings.Contains(message, "data sample") && strings.Contains(message, "query failed"):
		return "DATA_SAMPLE_QUERY_FAILED"
	case strings.Contains(message, "sql parameter") && strings.Contains(message, "query failed"):
		return "PARAMETER_QUERY_FAILED"
	case strings.Contains(message, "data sample") && (strings.Contains(message, "column") || strings.Contains(message, "schema")):
		return "DATA_SAMPLE_SCHEMA_FAILED"
	case strings.Contains(message, "type") || strings.Contains(message, "must be a number") || strings.Contains(message, "must be text") || strings.Contains(message, "must be boolean"):
		return "PARAMETER_TYPE_MISMATCH"
	default:
		return "PARAMETER_EVALUATION_FAILED"
	}
}

func truncateFailureMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 2000 {
		return message[:2000]
	}
	return message
}

func writeParameterLimitRejection(w http.ResponseWriter, runID string, model control.Model, resolveErr error, dryRun bool) bool {
	var limitErr *control.ParameterLimitError
	if !errors.As(resolveErr, &limitErr) {
		return false
	}
	response := map[string]interface{}{
		"status":      "rejected",
		"disposition": "run_rejected",
		"error":       "Run is not allowed: " + limitErr.Message + ".",
		"controlModel": map[string]interface{}{
			"id": model.ID, "familyId": model.FamilyID, "name": model.Name,
			"revision": model.Revision,
		},
		"violations": []control.OutputValue{limitErr.Violation},
	}
	if !dryRun {
		response["runId"] = runID
	}
	write(w, http.StatusUnprocessableEntity, response)
	return true
}

func (s *server) recentRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	runs := s.store.RecentRuns(20)
	models := s.store.Config().Models
	modelNames := map[string]string{}
	for _, model := range models {
		modelNames[model.ID] = model.Name
	}
	result := make([]map[string]interface{}, 0, len(runs))
	for _, run := range runs {
		summary := fmt.Sprintf("%d outputs", len(run.Outputs))
		if run.FailureMessage != "" {
			summary = run.FailureMessage
		} else if len(run.Outputs) > 0 {
			summary = fmt.Sprintf("%s = %v %s", run.Outputs[0].Parameter, run.Outputs[0].Value, run.Outputs[0].Unit)
		}
		result = append(result, map[string]interface{}{
			"id": run.RunID, "task": recentRunTask(run.Context),
			"model": fmt.Sprintf("%s · Rev %d", modelNames[run.ModelID], run.ModelRevision), "modelId": run.ModelID,
			"familyId": run.FamilyID, "status": run.Status, "parameters": run.Parameters, "outputs": run.Outputs,
			"reason": summary, "time": run.PreparedAt, "requestId": run.RequestID,
			"failureCode": run.FailureCode, "failureMessage": run.FailureMessage,
		})
	}
	write(w, 200, result)
}

func recentRunTask(context map[string]interface{}) string {
	subject := firstContextText(context, "lotId", "lot_id", "wafer_id", "waferId", "material", "machine")
	action := firstContextText(context, "action", "operation")
	if subject == "" {
		subject = "Run"
	}
	if action == "" {
		return subject
	}
	return subject + " · " + action
}

func firstContextText(context map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, exists := context[key]
		if !exists || value == nil {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}
func (s *server) complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[4] != "complete" {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	runID := parts[3]
	record, exists := s.store.Run(runID)
	if !exists {
		write(w, 404, map[string]string{"error": "prepared run not found"})
		return
	}
	model, exists := s.store.Model(record.ModelID)
	if !exists {
		write(w, 404, map[string]string{"error": "model revision not found"})
		return
	}
	if record.Status != "prepared" {
		write(w, 409, map[string]string{"error": "run completion was already reported"})
		return
	}
	var completion struct {
		Status         string                 `json:"status"`
		Feedback       map[string]interface{} `json:"feedback,omitempty"`
		FailureMessage string                 `json:"failureMessage,omitempty"`
	}
	if err := decode(r, &completion); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if completion.Status != "succeeded" && completion.Status != "failed" && completion.Status != "cancelled" {
		write(w, 422, map[string]string{"error": "completion status must be succeeded, failed, or cancelled"})
		return
	}
	feedback := completion.Feedback
	if feedback == nil {
		feedback = map[string]interface{}{}
	}
	if completion.Status == "succeeded" {
		allowed := map[string]bool{}
		for _, definition := range model.FeedbackInputs {
			allowed[definition.Name] = true
			value, supplied := feedback[definition.Name]
			if definition.Required && !supplied {
				write(w, 422, map[string]string{"error": "missing required feedback: " + definition.Name})
				return
			}
			if supplied && definition.Type == "number" {
				if _, err := strconv.ParseFloat(fmt.Sprint(value), 64); err != nil {
					write(w, 422, map[string]string{"error": "feedback " + definition.Name + " must be a number"})
					return
				}
			}
			if supplied && definition.Type == "string" {
				if _, ok := value.(string); !ok {
					write(w, 422, map[string]string{"error": "feedback " + definition.Name + " must be text"})
					return
				}
			}
		}
		for name := range feedback {
			if !allowed[name] {
				write(w, 422, map[string]string{"error": "unknown feedback field: " + name})
				return
			}
		}
	}
	updates := map[string]float64{}
	status := "completed"
	if completion.Status != "succeeded" {
		status = "run_" + completion.Status
	}
	ignored := []string{}
	if completion.Status == "succeeded" {
		var completeErr error
		updates, ignored, completeErr = s.store.CompleteRunWithFeedbackAdaptive(runID, model, feedback)
		if completeErr != nil {
			write(w, 409, map[string]string{"error": completeErr.Error()})
			return
		}
	} else {
		failureCode := ""
		failureMessage := ""
		if completion.Status == "failed" {
			failureCode = "EXTERNAL_PROCESS_FAILED"
			failureMessage = truncateFailureMessage(completion.FailureMessage)
			if failureMessage == "" {
				failureMessage = "The external application reported that processing failed."
			}
		}
		if err := s.store.CompleteRun(runID, status, feedback, updates, failureCode, failureMessage); err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
	}
	write(w, 200, map[string]interface{}{"runId": runID, "status": status, "adaptiveUpdates": updates, "staleAdaptiveUpdates": ignored})
}
