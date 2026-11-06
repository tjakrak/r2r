package main

import (
	"net/http"
	"r2r/internal/control"
	"sort"
	"strconv"
	"strings"
	"time"
)

type modelWithValidation struct {
	control.Model
	ValidationIssues []string `json:"validationIssues"`
}

func (s *server) modelWithValidation(model control.Model) modelWithValidation {
	issues := []string{}
	if model.Status == "draft" {
		issues = s.store.DraftValidationIssues(model)
	}
	return modelWithValidation{Model: model, ValidationIssues: issues}
}

func (s *server) writeSavedDraft(w http.ResponseWriter, status int, model control.Model) {
	write(w, status, s.modelWithValidation(model))
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
	if action == "folder" && r.Method == http.MethodPut {
		if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
			return
		}
		model, found := s.store.Model(id)
		if !found {
			write(w, 404, map[string]string{"error": "model revision not found"})
			return
		}
		current, _ := s.currentUser(r)
		if current.Role != "admin" && !strings.EqualFold(model.Owner, actorID(current)) {
			write(w, 403, map[string]string{"error": "only the model owner or an administrator can move this model"})
			return
		}
		var request struct {
			FolderID *string `json:"folderId"`
		}
		if err := decode(r, &request); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if request.FolderID == nil {
			write(w, 400, map[string]string{"error": "folderId is required; use an empty string for Unfiled"})
			return
		}
		folderID := strings.TrimSpace(*request.FolderID)
		if err := s.store.MoveModelFamily(model.FamilyID, folderID); err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
		_ = s.store.RecordAudit(actorID(current), "model_family.moved", "success", "model_family", model.FamilyID, r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), map[string]interface{}{"fromFolderId": model.FolderID, "toFolderId": folderID})
		write(w, 200, map[string]string{"status": "moved", "familyId": model.FamilyID, "folderId": folderID})
		return
	}
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
		s.writeSavedDraft(w, 200, updated)
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
		history := s.store.History(model.FamilyID)
		withValidation := make([]modelWithValidation, 0, len(history))
		for _, revision := range history {
			withValidation = append(withValidation, s.modelWithValidation(revision))
		}
		write(w, 200, withValidation)
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
		s.writeSavedDraft(w, 201, created)
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
	if action == "withdraw" && r.Method == http.MethodPost {
		if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
			return
		}
		current, _ := s.currentUser(r)
		target, found := s.store.Model(id)
		if !found {
			write(w, 404, map[string]string{"error": "model revision not found"})
			return
		}
		if current.Role != "admin" && !strings.EqualFold(strings.TrimSpace(target.SubmittedBy), strings.TrimSpace(actorID(current))) {
			write(w, 403, map[string]string{"error": "only the submitter or an administrator can withdraw this revision"})
			return
		}
		withdrawn, err := s.store.WithdrawSubmission(id, actorID(current), current.Role == "admin")
		if err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.RecordAudit(actorID(current), "model_submission.withdrawn", "success", "model_revision", id, r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), map[string]interface{}{"familyId": withdrawn.FamilyID, "revision": withdrawn.Revision}); err != nil {
			operationalLog("ERROR", "model_submission_withdrawal_audit_failed", map[string]interface{}{"modelId": id, "error": err.Error()})
		}
		write(w, 200, withdrawn)
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
	s.writeSavedDraft(w, 201, created)
}

func (s *server) modelFolders(w http.ResponseWriter, r *http.Request) {
	id := ""
	if r.URL.Path != "/api/model-folders" {
		id = strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/model-folders/"), "/")
	}
	if strings.Contains(id, "/") {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	if r.Method == http.MethodGet && id == "" {
		write(w, 200, s.store.Config().Folders)
		return
	}
	if id == "" && r.Method == http.MethodPost {
		if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
			return
		}
		var request struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decode(r, &request); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		current, _ := s.currentUser(r)
		folder, err := s.store.CreateFolder(request.Name, request.Description, actorID(current))
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		_ = s.store.RecordAudit(actorID(current), "model_folder.created", "success", "model_folder", folder.ID, r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), map[string]interface{}{"name": folder.Name})
		write(w, 201, folder)
		return
	}
	if id != "" && r.Method == http.MethodPut {
		if !s.requireRole(w, r, "engineer", "engineering_manager", "admin") {
			return
		}
		var request struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decode(r, &request); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		current, _ := s.currentUser(r)
		folder, err := s.store.UpdateFolder(id, request.Name, request.Description, actorID(current))
		if err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		_ = s.store.RecordAudit(actorID(current), "model_folder.updated", "success", "model_folder", folder.ID, r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), map[string]interface{}{"name": folder.Name})
		write(w, 200, folder)
		return
	}
	if id != "" && r.Method == http.MethodDelete {
		if !s.requireRole(w, r, "admin") {
			return
		}
		if err := s.store.DeleteFolder(id); err != nil {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
		current, _ := s.currentUser(r)
		_ = s.store.RecordAudit(actorID(current), "model_folder.deleted", "success", "model_folder", id, r.Header.Get("X-Request-ID"), clientIP(r.RemoteAddr), nil)
		write(w, 200, map[string]string{"status": "deleted"})
		return
	}
	write(w, 405, map[string]string{"error": "method not allowed"})
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
	model.FolderID = strings.TrimSpace(r.URL.Query().Get("folderId"))
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
	s.writeSavedDraft(w, 201, created)
}
