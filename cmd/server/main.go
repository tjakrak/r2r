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
	"r2r/internal/corporateauth"
	"r2r/internal/employeedirectory"
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
	authenticator      corporateauth.Authenticator
	employeeDirectory  employeedirectory.Repository
	mu                 sync.RWMutex
	users              map[string]user
	employees          map[string]employeeRecord
	corporatePasswords map[string]string
	sessions           map[string]user
}
type user struct {
	EmployeeID    string   `json:"employeeId"`
	Username      string   `json:"username"`
	DisplayName   string   `json:"displayName,omitempty"`
	Email         string   `json:"email,omitempty"`
	Role          string   `json:"role"`
	Status        string   `json:"status,omitempty"`
	DirectReports []string `json:"directReports,omitempty"`
	Password      string   `json:"-"` // Legacy test compatibility; production authentication is external.
}
type employeeRecord = employeedirectory.Record
type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var operationalLogMu sync.Mutex
var operationalDB *sql.DB

const applicationBasePath = "/R2RProcessControl"

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
	if operationalDB != nil {
		instanceID, _ := os.Hostname()
		if configured := strings.TrimSpace(os.Getenv("R2R_SERVICE_INSTANCE_ID")); configured != "" {
			instanceID = configured
		}
		version := strings.TrimSpace(os.Getenv("R2R_APPLICATION_VERSION"))
		if version == "" {
			version = "development"
		}
		_, _ = operationalDB.Exec(`INSERT PCS.OPERATIONAL_EVENTS
			(SEVERITY,EVENT_NAME,SERVICE_INSTANCE_ID,APPLICATION_VERSION,REQUEST_ID,TRACE_ID,RUN_ID,ACTOR_ID,HTTP_METHOD,ROUTE,STATUS_CODE,DURATION_MS,RESPONSE_BYTES,ERROR_CODE,ERROR_MESSAGE,CLIENT_ADDRESS)
			VALUES (@p1,@p2,@p3,@p4,NULLIF(@p5,N''),NULLIF(@p6,''),NULLIF(@p7,N''),NULLIF(@p8,N''),NULLIF(@p9,''),NULLIF(@p10,N''),@p11,@p12,@p13,NULLIF(@p14,N''),NULLIF(@p15,N''),NULLIF(@p16,''))`,
			level, event, instanceID, version, fieldString(fields, "requestId"), fieldString(fields, "traceId"), fieldString(fields, "runId"), fieldString(fields, "actor"), fieldString(fields, "method"), fieldString(fields, "path"), fieldInt(fields, "status"), fieldInt64(fields, "durationMs"), fieldInt64(fields, "bytes"), fieldString(fields, "errorCode"), fieldString(fields, "error"), fieldString(fields, "clientIp"))
	}
}

func fieldString(fields map[string]interface{}, key string) string {
	if value, ok := fields[key]; ok {
		return fmt.Sprint(value)
	}
	return ""
}
func fieldInt(fields map[string]interface{}, key string) interface{} {
	if value, ok := fields[key]; ok {
		return value
	}
	return nil
}
func fieldInt64(fields map[string]interface{}, key string) interface{} {
	if value, ok := fields[key]; ok {
		return value
	}
	return nil
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

func newRunID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err == nil {
		return "r2r_" + hex.EncodeToString(raw)
	}
	return fmt.Sprintf("r2r_%d", time.Now().UTC().UnixNano())
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
		r.Header.Set("X-Request-ID", requestID)
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
			if actor != "" && r.Method != http.MethodGet && s.store != nil && s.store.IsSQL() {
				action, entityType, entityID := auditRoute(r.Method, r.URL.Path)
				outcome := "success"
				if capture.status == http.StatusForbidden || capture.status == http.StatusUnauthorized {
					outcome = "denied"
				} else if capture.status >= 400 {
					outcome = "failure"
				}
				if err := s.store.RecordAudit(actor, action, outcome, entityType, entityID, requestID, clientIP(r.RemoteAddr), map[string]interface{}{"method": r.Method, "route": r.URL.Path, "statusCode": capture.status}); err != nil {
					operationalLog("ERROR", "audit_event_write_failed", map[string]interface{}{"requestId": requestID, "actor": actor, "error": err.Error()})
				}
			}
		}()
		if s.store != nil && s.store.IsSQL() && strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			err := s.store.Refresh(ctx)
			cancel()
			if err != nil {
				operationalLog("ERROR", "sql_store_refresh_failed", map[string]interface{}{"requestId": requestID, "error": err.Error()})
				write(capture, 503, map[string]string{"error": "production database is unavailable"})
				return
			}
		}
		next.ServeHTTP(capture, r)
	})
}

