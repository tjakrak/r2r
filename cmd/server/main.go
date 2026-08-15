package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"r2r/internal/control"
	"r2r/internal/store"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
)

type server struct {
	store              *store.Store
	mu                 sync.RWMutex
	users              map[string]user
	employees          map[string]employeeRecord
	corporatePasswords map[string]string
	sessions           map[string]user
}
type user struct {
	EmployeeID    string   `json:"employeeId"`
	Username      string   `json:"username"`
	Email         string   `json:"email,omitempty"`
	Role          string   `json:"role"`
	Status        string   `json:"status,omitempty"`
	DirectReports []string `json:"directReports,omitempty"`
	Password      string   `json:"-"` // Legacy test compatibility; production authentication is external.
}
type employeeRecord struct {
	IDNum        string `json:"IDNUM"`
	LoginID      string `json:"LOGINID"`
	Email        string `json:"EMAIL"`
	SupervisorID string `json:"SUPID"`
}
type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var operationalLogMu sync.Mutex

func operationalLog(level, event string, fields map[string]interface{}) {
	record := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"severity":  level,
		"event":     event,
		"service":   "r2r-api",
	}
	for key, value := range fields {
		record[key] = value
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	operationalLogMu.Lock()
	defer operationalLogMu.Unlock()
	_, _ = os.Stdout.Write(append(encoded, '\n'))
}

type responseCapture struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (capture *responseCapture) WriteHeader(status int) {
	capture.status = status
	capture.ResponseWriter.WriteHeader(status)
}

func (capture *responseCapture) Write(body []byte) (int, error) {
	if capture.status == 0 {
		capture.WriteHeader(http.StatusOK)
	}
	written, err := capture.ResponseWriter.Write(body)
	capture.bytes += written
	return written, err
}

func newRequestID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func clientIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

func (s *server) operationalLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := newRequestID()
		w.Header().Set("X-Request-ID", requestID)
		capture := &responseCapture{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				operationalLog("ERROR", "http_request_panic", map[string]interface{}{
					"requestId": requestID,
					"method":    r.Method,
					"path":      r.URL.Path,
					"error":     fmt.Sprint(recovered),
				})
				if capture.status == 0 {
					write(capture, 500, map[string]string{"error": "internal server error"})
				}
			}
			actor := ""
			if current, ok := s.currentUser(r); ok {
				actor = actorID(current)
			}
			severity := "INFO"
			if capture.status >= 500 {
				severity = "ERROR"
			} else if capture.status >= 400 {
				severity = "WARN"
			}
			operationalLog(severity, "http_request_completed", map[string]interface{}{
				"requestId":  requestID,
				"method":     r.Method,
				"path":       r.URL.Path,
				"status":     capture.status,
				"bytes":      capture.bytes,
				"durationMs": time.Since(started).Milliseconds(),
				"actor":      actor,
				"clientIp":   clientIP(r.RemoteAddr),
			})
		}()
		next.ServeHTTP(capture, r)
	})
}

func write(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v interface{}) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func (s *server) currentUser(r *http.Request) (user, bool) {
	cookie, err := r.Cookie("r2r_session")
	if err != nil {
		return user{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, ok := s.sessions[cookie.Value]
	return current, ok
}
func (s *server) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentUser(r); !ok {
			write(w, 401, map[string]string{"error": "authentication required"})
			return
		}
		next(w, r)
	}
}
func (s *server) requireRole(w http.ResponseWriter, r *http.Request, roles ...string) bool {
	current, ok := s.currentUser(r)
	if !ok {
		write(w, 401, map[string]string{"error": "authentication required"})
		return false
	}
	for _, role := range roles {
		if current.Role == role {
			return true
		}
	}
	write(w, 403, map[string]string{"error": "your role does not allow this action"})
	return false
}
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var login credentials
	if err := decode(r, &login); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	employee, employeeFound := s.employeeByLoginID(login.Username)
	registered, registeredFound := s.registeredUser(employee.IDNum)
	if !employeeFound || !registeredFound || (registered.Status != "" && registered.Status != "active") {
		write(w, 401, map[string]string{"error": "invalid username or password"})
		return
	}
	expectedPassword := s.corporatePasswords[strings.ToLower(employee.LoginID)]
	if expectedPassword == "" {
		expectedPassword = registered.Password
	}
	if subtle.ConstantTimeCompare([]byte(expectedPassword), []byte(login.Password)) != 1 {
		write(w, 401, map[string]string{"error": "invalid username or password"})
		return
	}
	registered.EmployeeID = employee.IDNum
	registered.Username = employee.LoginID
	registered.Email = employee.Email
	if registered.Status == "" {
		registered.Status = "active"
	}
	registered.DirectReports = nil
	for _, candidate := range s.employees {
		if strings.EqualFold(candidate.SupervisorID, employee.IDNum) {
			registered.DirectReports = append(registered.DirectReports, candidate.IDNum)
		}
	}
	sort.Strings(registered.DirectReports)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		write(w, 500, map[string]string{"error": "failed to create session"})
		return
	}
	token := hex.EncodeToString(tokenBytes)
	s.mu.Lock()
	s.sessions[token] = registered
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "r2r_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
	write(w, 200, registered)
}

