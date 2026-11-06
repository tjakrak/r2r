package main

import (
	"errors"
	"net/http"
	"r2r/internal/store"
	"sort"
	"strconv"
	"strings"
)

func (s *server) searchApplicationUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, "admin") {
		return
	}
	if r.Method != http.MethodGet {
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	page, pageSize := 1, 25
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil {
			write(w, 400, map[string]string{"error": "page must be a positive integer"})
			return
		}
	}
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil {
			write(w, 400, map[string]string{"error": "pageSize must be an integer from 1 to 100"})
			return
		}
	}
	result, err := s.store.SearchUserRegistrations(store.UserSearch{
		Query: r.URL.Query().Get("query"), Role: r.URL.Query().Get("role"),
		Status: r.URL.Query().Get("status"), Page: page, PageSize: pageSize,
	})
	if err != nil {
		if errors.Is(err, store.ErrInvalidUserSearch) {
			write(w, 400, map[string]string{"error": err.Error()})
		} else {
			write(w, 500, map[string]string{"error": "could not load users"})
		}
		return
	}
	items := make([]user, 0, len(result.Items))
	for _, registration := range result.Items {
		items = append(items, user{
			EmployeeID: registration.EmployeeID, Username: registration.LoginID,
			DisplayName: registration.DisplayName, Email: registration.Email,
			Role: registration.Role, Status: registration.Status,
		})
	}
	write(w, 200, map[string]interface{}{
		"items": items, "total": result.Total, "page": result.Page, "pageSize": result.PageSize,
	})
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