func auditRoute(method, path string) (string, string, string) {
	trimmed := strings.Trim(path, "/")
	parts := strings.Split(trimmed, "/")
	entityType, entityID := "api_resource", trimmed
	if len(parts) >= 2 {
		entityType = strings.TrimSuffix(parts[1], "s")
	}
	if len(parts) >= 3 {
		entityID = parts[2]
	}
	action := strings.ToLower(method) + "." + strings.ReplaceAll(trimmed, "/", ".")
	if len(parts) >= 4 {
		action = entityType + "." + parts[3]
	} else if method == http.MethodPost {
		action = entityType + ".created"
	} else if method == http.MethodPut {
		action = entityType + ".updated"
	} else if method == http.MethodDelete {
		action = entityType + ".deleted"
	}
	return action, entityType, entityID
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
	if s.store != nil && s.store.IsSQL() {
		registration, ok := s.store.SessionUser(cookie.Value)
		if !ok {
			return user{}, false
		}
		current := user{EmployeeID: registration.EmployeeID, Username: registration.LoginID, DisplayName: registration.DisplayName, Email: registration.Email, Role: registration.Role, Status: registration.Status}
		for _, candidate := range s.employees {
			if strings.EqualFold(candidate.SupervisorID, current.EmployeeID) {
				current.DirectReports = append(current.DirectReports, candidate.IDNum)
			}
		}
		sort.Strings(current.DirectReports)
		return current, true
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
		operationalLog("WARN", "login_rejected", map[string]interface{}{
			"requestId": r.Header.Get("X-Request-ID"),
			"errorCode": "invalid_request",
			"clientIp":  clientIP(r.RemoteAddr),
		})
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	employee, employeeFound := s.employeeByLoginID(login.Username)
	if !employeeFound {
		s.logLoginRejected(r, login.Username, "employee_not_found")
		write(w, 401, map[string]string{"error": "invalid username or password"})
		return
	}
	registered, registeredFound := s.registeredUser(employee.IDNum)
	if registeredFound && registered.Status != "" && registered.Status != "active" {
		s.logLoginRejected(r, login.Username, "user_not_active")
		write(w, 401, map[string]string{"error": "invalid username or password"})
		return
	}
	if s.authenticator != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		result, err := s.authenticator.Authenticate(ctx, employee.LoginID, login.Password)
		cancel()
		if err != nil {
			operationalLog("ERROR", "corporate_authentication_failed", map[string]interface{}{"requestId": r.Header.Get("X-Request-ID"), "error": err.Error()})
			write(w, http.StatusServiceUnavailable, map[string]string{"error": "corporate authentication service is unavailable"})
			return
		}
		if !result.Authenticated {
			s.logLoginRejected(r, login.Username, "corporate_credentials_rejected")
			write(w, 401, map[string]string{"error": "invalid username or password"})
			return
		}
		if result.EmployeeID != "" && !strings.EqualFold(strings.Trim(strings.TrimSpace(result.EmployeeID), "#"), strings.Trim(strings.TrimSpace(employee.IDNum), "#")) {
			s.logLoginRejected(r, login.Username, "employee_identity_mismatch")
			write(w, 401, map[string]string{"error": "invalid username or password"})
			return
		}
		if !registeredFound {
			registration := store.UserRegistration{
				EmployeeID: employee.IDNum, LoginID: employee.LoginID, DisplayName: employee.DisplayName(),
				Email: employee.Email, Role: "viewer", Status: "active",
			}
			if s.store != nil {
				created, err := s.store.UpsertUserRegistration(registration)
				if err != nil {
					operationalLog("ERROR", "user_auto_registration_failed", map[string]interface{}{"requestId": r.Header.Get("X-Request-ID"), "employeeId": employee.IDNum, "error": err.Error()})
					write(w, 500, map[string]string{"error": "failed to create R2R user registration"})
					return
				}
				registration = created
			} else {
				s.mu.Lock()
				if s.users == nil {
					s.users = map[string]user{}
				}
				s.users[employee.IDNum] = user{EmployeeID: employee.IDNum, Username: employee.LoginID, DisplayName: employee.DisplayName(), Email: employee.Email, Role: "viewer", Status: "active"}
				s.mu.Unlock()
			}
			registered = user{EmployeeID: registration.EmployeeID, Username: registration.LoginID, DisplayName: registration.DisplayName, Email: registration.Email, Role: registration.Role, Status: registration.Status}
			registeredFound = true
			operationalLog("INFO", "user_auto_registered", map[string]interface{}{"requestId": r.Header.Get("X-Request-ID"), "employeeId": employee.IDNum, "role": "viewer"})
			if s.store != nil {
				_ = s.store.RecordAudit(employee.IDNum, "user.auto_registered", "success", "user", employee.IDNum, r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), map[string]interface{}{"role": "viewer", "source": "corporate_login"})
			}
		}
	} else {
		if !registeredFound {
			s.logLoginRejected(r, login.Username, "r2r_registration_not_found")
			write(w, 401, map[string]string{"error": "invalid username or password"})
			return
		}
		expectedPassword := s.corporatePasswords[strings.ToLower(employee.LoginID)]
		if expectedPassword == "" {
			expectedPassword = registered.Password
		}
		if subtle.ConstantTimeCompare([]byte(expectedPassword), []byte(login.Password)) != 1 {
			s.logLoginRejected(r, login.Username, "development_credentials_rejected")
			write(w, 401, map[string]string{"error": "invalid username or password"})
			return
		}
	}
	registered.EmployeeID = employee.IDNum
	registered.Username = employee.LoginID
	registered.DisplayName = employee.DisplayName()
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
	if s.store.IsSQL() {
		if err := s.store.CreateSession(token, registered.EmployeeID, clientIP(r.RemoteAddr), time.Now().UTC().Add(8*time.Hour)); err != nil {
			operationalLog("ERROR", "session_create_failed", map[string]interface{}{"actor": registered.EmployeeID, "error": err.Error()})
			write(w, 500, map[string]string{"error": "failed to create session"})
			return
		}
	} else {
		s.mu.Lock()
		s.sessions[token] = registered
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "r2r_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
	write(w, 200, registered)
}