func (s *server) employeeByLoginID(loginID string) (employeeRecord, bool) {
	for _, employee := range s.employees {
		if strings.EqualFold(strings.TrimSpace(employee.LoginID), strings.TrimSpace(loginID)) {
			return employee, true
		}
	}
	return employeeRecord{}, false
}

func (s *server) employeeByID(employeeID string) (employeeRecord, bool) {
	employee, ok := s.employees[strings.TrimSpace(employeeID)]
	if ok {
		return employee, true
	}
	for _, candidate := range s.employees {
		if strings.EqualFold(candidate.IDNum, employeeID) {
			return candidate, true
		}
	}
	return employeeRecord{}, false
}

func (s *server) registeredUser(employeeID string) (user, bool) {
	if s.store != nil {
		for _, registration := range s.store.UserRegistrations() {
			if strings.EqualFold(registration.EmployeeID, employeeID) {
				return user{EmployeeID: registration.EmployeeID, Username: registration.LoginID, Email: registration.Email, Role: registration.Role, Status: registration.Status}, true
			}
		}
	}
	for _, candidate := range s.users {
		if strings.EqualFold(candidate.EmployeeID, employeeID) ||
			(candidate.EmployeeID == "" && strings.EqualFold(candidate.Username, employeeID)) {
			return candidate, true
		}
	}
	return user{}, false
}

