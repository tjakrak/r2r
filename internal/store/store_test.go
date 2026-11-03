package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"r2r/internal/control"
)

func TestCreateSourcePersistsFirstConfiguredConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data, _ := json.Marshal(Config{})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateSource(Source{ID: "process-db", Name: "Process DB", Engine: "sqlserver", Host: "db.local", Port: 1433, Database: "process", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.Type != "sql" || created.TLSMode != "require" || created.Location != "db.local:1433/process" {
		t.Fatalf("unexpected source defaults: %+v", created)
	}
	if _, err := s.CreateSource(created); err == nil {
		t.Fatal("expected duplicate source identifier to fail")
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := reopened.Source("process-db"); !exists {
		t.Fatal("created source was not persisted")
	}
}

func TestInputRegistryControlsModelFields(t *testing.T) {
	definitions := []InputDefinition{{Name: "chamber", Category: "index", DataType: "string", Status: "active"}, {Name: "run_type", Category: "external", DataType: "string", Status: "active"}}
	model := control.Model{Indexes: map[string]control.IndexValues{"machine": {"M1"}, "action": {"run"}, "material": {"Si"}, "chamber": {"A"}}, ExternalInputs: []control.ExternalInputDefinition{{Name: "run_type", Type: "string"}}}
	if err := validateRegisteredInputs(model, definitions); err != nil {
		t.Fatal(err)
	}
	model.ExternalInputs[0].Type = "number"
	if err := validateRegisteredInputs(model, definitions); err == nil {
		t.Fatal("expected registered type mismatch")
	}
	model.ExternalInputs = nil
	model.Indexes["unregistered"] = control.IndexValues{"x"}
	if err := validateRegisteredInputs(model, definitions); err == nil {
		t.Fatal("expected unregistered index rejection")
	}
}

func TestDeleteInputDefinitionProtectsReferencesAndSystemFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config := Config{
		InputDefinitions: []InputDefinition{{Name: "unused", Category: "external", DataType: "string", Status: "active"}, {Name: "used", Category: "external", DataType: "string", Status: "active"}},
		Models:           []control.Model{{Name: "Model", Revision: 2, Status: "approved", ExternalInputs: []control.ExternalInputDefinition{{Name: "used", Type: "string"}}}},
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInputDefinition("used"); err == nil {
		t.Fatal("expected referenced definition to be protected")
	}
	if err := s.DeleteInputDefinition("machine"); err == nil {
		t.Fatal("expected system definition to be protected")
	}
	if err := s.DeleteInputDefinition("unused"); err != nil {
		t.Fatal(err)
	}
	for _, definition := range s.Config().InputDefinitions {
		if definition.Name == "unused" {
			t.Fatal("unused definition was not deleted")
		}
	}
}

func TestDeleteInputDefinitionIgnoresSupersededRevisionReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config := Config{
		InputDefinitions: []InputDefinition{{Name: "legacy_input", Category: "external", DataType: "string", Status: "active"}},
		Models: []control.Model{{
			Name:           "Legacy model",
			Revision:       1,
			Status:         "superseded",
			ExternalInputs: []control.ExternalInputDefinition{{Name: "legacy_input", Type: "string"}},
		}},
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInputDefinition("legacy_input"); err != nil {
		t.Fatalf("superseded revision should not block deletion: %v", err)
	}
}

func TestRevisionLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	model := control.Model{ID: "etch-r1", FamilyID: "etch", Name: "Etch", Revision: 1, Status: "approved", Enabled: true, Indexes: map[string]control.IndexValues{"machine": {"E1"}, "action": {"ETCH"}, "material": {"OXIDE"}}, Parameters: []control.ParameterDefinition{{Key: "measured", Description: "Measured", Source: "constant", Definition: "2"}, {Key: "recipe", Source: "formula", Definition: "measured * 2"}}, Outputs: []string{"recipe"}}
	data, _ := json.Marshal(Config{Models: []control.Model{model}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	next := model
	next.Parameters[1].Definition = "measured * 3"
	next.ChangeNote = "Capability tuning"
	next.CreatedBy = "ME"
	draft, err := s.CreateRevision(model.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Revision != 2 || draft.Status != "draft" || draft.Enabled {
		t.Fatalf("bad draft: %+v", draft)
	}
	if _, err := s.Approve(draft.ID, "ADMIN", false); err == nil {
		t.Fatal("expected direct draft approval to be rejected")
	}
	submitted, err := s.SubmitForApproval(draft.ID, "ME")
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Status != "pending_approval" || submitted.SubmittedBy != "ME" || submitted.SubmittedAt == "" {
		t.Fatalf("bad submission: %+v", submitted)
	}
	if _, err := s.UpdateDraft(submitted.ID, submitted); err == nil {
		t.Fatal("expected pending revision editing to be rejected")
	}
	if _, err := s.Approve(submitted.ID, "ME", false); err == nil {
		t.Fatal("expected self-approval to be rejected")
	}
	approved, err := s.Approve(submitted.ID, "ADMIN", false)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != "approved" || !approved.Enabled || approved.ApprovedBy != "ADMIN" || approved.ApprovedAt == "" {
		t.Fatalf("bad approval: %+v", approved)
	}
	old, _ := s.Model(model.ID)
	if old.Status != "superseded" || old.Enabled {
		t.Fatalf("old revision still active: %+v", old)
	}
	branch := model
	branch.Parameters[1].Definition = "measured * 4"
	branch.ChangeNote = "Branch from the superseded baseline"
	branch.CreatedBy = "ME"
	branchedDraft, err := s.CreateRevision(model.ID, branch)
	if err != nil {
		t.Fatal(err)
	}
	if branchedDraft.Revision != 3 || branchedDraft.Status != "draft" || branchedDraft.Parameters[1].Definition != "measured * 4" {
		t.Fatalf("bad branched draft: %+v", branchedDraft)
	}
}

func TestSubmissionAndApprovalRequireEnabledDataSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	model := control.Model{
		ID: "sql-model-r1", FamilyID: "sql-model", Name: "SQL model", Revision: 1,
		Status: "draft", ChangeNote: "Initial", CreatedBy: "ME",
		Indexes: map[string]control.IndexValues{
			"machine": {"M1"}, "action": {"RUN"}, "material": {"WAFER"},
		},
		Parameters: []control.ParameterDefinition{{
			Key: "measurement", Source: "sql", DataType: "number",
			DataSourceID: "measurements", Definition: "SELECT 1",
		}},
		Outputs: []string{"measurement"},
	}
	data, _ := json.Marshal(Config{Models: []control.Model{model}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitForApproval(model.ID, "ME"); err == nil || !strings.Contains(err.Error(), "unknown data source") {
		t.Fatalf("expected missing source to block submission, got %v", err)
	}
	source, err := s.CreateSource(Source{ID: "measurements", Name: "Measurements", Engine: "sqlserver", Host: "db.local", Port: 1433, Database: "process", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitForApproval(model.ID, "ME"); err != nil {
		t.Fatalf("enabled source should allow submission: %v", err)
	}
	source.Enabled = false
	if _, err := s.UpdateSource(source.ID, source); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(model.ID, "ADMIN", false); err == nil || !strings.Contains(err.Error(), "disabled data source") {
		t.Fatalf("expected disabled source to block approval, got %v", err)
	}
	source.Enabled = true
	if _, err := s.UpdateSource(source.ID, source); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(model.ID, "ADMIN", false); err != nil {
		t.Fatalf("re-enabled source should allow approval: %v", err)
	}
}

func TestRejectReturnsPendingRevisionToDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	model := control.Model{ID: "etch-r1", FamilyID: "etch", Name: "Etch", Revision: 1, Status: "draft", ChangeNote: "Initial", CreatedBy: "ME", Indexes: map[string]control.IndexValues{"machine": {"E1"}, "action": {"ETCH"}, "material": {"OXIDE"}}, Parameters: []control.ParameterDefinition{{Key: "recipe", Source: "constant", Definition: "2"}}, Outputs: []string{"recipe"}}
	data, _ := json.Marshal(Config{Models: []control.Model{model}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitForApproval(model.ID, "ME"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reject(model.ID, "ADMIN", ""); err == nil {
		t.Fatal("expected rejection reason to be required")
	}
	rejected, err := s.Reject(model.ID, "ADMIN", "Add validation evidence")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != "draft" || rejected.RejectedBy != "ADMIN" || rejected.RejectionReason != "Add validation evidence" {
		t.Fatalf("bad rejected draft: %+v", rejected)
	}
}

func TestCreateModelStartsAsDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	model := control.Model{Name: "New Model", CreatedBy: "ME", ChangeNote: "Initial design", Indexes: map[string]control.IndexValues{"machine": {"M1"}, "action": {"ETCH"}, "material": {"OXIDE"}}, Parameters: []control.ParameterDefinition{{Key: "measurement", Description: "Measurement", Source: "constant", Definition: "2"}, {Key: "recipe", Source: "formula", Definition: "measurement * 2"}}, Outputs: []string{"recipe"}}
	created, err := s.CreateModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.Status != "draft" || created.Enabled {
		t.Fatalf("bad initial model: %+v", created)
	}
	if created.FamilyID != "new-model" || created.ID != "new-model-r1" {
		t.Fatalf("expected generated internal identifiers, got %+v", created)
	}
}

func TestModelFolderPersistsAndFollowsRevisionFamily(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := s.CreateFolder("Etch", "Etch models", "manager")
	if err != nil {
		t.Fatal(err)
	}
	model := control.Model{Name: "Folder example", FolderID: folder.ID, CreatedBy: "engineer", ChangeNote: "Initial", Indexes: map[string]control.IndexValues{"machine": {"M1"}, "action": {"ETCH"}, "material": {"OXIDE"}}, Parameters: []control.ParameterDefinition{{Key: "recipe", Source: "constant", Definition: "2"}}, Outputs: []string{"recipe"}}
	created, err := s.CreateModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if created.FolderID != folder.ID {
		t.Fatalf("folder not assigned: %+v", created)
	}
	if err := s.DeleteFolder(folder.ID); err == nil {
		t.Fatal("expected nonempty folder deletion to fail")
	}
	revision, err := s.CreateRevision(created.ID, model)
	if err != nil {
		t.Fatal(err)
	}
	if revision.FolderID != folder.ID {
		t.Fatal("revision did not inherit folder")
	}
	if err := s.MoveModelFamily(created.FamilyID, ""); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range reopened.Config().Models {
		if item.FamilyID == created.FamilyID && item.FolderID != "" {
			t.Fatalf("family revision retained old folder: %+v", item)
		}
	}
	if err := reopened.DeleteFolder(folder.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteDraftRevisionAndFamily(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	base := control.Model{ID: "etch-r1", FamilyID: "etch", Name: "Etch", Revision: 1, Status: "approved", Enabled: true, Indexes: map[string]control.IndexValues{"machine": {"E1"}, "action": {"ETCH"}, "material": {"OXIDE"}}, Parameters: []control.ParameterDefinition{{Key: "recipe", Source: "constant", Definition: "2"}}, Outputs: []string{"recipe"}}
	data, _ := json.Marshal(Config{Models: []control.Model{base}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	next := base
	next.ChangeNote = "Draft to remove"
	next.CreatedBy = "ME"
	draft, err := s.CreateRevision(base.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	draft.Name = "Updated draft"
	updated, err := s.UpdateDraft(draft.ID, draft)
	if err != nil {
		t.Fatalf("update draft revision: %v", err)
	}
	if updated.ID != draft.ID || updated.Revision != draft.Revision || updated.Name != "Updated draft" {
		t.Fatalf("draft identity changed during update: %+v", updated)
	}
	if err := s.DeleteRevision(draft.ID); err != nil {
		t.Fatalf("delete draft revision: %v", err)
	}
	if _, exists := s.Model(draft.ID); exists {
		t.Fatal("deleted draft revision still exists")
	}
	if err := s.DeleteRevision(base.ID); err == nil {
		t.Fatal("expected approved revision deletion to be rejected")
	}
	if _, err := s.UpdateDraft(base.ID, base); err == nil {
		t.Fatal("expected approved revision update to be rejected")
	}
	if err := s.DeleteFamily(base.ID); err != nil {
		t.Fatalf("delete model family: %v", err)
	}
	if len(s.History(base.FamilyID)) != 0 {
		t.Fatal("model family revisions still exist")
	}
}

func TestDeleteFamilyRejectsRetainedExecutionHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	model := control.Model{ID: "etch-r1", FamilyID: "etch", Name: "Etch", Revision: 1, Status: "approved"}
	data, _ := json.Marshal(Config{
		Models: []control.Model{model},
		Runs:   []RunRecord{{RunID: "run-1", FamilyID: "etch", ModelID: model.ID}},
	})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFamily(model.ID); err == nil || !strings.Contains(err.Error(), "execution history") {
		t.Fatalf("expected retained run history to block deletion, got %v", err)
	}
	if _, exists := s.Model(model.ID); !exists {
		t.Fatal("blocked family deletion removed the model")
	}
}

func TestDeactivateAndPurgeFamilyWithExecutionHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	model := control.Model{ID: "etch-r1", FamilyID: "etch", Name: "Etch", Revision: 1, Status: "approved", Enabled: true}
	data, _ := json.Marshal(Config{
		Models:         []control.Model{model},
		Runs:           []RunRecord{{RunID: "run-1", FamilyID: "etch", ModelID: model.ID}},
		AdaptiveStates: map[string]AdaptiveState{"etch|machine=M1": {Value: 1}},
	})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	deactivated, err := s.DeactivateFamily(model.ID)
	if err != nil || deactivated.Enabled {
		t.Fatalf("deactivate family failed: %+v %v", deactivated, err)
	}
	summary, err := s.ModelPurgeSummary(model.ID)
	if err != nil || summary.RevisionCount != 1 || summary.RunCount != 1 || summary.ModelName != "Etch" {
		t.Fatalf("unexpected purge summary: %+v %v", summary, err)
	}
	purged, err := s.PurgeFamily(model.ID)
	if err != nil || purged.RevisionCount != 1 || purged.RunCount != 1 {
		t.Fatalf("purge family failed: %+v %v", purged, err)
	}
	if _, exists := s.Model(model.ID); exists || len(s.Config().Runs) != 0 || len(s.Config().AdaptiveStates) != 0 {
		t.Fatal("purge retained current model, run, or adaptive state")
	}
}

func TestReactivateFamilyEnablesLatestApprovedRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	model := control.Model{
		ID: "etch-r2", FamilyID: "etch", Name: "Etch", Revision: 2,
		Status: "approved", Enabled: false,
		Indexes: map[string]control.IndexValues{
			"machine": {"M1"}, "action": {"RUN"}, "material": {"WAFER"},
		},
		Parameters: []control.ParameterDefinition{{Key: "recipe", Source: "expression", DataType: "string", Definition: `"R1"`}},
		Outputs:    []string{"recipe"},
	}
	data, _ := json.Marshal(Config{Models: []control.Model{model}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reactivated, err := s.ReactivateFamily(model.ID)
	if err != nil || !reactivated.Enabled || reactivated.ID != model.ID {
		t.Fatalf("reactivate family failed: %+v %v", reactivated, err)
	}
	if _, err := s.ReactivateFamily(model.ID); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("expected active model conflict, got %v", err)
	}
}

func TestPreparedRunLimitUsesConfiguredPartition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	model := control.Model{
		FamilyID:        "adaptive-model",
		ExecutionPolicy: control.ExecutionPolicy{MaxUnfinishedRuns: 1, PartitionBy: []string{"machine", "chamber"}},
		Indexes:         map[string]control.IndexValues{"machine": {"M1"}, "chamber": {"1", "2"}},
		Parameters: []control.ParameterDefinition{{
			Key: "offset", Source: "constant", Definition: "3",
			Adaptive: &control.AdaptiveDefinition{},
		}},
	}
	record := func(id, chamber string) RunRecord {
		return RunRecord{
			RunID: id, FamilyID: model.FamilyID, Status: "prepared",
			Context: map[string]interface{}{"machine": "M1", "chamber": chamber},
		}
	}
	if _, err := s.RecordPreparedRun(record("run-1", "1"), model, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordPreparedRun(record("run-2", "1"), model, nil); err == nil {
		t.Fatal("expected a second unfinished run in the same chamber to be rejected")
	}
	if _, err := s.RecordPreparedRun(record("run-3", "2"), model, nil); err != nil {
		t.Fatalf("independent chamber should not be blocked: %v", err)
	}
	if err := s.CompleteRun("run-1", "completed", nil, nil, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordPreparedRun(record("run-4", "1"), model, nil); err != nil {
		t.Fatalf("completed run should release the partition: %v", err)
	}
}

func TestPreparedRunsAreUnlimitedWhenLimitIsOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	model := control.Model{
		FamilyID: "unlimited-model",
		Indexes:  map[string]control.IndexValues{"machine": {"M1"}},
		ExecutionPolicy: control.ExecutionPolicy{
			PartitionBy: []string{"machine"},
		},
	}
	for _, runID := range []string{"run-1", "run-2", "run-3"} {
		_, err := s.RecordPreparedRun(RunRecord{
			RunID: runID, FamilyID: model.FamilyID, Status: "prepared",
			Context: map[string]interface{}{"machine": "M1"},
		}, model, nil)
		if err != nil {
			t.Fatalf("omitted limit should allow %s: %v", runID, err)
		}
	}
}

func TestPreparedRunPartitionCanUseExternalInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	model := control.Model{
		FamilyID:        "partitioned-model",
		Indexes:         map[string]control.IndexValues{"machine": {"M1"}},
		ExternalInputs:  []control.ExternalInputDefinition{{Name: "lot", Type: "string"}},
		ExecutionPolicy: control.ExecutionPolicy{MaxUnfinishedRuns: 1, PartitionBy: []string{"lot"}},
	}
	record := func(id, lot string) RunRecord {
		return RunRecord{RunID: id, FamilyID: model.FamilyID, Status: "prepared", Context: map[string]interface{}{"machine": "M1", "lot": lot}}
	}
	if _, err := s.RecordPreparedRun(record("run-a", "LOT-A"), model, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordPreparedRun(record("run-b", "LOT-A"), model, nil); err == nil {
		t.Fatal("expected the same lot partition to reach its limit")
	}
	if _, err := s.RecordPreparedRun(record("run-c", "LOT-B"), model, nil); err != nil {
		t.Fatalf("different lot partition should remain available: %v", err)
	}
}

func TestAdaptiveStatePersistsAndIsIdempotentPerRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	model := control.Model{FamilyID: "adaptive-model", Indexes: map[string]control.IndexValues{"machine": {"M1"}, "chamber": {"1", "2"}}, ExecutionPolicy: control.ExecutionPolicy{PartitionBy: []string{"machine", "chamber"}}, Parameters: []control.ParameterDefinition{{Key: "offset", Source: "constant", Definition: "3", Adaptive: &control.AdaptiveDefinition{InitialValue: 3, UpdateMethod: "ewma", UpdateParameter: "measurement", Alpha: .25}}}}
	data, _ := json.Marshal(Config{Models: []control.Model{model}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	context := map[string]interface{}{"machine": "M1", "chamber": "1"}
	if err := s.CommitAdaptive(model, context, "run-1", map[string]float64{"offset": 4}); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitAdaptive(model, context, "run-1", map[string]float64{"offset": 9}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if value := reloaded.AdaptiveValues(model, context)["offset"]; value != 4 {
		t.Fatalf("expected persisted idempotent value 4, got %g", value)
	}
	chamberTwo := map[string]interface{}{"machine": "M1", "chamber": "2"}
	if err := reloaded.CommitAdaptive(model, chamberTwo, "run-2", map[string]float64{"offset": 8}); err != nil {
		t.Fatal(err)
	}
	if value := reloaded.AdaptiveValues(model, context)["offset"]; value != 4 {
		t.Fatalf("chamber 1 state changed unexpectedly: %g", value)
	}
	if value := reloaded.AdaptiveValues(model, chamberTwo)["offset"]; value != 8 {
		t.Fatalf("expected chamber 2 state 8, got %g", value)
	}
}

func TestPostRunFeedbackUsesInitialValueAndIgnoresStaleCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	model := control.Model{
		FamilyID:        "polish",
		ExecutionPolicy: control.ExecutionPolicy{MaxUnfinishedRuns: 3},
		Indexes:         map[string]control.IndexValues{"machine": {"P1"}},
		FeedbackInputs:  []control.FeedbackInputDefinition{{Name: "removal", Type: "number", Required: true}},
		Parameters: []control.ParameterDefinition{{
			Key: "offset", Source: "constant", Definition: "3",
			Adaptive: &control.AdaptiveDefinition{
				InitialValue: 3, UpdateMethod: "ewma", Alpha: .5,
				UpdateInput: &control.AdaptiveUpdateInput{Source: "post-run-feedback", Name: "removal"},
			},
		}},
	}
	record := func(id string) RunRecord {
		return RunRecord{
			RunID: id, FamilyID: model.FamilyID, Status: "prepared",
			Context:    map[string]interface{}{"machine": "P1"},
			Parameters: map[string]interface{}{"offset": 3.0},
		}
	}
	sequenceOne, err := s.RecordPreparedRun(record("run-1"), model, nil)
	if err != nil {
		t.Fatal(err)
	}
	sequenceTwo, err := s.RecordPreparedRun(record("run-2"), model, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sequenceOne != 1 || sequenceTwo != 2 {
		t.Fatalf("unexpected run sequences: %d, %d", sequenceOne, sequenceTwo)
	}
	applied, ignored, err := s.CompleteRunWithFeedbackAdaptive("run-2", model, map[string]interface{}{"removal": 10.0})
	if err != nil || applied["offset"] != 6.5 || len(ignored) != 0 {
		t.Fatalf("newest completion was not applied from the initial value: %+v %+v %v", applied, ignored, err)
	}
	applied, ignored, err = s.CompleteRunWithFeedbackAdaptive("run-1", model, map[string]interface{}{"removal": 4.0})
	if err != nil || len(applied) != 0 || len(ignored) != 1 || ignored[0] != "offset" {
		t.Fatalf("stale completion was not ignored: %+v %+v %v", applied, ignored, err)
	}
	if value := s.AdaptiveValues(model, map[string]interface{}{"machine": "P1"})["offset"]; value != 6.5 {
		t.Fatalf("stale completion changed adaptive state to %g", value)
	}
	staleRun, _ := s.Run("run-1")
	if staleRun.Status != "completed" {
		t.Fatalf("stale feedback prevented run completion: %+v", staleRun)
	}
}