func (s *server) logLoginRejected(r *http.Request, username, reason string) {
	operationalLog("WARN", "login_rejected", map[string]interface{}{
		"requestId": r.Header.Get("X-Request-ID"),
		"actor":     strings.TrimSpace(username),
		"errorCode": reason,
		"clientIp":  clientIP(r.RemoteAddr),
	})
}

func (s *server) employeeByLoginID(loginID string) (employeeRecord, bool) {
	if s.employeeDirectory != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		employee, err := s.employeeDirectory.ByLoginID(ctx, loginID)
		if err == nil {
			return employee, true
		}
		if !errors.Is(err, sql.ErrNoRows) {
			operationalLog("ERROR", "employee_directory_lookup_failed", map[string]interface{}{"error": err.Error()})
		}
		return employeeRecord{}, false
	}
	for _, employee := range s.employees {
		if strings.EqualFold(strings.TrimSpace(employee.LoginID), strings.TrimSpace(loginID)) {
			return employee, true
		}
	}
	return employeeRecord{}, false
}

func (s *server) employeeByID(employeeID string) (employeeRecord, bool) {
	if s.employeeDirectory != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		employee, err := s.employeeDirectory.ByEmployeeID(ctx, employeeID)
		if err == nil {
			return employee, true
		}
		if !errors.Is(err, sql.ErrNoRows) {
			operationalLog("ERROR", "employee_directory_lookup_failed", map[string]interface{}{"error": err.Error()})
		}
		return employeeRecord{}, false
	}
	employee, ok := s.employees[strings.TrimSpace(employeeID)]
	if ok {
		return employee, true
	}
	for _, candidate := range s.employees {
		if strings.EqualFold(strings.TrimSpace(candidate.IDNum), strings.TrimSpace(employeeID)) {
			return candidate, true
		}
	}
	return employeeRecord{}, false
}

