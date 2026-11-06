package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var operationalLogMu sync.Mutex
var operationalDB *sql.DB

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

func applicationRelativePath(path string) string {
	if applicationBasePath != "" && path == applicationBasePath {
		return "/"
	}
	if applicationBasePath != "" && strings.HasPrefix(path, applicationBasePath+"/") {
		return strings.TrimPrefix(path, applicationBasePath)
	}
	return path
}

func (s *server) operationalLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := newRequestID()
		relativePath := applicationRelativePath(r.URL.Path)
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
				action, entityType, entityID := auditRoute(r.Method, relativePath)
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
		if s.store != nil && s.store.IsSQL() && strings.HasPrefix(relativePath, "/api/") && relativePath != "/api/health" {
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
