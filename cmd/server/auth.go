package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"r2r/internal/store"
	"sort"
	"strings"
	"time"
)

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