func actorID(current user) string {
	if strings.TrimSpace(current.EmployeeID) != "" {
		return current.EmployeeID
	}
	return current.Username
}
func (s *server) session(w http.ResponseWriter, r *http.Request) {
	current, ok := s.currentUser(r)
	if !ok {
		write(w, 401, map[string]string{"error": "authentication required"})
		return
	}
	write(w, 200, current)
}
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("r2r_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "r2r_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	write(w, 200, map[string]string{"status": "logged_out"})
}
func (s *server) config(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	write(w, 200, s.store.Config())
}
func (s *server) dependencies(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var model control.Model
	if err := decode(r, &model); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	graph, err := control.BuildDependencyGraph(model)
	if err != nil {
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	write(w, 200, graph)
}
func (s *server) prepare(w http.ResponseWriter, r *http.Request) {
	s.executeRun(w, r, false)
}
func (s *server) executeRun(w http.ResponseWriter, r *http.Request, commitAdaptive bool) {
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
	model, reason, err := control.Select(s.store.Config().Models, req.Context)
	if err != nil {
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	hasAdaptive := false
	for _, parameter := range model.Parameters {
		if parameter.Adaptive != nil {
			hasAdaptive = true
			break
		}
	}
	if hasAdaptive && strings.TrimSpace(req.RunID) == "" {
		write(w, 400, map[string]string{"error": "runId is required when the model has adaptive parameters"})
		return
	}
	limitResults := map[string]control.OutputValue{}
	collectLimit := func(result control.OutputValue) { limitResults[result.Parameter] = result }
	values, samples, unresolvedSQL, err := control.ResolveModelWithLimitCollector(model, req.Context, req.ParameterValues, nil, s.store.AdaptiveValues(model, req.Context), s.sqlParameterResolver(r.Context()), s.sqlDataSampleResolver(r.Context()), collectLimit)
	if err != nil {
		if writeParameterLimitRejection(w, req, model, err) {
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
		write(w, 501, map[string]interface{}{"error": "SQL resolver is not configured", "unresolvedParameters": missing, "controlModel": map[string]interface{}{"id": model.ID, "revision": model.Revision}})
		return
	}
	adaptiveUpdates := map[string]float64{}
	if commitAdaptive || !s.store.HasUnfinishedRun(model, req.Context) {
		adaptiveUpdates, err = control.CalculateAdaptiveUpdatesForSource(model, values, "parameter")
		if err != nil {
			write(w, 422, map[string]string{"error": err.Error()})
			return
		}
	}
	if len(adaptiveUpdates) > 0 {
		nextAdaptive := s.store.AdaptiveValues(model, req.Context)
		for key, value := range adaptiveUpdates {
			nextAdaptive[key] = value
		}
		limitResults = map[string]control.OutputValue{}
		collectLimit = func(result control.OutputValue) { limitResults[result.Parameter] = result }
		values, samples, unresolvedSQL, err = control.ResolveModelWithLimitCollector(model, req.Context, values, samples, nextAdaptive, nil, nil, collectLimit)
		if err != nil {
			if writeParameterLimitRejection(w, req, model, err) {
				return
			}
			write(w, 422, map[string]string{"error": err.Error()})
			return
		}
		if len(unresolvedSQL) > 0 {
			write(w, 500, map[string]string{"error": "failed to reevaluate parameters after adaptive update"})
			return
		}
	}
	outputs, err := control.EvaluateResolvedOutputs(model, values, limitResults)
	if err != nil {
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
		write(w, 422, map[string]interface{}{
			"status":      "rejected",
			"disposition": "run_rejected",
			"error":       "Run is not allowed: " + strings.Join(rejectionReasons, "; ") + ".",
			"runId":       req.RunID,
			"controlModel": map[string]interface{}{
				"id": model.ID, "familyId": model.FamilyID, "name": model.Name,
				"revision": model.Revision,
			},
			"violations": rejected,
		})
		return
	}
	if commitAdaptive {
		if err := s.store.CommitAdaptive(model, req.Context, req.RunID, adaptiveUpdates); err != nil {
			write(w, 500, map[string]string{"error": "failed to persist adaptive state"})
			return
		}
	}
	id := fmt.Sprintf("r2r_%d", time.Now().UnixNano())
	runID := req.RunID
	if strings.TrimSpace(runID) == "" {
		runID = id
	}
	status := "completed"
	if !commitAdaptive {
		status = "prepared"
	}
	result := map[string]interface{}{"id": id, "runId": runID, "status": status, "controlModel": map[string]interface{}{"id": model.ID, "familyId": model.FamilyID, "name": model.Name, "revision": model.Revision, "owner": model.Owner}, "routing": map[string]string{"reason": reason}, "parameters": values, "adaptiveUpdates": adaptiveUpdates, "dataSamples": samples, "outputs": outputs, "disposition": disposition, "meta": map[string]string{"receivedAt": time.Now().UTC().Format(time.RFC3339)}}
	record := store.RunRecord{RunID: runID, FamilyID: model.FamilyID, ModelID: model.ID, ModelRevision: model.Revision, Status: status, PreparedAt: time.Now().UTC().Format(time.RFC3339), Context: req.Context, Parameters: values, Outputs: outputs, AdaptiveUpdates: adaptiveUpdates}
	var recordErr error
	var sequence int64
	if commitAdaptive {
		recordErr = s.store.RecordRun(record)
	} else {
		sequence, recordErr = s.store.RecordPreparedRun(record, model, adaptiveUpdates)
	}
	if recordErr != nil && !commitAdaptive {
		write(w, 409, map[string]string{"error": recordErr.Error()})
		return
	}
	if sequence > 0 {
		result["sequence"] = sequence
	}
	write(w, 200, result)
}

func writeParameterLimitRejection(w http.ResponseWriter, req control.Request, model control.Model, resolveErr error) bool {
	var limitErr *control.ParameterLimitError
	if !errors.As(resolveErr, &limitErr) {
		return false
	}
	write(w, http.StatusUnprocessableEntity, map[string]interface{}{
		"status":      "rejected",
		"disposition": "run_rejected",
		"error":       "Run is not allowed: " + limitErr.Message + ".",
		"runId":       req.RunID,
		"controlModel": map[string]interface{}{
			"id": model.ID, "familyId": model.FamilyID, "name": model.Name,
			"revision": model.Revision,
		},
		"violations": []control.OutputValue{limitErr.Violation},
	})
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
		if len(run.Outputs) > 0 {
			summary = fmt.Sprintf("%s = %v %s", run.Outputs[0].Parameter, run.Outputs[0].Value, run.Outputs[0].Unit)
		}
		result = append(result, map[string]interface{}{
			"id": run.RunID, "task": fmt.Sprintf("%v · %v", run.Context["lotId"], run.Context["action"]),
			"model": fmt.Sprintf("%s · Rev %d", modelNames[run.ModelID], run.ModelRevision), "modelId": run.ModelID,
			"familyId": run.FamilyID, "status": run.Status, "parameters": run.Parameters, "outputs": run.Outputs,
			"reason": summary, "time": run.PreparedAt,
		})
	}
	write(w, 200, result)
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
		Status   string                 `json:"status"`
		Feedback map[string]interface{} `json:"feedback,omitempty"`
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
		if err := s.store.CompleteRun(runID, status, feedback, updates); err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
	}
	write(w, 200, map[string]interface{}{"runId": runID, "status": status, "adaptiveUpdates": updates, "staleAdaptiveUpdates": ignored})
}
func (s *server) revisions(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 3 && r.Method == "DELETE" {
		if !s.requireRole(w, r, "admin") {
			return
		}
		if err := s.store.DeleteFamily(parts[2]); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, map[string]string{"status": "deleted"})
		return
	}
	if len(parts) < 4 {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	id := parts[2]
	action := parts[3]
	if action == "revision" && r.Method == "DELETE" {
		if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
			return
		}
		if err := s.store.DeleteRevision(id); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, map[string]string{"status": "deleted"})
		return
	}
	if action == "revision" && r.Method == "PUT" {
		if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
			return
		}
		current, _ := s.currentUser(r)
		var next control.Model
		if err := decode(r, &next); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		next.Owner = actorID(current)
		next.CreatedBy = actorID(current)
		updated, err := s.store.UpdateDraft(id, next)
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, updated)
		return
	}
	if action == "transfer" && r.Method == "POST" {
		if !s.requireRole(w, r, "admin") {
			return
		}
		var request struct {
			EmployeeID string `json:"employeeId"`
		}
		if err := decode(r, &request); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		newOwner, registered := s.registeredUser(request.EmployeeID)
		if !registered || newOwner.Status != "active" || (newOwner.Role != "engineer" && newOwner.Role != "admin") {
			write(w, 400, map[string]string{"error": "the new owner must be an active registered engineer or administrator"})
			return
		}
		transferred, err := s.store.TransferDraftOwner(id, request.EmployeeID)
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, transferred)
		return
	}
	if action == "history" && r.Method == "GET" {
		model, ok := s.store.Model(id)
		if !ok {
			write(w, 404, map[string]string{"error": "model not found"})
			return
		}
		write(w, 200, s.store.History(model.FamilyID))
		return
	}
	if action == "contract" && r.Method == "GET" {
		model, ok := s.store.Model(id)
		if !ok {
			write(w, 404, map[string]string{"error": "model not found"})
			return
		}
		write(w, 200, modelContract(model))
		return
	}
	if action == "runs" && r.Method == "GET" {
		model, ok := s.store.Model(id)
		if !ok {
			write(w, 404, map[string]string{"error": "model not found"})
			return
		}
		write(w, 200, s.store.RunsForFamily(model.FamilyID, time.Now().UTC().Add(-14*24*time.Hour)))
		return
	}
	if action == "revisions" && r.Method == "POST" {
		if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
			return
		}
		current, _ := s.currentUser(r)
		var next control.Model
		if err := decode(r, &next); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		next.Owner = actorID(current)
		next.CreatedBy = actorID(current)
		created, err := s.store.CreateRevision(id, next)
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 201, created)
		return
	}
	if action == "approve" && r.Method == "POST" {
		if !s.requireRole(w, r, "engineering_manager", "admin") {
			return
		}
		current, _ := s.currentUser(r)
		target, found := s.store.Model(id)
		if !found {
			write(w, 404, map[string]string{"error": "model revision not found"})
			return
		}
		if current.Role != "admin" {
			submitter, found := s.employeeByID(target.SubmittedBy)
			if !found || strings.TrimSpace(submitter.SupervisorID) == "" {
				write(w, 409, map[string]string{"error": "the submitter's supervisor could not be resolved from the corporate directory"})
				return
			}
			if !strings.EqualFold(strings.TrimSpace(submitter.SupervisorID), strings.TrimSpace(current.EmployeeID)) {
				write(w, 403, map[string]string{"error": "only the submitter's direct supervisor or an administrator can approve this revision"})
				return
			}
		}
		approved, err := s.store.Approve(id, actorID(current), current.Role == "admin")
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, approved)
		return
	}
	if action == "submit" && r.Method == "POST" {
		if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
			return
		}
		current, _ := s.currentUser(r)
		submitted, err := s.store.SubmitForApproval(id, actorID(current))
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, submitted)
		return
	}
	if action == "reject" && r.Method == "POST" {
		if !s.requireRole(w, r, "engineering_manager", "admin") {
			return
		}
		var request struct {
			Reason string `json:"reason"`
		}
		if err := decode(r, &request); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		current, _ := s.currentUser(r)
		rejected, err := s.store.Reject(id, actorID(current), request.Reason)
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, rejected)
		return
	}
	write(w, 404, map[string]string{"error": "not found"})
}

