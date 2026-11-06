package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"r2r/internal/control"
)

type RunSearch struct {
	From      time.Time
	To        time.Time // Exclusive.
	FamilyIDs []string
	FolderID  string
	Status    string
	Offset    int
	Limit     int
}

type RunSearchResult struct {
	Runs  []RunRecord `json:"runs"`
	Total int         `json:"total"`
}

func (s *Store) SearchRuns(filter RunSearch) (RunSearchResult, error) {
	if filter.From.IsZero() || filter.To.IsZero() || !filter.From.Before(filter.To) {
		return RunSearchResult{}, errors.New("a valid date range is required")
	}
	if filter.Limit < 1 || filter.Limit > 2000 || filter.Offset < 0 {
		return RunSearchResult{}, errors.New("invalid run page size or offset")
	}
	if s.db != nil {
		return s.searchRunsSQL(filter)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	folders := make(map[string]string)
	for _, model := range s.config.Models {
		folders[model.FamilyID] = model.FolderID
	}
	selected := make(map[string]bool, len(filter.FamilyIDs))
	for _, id := range filter.FamilyIDs {
		selected[id] = true
	}
	matched := make([]RunRecord, 0)
	for _, run := range s.config.Runs {
		prepared, err := time.Parse(time.RFC3339Nano, run.PreparedAt)
		if err != nil || prepared.Before(filter.From) || !prepared.Before(filter.To) ||
			(filter.Status != "" && run.Status != filter.Status) ||
			(filter.FolderID != "" && folders[run.FamilyID] != filter.FolderID) ||
			(len(selected) > 0 && !selected[run.FamilyID]) {
			continue
		}
		matched = append(matched, run)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].PreparedAt == matched[j].PreparedAt {
			return matched[i].Sequence > matched[j].Sequence
		}
		return matched[i].PreparedAt > matched[j].PreparedAt
	})
	result := RunSearchResult{Runs: []RunRecord{}, Total: len(matched)}
	if filter.Offset < len(matched) {
		end := filter.Offset + filter.Limit
		if end > len(matched) {
			end = len(matched)
		}
		result.Runs = append(result.Runs, matched[filter.Offset:end]...)
	}
	return result, nil
}

func (s *Store) searchRunsSQL(filter RunSearch) (RunSearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := []interface{}{filter.From.UTC(), filter.To.UTC()}
	where := []string{"R.PREPARED_AT_UTC >= @p1", "R.PREPARED_AT_UTC < @p2"}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, fmt.Sprintf("R.STATUS = @p%d", len(args)))
	}
	if filter.FolderID != "" {
		args = append(args, filter.FolderID)
		where = append(where, fmt.Sprintf("F.FOLDER_ID = @p%d", len(args)))
	}
	if len(filter.FamilyIDs) > 0 {
		places := make([]string, 0, len(filter.FamilyIDs))
		for _, id := range filter.FamilyIDs {
			args = append(args, id)
			places = append(places, fmt.Sprintf("@p%d", len(args)))
		}
		where = append(where, "R.FAMILY_ID IN ("+strings.Join(places, ",")+")")
	}
	clause := " FROM PCS.RUNS R JOIN PCS.MODEL_FAMILIES F ON F.FAMILY_ID=R.FAMILY_ID WHERE " + strings.Join(where, " AND ")
	var result RunSearchResult
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT_BIG(*)"+clause, args...).Scan(&result.Total); err != nil {
		return RunSearchResult{}, err
	}
	result.Runs = []RunRecord{}
	if result.Total == 0 || filter.Offset >= result.Total {
		return result, nil
	}
	pageArgs := append(append([]interface{}{}, args...), filter.Offset, filter.Limit)
	query := `SELECT R.RUN_ID,R.RUN_SEQUENCE,R.FAMILY_ID,R.MODEL_ID,R.MODEL_REVISION,R.STATUS,
		COALESCE(R.DISPOSITION,''),R.PREPARED_AT_UTC,R.COMPLETED_AT_UTC,R.CONTEXT_JSON,R.PARAMETERS_JSON,
		R.FEEDBACK_JSON,R.ADAPTIVE_UPDATES_JSON,COALESCE(R.REQUEST_ID,N''),COALESCE(R.FAILURE_CODE,N''),
		COALESCE(R.FAILURE_MESSAGE,N'')` + clause +
		fmt.Sprintf(" ORDER BY R.PREPARED_AT_UTC DESC,R.RUN_SEQUENCE DESC OFFSET @p%d ROWS FETCH NEXT @p%d ROWS ONLY", len(pageArgs)-1, len(pageArgs))
	rows, err := s.db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return RunSearchResult{}, err
	}
	for rows.Next() {
		var run RunRecord
		var prepared time.Time
		var completed sql.NullTime
		var contextJSON, parametersJSON []byte
		var feedbackJSON, adaptiveJSON sql.NullString
		if err := rows.Scan(&run.RunID, &run.Sequence, &run.FamilyID, &run.ModelID, &run.ModelRevision,
			&run.Status, &run.Disposition, &prepared, &completed, &contextJSON, &parametersJSON,
			&feedbackJSON, &adaptiveJSON, &run.RequestID, &run.FailureCode, &run.FailureMessage); err != nil {
			rows.Close()
			return RunSearchResult{}, err
		}
		run.PreparedAt = prepared.UTC().Format(time.RFC3339Nano)
		if completed.Valid {
			run.CompletedAt = completed.Time.UTC().Format(time.RFC3339Nano)
		}
		if err := json.Unmarshal(contextJSON, &run.Context); err != nil {
			rows.Close()
			return RunSearchResult{}, err
		}
		if err := json.Unmarshal(parametersJSON, &run.Parameters); err != nil {
			rows.Close()
			return RunSearchResult{}, err
		}
		if feedbackJSON.Valid {
			if err := json.Unmarshal([]byte(feedbackJSON.String), &run.Feedback); err != nil {
				rows.Close()
				return RunSearchResult{}, err
			}
		}
		if adaptiveJSON.Valid {
			if err := json.Unmarshal([]byte(adaptiveJSON.String), &run.AdaptiveUpdates); err != nil {
				rows.Close()
				return RunSearchResult{}, err
			}
		}
		result.Runs = append(result.Runs, run)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return RunSearchResult{}, err
	}
	rows.Close()
	if err := s.loadSearchDetails(ctx, result.Runs); err != nil {
		return RunSearchResult{}, err
	}
	return result, nil
}