func (s *server) registeredUser(employeeID string) (user, bool) {
	if s.store != nil {
		for _, registration := range s.store.UserRegistrations() {
			if strings.EqualFold(strings.TrimSpace(registration.EmployeeID), strings.TrimSpace(employeeID)) {
				return user{EmployeeID: registration.EmployeeID, Username: registration.LoginID, DisplayName: registration.DisplayName, Email: registration.Email, Role: registration.Role, Status: registration.Status}, true
			}
		}
	}
	for _, candidate := range s.users {
		if strings.EqualFold(strings.TrimSpace(candidate.EmployeeID), strings.TrimSpace(employeeID)) ||
			(strings.TrimSpace(candidate.EmployeeID) == "" && strings.EqualFold(strings.TrimSpace(candidate.Username), strings.TrimSpace(employeeID))) {
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
		if s.store.IsSQL() {
			_ = s.store.RevokeSession(cookie.Value)
		} else {
			s.mu.Lock()
			delete(s.sessions, cookie.Value)
			s.mu.Unlock()
		}
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
func (s *server) explainExpression(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var request struct {
		Expression string `json:"expression"`
	}
	if err := decode(r, &request); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	explanation, err := control.ExplainExpression(request.Expression)
	if err != nil {
		write(w, 422, map[string]string{"error": "expression cannot be explained until its syntax is valid: " + err.Error()})
		return
	}
	write(w, 200, explanation)
}
func (s *server) refactorParameter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var request struct {
		Action  string        `json:"action"`
		Model   control.Model `json:"model"`
		OldName string        `json:"oldName"`
		NewName string        `json:"newName,omitempty"`
	}
	if err := decode(r, &request); err != nil {
		write(w, 400, map[string]string{"error": err.Error()})
		return
	}
	switch request.Action {
	case "find":
		write(w, 200, map[string]interface{}{
			"usages": control.FindParameterUsages(request.Model, request.OldName),
		})
	case "rename":
		updated, usages, err := control.RenameParameter(request.Model, request.OldName, request.NewName)
		if err != nil {
			write(w, 422, map[string]interface{}{"error": err.Error(), "usages": usages})
			return
		}
		write(w, 200, map[string]interface{}{"model": updated, "usages": usages})
	default:
		write(w, 400, map[string]string{"error": "action must be find or rename"})
	}
}
func (s *server) prepare(w http.ResponseWriter, r *http.Request) {
	s.executeRun(w, r, false)
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

func prepareExecutionContext(model control.Model, indexes, externalInputs map[string]interface{}) (map[string]interface{}, error) {
	context := make(map[string]interface{}, len(indexes)+len(externalInputs))
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
		input, exists := declared[strings.ToLower(name)]
		if !exists {
			return nil, fmt.Errorf("external input %q is not declared by the selected model", name)
		}
		if category, duplicate := names[strings.ToLower(name)]; duplicate {
			return nil, fmt.Errorf("input %q cannot be supplied as both %s and external input", name, category)
		}
		context[input.Name] = value
		names[strings.ToLower(input.Name)] = "external input"
	}
	return context, nil
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
	indexes, err := validatePrepareIndexes(req.Indexes, s.store.Config().InputDefinitions)
	if err != nil {
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	model, reason, err := control.Select(s.store.Config().Models, indexes)
	if err != nil {
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	context, err := prepareExecutionContext(model, indexes, req.ExternalInputs)
	if err != nil {
		write(w, 422, map[string]string{"error": err.Error()})
		return
	}
	runID := newRunID()
	requestID := r.Header.Get("X-Request-ID")
	limitResults := map[string]control.OutputValue{}
	collectLimit := func(result control.OutputValue) { limitResults[result.Parameter] = result }
	values, samples, unresolvedSQL, err := control.ResolveModelWithLimitCollector(model, context, nil, nil, s.store.AdaptiveValues(model, context), s.sqlParameterResolver(r.Context()), s.sqlDataSampleResolver(r.Context()), collectLimit)
	if err != nil {
		s.recordExecutionFailure(runID, requestID, model, context, values, samples, nil, failureCodeForExecutionError(err), err.Error(), "run_rejected")
		if writeParameterLimitRejection(w, runID, model, err) {
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
		s.recordExecutionFailure(runID, requestID, model, context, values, samples, nil, "PARAMETER_EVALUATION_FAILED", "SQL resolver is not configured", "run_rejected")
		write(w, 501, map[string]interface{}{"error": "SQL resolver is not configured", "unresolvedParameters": missing, "controlModel": map[string]interface{}{"id": model.ID, "revision": model.Revision}})
		return
	}
	adaptiveUpdates := map[string]float64{}
	if commitAdaptive || !s.store.HasUnfinishedRun(model, context) {
		adaptiveUpdates, err = control.CalculateAdaptiveUpdatesForSource(model, values, "parameter")
		if err != nil {
			s.recordExecutionFailure(runID, requestID, model, context, values, samples, nil, "PARAMETER_EVALUATION_FAILED", err.Error(), "run_rejected")
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
			s.recordExecutionFailure(runID, requestID, model, context, values, samples, nil, failureCodeForExecutionError(err), err.Error(), "run_rejected")
			if writeParameterLimitRejection(w, runID, model, err) {
				return
			}
			write(w, 422, map[string]string{"error": err.Error()})
			return
		}
		if len(unresolvedSQL) > 0 {
			s.recordExecutionFailure(runID, requestID, model, context, values, samples, nil, "PARAMETER_EVALUATION_FAILED", "failed to reevaluate parameters after adaptive update", "run_rejected")
			write(w, 500, map[string]string{"error": "failed to reevaluate parameters after adaptive update"})
			return
		}
	}
	outputs, err := control.EvaluateResolvedOutputs(model, values, limitResults)
	if err != nil {
		s.recordExecutionFailure(runID, requestID, model, context, values, samples, nil, failureCodeForExecutionError(err), err.Error(), "run_rejected")
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
		s.recordExecutionFailure(runID, requestID, model, context, values, samples, outputs, "PARAMETER_LIMIT_REJECTED", failureMessage, "run_rejected")
		write(w, 422, map[string]interface{}{
			"status":      "rejected",
			"disposition": "run_rejected",
			"error":       failureMessage,
			"runId":       runID,
			"controlModel": map[string]interface{}{
				"id": model.ID, "familyId": model.FamilyID, "name": model.Name,
				"revision": model.Revision,
			},
			"violations": rejected,
		})
		return
	}
	if commitAdaptive {
		if err := s.store.CommitAdaptive(model, context, runID, adaptiveUpdates); err != nil {
			s.recordExecutionFailure(runID, requestID, model, context, values, samples, outputs, "RUN_PERSISTENCE_FAILED", "failed to persist adaptive state", "run_rejected")
			write(w, 500, map[string]string{"error": "failed to persist adaptive state"})
			return
		}
	}
	id := runID
	status := "completed"
	if !commitAdaptive {
		status = "prepared"
	}
	result := map[string]interface{}{"id": id, "runId": runID, "requestId": requestID, "status": status, "controlModel": map[string]interface{}{"id": model.ID, "familyId": model.FamilyID, "name": model.Name, "revision": model.Revision, "owner": model.Owner}, "routing": map[string]string{"reason": reason}, "parameters": values, "adaptiveUpdates": adaptiveUpdates, "dataSamples": samples, "outputs": outputs, "disposition": disposition, "meta": map[string]string{"receivedAt": time.Now().UTC().Format(time.RFC3339)}}
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

func writeParameterLimitRejection(w http.ResponseWriter, runID string, model control.Model, resolveErr error) bool {
	var limitErr *control.ParameterLimitError
	if !errors.As(resolveErr, &limitErr) {
		return false
	}
	write(w, http.StatusUnprocessableEntity, map[string]interface{}{
		"status":      "rejected",
		"disposition": "run_rejected",
		"error":       "Run is not allowed: " + limitErr.Message + ".",
		"runId":       runID,
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
	if action == "deactivate" && r.Method == "POST" {
		if !s.requireRole(w, r, "admin") {
			return
		}
		deactivated, err := s.store.DeactivateFamily(id)
		if err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, deactivated)
		return
	}
	if action == "reactivate" && r.Method == "POST" {
		if !s.requireRole(w, r, "admin") {
			return
		}
		var request struct {
			Reason string `json:"reason"`
		}
		if err := decode(r, &request); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		request.Reason = strings.TrimSpace(request.Reason)
		if request.Reason == "" {
			write(w, 400, map[string]string{"error": "reactivation reason is required"})
			return
		}
		reactivated, err := s.store.ReactivateFamily(id)
		if err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
		current, _ := s.currentUser(r)
		if err := s.store.RecordAudit(actorID(current), "model.reactivated", "success", "model_family", reactivated.FamilyID, r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), map[string]interface{}{"modelId": reactivated.ID, "revision": reactivated.Revision, "reason": request.Reason}); err != nil {
			operationalLog("ERROR", "model_reactivation_audit_failed", map[string]interface{}{"familyId": reactivated.FamilyID, "error": err.Error()})
		}
		write(w, 200, reactivated)
		return
	}
	if action == "purge-summary" && r.Method == "GET" {
		if !s.requireRole(w, r, "admin") {
			return
		}
		summary, err := s.store.ModelPurgeSummary(id)
		if err != nil {
			write(w, 404, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, summary)
		return
	}
	if action == "purge" && r.Method == "DELETE" {
		if !s.requireRole(w, r, "admin") {
			return
		}
		var request struct {
			ModelName string `json:"modelName"`
		}
		if err := decode(r, &request); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		summary, err := s.store.ModelPurgeSummary(id)
		if err != nil {
			write(w, 404, map[string]string{"error": err.Error()})
			return
		}
		if request.ModelName != summary.ModelName {
			write(w, 400, map[string]string{"error": "typed model name does not match"})
			return
		}
		purged, err := s.store.PurgeFamily(id)
		if err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
		current, _ := s.currentUser(r)
		if err := s.store.RecordAudit(actorID(current), "model.purged", "success", "model_family", purged.FamilyID, r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), purged); err != nil {
			operationalLog("ERROR", "model_purge_audit_failed", map[string]interface{}{"familyId": purged.FamilyID, "error": err.Error()})
		}
		write(w, 200, map[string]interface{}{"status": "purged", "summary": purged})
		return
	}
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
		days := 14
		if requested := strings.TrimSpace(r.URL.Query().Get("days")); requested != "" {
			parsed, err := strconv.Atoi(requested)
			if err != nil || parsed < 1 || parsed > 90 {
				write(w, 400, map[string]string{"error": "days must be an integer from 1 to 90"})
				return
			}
			days = parsed
		}
		write(w, 200, s.store.RunsForFamily(model.FamilyID, time.Now().UTC().Add(-time.Duration(days)*24*time.Hour)))
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
		normalizedName := strings.ToLower(name)
		required := normalizedName == "machine" || normalizedName == "action" || normalizedName == "material"
		indexes = append(indexes, map[string]interface{}{"name": name, "type": "string", "required": required, "acceptedValues": []string(model.Indexes[name])})
	}
	externalInputs := []map[string]interface{}{}
	for _, input := range model.ExternalInputs {
		externalInputs = append(externalInputs, map[string]interface{}{"name": input.Name, "type": input.Type, "required": true, "description": input.Description})
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
		"outputs": outputs,
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
	if len(parts) == 2 && parts[1] == "test-query" {
		s.testSourceQuery(w, r, parts[0])
		return
	}
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
			EmployeeID: employee.IDNum, LoginID: employee.LoginID, DisplayName: employee.DisplayName(), Email: employee.Email,
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
		write(w, 200, user{EmployeeID: updated.EmployeeID, Username: updated.LoginID, DisplayName: updated.DisplayName, Email: updated.Email, Role: updated.Role, Status: updated.Status})
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
			usersByEmployeeID[registration.EmployeeID] = user{EmployeeID: registration.EmployeeID, Username: registration.LoginID, DisplayName: registration.DisplayName, Email: registration.Email, Role: registration.Role, Status: registration.Status}
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
		EmployeeID:  employee.IDNum,
		Username:    employee.LoginID,
		DisplayName: employee.DisplayName(),
		Email:       employee.Email,
		Role:        request.Role,
		Status:      "active",
	}
	if _, err := s.store.UpsertUserRegistration(store.UserRegistration{
		EmployeeID: employee.IDNum, LoginID: employee.LoginID, DisplayName: employee.DisplayName(), Email: employee.Email, Role: request.Role, Status: "active",
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

type sourceTestRequest struct {
	Source   store.Source `json:"source"`
	Password string       `json:"password"`
}

type sourceQueryTestRequest struct {
	QueryType string                               `json:"queryType"`
	Query     string                               `json:"query"`
	Inputs    map[string]interface{}               `json:"inputs"`
	DataType  string                               `json:"dataType,omitempty"`
	Columns   []control.DataSampleColumnDefinition `json:"columns,omitempty"`
	Limit     int                                  `json:"limit,omitempty"`
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

func readOnlySQL(query string) (string, error) {
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if !strings.HasPrefix(strings.ToUpper(query), "SELECT ") || strings.Contains(query, ";") {
		return "", errors.New("query must be one read-only SELECT statement")
	}
	return query, nil
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

func normalizeSQLTestValue(value interface{}, dataType string) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	if bytes, ok := value.([]byte); ok {
		value = string(bytes)
	}
	switch dataType {
	case "number":
		number, err := strconv.ParseFloat(fmt.Sprint(value), 64)
		if err != nil {
			return nil, errors.New("query result must be a number")
		}
		return number, nil
	case "boolean":
		if boolean, ok := value.(bool); ok {
			return boolean, nil
		}
		boolean, err := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
		if err != nil {
			return nil, errors.New("query result must be boolean")
		}
		return boolean, nil
	case "string":
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("query result must be text")
		}
		return text, nil
	default:
		return nil, fmt.Errorf("unsupported declared data type %q", dataType)
	}
}

func validateSQLTestRows(rows []map[string]interface{}, columns []control.DataSampleColumnDefinition) error {
	for rowIndex, row := range rows {
		for _, column := range columns {
			var raw interface{}
			found := false
			for name, value := range row {
				if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(column.Name)) {
					raw, found = value, true
					break
				}
			}
			if !found {
				return fmt.Errorf("row %d has no declared column %q", rowIndex+1, column.Name)
			}
			value, err := normalizeSQLTestValue(raw, column.Type)
			if err != nil {
				return fmt.Errorf("row %d column %s: %w", rowIndex+1, column.Name, err)
			}
			row[column.Name] = value
		}
	}
	return nil
}

func (s *server) testSourceQuery(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
		return
	}
	source, exists := s.store.Source(id)
	if !exists {
		write(w, http.StatusNotFound, map[string]string{"error": "data source not found"})
		return
	}
	var request sourceQueryTestRequest
	if err := decode(r, &request); err != nil {
		write(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if request.QueryType != "parameter" && request.QueryType != "data_sample" {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": "queryType must be parameter or data_sample"})
		return
	}
	query, err := readOnlySQL(request.Query)
	if err != nil {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	query, arguments, err := prepareSQLQuery(source.Engine, query, request.Inputs)
	if err != nil {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	password := ""
	if source.SecretRef != "" {
		password = os.Getenv(source.SecretRef)
		if password == "" {
			write(w, http.StatusUnprocessableEntity, map[string]string{"error": "secret environment variable " + source.SecretRef + " is not configured"})
			return
		}
	}
	driver, dsn, err := sourceDSN(source, password)
	if err != nil {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	timeout := source.TimeoutSeconds
	if timeout < 1 {
		timeout = 5
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": "open connection: " + err.Error()})
		return
	}
	defer db.Close()
	started := time.Now()
	rows, err := db.QueryContext(ctx, query, arguments...)
	if err != nil {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": "query failed: " + err.Error()})
		return
	}
	defer rows.Close()
	columnNames, err := rows.Columns()
	if err != nil {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": "read columns: " + err.Error()})
		return
	}
	if request.QueryType == "parameter" && len(columnNames) != 1 {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": "parameter query must return exactly one column"})
		return
	}
	limit := request.Limit
	if limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	result := make([]map[string]interface{}, 0, limit)
	truncated := false
	for rows.Next() {
		if len(result) == limit {
			truncated = true
			break
		}
		raw := make([]interface{}, len(columnNames))
		destinations := make([]interface{}, len(columnNames))
		for index := range raw {
			destinations[index] = &raw[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			write(w, http.StatusUnprocessableEntity, map[string]string{"error": "scan row: " + err.Error()})
			return
		}
		row := map[string]interface{}{}
		for index, name := range columnNames {
			value := raw[index]
			if bytes, ok := value.([]byte); ok {
				value = string(bytes)
			}
			row[name] = value
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": "read rows: " + err.Error()})
		return
	}
	elapsed := time.Since(started).Milliseconds()
	if request.QueryType == "parameter" {
		if len(result) == 0 {
			write(w, http.StatusUnprocessableEntity, map[string]string{"error": "parameter query returned no rows"})
			return
		}
		value, err := normalizeSQLTestValue(result[0][columnNames[0]], request.DataType)
		if err != nil {
			write(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		write(w, http.StatusOK, map[string]interface{}{"status": "success", "queryType": "parameter", "value": value, "dataType": request.DataType, "elapsedMs": elapsed})
		return
	}
	if err := validateSQLTestRows(result, request.Columns); err != nil {
		write(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	write(w, http.StatusOK, map[string]interface{}{"status": "success", "queryType": "data_sample", "columns": columnNames, "rows": result, "rowCount": len(result), "truncated": truncated, "elapsedMs": elapsed})
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
	var st *store.Store
	var err error
	connectionString := strings.TrimSpace(os.Getenv("R2R_DATABASE_CONNECTION_STRING"))
	if connectionString != "" {
		db, openErr := sql.Open("sqlserver", connectionString)
		if openErr != nil {
			err = openErr
		} else {
			db.SetMaxOpenConns(25)
			db.SetMaxIdleConns(5)
			db.SetConnMaxLifetime(30 * time.Minute)
			db.SetConnMaxIdleTime(5 * time.Minute)
			st, err = store.OpenSQL(db)
			if err == nil {
				operationalDB = db
			}
		}
	} else {
		st, err = store.Open(data)
	}
	if err != nil {
		operationalLog("FATAL", "store_open_failed", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}
	var authenticator corporateauth.Authenticator
	authenticationURL := strings.TrimSpace(os.Getenv("R2R_AUTH_API_URL"))
	if authenticationURL != "" {
		authenticationClient, clientErr := corporateauth.NewClient(
			authenticationURL,
			os.Getenv("R2R_AUTH_API_KEY"),
			&http.Client{Timeout: 10 * time.Second},
		)
		if clientErr != nil {
			operationalLog("FATAL", "corporate_authentication_configuration_failed", map[string]interface{}{"error": clientErr.Error()})
			os.Exit(1)
		}
		authenticator = authenticationClient
	}
	var employeeDirectory employeedirectory.Repository
	employeeConnectionString := strings.TrimSpace(os.Getenv("R2R_EMPLOYEE_DATABASE_CONNECTION_STRING"))
	if employeeConnectionString != "" {
		employeeDB, openErr := sql.Open("sqlserver", employeeConnectionString)
		err = openErr
		if err == nil {
			employeeDB.SetMaxOpenConns(10)
			employeeDB.SetMaxIdleConns(2)
			employeeDB.SetConnMaxLifetime(30 * time.Minute)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err = employeeDB.PingContext(ctx)
			cancel()
		}
		if err != nil {
			operationalLog("FATAL", "employee_database_open_failed", map[string]interface{}{"error": err.Error()})
			os.Exit(1)
		}
		employeeDirectory = employeedirectory.NewSQLRepository(employeeDB)
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
	if !st.IsSQL() && len(st.UserRegistrations()) == 0 {
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
		store: st, authenticator: authenticator, employeeDirectory: employeeDirectory, sessions: map[string]user{}, employees: employees,
		corporatePasswords: map[string]string{
			"admin": password("R2R_ADMIN_PASSWORD", "admin"), "manager": password("R2R_MANAGER_PASSWORD", "manager"),
			"engineer": password("R2R_ENGINEER_PASSWORD", "engineer"), "viewer": password("R2R_VIEWER_PASSWORD", "viewer"),
			"engineer2": password("R2R_ENGINEER2_PASSWORD", "engineer2"),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.Ping(ctx); err != nil {
			write(w, 503, map[string]string{"status": "unavailable", "service": "go", "database": "disconnected"})
			return
		}
		databaseStatus := "json"
		if st.IsSQL() {
			databaseStatus = "connected"
		}
		write(w, 200, map[string]string{"status": "ok", "service": "go", "database": databaseStatus})
	})
	mux.HandleFunc("/api/login", s.login)
	mux.HandleFunc("/api/session", s.session)
	mux.HandleFunc("/api/logout", s.logout)
	mux.HandleFunc("/api/config", s.authenticated(s.config))
	mux.HandleFunc("/api/dependencies", s.authenticated(s.dependencies))
	mux.HandleFunc("/api/expressions/explain", s.authenticated(s.explainExpression))
	mux.HandleFunc("/api/parameters/refactor", s.authenticated(s.refactorParameter))
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
	basePath := applicationBasePath
	var handler http.Handler = mux
	if basePath != "" {
		mounted := http.NewServeMux()
		mounted.Handle(basePath+"/", http.StripPrefix(basePath, mux))
		mounted.HandleFunc(basePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, basePath+"/", http.StatusPermanentRedirect)
		})
		handler = mounted
	}
	port := listenPort()
	operationalLog("INFO", "server_started", map[string]interface{}{"port": port, "basePath": basePath})
	if err := http.ListenAndServe(":"+port, s.operationalLogging(handler)); err != nil {
		operationalLog("FATAL", "server_stopped", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}
}

func listenPort() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return port
	}
	if port := strings.TrimSpace(os.Getenv("HTTP_PLATFORM_PORT")); port != "" {
		return port
	}
	return "4173"
}