func modelContract(model control.Model) map[string]interface{} {
	indexes := []map[string]interface{}{}
	indexNames := make([]string, 0, len(model.Indexes))
	for name := range model.Indexes {
		indexNames = append(indexNames, name)
	}
	sort.Strings(indexNames)
	for _, name := range indexNames {
		indexes = append(indexes, map[string]interface{}{"name": name, "type": "string", "required": true, "acceptedValues": []string(model.Indexes[name])})
	}
	externalInputs := []map[string]interface{}{}
	for _, input := range model.ExternalInputs {
		externalInputs = append(externalInputs, map[string]interface{}{"name": input.Name, "type": input.Type, "required": true, "description": input.Description})
	}
	overrides := []map[string]interface{}{}
	for _, parameter := range model.Parameters {
		if parameter.Source == "sql" {
			overrides = append(overrides, map[string]interface{}{"name": parameter.Key, "type": parameterDataTypeForContract(parameter), "required": false, "description": parameter.Description})
		}
	}
	outputs := []map[string]interface{}{}
	definitions := map[string]control.ParameterDefinition{}
	for _, parameter := range model.Parameters {
		definitions[parameter.Key] = parameter
	}
	for _, name := range model.Outputs {
		definition := definitions[name]
		outputs = append(outputs, map[string]interface{}{"name": name, "type": parameterDataTypeForContract(definition), "unit": definition.Unit})
	}
	return map[string]interface{}{
		"modelId": model.ID, "familyId": model.FamilyID, "revision": model.Revision,
		"endpoint": "/api/v1/runs/prepare", "indexes": indexes, "externalInputs": externalInputs,
		"optionalParameterOverrides": overrides, "outputs": outputs,
	}
}

func parameterDataTypeForContract(parameter control.ParameterDefinition) string {
	if parameter.DataType == "" {
		return "number"
	}
	return parameter.DataType
}
func (s *server) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
		return
	}
	current, _ := s.currentUser(r)
	var model control.Model
	if err := decode(r, &model); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	model.Owner = actorID(current)
	model.CreatedBy = actorID(current)
	created, err := s.store.CreateModel(model)
	if err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	write(w, 201, created)
}