func (s *Store) loadSearchDetails(ctx context.Context, runs []RunRecord) error {
	for start := 0; start < len(runs); start += 200 {
		end := start + 200
		if end > len(runs) {
			end = len(runs)
		}
		ids := make([]interface{}, 0, end-start)
		places := make([]string, 0, end-start)
		positions := make(map[string]int, end-start)
		for index := start; index < end; index++ {
			ids = append(ids, runs[index].RunID)
			places = append(places, fmt.Sprintf("@p%d", len(ids)))
			positions[runs[index].RunID] = index
		}
		in := strings.Join(places, ",")
		outputs, err := s.db.QueryContext(ctx, `SELECT RUN_ID,PARAMETER_NAME,DATA_TYPE,IS_NULL,NUMBER_VALUE,TEXT_VALUE,BOOLEAN_VALUE,
			CALCULATED_NUMBER_VALUE,COALESCE(UNIT,N''),LSL,USL,OUT_OF_LIMIT,COALESCE(LIMIT_ACTION,'')
			FROM PCS.RUN_OUTPUTS WHERE RUN_ID IN (`+in+`) ORDER BY RUN_ID,OUTPUT_ORDINAL`, ids...)
		if err != nil {
			return err
		}
		for outputs.Next() {
			var runID, kind, action string
			var value control.OutputValue
			var isNull bool
			var number, calculated, lsl, usl sql.NullFloat64
			var text sql.NullString
			var boolean sql.NullBool
			if err := outputs.Scan(&runID, &value.Parameter, &kind, &isNull, &number, &text, &boolean,
				&calculated, &value.Unit, &lsl, &usl, &value.BoundLimited, &action); err != nil {
				outputs.Close()
				return err
			}
			if !isNull {
				switch kind {
				case "number":
					value.Value = number.Float64
				case "boolean":
					value.Value = boolean.Bool
				default:
					value.Value = text.String
				}
			}
			if calculated.Valid {
				value.CalculatedValue = calculated.Float64
			}
			if lsl.Valid {
				x := lsl.Float64
				value.LSL = &x
			}
			if usl.Valid {
				x := usl.Float64
				value.USL = &x
			}
			value.LimitAction = action
			runs[positions[runID]].Outputs = append(runs[positions[runID]].Outputs, value)
		}
		if err := outputs.Err(); err != nil {
			outputs.Close()
			return err
		}
		outputs.Close()
		samples, err := s.db.QueryContext(ctx, `SELECT RUN_ID,SAMPLE_NAME,SNAPSHOT_JSON FROM PCS.RUN_DATA_SAMPLES WHERE RUN_ID IN (`+in+`)`, ids...)
		if err != nil {
			return err
		}
		for samples.Next() {
			var runID, name string
			var snapshot sql.NullString
			if err := samples.Scan(&runID, &name, &snapshot); err != nil {
				samples.Close()
				return err
			}
			if !snapshot.Valid {
				continue
			}
			var values []map[string]interface{}
			if err := json.Unmarshal([]byte(snapshot.String), &values); err != nil {
				samples.Close()
				return err
			}
			index := positions[runID]
			if runs[index].DataSamples == nil {
				runs[index].DataSamples = make(map[string][]map[string]interface{})
			}
			runs[index].DataSamples[name] = values
		}
		if err := samples.Err(); err != nil {
			samples.Close()
			return err
		}
		samples.Close()
	}
	return nil
}
