package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"r2r/internal/control"
	"r2r/internal/store"
	"regexp"
	"strconv"
	"strings"
	"time"
)

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