func (s *server) importModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
		return
	}
	current, _ := s.currentUser(r)
	var model control.Model
	if err := decode(r, &model); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}

	// Identity and workflow state always come from R2R, never from imported JSON.
	model.ID = ""
	model.FamilyID = ""
	model.Revision = 0
	model.Status = "draft"
	model.Enabled = false
	model.Owner = actorID(current)
	model.CreatedBy = actorID(current)
	model.CreatedAt = ""
	model.SubmittedBy = ""
	model.SubmittedAt = ""
	model.RejectedBy = ""
	model.RejectedAt = ""
	model.RejectionReason = ""
	model.ApprovedBy = ""
	model.ApprovedAt = ""
	if strings.TrimSpace(model.ChangeNote) == "" {
		model.ChangeNote = "Imported from JSON"
	}

	created, err := s.store.CreateModel(model)
	if err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	write(w, 201, created)
}
func (s *server) sources(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sources/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 2 && parts[1] == "test" {
		s.testSource(w, r, parts[0])
		return
	}
	if path == "" && r.Method == "POST" {
		if !s.requireRole(w, r, "admin") {
			return
		}
		var source store.Source
		if err := decode(r, &source); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		created, err := s.store.CreateSource(source)
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 201, created)
		return
	}
	if r.Method != "PUT" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.requireRole(w, r, "admin") {
		return
	}
	id := path
	if id == "" {
		write(w, 404, map[string]string{"error": "data source not found"})
		return
	}
	var source store.Source
	if err := decode(r, &source); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	updated, err := s.store.UpdateSource(id, source)
	if err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	write(w, 200, updated)
}

func (s *server) inputDefinitions(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, "admin") {
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/input-definitions/"), "/")
	if r.Method == "DELETE" && name != "" {
		if err := s.store.DeleteInputDefinition(name); err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, map[string]string{"status": "deleted"})
		return
	}
	var definition store.InputDefinition
	if err := decode(r, &definition); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if r.Method == "POST" && name == "" {
		created, err := s.store.CreateInputDefinition(definition)
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 201, created)
		return
	}
	if r.Method == "PUT" && name != "" {
		updated, err := s.store.UpdateInputDefinition(name, definition)
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, updated)
		return
	}
	write(w, 405, map[string]string{"error": "method not allowed"})
}

func (s *server) applicationUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, "admin") {
		return
	}
	employeeID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/users"), "/")
	if r.Method == "DELETE" && employeeID != "" {
		current, _ := s.currentUser(r)
		if strings.EqualFold(actorID(current), employeeID) {
			write(w, 409, map[string]string{"error": "you cannot remove your own R2R registration"})
			return
		}
		registrations := s.store.UserRegistrations()
		targetRole := ""
		adminCount := 0
		for _, registration := range registrations {
			if registration.Role == "admin" && registration.Status == "active" {
				adminCount++
			}
			if strings.EqualFold(registration.EmployeeID, employeeID) {
				targetRole = registration.Role
			}
		}
		if targetRole == "admin" && adminCount <= 1 {
			write(w, 409, map[string]string{"error": "the last active administrator cannot be removed"})
			return
		}
		for _, model := range s.store.Config().Models {
			if strings.EqualFold(model.Owner, employeeID) || strings.EqualFold(model.CreatedBy, employeeID) ||
				strings.EqualFold(model.SubmittedBy, employeeID) || strings.EqualFold(model.ApprovedBy, employeeID) ||
				strings.EqualFold(model.RejectedBy, employeeID) {
				write(w, 409, map[string]string{"error": "this employee has model history and must be disabled instead of permanently removed"})
				return
			}
		}
		if err := s.store.DeleteUserRegistration(employeeID); err != nil {
			write(w, 404, map[string]string{"error": err.Error()})
			return
		}
		s.mu.Lock()
		for token, sessionUser := range s.sessions {
			if strings.EqualFold(sessionUser.EmployeeID, employeeID) {
				delete(s.sessions, token)
			}
		}
		s.mu.Unlock()
		write(w, 200, map[string]string{"status": "removed"})
		return
	}
	if r.Method == "PUT" && employeeID != "" {
		var request struct {
			Role   string `json:"role"`
			Status string `json:"status"`
		}
		if err := decode(r, &request); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		employee, found := s.employeeByID(employeeID)
		if !found {
			write(w, 404, map[string]string{"error": "employee ID was not found in the corporate directory"})
			return
		}
		if request.Status != "active" && request.Status != "disabled" {
			write(w, 400, map[string]string{"error": "status must be active or disabled"})
			return
		}
		current, _ := s.currentUser(r)
		if strings.EqualFold(actorID(current), employeeID) && (request.Status == "disabled" || request.Role != current.Role) {
			write(w, 409, map[string]string{"error": "you cannot disable your own registration or change your own role"})
			return
		}
		updated, err := s.store.UpsertUserRegistration(store.UserRegistration{
			EmployeeID: employee.IDNum, LoginID: employee.LoginID, Email: employee.Email,
			Role: request.Role, Status: request.Status,
		})
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if updated.Status == "disabled" {
			s.mu.Lock()
			for token, sessionUser := range s.sessions {
				if strings.EqualFold(sessionUser.EmployeeID, employeeID) {
					delete(s.sessions, token)
				}
			}
			s.mu.Unlock()
		}
		write(w, 200, user{EmployeeID: updated.EmployeeID, Username: updated.LoginID, Email: updated.Email, Role: updated.Role, Status: updated.Status})
		return
	}
	if r.Method == "GET" && employeeID == "" {
		s.mu.RLock()
		usersByEmployeeID := map[string]user{}
		for _, registered := range s.users {
			registered.Password = ""
			usersByEmployeeID[registered.EmployeeID] = registered
		}
		s.mu.RUnlock()
		for _, registration := range s.store.UserRegistrations() {
			usersByEmployeeID[registration.EmployeeID] = user{EmployeeID: registration.EmployeeID, Username: registration.LoginID, Email: registration.Email, Role: registration.Role, Status: registration.Status}
		}
		users := make([]user, 0, len(usersByEmployeeID))
		for _, registered := range usersByEmployeeID {
			users = append(users, registered)
		}
		sort.Slice(users, func(i, j int) bool { return users[i].EmployeeID < users[j].EmployeeID })
		write(w, 200, users)
		return
	}
	if r.Method != "POST" || employeeID != "" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var request struct {
		EmployeeID string `json:"employeeId"`
		Role       string `json:"role"`
	}
	if err := decode(r, &request); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	employee, found := s.employeeByID(request.EmployeeID)
	if !found {
		write(w, 404, map[string]string{"error": "employee ID was not found in the corporate directory"})
		return
	}
	validRole := request.Role == "viewer" || request.Role == "engineer" || request.Role == "engineering_manager" || request.Role == "admin"
	if !validRole {
		write(w, 400, map[string]string{"error": "role must be viewer, engineer, engineering_manager, or admin"})
		return
	}
	registered := user{
		EmployeeID: employee.IDNum,
		Username:   employee.LoginID,
		Email:      employee.Email,
		Role:       request.Role,
		Status:     "active",
	}
	if _, err := s.store.UpsertUserRegistration(store.UserRegistration{
		EmployeeID: employee.IDNum, LoginID: employee.LoginID, Email: employee.Email, Role: request.Role, Status: "active",
	}); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	write(w, 201, registered)
}

