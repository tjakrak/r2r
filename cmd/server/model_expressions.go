package main

import (
	"net/http"
	"r2r/internal/control"
)

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
