package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"r2r/internal/control"
	"r2r/internal/corporateauth"
	"r2r/internal/store"
	"strings"
	"sync"
	"testing"
	"time"
)

type testAuthenticator struct {
	result corporateauth.Result
}

func (a testAuthenticator) Authenticate(context.Context, string, string) (corporateauth.Result, error) {
	return a.result, nil
}

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

func TestApplicationRelativePathRemovesConfiguredBasePath(t *testing.T) {
	tests := map[string]string{
		"/R2RProcessControl/api/runs/recent": "/api/runs/recent",
		"/R2RProcessControl":                 "/",
		"/api/runs/recent":                   "/api/runs/recent",
		"/another/api/path":                  "/another/api/path",
	}
	for input, expected := range tests {
		if actual := applicationRelativePath(input); actual != expected {
			t.Fatalf("applicationRelativePath(%q) = %q; want %q", input, actual, expected)
		}
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
	if len(contract["indexes"].([]map[string]interface{})) != 1 || len(contract["externalInputs"].([]map[string]interface{})) != 1 || len(contract["outputs"].([]map[string]interface{})) != 1 {
		t.Fatalf("incomplete contract: %#v", contract)
	}
	if _, exists := contract["optionalParameterOverrides"]; exists {
		t.Fatalf("production contract must not advertise parameter overrides: %#v", contract)
	}
}

func TestModelContractMarksOnlyCoreIndexesRequired(t *testing.T) {
	model := control.Model{Indexes: map[string]control.IndexValues{
		"machine": {"M1"},
		"chamber": {"C1"},
	}}
	contract := modelContract(model)
	required := map[string]bool{}
	for _, index := range contract["indexes"].([]map[string]interface{}) {
		required[index["name"].(string)] = index["required"].(bool)
	}
	if !required["machine"] || required["chamber"] {
		t.Fatalf("unexpected index requirements: %#v", required)
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

func TestPrepareRejectsCallerSuppliedRunID(t *testing.T) {
	s := &server{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(`{"runId":"external-id","context":{}}`))
	recorder := httptest.NewRecorder()
	s.prepare(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown field") {
		t.Fatalf("expected caller-supplied runId to be rejected, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPrepareRejectsCallerSuppliedParameterValues(t *testing.T) {
	s := &server{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(`{"parameterValues":{"latest_cd":42.5},"context":{}}`))
	recorder := httptest.NewRecorder()
	s.prepare(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown field") {
		t.Fatalf("expected parameterValues to be rejected, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPrepareRejectsLegacyCombinedContext(t *testing.T) {
	s := &server{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(`{"context":{"machine":"M1"}}`))
	recorder := httptest.NewRecorder()
	s.prepare(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown field") {
		t.Fatalf("expected legacy context to be rejected, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPrepareReturnsNormalNoMatchOutcomeWithoutCreatingRun(t *testing.T) {
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
	s := &server{store: st}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(
		`{"indexes":{"machine":"M1","action":"PROCESS","material":"PRODUCT_A"},"externalInputs":{}}`,
	))
	recorder := httptest.NewRecorder()
	s.prepare(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "no_match" || response["disposition"] != "no_control_recommendation" {
		t.Fatalf("unexpected no-match response: %#v", response)
	}
	outputs, ok := response["outputs"].([]interface{})
	if !ok || len(outputs) != 0 {
		t.Fatalf("expected an empty outputs array: %#v", response["outputs"])
	}
	if _, exists := response["runId"]; exists {
		t.Fatalf("no-match response must not contain a runId: %#v", response)
	}
	if runs := st.RecentRuns(10); len(runs) != 0 {
		t.Fatalf("no-match response created a run: %+v", runs)
	}
}

func TestPrepareIgnoresUndeclaredExternalInputsAfterModelSelection(t *testing.T) {
	model := control.Model{
		ID: "ibe801-r1", FamilyID: "ibe801", Name: "IBE801", Revision: 1,
		Status: "approved", Enabled: true,
		Indexes: map[string]control.IndexValues{
			"machine": {"IBE801"}, "action": {"CALCULATE"}, "material": {"PRODUCT_A"},
		},
		ExternalInputs: []control.ExternalInputDefinition{{Name: "target", Type: "number"}},
		Parameters:     []control.ParameterDefinition{{Key: "result", Source: "expression", Definition: "@target + 1", DataType: "number"}},
		Outputs:        []string{"result"},
	}
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
	s := &server{store: st}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(
		`{"indexes":{"machine":"IBE801","action":"CALCULATE","material":"PRODUCT_A"},"externalInputs":{"target":2,"operation":"BS0P","lot_id":"L1"}}`,
	))
	recorder := httptest.NewRecorder()
	s.prepare(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("prepare failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		RunID                 string                 `json:"runId"`
		IgnoredExternalInputs []string               `json:"ignoredExternalInputs"`
		Parameters            map[string]interface{} `json:"parameters"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.IgnoredExternalInputs) != 2 || response.IgnoredExternalInputs[0] != "lot_id" || response.IgnoredExternalInputs[1] != "operation" {
		t.Fatalf("unexpected ignored inputs: %#v", response.IgnoredExternalInputs)
	}
	if response.Parameters["result"] != float64(3) || response.Parameters["operation"] != nil {
		t.Fatalf("extra input affected evaluation: %#v", response.Parameters)
	}
	run, ok := st.Run(response.RunID)
	if !ok || run.Context["operation"] != nil || run.Context["lot_id"] != nil {
		t.Fatalf("extra inputs were persisted in run context: %+v", run)
	}

	missing := httptest.NewRecorder()
	s.prepare(missing, httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(
		`{"indexes":{"machine":"IBE801","action":"CALCULATE","material":"PRODUCT_A"},"externalInputs":{"operation":"BS0P"}}`,
	)))
	if missing.Code != http.StatusUnprocessableEntity || !strings.Contains(missing.Body.String(), "missing required external input") {
		t.Fatalf("missing declared input was not rejected: %d %s", missing.Code, missing.Body.String())
	}

	wrongType := httptest.NewRecorder()
	s.prepare(wrongType, httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(
		`{"indexes":{"machine":"IBE801","action":"CALCULATE","material":"PRODUCT_A"},"externalInputs":{"target":{},"operation":"BS0P"}}`,
	)))
	if wrongType.Code != http.StatusUnprocessableEntity || !strings.Contains(wrongType.Body.String(), "must be a number") {
		t.Fatalf("wrongly typed declared input was not rejected: %d %s", wrongType.Code, wrongType.Body.String())
	}
}

func TestReadOnlySQLAcceptsOneSelectAndRejectsAdditionalStatements(t *testing.T) {
	query, err := readOnlySQL("  SELECT value FROM measurements; ")
	if err != nil || query != "SELECT value FROM measurements" {
		t.Fatalf("unexpected normalized query %q: %v", query, err)
	}
	for _, invalid := range []string{
		"UPDATE measurements SET value = 1",
		"SELECT value FROM measurements; DELETE FROM measurements",
	} {
		if _, err := readOnlySQL(invalid); err == nil {
			t.Fatalf("expected query to be rejected: %s", invalid)
		}
	}
}

func TestNormalizeSQLTestValueUsesDeclaredType(t *testing.T) {
	number, err := normalizeSQLTestValue(int64(12), "number")
	if err != nil || number != float64(12) {
		t.Fatalf("unexpected number result: %v %v", number, err)
	}
	boolean, err := normalizeSQLTestValue(true, "boolean")
	if err != nil || boolean != true {
		t.Fatalf("unexpected boolean result: %v %v", boolean, err)
	}
	if _, err := normalizeSQLTestValue(int64(12), "string"); err == nil {
		t.Fatal("expected a non-text SQL value to fail text validation")
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

func TestCorporateLoginAutoRegistersUnregisteredEmployeeAsViewer(t *testing.T) {
	data, _ := json.Marshal(store.Config{})
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
		authenticator: testAuthenticator{result: corporateauth.Result{
			Authenticated: true,
			EmployeeID:    "E301",
		}},
		employees: map[string]employeeRecord{
			"E301": {IDNum: "E301", LoginID: "new.employee", FirstName: "New", LastName: "Employee", Email: "new.employee@example.test"},
		},
		sessions: map[string]user{},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"new.employee","password":"valid"}`))
	recorder := httptest.NewRecorder()
	s.login(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"role":"viewer"`) {
		t.Fatalf("corporate login did not create a viewer: %d %s", recorder.Code, recorder.Body.String())
	}
	registrations := st.UserRegistrations()
	if len(registrations) != 1 || registrations[0].EmployeeID != "E301" || registrations[0].Role != "viewer" || registrations[0].Status != "active" {
		t.Fatalf("unexpected auto-registration: %+v", registrations)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"new.employee","password":"valid"}`))
	recorder = httptest.NewRecorder()
	s.login(recorder, request)
	if recorder.Code != http.StatusOK || len(st.UserRegistrations()) != 1 {
		t.Fatalf("repeat login duplicated or rejected registration: %d %+v", recorder.Code, st.UserRegistrations())
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

func TestIndexedSimulationSampleHeadersCreateMultipleRows(t *testing.T) {
	samples := map[string][]map[string]interface{}{}
	if !addSimulationSampleValue(samples, "wafer_history[0].operation", "CLU170") ||
		!addSimulationSampleValue(samples, "wafer_history[1].operation", "CLU160") ||
		!addSimulationSampleValue(samples, "wafer_history[2].measurement", "0.030") {
		t.Fatal("indexed data-sample headers were not recognized")
	}
	if len(samples["wafer_history"]) != 3 {
		t.Fatalf("expected three sample rows, got %#v", samples["wafer_history"])
	}
	if samples["wafer_history"][1]["operation"] != "CLU160" || samples["wafer_history"][2]["measurement"] != "0.030" {
		t.Fatalf("indexed values were assigned to the wrong rows: %#v", samples["wafer_history"])
	}
	if !addSimulationSampleValue(samples, "wafer_history[4].operation", "") || len(samples["wafer_history"]) != 3 {
		t.Fatal("blank indexed values should not create empty sample rows")
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

func TestRecentRunTaskUsesAvailableExecutionIdentity(t *testing.T) {
	tests := []struct {
		name    string
		context map[string]interface{}
		want    string
	}{
		{"legacy lot", map[string]interface{}{"lotId": "LOT-1", "action": "CALCULATE"}, "LOT-1 · CALCULATE"},
		{"wafer", map[string]interface{}{"wafer_id": "W01", "action": "RECIPE_SELECTION"}, "W01 · RECIPE_SELECTION"},
		{"operation fallback", map[string]interface{}{"machine": "M1", "operation": "ARI170"}, "M1 · ARI170"},
		{"missing context", map[string]interface{}{}, "Run"},
		{"nil values", map[string]interface{}{"lotId": nil, "action": nil}, "Run"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recentRunTask(test.context); got != test.want {
				t.Fatalf("recentRunTask() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestListenPort(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("HTTP_PLATFORM_PORT", "")
	if got := listenPort(); got != "4173" {
		t.Fatalf("default listen port = %q, want 4173", got)
	}

	t.Setenv("HTTP_PLATFORM_PORT", "58123")
	if got := listenPort(); got != "58123" {
		t.Fatalf("HTTP platform listen port = %q, want 58123", got)
	}

	t.Setenv("PORT", "9000")
	if got := listenPort(); got != "9000" {
		t.Fatalf("explicit listen port = %q, want 9000", got)
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

func TestCompleteRecordsExternalFailureDetails(t *testing.T) {
	s, runID := completionTestServer(t)
	body := `{"status":"failed","failureMessage":"Equipment interlock activated."}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+runID+"/complete", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.complete(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("failed completion was rejected: %d %s", recorder.Code, recorder.Body.String())
	}
	record, _ := s.store.Run(runID)
	if record.Status != "run_failed" || record.FailureCode != "EXTERNAL_PROCESS_FAILED" || record.FailureMessage != "Equipment interlock activated." {
		t.Fatalf("external failure details were not recorded: %+v", record)
	}
}

func TestPrepareRecordsOutOfLimitRunFailure(t *testing.T) {
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
	body := `{"indexes":{"machine":"M1","action":"TEST","material":"OXIDE"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/prepare", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.prepare(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(recorder.Body.String(), `"disposition":"run_rejected"`) ||
		!strings.Contains(recorder.Body.String(), `parameter \"result\" value 2 is below LSL 3`) {
		t.Fatalf("expected structured run rejection, got %d %s", recorder.Code, recorder.Body.String())
	}
	runs := st.RecentRuns(10)
	if len(runs) != 1 || runs[0].Status != "run_failed" || runs[0].FailureCode != "PARAMETER_LIMIT_REJECTED" {
		t.Fatalf("rejected run failure was not persisted correctly: %+v", runs)
	}
	if !strings.Contains(runs[0].FailureMessage, `parameter "result" value 2 is below LSL 3`) {
		t.Fatalf("rejected run failure message was not persisted: %+v", runs[0])
	}
}

func TestProductionSimulationUsesPrepareContractWithoutPersistingRun(t *testing.T) {
	model := control.Model{
		ID: "simulation-r1", FamilyID: "simulation", Name: "Simulation", Revision: 1,
		Status: "approved", Enabled: true,
		Indexes: map[string]control.IndexValues{
			"machine": {"M1"}, "action": {"TEST"}, "material": {"OXIDE"},
		},
		Parameters: []control.ParameterDefinition{{
			Key: "result", Source: "expression", Definition: "1 + 1", DataType: "number",
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
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/simulate", strings.NewReader(
		`{"indexes":{"machine":"M1","action":"TEST","material":"OXIDE"},"externalInputs":{}}`,
	))
	recorder := httptest.NewRecorder()
	s.simulateProductionRun(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("simulation failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "simulated" {
		t.Fatalf("unexpected simulation response: %#v", response)
	}
	if _, exists := response["simulation"]; exists {
		t.Fatalf("dry-run response must not include a redundant simulation flag: %#v", response)
	}
	if _, exists := response["committed"]; exists {
		t.Fatalf("dry-run response must not include a redundant committed flag: %#v", response)
	}
	if _, exists := response["runId"]; exists {
		t.Fatalf("dry-run response must not expose a runId: %#v", response)
	}
	if runs := st.RecentRuns(10); len(runs) != 0 {
		t.Fatalf("production simulation persisted runs: %+v", runs)
	}
}

func TestRejectedProductionSimulationDoesNotPersistFailure(t *testing.T) {
	model := control.Model{
		ID: "simulation-limit-r1", FamilyID: "simulation-limit", Name: "Simulation limit", Revision: 1,
		Status: "approved", Enabled: true,
		Indexes: map[string]control.IndexValues{
			"machine": {"M1"}, "action": {"TEST"}, "material": {"OXIDE"},
		},
		Parameters: []control.ParameterDefinition{{
			Key: "result", Source: "expression", Definition: "2", DataType: "number",
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
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runs/simulate", strings.NewReader(
		`{"indexes":{"machine":"M1","action":"TEST","material":"OXIDE"},"externalInputs":{}}`,
	))
	recorder := httptest.NewRecorder()
	s.simulateProductionRun(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"status":"rejected"`) {
		t.Fatalf("unexpected rejection: %d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), `"simulation"`) || strings.Contains(recorder.Body.String(), `"committed"`) {
		t.Fatalf("rejected dry run contains redundant flags: %s", recorder.Body.String())
	}
	if runs := st.RecentRuns(10); len(runs) != 0 {
		t.Fatalf("rejected production simulation persisted a failure: %+v", runs)
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