type simulationRequest struct {
	CSV        string `json:"csv"`
	Sequential bool   `json:"sequential"`
	ModelID    string `json:"modelId,omitempty"`
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
			if parts := strings.SplitN(header, ".", 2); len(parts) == 2 {
				if len(samples[parts[0]]) == 0 {
					samples[parts[0]] = []map[string]interface{}{{}}
				}
				samples[parts[0]][0][parts[1]] = strings.TrimSpace(record[index])
			}
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

type sourceTestRequest struct {
	Source   store.Source `json:"source"`
	Password string       `json:"password"`
}

func sourceDSN(source store.Source, password string) (string, string, error) {
	switch source.Engine {
	case "postgres":
		connection := &url.URL{Scheme: "postgres", Host: fmt.Sprintf("%s:%d", source.Host, source.Port), Path: source.Database}
		connection.User = url.UserPassword(source.Username, password)
		query := connection.Query()
		query.Set("sslmode", source.TLSMode)
		connection.RawQuery = query.Encode()
		return "pgx", connection.String(), nil
	case "mysql":
		tlsMode := source.TLSMode
		if tlsMode == "disable" {
			tlsMode = "false"
		}
		return "mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?timeout=%ds&tls=%s", url.QueryEscape(source.Username), url.QueryEscape(password), source.Host, source.Port, source.Database, source.TimeoutSeconds, url.QueryEscape(tlsMode)), nil
	case "sqlserver":
		connection := &url.URL{Scheme: "sqlserver", Host: fmt.Sprintf("%s:%d", source.Host, source.Port)}
		connection.User = url.UserPassword(source.Username, password)
		query := connection.Query()
		query.Set("database", source.Database)
		if source.TLSMode == "disable" {
			query.Set("encrypt", "disable")
		} else {
			query.Set("encrypt", "true")
		}
		connection.RawQuery = query.Encode()
		return "sqlserver", connection.String(), nil
	default:
		return "", "", fmt.Errorf("unsupported database engine %q", source.Engine)
	}
}

var sqlParameterReference = regexp.MustCompile(`@[A-Za-z_][A-Za-z0-9_]*`)

func (s *server) sqlParameterResolver(requestContext context.Context) control.SQLParameterResolver {
	return func(parameter control.ParameterDefinition, resolved map[string]interface{}) (interface{}, error) {
		source, exists := s.store.Source(parameter.DataSourceID)
		if !exists {
			return nil, fmt.Errorf("SQL parameter %s: data source %q was not found", parameter.Key, parameter.DataSourceID)
		}
		if !source.Enabled {
			return nil, fmt.Errorf("SQL parameter %s: data source %q is not enabled", parameter.Key, source.Name)
		}
		query := strings.TrimSpace(parameter.Definition)
		query = strings.TrimSuffix(query, ";")
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), "SELECT ") || strings.Contains(query, ";") {
			return nil, fmt.Errorf("SQL parameter %s: query must be one read-only SELECT statement", parameter.Key)
		}
		query, arguments, err := prepareSQLQuery(source.Engine, query, resolved)
		if err != nil {
			return nil, fmt.Errorf("SQL parameter %s: %w", parameter.Key, err)
		}
		password := ""
		if source.SecretRef != "" {
			password = os.Getenv(source.SecretRef)
			if password == "" {
				return nil, fmt.Errorf("SQL parameter %s: secret environment variable %s is not configured", parameter.Key, source.SecretRef)
			}
		}
		driver, dsn, err := sourceDSN(source, password)
		if err != nil {
			return nil, fmt.Errorf("SQL parameter %s: %w", parameter.Key, err)
		}
		timeout := source.TimeoutSeconds
		if timeout < 1 {
			timeout = 5
		}
		ctx, cancel := context.WithTimeout(requestContext, time.Duration(timeout)*time.Second)
		defer cancel()
		db, err := sql.Open(driver, dsn)
		if err != nil {
			return nil, fmt.Errorf("SQL parameter %s: open connection: %w", parameter.Key, err)
		}
		defer db.Close()
		var value interface{}
		if err := db.QueryRowContext(ctx, query, arguments...).Scan(&value); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("SQL parameter %s: query returned no rows", parameter.Key)
			}
			return nil, fmt.Errorf("SQL parameter %s: query failed: %w", parameter.Key, err)
		}
		if bytes, ok := value.([]byte); ok {
			return string(bytes), nil
		}
		return value, nil
	}
}

