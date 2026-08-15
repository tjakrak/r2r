package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"r2r/internal/control"
	"r2r/internal/store"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBindSQLParametersUsesPreviouslyResolvedValues(t *testing.T) {
	arguments, err := bindSQLParameters(
		"SELECT value FROM metrology WHERE wafer_id = @wafer_id AND target = @target AND duplicate = @target",
		map[string]interface{}{"wafer_id": "W-42", "target": 100.0},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(arguments) != 2 {
		t.Fatalf("expected two unique arguments, got %+v", arguments)
	}
	first := arguments[0].(sql.NamedArg)
	second := arguments[1].(sql.NamedArg)
	if first.Name != "wafer_id" || first.Value != "W-42" || second.Name != "target" || second.Value != 100.0 {
		t.Fatalf("unexpected named arguments: %+v", arguments)
	}
}

func TestBindSQLParametersRejectsLaterOrUnknownValues(t *testing.T) {
	_, err := bindSQLParameters("SELECT value WHERE wafer_id = @wafer_id", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "has not been declared earlier") {
		t.Fatalf("expected missing ordered parameter error, got %v", err)
	}
}

func TestCoerceSimulationValuesUsesDeclaredTypes(t *testing.T) {
	model := control.Model{
		ExternalInputs: []control.ExternalInputDefinition{{Name: "measured_cd", Type: "number"}, {Name: "enabled", Type: "boolean"}},
		DataSamples:    []control.DataSampleDefinition{{Key: "history", Columns: []control.DataSampleColumnDefinition{{Name: "value", Type: "number"}}}},
	}
	values := map[string]interface{}{"measured_cd": "1.5", "enabled": "true"}
	samples := map[string][]map[string]interface{}{"history": {{"value": "2.25"}}}
	if err := coerceSimulationValues(model, values, samples); err != nil {
		t.Fatal(err)
	}
	if values["measured_cd"] != float64(1.5) || values["enabled"] != true || samples["history"][0]["value"] != float64(2.25) {
		t.Fatalf("values were not coerced: %#v %#v", values, samples)
	}
}

func TestModelContractDescribesExternalInputsAndOutputs(t *testing.T) {
	model := control.Model{
		ID: "model-r1", FamilyID: "model", Revision: 1,
		Indexes:        map[string]control.IndexValues{"machine": {"M1", "M2"}},
		ExternalInputs: []control.ExternalInputDefinition{{Name: "measured_cd", Type: "number"}},
		Parameters:     []control.ParameterDefinition{{Key: "sql_value", Source: "sql", DataType: "number"}, {Key: "decision", Source: "expression", DataType: "boolean"}},
		Outputs:        []string{"decision"},
	}
	contract := modelContract(model)
	if len(contract["indexes"].([]map[string]interface{})) != 1 || len(contract["externalInputs"].([]map[string]interface{})) != 1 || len(contract["optionalParameterOverrides"].([]map[string]interface{})) != 1 || len(contract["outputs"].([]map[string]interface{})) != 1 {
		t.Fatalf("incomplete contract: %#v", contract)
	}
}

func TestPrepareSQLQueryRewritesExternalDatabaseParameters(t *testing.T) {
	query := "SELECT value WHERE first = @target OR second = @target AND lot = @lot"
	postgresQuery, postgresArgs, err := prepareSQLQuery("postgres", query, map[string]interface{}{"target": 10, "lot": "L1"})
	if err != nil || postgresQuery != "SELECT value WHERE first = $1 OR second = $1 AND lot = $2" || len(postgresArgs) != 2 {
		t.Fatalf("unexpected PostgreSQL binding: %s %+v %v", postgresQuery, postgresArgs, err)
	}
	mysqlQuery, mysqlArgs, err := prepareSQLQuery("mysql", query, map[string]interface{}{"target": 10, "lot": "L1"})
	if err != nil || mysqlQuery != "SELECT value WHERE first = ? OR second = ? AND lot = ?" || len(mysqlArgs) != 3 {
		t.Fatalf("unexpected MySQL binding: %s %+v %v", mysqlQuery, mysqlArgs, err)
	}
}

func TestPrepareRejectsCallerSuppliedDataSamples(t *testing.T) {
	s := &server{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(`{"dataSamples":{"history":[{"value":1}]}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.prepare(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "cannot be supplied") {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLoginCreatesAuthenticatedRoleSession(t *testing.T) {
	s := &server{
		users: map[string]user{"E300": {EmployeeID: "E300", Username: "engineer", Role: "engineer", Status: "active"}},
		employees: map[string]employeeRecord{
			"E300": {IDNum: "E300", LoginID: "engineer", Email: "engineer@example.test", SupervisorID: "M200"},
		},
		corporatePasswords: map[string]string{"engineer": "secret"}, sessions: map[string]user{}, mu: sync.RWMutex{},
	}
	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"engineer","password":"secret"}`))
	recorder := httptest.NewRecorder()
	s.login(recorder, login)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", recorder.Code, recorder.Body.String())
	}
	response := recorder.Result()
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("expected an HTTP-only session cookie")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(cookies[0])
	recorder = httptest.NewRecorder()
	s.session(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"role":"engineer"`) || !strings.Contains(recorder.Body.String(), `"employeeId":"E300"`) {
		t.Fatalf("session failed: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLoginRejectsAuthenticatedEmployeeWhoIsNotRegisteredInR2R(t *testing.T) {
	s := &server{
		users: map[string]user{}, employees: map[string]employeeRecord{
			"E301": {IDNum: "E301", LoginID: "new.employee", Email: "new.employee@example.test"},
		},
		corporatePasswords: map[string]string{"new.employee": "secret"}, sessions: map[string]user{},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"new.employee","password":"secret"}`))
	recorder := httptest.NewRecorder()
	s.login(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unregistered employee was allowed to sign in: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEngineeringManagerApprovalRoleIsNotAdministrator(t *testing.T) {
	s := &server{sessions: map[string]user{"manager-token": {Username: "manager", Role: "engineering_manager"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/models/model-r2/approve", nil)
	request.AddCookie(&http.Cookie{Name: "r2r_session", Value: "manager-token"})
	if !s.requireRole(httptest.NewRecorder(), request, "engineering_manager", "admin") {
		t.Fatal("engineering manager should be allowed to approve")
	}
	recorder := httptest.NewRecorder()
	if s.requireRole(recorder, request, "admin") || recorder.Code != http.StatusForbidden {
		t.Fatalf("engineering manager unexpectedly received administrator access: %d", recorder.Code)
	}
}

func TestOnlySubmittersDirectSupervisorCanApprove(t *testing.T) {
	model := control.Model{
		ID: "model-r1", FamilyID: "model", Name: "Model", Revision: 1,
		Status: "pending_approval", SubmittedBy: "E300", SubmittedAt: time.Now().UTC().Format(time.RFC3339),
		Indexes:    map[string]control.IndexValues{"machine": {"M1"}, "action": {"RUN"}, "material": {"PRODUCT"}},
		Parameters: []control.ParameterDefinition{{Key: "result", Source: "expression", Definition: "1 + 1"}}, Outputs: []string{"result"},
	}
	data, _ := json.Marshal(store.Config{Models: []control.Model{model}})
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{
		store: st,
		employees: map[string]employeeRecord{
			"E300": {IDNum: "E300", LoginID: "engineer", SupervisorID: "M200"},
			"M200": {IDNum: "M200", LoginID: "manager"},
			"M201": {IDNum: "M201", LoginID: "other.manager"},
		},
		sessions: map[string]user{
			"supervisor-token": {EmployeeID: "M200", Username: "manager", Role: "engineering_manager"},
			"other-token":      {EmployeeID: "M201", Username: "other.manager", Role: "engineering_manager"},
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/models/model-r1/approve", nil)
	request.AddCookie(&http.Cookie{Name: "r2r_session", Value: "other-token"})
	recorder := httptest.NewRecorder()
	s.revisions(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "direct supervisor") {
		t.Fatalf("non-supervisor approval was not rejected: %d %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/models/model-r1/approve", nil)
	request.AddCookie(&http.Cookie{Name: "r2r_session", Value: "supervisor-token"})
	recorder = httptest.NewRecorder()
	s.revisions(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("direct supervisor could not approve: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestImportModelCreatesValidatedDraftAndIgnoresWorkflowMetadata(t *testing.T) {
	data, err := json.Marshal(store.Config{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{
		store: st,
		sessions: map[string]user{
			"engineer-token": {Username: "engineer", Role: "engineer"},
		},
	}
	body := `{
		"id":"trusted-id","familyId":"trusted-family","revision":99,
		"name":"Imported Model","owner":"someone-else","status":"approved","enabled":true,
		"approvedBy":"someone-else","approvedAt":"2026-01-01T00:00:00Z",
		"indexes":{"machine":["M1"],"action":["RUN"],"material":["PRODUCT"]},
		"parameters":[{"key":"result","dataType":"number","source":"expression","definition":"1 + 1","unit":"nm"}],
		"dataSamples":[],"outputs":["result"]
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/models/import", strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: "r2r_session", Value: "engineer-token"})
	recorder := httptest.NewRecorder()
	s.authenticated(s.importModel)(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("import failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var created control.Model
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "imported-model-r1" || created.FamilyID != "imported-model" || created.Revision != 1 {
		t.Fatalf("server did not generate model identity: %+v", created)
	}
	if created.Status != "draft" || created.Enabled || created.Owner != "engineer" || created.CreatedBy != "engineer" || created.ApprovedBy != "" {
		t.Fatalf("imported workflow metadata was trusted: %+v", created)
	}
	if created.ChangeNote != "Imported from JSON" {
		t.Fatalf("missing default import note: %q", created.ChangeNote)
	}
}

func TestOperationalLoggingAddsRequestIDWithoutChangingResponse(t *testing.T) {
	s := &server{sessions: map[string]user{}}
	handler := s.operationalLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write(w, http.StatusCreated, map[string]string{"status": "created"})
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || recorder.Header().Get("X-Request-ID") == "" {
		t.Fatalf("middleware changed response or omitted request ID: %d %+v", recorder.Code, recorder.Header())
	}
}

func TestClientIPSupportsIPv4AndIPv6(t *testing.T) {
	if got := clientIP("192.0.2.10:4173"); got != "192.0.2.10" {
		t.Fatalf("unexpected IPv4 address: %s", got)
	}
	if got := clientIP("[2001:db8::10]:4173"); got != "2001:db8::10" {
		t.Fatalf("unexpected IPv6 address: %s", got)
	}
}

func TestCompleteRequiresFeedbackAndKeepsRunPrepared(t *testing.T) {
	s, runID := completionTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+runID+"/complete", strings.NewReader(`{"status":"succeeded"}`))
	recorder := httptest.NewRecorder()
	s.complete(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected missing feedback rejection, got %d %s", recorder.Code, recorder.Body.String())
	}
	record, _ := s.store.Run(runID)
	if record.Status != "prepared" {
		t.Fatalf("rejected completion changed run status to %s", record.Status)
	}
}

func TestCompleteAcceptsRequiredFeedbackInSameRequest(t *testing.T) {
	s, runID := completionTestServer(t)
	body := `{"status":"succeeded","feedback":{"measured_thickness":101.4}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+runID+"/complete", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.complete(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("completion failed: %d %s", recorder.Code, recorder.Body.String())
	}
	record, _ := s.store.Run(runID)
	if record.Status != "completed" || record.CompletedAt == "" {
		t.Fatalf("run was not completed: %+v", record)
	}
	if value := s.store.AdaptiveValues(modelForCompletionTest(), nil)["offset"]; value != 52.2 {
		t.Fatalf("completion feedback did not update adaptive state: %g", value)
	}
}

func TestPrepareRejectsOutOfLimitRunWithoutRecordingIt(t *testing.T) {
	model := control.Model{
		ID: "limit-r1", FamilyID: "limit", Name: "Limit", Revision: 1,
		Status: "approved", Enabled: true,
		Indexes: map[string]control.IndexValues{
			"machine": {"M1"}, "action": {"TEST"}, "material": {"OXIDE"},
		},
		Parameters: []control.ParameterDefinition{{
			Key: "result", Source: "formula", Definition: "1 + 1",
			LSL: floatPointer(3), LimitAction: "reject",
		}},
		Outputs: []string{"result"},
	}
	data, _ := json.Marshal(store.Config{Models: []control.Model{model}})
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: st}
	body := `{"runId":"rejected-run","context":{"machine":"M1","action":"TEST","material":"OXIDE"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.prepare(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(recorder.Body.String(), `"disposition":"run_rejected"`) ||
		!strings.Contains(recorder.Body.String(), `parameter \"result\" value 2 is below LSL 3`) {
		t.Fatalf("expected structured run rejection, got %d %s", recorder.Code, recorder.Body.String())
	}
	if _, exists := st.Run("rejected-run"); exists {
		t.Fatal("rejected run was persisted")
	}
}

func floatPointer(value float64) *float64 { return &value }

func completionTestServer(t *testing.T) (*server, string) {
	t.Helper()
	model := modelForCompletionTest()
	data, err := json.Marshal(store.Config{Models: []control.Model{model}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const runID = "run-100"
	if err := st.RecordRun(store.RunRecord{
		RunID: runID, Sequence: 1, ModelID: model.ID, FamilyID: model.FamilyID,
		Status: "prepared", Parameters: map[string]interface{}{"offset": 3.0},
	}); err != nil {
		t.Fatal(err)
	}
	return &server{store: st}, runID
}

func modelForCompletionTest() control.Model {
	return control.Model{
		ID: "polish-r1", FamilyID: "polish", Name: "Polish", Revision: 1,
		FeedbackInputs: []control.FeedbackInputDefinition{{
			Name: "measured_thickness", Type: "number", Required: true,
		}},
		Parameters: []control.ParameterDefinition{{
			Key: "offset", Source: "constant", Definition: "3",
			Adaptive: &control.AdaptiveDefinition{
				InitialValue: 3, UpdateMethod: "ewma", Alpha: .5,
				UpdateInput: &control.AdaptiveUpdateInput{
					Source: "post-run-feedback", Name: "measured_thickness",
				},
			},
		}},
	}
}
