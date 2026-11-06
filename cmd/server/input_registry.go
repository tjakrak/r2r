package main

import (
	"net/http"
	"r2r/internal/store"
	"strings"
)

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