func (s *server) sqlDataSampleResolver(requestContext context.Context) control.DataSampleResolver {
	return func(sample control.DataSampleDefinition, resolved map[string]interface{}) ([]map[string]interface{}, error) {
		source, exists := s.store.Source(sample.DataSourceID)
		if !exists {
			return nil, fmt.Errorf("data sample %s: data source %q was not found", sample.Key, sample.DataSourceID)
		}
		if !source.Enabled {
			return nil, fmt.Errorf("data sample %s: data source %q is not enabled", sample.Key, source.Name)
		}
		query := strings.TrimSpace(sample.Query)
		query = strings.TrimSuffix(query, ";")
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), "SELECT ") || strings.Contains(query, ";") {
			return nil, fmt.Errorf("data sample %s: query must be one read-only SELECT statement", sample.Key)
		}
		query, arguments, err := prepareSQLQuery(source.Engine, query, resolved)
		if err != nil {
			return nil, fmt.Errorf("data sample %s: %w", sample.Key, err)
		}
		password := ""
		if source.SecretRef != "" {
			password = os.Getenv(source.SecretRef)
			if password == "" {
				return nil, fmt.Errorf("data sample %s: secret environment variable %s is not configured", sample.Key, source.SecretRef)
			}
		}
		driver, dsn, err := sourceDSN(source, password)
		if err != nil {
			return nil, fmt.Errorf("data sample %s: %w", sample.Key, err)
		}
		timeout := source.TimeoutSeconds
		if timeout < 1 {
			timeout = 5
		}
		ctx, cancel := context.WithTimeout(requestContext, time.Duration(timeout)*time.Second)
		defer cancel()
		db, err := sql.Open(driver, dsn)
		if err != nil {
			return nil, fmt.Errorf("data sample %s: open connection: %w", sample.Key, err)
		}
		defer db.Close()
		rows, err := db.QueryContext(ctx, query, arguments...)
		if err != nil {
			return nil, fmt.Errorf("data sample %s: query failed: %w", sample.Key, err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("data sample %s: read columns: %w", sample.Key, err)
		}
		result := []map[string]interface{}{}
		for rows.Next() && len(result) < sample.Limit {
			raw := make([]interface{}, len(columns))
			destinations := make([]interface{}, len(columns))
			for index := range raw {
				destinations[index] = &raw[index]
			}
			if err := rows.Scan(destinations...); err != nil {
				return nil, fmt.Errorf("data sample %s: scan row: %w", sample.Key, err)
			}
			row := map[string]interface{}{}
			for index, column := range columns {
				if bytes, ok := raw[index].([]byte); ok {
					row[column] = string(bytes)
				} else {
					row[column] = raw[index]
				}
			}
			result = append(result, row)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("data sample %s: read rows: %w", sample.Key, err)
		}
		return result, nil
	}
}

func bindSQLParameters(query string, resolved map[string]interface{}) ([]interface{}, error) {
	_, arguments, err := prepareSQLQuery("sqlserver", query, resolved)
	return arguments, err
}

func prepareSQLQuery(engine, query string, resolved map[string]interface{}) (string, []interface{}, error) {
	seen := map[string]bool{}
	arguments := []interface{}{}
	positions := map[string]int{}
	locations := sqlParameterReference.FindAllStringIndex(query, -1)
	var rewritten strings.Builder
	last := 0
	for _, location := range locations {
		if location[0] > 0 && query[location[0]-1] == '@' {
			continue
		}
		name := query[location[0]+1 : location[1]]
		normalized := strings.ToLower(name)
		value, exists := resolved[name]
		if !exists {
			for candidate, candidateValue := range resolved {
				if strings.EqualFold(candidate, name) {
					value = candidateValue
					exists = true
					break
				}
			}
		}
		if !exists {
			return "", nil, fmt.Errorf("query references @%s, but that parameter has not been declared earlier", name)
		}
		rewritten.WriteString(query[last:location[0]])
		switch engine {
		case "sqlserver":
			rewritten.WriteString(query[location[0]:location[1]])
			if !seen[normalized] {
				arguments = append(arguments, sql.Named(name, value))
				seen[normalized] = true
			}
		case "postgres":
			position, exists := positions[normalized]
			if !exists {
				arguments = append(arguments, value)
				position = len(arguments)
				positions[normalized] = position
			}
			rewritten.WriteString(fmt.Sprintf("$%d", position))
		case "mysql":
			rewritten.WriteByte('?')
			arguments = append(arguments, value)
		default:
			return "", nil, fmt.Errorf("unsupported database engine %q", engine)
		}
		last = location[1]
	}
	if len(locations) == 0 {
		return query, arguments, nil
	}
	rewritten.WriteString(query[last:])
	return rewritten.String(), arguments, nil
}

