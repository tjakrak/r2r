package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"r2r/internal/control"
	"r2r/internal/store"
	"regexp"
	"strconv"
	"strings"
)

type simulationRequest struct {
	CSV        string `json:"csv"`
	Sequential bool   `json:"sequential"`
	ModelID    string `json:"modelId,omitempty"`
}

var indexedSimulationSampleHeader = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\[([0-9]+)\]\.([A-Za-z_][A-Za-z0-9_]*)$`)

func addSimulationSampleValue(samples map[string][]map[string]interface{}, header, rawValue string) bool {
	if match := indexedSimulationSampleHeader.FindStringSubmatch(header); len(match) == 4 {
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return true
		}
		ordinal, _ := strconv.Atoi(match[2])
		for len(samples[match[1]]) <= ordinal {
			samples[match[1]] = append(samples[match[1]], map[string]interface{}{})
		}
		samples[match[1]][ordinal][match[3]] = value
		return true
	}
	if parts := strings.SplitN(header, ".", 2); len(parts) == 2 {
		if len(samples[parts[0]]) == 0 {
			samples[parts[0]] = []map[string]interface{}{{}}
		}
		samples[parts[0]][0][parts[1]] = strings.TrimSpace(rawValue)
		return true
	}
	return false
}

func (s *server) simulate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var request simulationRequest
	if err := decode(r, &request); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if request.ModelID != "" && !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
		return
	}
	reader := csv.NewReader(bytes.NewBufferString(request.CSV))
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		write(w, 400, map[string]string{"error": "CSV needs a header and at least one data row"})
		return
	}
	if len(records) > 501 {
		write(w, 400, map[string]string{"error": "simulation is limited to 500 rows"})
		return
	}
	headers := records[0]
	results := make([]map[string]interface{}, 0, len(records)-1)
	simulationAdaptive := map[string]float64{}
	for rowIndex, record := range records[1:] {
		result := map[string]interface{}{"row": rowIndex + 1}
		if len(record) != len(headers) {
			result["status"] = "failed"
			result["error"] = "column count does not match header"
			results = append(results, result)
			continue
		}
		values := map[string]interface{}{}
		samples := map[string][]map[string]interface{}{}
		for index, header := range headers {
			header = strings.TrimSpace(header)
			values[header] = strings.TrimSpace(record[index])
			addSimulationSampleValue(samples, header, record[index])
		}
		var model control.Model
		var reason string
		var selectErr error
		if request.ModelID != "" {
			var exists bool
			model, exists = s.store.Model(request.ModelID)
			if !exists {
				selectErr = errors.New("selected draft revision was not found")
			} else if model.Status != "draft" && model.Status != "pending_approval" && model.Status != "approved" {
				selectErr = errors.New("selected revision is not available for simulation")
			} else {
				reason = "Explicit revision simulation"
			}
		} else {
			model, reason, selectErr = control.Select(s.store.Config().Models, values)
		}
		if selectErr != nil {
			result["status"] = "failed"
			result["error"] = selectErr.Error()
			results = append(results, result)
			continue
		}
		if validationErr := control.ValidateModel(model); validationErr != nil {
			result["status"] = "failed"
			result["error"] = "model is not ready for simulation: " + validationErr.Error()
			results = append(results, result)
			continue
		}
		dataSampleSources := map[string]string{}
		for _, definition := range model.DataSamples {
			source := "model static table"
			if definition.Source == "sql" {
				if len(samples[definition.Key]) > 0 {
					source = "CSV"
				} else {
					source = "connected database"
				}
			}
			dataSampleSources[definition.Key] = source
		}
		result["dataSampleSources"] = dataSampleSources
		if coercionErr := coerceSimulationValues(model, values, samples); coercionErr != nil {
			result["status"] = "failed"
			result["error"] = coercionErr.Error()
			results = append(results, result)
			continue
		}
		requestValues := make(map[string]interface{}, len(values))
		for name, value := range values {
			requestValues[name] = value
		}
		requestSamples := make(map[string][]map[string]interface{}, len(samples))
		for sampleName, rows := range samples {
			requestRows := make([]map[string]interface{}, len(rows))
			for rowIndex, row := range rows {
				requestRows[rowIndex] = make(map[string]interface{}, len(row))
				for column, value := range row {
					requestRows[rowIndex][column] = value
				}
			}
			requestSamples[sampleName] = requestRows
		}
		result["request"] = map[string]interface{}{"values": requestValues, "dataSamples": requestSamples}
		adaptiveValues := s.store.AdaptiveValues(model, values)
		if request.Sequential {
			for _, parameter := range model.Parameters {
				if parameter.Adaptive == nil {
					continue
				}
				key := store.AdaptiveStateKey(model, parameter, values)
				if value, exists := simulationAdaptive[key]; exists {
					adaptiveValues[parameter.Key] = value
				}
			}
		}
		limitResults := map[string]control.OutputValue{}
		collectLimit := func(result control.OutputValue) { limitResults[result.Parameter] = result }
		resolved, resolvedSamples, unresolved, resolveErr := control.ResolveModelWithLimitCollector(model, values, values, samples, adaptiveValues, s.sqlParameterResolver(r.Context()), s.sqlDataSampleResolver(r.Context()), collectLimit)
		if resolveErr != nil {
			result["status"] = "failed"
			result["error"] = resolveErr.Error()
			results = append(results, result)
			continue
		}
		if len(unresolved) > 0 {
			keys := make([]string, 0, len(unresolved))
			for _, parameter := range unresolved {
				keys = append(keys, parameter.Key)
			}
			result["status"] = "failed"
			result["error"] = "missing SQL columns: " + strings.Join(keys, ", ")
			results = append(results, result)
			continue
		}
		updates, updateErr := control.CalculateAdaptiveUpdatesForSource(model, resolved, "parameter")
		if updateErr != nil {
			result["status"] = "failed"
			result["error"] = updateErr.Error()
			results = append(results, result)
			continue
		}
		if len(updates) > 0 {
			for key, value := range updates {
				adaptiveValues[key] = value
			}
			limitResults = map[string]control.OutputValue{}
			collectLimit = func(result control.OutputValue) { limitResults[result.Parameter] = result }
			resolved, resolvedSamples, unresolved, resolveErr = control.ResolveModelWithLimitCollector(model, values, resolved, resolvedSamples, adaptiveValues, nil, nil, collectLimit)
			if resolveErr != nil || len(unresolved) > 0 {
				result["status"] = "failed"
				result["error"] = "failed to reevaluate simulation after adaptive update"
				results = append(results, result)
				continue
			}
		}
		outputs, outputErr := control.EvaluateResolvedOutputs(model, resolved, limitResults)
		if outputErr != nil {
			result["status"] = "failed"
			result["error"] = outputErr.Error()
			results = append(results, result)
			continue
		}
		result["status"] = "completed"
		result["model"] = fmt.Sprintf("%s · Rev %d", model.Name, model.Revision)
		result["routing"] = reason
		result["parameters"] = resolved
		result["dataSamples"] = resolvedSamples
		result["outputs"] = outputs
		result["adaptivePreview"] = updates
		if request.Sequential {
			for parameter, value := range updates {
				for _, definition := range model.Parameters {
					if definition.Key == parameter {
						simulationAdaptive[store.AdaptiveStateKey(model, definition, values)] = value
						break
					}
				}
			}
		}
		results = append(results, result)
	}
	mode := "independent"
	if request.Sequential {
		mode = "sequential"
	}
	write(w, 200, map[string]interface{}{"rows": results, "count": len(results), "committed": false, "mode": mode})
}

func coerceSimulationValues(model control.Model, values map[string]interface{}, samples map[string][]map[string]interface{}) error {
	types := map[string]string{}
	for _, input := range model.ExternalInputs {
		types[strings.ToLower(input.Name)] = input.Type
	}
	for _, parameter := range model.Parameters {
		types[strings.ToLower(parameter.Key)] = parameter.DataType
		if types[strings.ToLower(parameter.Key)] == "" {
			types[strings.ToLower(parameter.Key)] = "number"
		}
	}
	coerce := func(name string, value interface{}, dataType string) (interface{}, error) {
		text := strings.TrimSpace(fmt.Sprint(value))
		switch dataType {
		case "number":
			number, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, fmt.Errorf("simulation value %s must be a number", name)
			}
			return number, nil
		case "boolean":
			boolean, err := strconv.ParseBool(text)
			if err != nil {
				return nil, fmt.Errorf("simulation value %s must be true or false", name)
			}
			return boolean, nil
		default:
			return text, nil
		}
	}
	for name, value := range values {
		if dataType, exists := types[strings.ToLower(name)]; exists {
			converted, err := coerce(name, value, dataType)
			if err != nil {
				return err
			}
			values[name] = converted
		}
	}
	for _, definition := range model.DataSamples {
		columnTypes := map[string]string{}
		for _, column := range definition.Columns {
			columnTypes[strings.ToLower(column.Name)] = column.Type
		}
		for _, row := range samples[definition.Key] {
			for name, value := range row {
				if dataType, exists := columnTypes[strings.ToLower(name)]; exists {
					converted, err := coerce(definition.Key+"."+name, value, dataType)
					if err != nil {
						return err
					}
					row[name] = converted
				}
			}
		}
	}
	return nil
}
