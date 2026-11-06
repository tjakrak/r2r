package main

import (
	"r2r/internal/corporateauth"
	"r2r/internal/employeedirectory"
	"r2r/internal/store"
	"sync"
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