func (s *server) testSource(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != "POST" {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.requireRole(w, r, "admin") {
		return
	}
	var request sourceTestRequest
	if err := decode(r, &request); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	source := request.Source
	source.ID = id
	password := request.Password
	if password == "" && source.SecretRef != "" {
		password = os.Getenv(source.SecretRef)
	}
	driver, dsn, err := sourceDSN(source, password)
	if err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	timeout := source.TimeoutSeconds
	if timeout < 1 {
		timeout = 5
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()
	started := time.Now()
	db, err := sql.Open(driver, dsn)
	if err == nil {
		err = db.PingContext(ctx)
		_ = db.Close()
	}
	elapsed := time.Since(started).Milliseconds()
	status := "success"
	message := "Connection and authentication succeeded"
	code := 200
	if err != nil {
		status = "failed"
		message = err.Error()
		code = 422
	}
	updated, recordErr := s.store.RecordSourceTest(id, status, message, elapsed)
	if recordErr != nil {
		write(w, 404, map[string]string{"error": recordErr.Error()})
		return
	}
	response := map[string]interface{}{"status": status, "message": message, "elapsedMs": elapsed, "testedAt": updated.LastTestAt}
	if err != nil {
		response["error"] = message
	}
	write(w, code, response)
}
func main() {
	root := "."
	data := filepath.Join(root, "data", "config.json")
	st, err := store.Open(data)
	if err != nil {
		operationalLog("FATAL", "store_open_failed", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}
	password := func(name, fallback string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		return fallback
	}
	employees := map[string]employeeRecord{
		"A100": {IDNum: "A100", LoginID: "admin", Email: "admin@example.test"},
		"M200": {IDNum: "M200", LoginID: "manager", Email: "manager@example.test", SupervisorID: "A100"},
		"E300": {IDNum: "E300", LoginID: "engineer", Email: "engineer@example.test", SupervisorID: "M200"},
		"E301": {IDNum: "E301", LoginID: "engineer2", Email: "engineer2@example.test", SupervisorID: "M200"},
		"V400": {IDNum: "V400", LoginID: "viewer", Email: "viewer@example.test", SupervisorID: "M200"},
	}
	if len(st.UserRegistrations()) == 0 {
		for _, registration := range []store.UserRegistration{
			{EmployeeID: "A100", LoginID: "admin", Email: "admin@example.test", Role: "admin", Status: "active"},
			{EmployeeID: "M200", LoginID: "manager", Email: "manager@example.test", Role: "engineering_manager", Status: "active"},
			{EmployeeID: "E300", LoginID: "engineer", Email: "engineer@example.test", Role: "engineer", Status: "active"},
			{EmployeeID: "V400", LoginID: "viewer", Email: "viewer@example.test", Role: "viewer", Status: "active"},
		} {
			if _, err := st.UpsertUserRegistration(registration); err != nil {
				operationalLog("FATAL", "seed_user_registration_failed", map[string]interface{}{"employeeId": registration.EmployeeID, "error": err.Error()})
				os.Exit(1)
			}
		}
	}
	s := &server{
		store: st, sessions: map[string]user{}, employees: employees,
		corporatePasswords: map[string]string{
			"admin": password("R2R_ADMIN_PASSWORD", "admin"), "manager": password("R2R_MANAGER_PASSWORD", "manager"),
			"engineer": password("R2R_ENGINEER_PASSWORD", "engineer"), "viewer": password("R2R_VIEWER_PASSWORD", "viewer"),
			"engineer2": password("R2R_ENGINEER2_PASSWORD", "engineer2"),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"status": "ok", "service": "go"})
	})
	mux.HandleFunc("/api/login", s.login)
	mux.HandleFunc("/api/session", s.session)
	mux.HandleFunc("/api/logout", s.logout)
	mux.HandleFunc("/api/config", s.authenticated(s.config))
	mux.HandleFunc("/api/dependencies", s.authenticated(s.dependencies))
	mux.HandleFunc("/api/v1/runs/prepare", s.prepare)
	mux.HandleFunc("/api/v1/runs/", s.complete)
	mux.HandleFunc("/api/runs/recent", s.authenticated(s.recentRuns))
	mux.HandleFunc("/api/models", s.authenticated(s.models))
	mux.HandleFunc("/api/models/import", s.authenticated(s.importModel))
	mux.HandleFunc("/api/models/", s.authenticated(s.revisions))
	mux.HandleFunc("/api/sources/", s.authenticated(s.sources))
	mux.HandleFunc("/api/input-definitions/", s.authenticated(s.inputDefinitions))
	mux.HandleFunc("/api/users", s.authenticated(s.applicationUsers))
	mux.HandleFunc("/api/users/", s.authenticated(s.applicationUsers))
	mux.HandleFunc("/api/simulate", s.authenticated(s.simulate))
	dist := http.FileServer(http.Dir(filepath.Join(root, "dist")))
	mux.Handle("/", dist)
	port := os.Getenv("PORT")
	if port == "" {
		port = "4173"
	}
	operationalLog("INFO", "server_started", map[string]interface{}{"port": port})
	if err := http.ListenAndServe(":"+port, s.operationalLogging(mux)); err != nil {
		operationalLog("FATAL", "server_stopped", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}
}
