package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"r2r/internal/corporateauth"
	"r2r/internal/employeedirectory"
	"r2r/internal/store"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
)

const applicationBasePath = "/R2RProcessControl"

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
	mux.HandleFunc("/api/v1/runs/simulate", s.simulateProductionRun)
	mux.HandleFunc("/api/v1/runs/", s.complete)
	mux.HandleFunc("/api/runs/recent", s.authenticated(s.recentRuns))
	mux.HandleFunc("/api/runs/export", s.authenticated(s.exportExecutions))
	mux.HandleFunc("/api/runs", s.authenticated(s.executions))
	mux.HandleFunc("/api/models", s.authenticated(s.models))
	mux.HandleFunc("/api/models/import", s.authenticated(s.importModel))
	mux.HandleFunc("/api/model-folders/", s.authenticated(s.modelFolders))
	mux.HandleFunc("/api/model-folders", s.authenticated(s.modelFolders))
	mux.HandleFunc("/api/models/", s.authenticated(s.revisions))
	mux.HandleFunc("/api/sources/", s.authenticated(s.sources))
	mux.HandleFunc("/api/input-definitions/", s.authenticated(s.inputDefinitions))
	mux.HandleFunc("/api/users", s.authenticated(s.applicationUsers))
	mux.HandleFunc("/api/users/search", s.authenticated(s.searchApplicationUsers))
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
