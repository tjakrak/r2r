package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"r2r/internal/control"
	"sort"
	"strings"
	"sync"
	"time"
)

type Source struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Engine          string `json:"engine"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Database        string `json:"database"`
	Username        string `json:"username,omitempty"`
	SecretRef       string `json:"secretRef,omitempty"`
	TLSMode         string `json:"tlsMode"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	Location        string `json:"location"`
	Enabled         bool   `json:"enabled"`
	LastTestStatus  string `json:"lastTestStatus,omitempty"`
	LastTestMessage string `json:"lastTestMessage,omitempty"`
	LastTestAt      string `json:"lastTestAt,omitempty"`
	LastTestMS      int64  `json:"lastTestMs,omitempty"`
}
type InputDefinition struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Category    string `json:"category"`
	DataType    string `json:"dataType"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	System      bool   `json:"system,omitempty"`
}
type UserRegistration struct {
	EmployeeID  string `json:"employeeId"`
	LoginID     string `json:"loginId"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}
type Config struct {
	Sources          []Source                 `json:"sources"`
	Folders          []ModelFolder            `json:"folders"`
	InputDefinitions []InputDefinition        `json:"inputDefinitions"`
	Users            []UserRegistration       `json:"users,omitempty"`
	Models           []control.Model          `json:"models"`
	AdaptiveStates   map[string]AdaptiveState `json:"adaptiveStates,omitempty"`
	Runs             []RunRecord              `json:"runs,omitempty"`
	NextRunSequence  int64                    `json:"nextRunSequence,omitempty"`
}

type ModelFolder struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedBy   string `json:"createdBy"`
	CreatedAt   string `json:"createdAt"`
	UpdatedBy   string `json:"updatedBy"`
	UpdatedAt   string `json:"updatedAt"`
}

func (s *Store) folderExists(id string) bool {
	if id == "" {
		return true
	}
	for _, folder := range s.config.Folders {
		if folder.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) CreateFolder(name, description, actor string) (ModelFolder, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 200 || len([]rune(description)) > 1000 {
		return ModelFolder{}, errors.New("folder name is required (maximum 200 characters); description may be up to 1000 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, folder := range s.config.Folders {
		if strings.EqualFold(folder.Name, name) {
			return ModelFolder{}, errors.New("a folder with this name already exists")
		}
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ModelFolder{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	folder := ModelFolder{ID: "folder-" + hex.EncodeToString(bytes), Name: name, Description: description, CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now}
	if err := s.saveFolderSQL(folder); err != nil {
		return ModelFolder{}, err
	}
	s.config.Folders = append(s.config.Folders, folder)
	if err := s.persist(); err != nil {
		return ModelFolder{}, err
	}
	return folder, nil
}

func (s *Store) UpdateFolder(id, name, description, actor string) (ModelFolder, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 200 || len([]rune(description)) > 1000 {
		return ModelFolder{}, errors.New("folder name is required (maximum 200 characters); description may be up to 1000 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, folder := range s.config.Folders {
		if folder.ID != id && strings.EqualFold(folder.Name, name) {
			return ModelFolder{}, errors.New("a folder with this name already exists")
		}
	}
	for index, folder := range s.config.Folders {
		if folder.ID != id {
			continue
		}
		folder.Name, folder.Description, folder.UpdatedBy, folder.UpdatedAt = name, description, actor, time.Now().UTC().Format(time.RFC3339Nano)
		if err := s.saveFolderSQL(folder); err != nil {
			return ModelFolder{}, err
		}
		s.config.Folders[index] = folder
		return folder, s.persist()
	}
	return ModelFolder{}, errors.New("folder not found")
}

func (s *Store) DeleteFolder(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, model := range s.config.Models {
		if model.FolderID == id {
			return errors.New("move all models out of this folder before deleting it")
		}
	}
	for index, folder := range s.config.Folders {
		if folder.ID != id {
			continue
		}
		if err := s.deleteFolderSQL(id); err != nil {
			return err
		}
		s.config.Folders = append(s.config.Folders[:index], s.config.Folders[index+1:]...)
		return s.persist()
	}
	return errors.New("folder not found")
}

func (s *Store) MoveModelFamily(familyID, folderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.folderExists(folderID) {
		return errors.New("destination folder not found")
	}
	found := false
	for _, model := range s.config.Models {
		if model.FamilyID == familyID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("model family not found")
	}
	if err := s.moveModelFamilySQL(familyID, folderID); err != nil {
		return err
	}
	for index := range s.config.Models {
		if s.config.Models[index].FamilyID == familyID {
			s.config.Models[index].FolderID = folderID
		}
	}
	return s.persist()
}

type ModelPurgeSummary struct {
	FamilyID      string `json:"familyId"`
	ModelName     string `json:"modelName"`
	RevisionCount int    `json:"revisionCount"`
	RunCount      int    `json:"runCount"`
}

func (s *Store) UserRegistrations() []UserRegistration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]UserRegistration(nil), s.config.Users...)
}

func (s *Store) UpsertUserRegistration(next UserRegistration) (UserRegistration, error) {
	next.EmployeeID = strings.TrimSpace(next.EmployeeID)
	next.LoginID = strings.TrimSpace(next.LoginID)
	next.DisplayName = strings.TrimSpace(next.DisplayName)
	if next.DisplayName == "" {
		next.DisplayName = next.LoginID
	}
	if next.Status == "" {
		next.Status = "active"
	}
	validRole := next.Role == "viewer" || next.Role == "engineer" || next.Role == "engineering_manager" || next.Role == "admin"
	if next.EmployeeID == "" || next.LoginID == "" || !validRole || (next.Status != "active" && next.Status != "disabled") {
		return UserRegistration{}, errors.New("user registration needs an employee ID, login ID, valid role, and valid status")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, existing := range s.config.Users {
		if strings.EqualFold(strings.TrimSpace(existing.EmployeeID), next.EmployeeID) {
			s.config.Users[index] = next
			return next, s.persistUser(next)
		}
	}
	s.config.Users = append(s.config.Users, next)
	return next, s.persistUser(next)
}

func (s *Store) DeleteUserRegistration(employeeID string) error {
	employeeID = strings.TrimSpace(employeeID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, existing := range s.config.Users {
		if strings.EqualFold(strings.TrimSpace(existing.EmployeeID), employeeID) {
			if err := s.deleteSQL("USERS", "EMPLOYEE_ID", employeeID); err != nil {
				return err
			}
			s.config.Users = append(s.config.Users[:index], s.config.Users[index+1:]...)
			return s.persist()
		}
	}
	return errors.New("R2R user registration not found")
}

func (s *Store) TransferDraftOwner(modelID, employeeID string) (control.Model, error) {
	employeeID = strings.TrimSpace(employeeID)
	if employeeID == "" {
		return control.Model{}, errors.New("new owner employee ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, model := range s.config.Models {
		if model.ID != modelID {
			continue
		}
		if model.Status != "draft" {
			return control.Model{}, errors.New("only draft revisions can be transferred")
		}
		s.config.Models[index].Owner = employeeID
		if err := s.persistModels(s.config.Models[index]); err != nil {
			return control.Model{}, err
		}
		return s.config.Models[index], nil
	}
	return control.Model{}, errors.New("model revision not found")
}

type RunRecord struct {
	RunID           string                              `json:"runId"`
	FamilyID        string                              `json:"familyId"`
	ModelID         string                              `json:"modelId"`
	ModelRevision   int                                 `json:"modelRevision"`
	Sequence        int64                               `json:"sequence"`
	Status          string                              `json:"status"`
	PreparedAt      string                              `json:"preparedAt"`
	CompletedAt     string                              `json:"completedAt,omitempty"`
	Context         map[string]interface{}              `json:"context"`
	Parameters      map[string]interface{}              `json:"parameters"`
	Outputs         []control.OutputValue               `json:"outputs"`
	Feedback        map[string]interface{}              `json:"feedback,omitempty"`
	AdaptiveUpdates map[string]float64                  `json:"adaptiveUpdates,omitempty"`
	DataSamples     map[string][]map[string]interface{} `json:"dataSamples,omitempty"`
	Disposition     string                              `json:"disposition,omitempty"`
	RequestID       string                              `json:"requestId,omitempty"`
	FailureCode     string                              `json:"failureCode,omitempty"`
	FailureMessage  string                              `json:"failureMessage,omitempty"`
}
type AdaptiveState struct {
	Value        float64 `json:"value"`
	LastRunID    string  `json:"lastRunId"`
	LastSequence int64   `json:"lastSequence,omitempty"`
	UpdatedAt    string  `json:"updatedAt"`
}
type Store struct {
	mu     sync.RWMutex
	path   string
	db     *sql.DB
	config Config
}

func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &s.config); err != nil {
		return nil, err
	}
	s.seedInputDefinitions()
	for modelIndex := range s.config.Models {
		for parameterIndex := range s.config.Models[modelIndex].Parameters {
			parameter := &s.config.Models[modelIndex].Parameters[parameterIndex]
			if parameter.Source == "adaptive" && parameter.Adaptive != nil {
				parameter.Source = "constant"
				parameter.Definition = fmt.Sprint(parameter.Adaptive.InitialValue)
			}
			if parameter.Adaptive != nil && parameter.Adaptive.UpdateParameter == "" {
				parameter.Adaptive.UpdateParameter = parameter.Adaptive.MeasurementParameter
				parameter.Adaptive.MeasurementParameter = ""
			}
			if parameter.Adaptive != nil && parameter.Adaptive.UpdateInput == nil {
				parameter.Adaptive.UpdateInput = &control.AdaptiveUpdateInput{
					Source: "parameter", Name: parameter.Adaptive.UpdateParameter,
				}
				parameter.Adaptive.UpdateParameter = ""
			}
		}
	}
	for _, run := range s.config.Runs {
		if run.Sequence > s.config.NextRunSequence {
			s.config.NextRunSequence = run.Sequence
		}
	}
	return s, nil
}

func (s *Store) seedInputDefinitions() {
	seen := map[string]bool{}
	for _, definition := range s.config.InputDefinitions {
		seen[definition.Category+"\x00"+strings.ToLower(definition.Name)] = true
	}
	add := func(definition InputDefinition) {
		key := definition.Category + "\x00" + strings.ToLower(definition.Name)
		if !seen[key] {
			seen[key] = true
			s.config.InputDefinitions = append(s.config.InputDefinitions, definition)
		}
	}
	for _, name := range []string{"machine", "action", "material"} {
		add(InputDefinition{Name: name, DisplayName: strings.Title(name), Category: "index", DataType: "string", Status: "active", System: true})
	}
	for _, model := range s.config.Models {
		for name := range model.Indexes {
			if name != "machine" && name != "action" && name != "material" {
				add(InputDefinition{Name: name, DisplayName: name, Category: "index", DataType: "string", Status: "active"})
			}
		}
		for _, input := range model.ExternalInputs {
			add(InputDefinition{Name: input.Name, DisplayName: input.Name, Category: "external", DataType: input.Type, Description: input.Description, Status: "active"})
		}
	}
}

func (s *Store) CreateInputDefinition(next InputDefinition) (InputDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := normalizeInputDefinition(&next); err != nil {
		return InputDefinition{}, err
	}
	for _, existing := range s.config.InputDefinitions {
		if strings.EqualFold(existing.Name, next.Name) {
			return InputDefinition{}, fmt.Errorf("input name %q already exists", next.Name)
		}
	}
	s.config.InputDefinitions = append(s.config.InputDefinitions, next)
	if err := s.persistInput(next); err != nil {
		return InputDefinition{}, err
	}
	return next, nil
}

func (s *Store) UpdateInputDefinition(name string, next InputDefinition) (InputDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, existing := range s.config.InputDefinitions {
		if strings.EqualFold(existing.Name, name) {
			next.Name = existing.Name
			next.Category = existing.Category
			next.System = existing.System
			if err := normalizeInputDefinition(&next); err != nil {
				return InputDefinition{}, err
			}
			s.config.InputDefinitions[index] = next
			if err := s.persistInput(next); err != nil {
				return InputDefinition{}, err
			}
			return next, nil
		}
	}
	return InputDefinition{}, errors.New("input definition not found")
}

func (s *Store) DeleteInputDefinition(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	definitionIndex := -1
	for index, definition := range s.config.InputDefinitions {
		if strings.EqualFold(definition.Name, name) {
			if definition.System {
				return fmt.Errorf("system input definition %s cannot be deleted", definition.Name)
			}
			definitionIndex = index
			break
		}
	}
	if definitionIndex < 0 {
		return errors.New("input definition not found")
	}
	for _, model := range s.config.Models {
		if model.Status != "approved" && model.Status != "draft" && model.Status != "pending_approval" {
			continue
		}
		for indexName := range model.Indexes {
			if strings.EqualFold(indexName, name) {
				return fmt.Errorf("input definition %s is referenced by model %s revision %d", name, model.Name, model.Revision)
			}
		}
		for _, input := range model.ExternalInputs {
			if strings.EqualFold(input.Name, name) {
				return fmt.Errorf("input definition %s is referenced by model %s revision %d", name, model.Name, model.Revision)
			}
		}
	}
	if err := s.deleteSQL("INPUT_DEFINITIONS", "INPUT_NAME", name); err != nil {
		return err
	}
	s.config.InputDefinitions = append(s.config.InputDefinitions[:definitionIndex], s.config.InputDefinitions[definitionIndex+1:]...)
	return s.persist()
}

func normalizeInputDefinition(definition *InputDefinition) error {
	definition.Name = strings.TrimSpace(definition.Name)
	if definition.DisplayName == "" {
		definition.DisplayName = definition.Name
	}
	if definition.Status == "" {
		definition.Status = "active"
	}
	if definition.Name == "" || (definition.Category != "index" && definition.Category != "external") || (definition.DataType != "string" && definition.DataType != "number" && definition.DataType != "boolean") || (definition.Status != "active" && definition.Status != "deprecated") {
		return errors.New("input definition needs a name, category, data type, and valid status")
	}
	if definition.Category == "index" {
		definition.DataType = "string"
	}
	return nil
}

func validateRegisteredInputs(model control.Model, definitions []InputDefinition) error {
	registered := map[string]InputDefinition{}
	for _, definition := range definitions {
		registered[strings.ToLower(definition.Name)] = definition
	}
	for name := range model.Indexes {
		if _, core := map[string]bool{"machine": true, "action": true, "material": true}[strings.ToLower(name)]; core {
			continue
		}
		definition, exists := registered[strings.ToLower(name)]
		if !exists || definition.Category != "index" || definition.Status != "active" {
			return fmt.Errorf("additional index %s is not an active registered index", name)
		}
	}
	for _, input := range model.ExternalInputs {
		definition, exists := registered[strings.ToLower(input.Name)]
		if !exists || definition.Category != "external" || definition.Status != "active" {
			return fmt.Errorf("external input %s is not active in the input registry", input.Name)
		}
		if definition.DataType != input.Type {
			return fmt.Errorf("external input %s must use registered type %s", input.Name, definition.DataType)
		}
	}
	return nil
}
func (s *Store) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, _ := json.Marshal(s.config)
	var copy Config
	_ = json.Unmarshal(data, &copy)
	return copy
}
func (s *Store) RecordRun(record RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.config.Runs {
		if existing.RunID == record.RunID {
			return errors.New("runId already exists")
		}
	}
	s.config.Runs = append(s.config.Runs, record)
	return s.persistRun(record)
}
func (s *Store) RecordPreparedRun(record RunRecord, model control.Model, adaptiveUpdates map[string]float64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.config.Runs {
		if existing.RunID == record.RunID {
			return 0, errors.New("runId already exists")
		}
	}
	limit := model.ExecutionPolicy.MaxUnfinishedRuns
	if limit > 0 {
		partition := executionPartitionKey(model, record.Context)
		unfinished := 0
		for _, existing := range s.config.Runs {
			if existing.FamilyID != model.FamilyID || existing.Status != "prepared" {
				continue
			}
			if partition == executionPartitionKey(model, existing.Context) {
				unfinished++
			}
		}
		if unfinished >= limit {
			return 0, fmt.Errorf("maximum unfinished runs reached for this partition (%d)", limit)
		}
	}
	s.config.NextRunSequence++
	record.Sequence = s.config.NextRunSequence
	if s.config.AdaptiveStates == nil {
		s.config.AdaptiveStates = map[string]AdaptiveState{}
	}
	for parameter, value := range adaptiveUpdates {
		for _, definition := range model.Parameters {
			if definition.Key == parameter && definition.Adaptive != nil {
				key := AdaptiveStateKey(model, definition, record.Context)
				s.config.AdaptiveStates[key] = AdaptiveState{
					Value: value, LastRunID: record.RunID,
					LastSequence: record.Sequence,
					UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
				}
				break
			}
		}
	}
	s.config.Runs = append(s.config.Runs, record)
	return record.Sequence, s.persistRun(record)
}
func (s *Store) Run(id string) (RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.config.Runs {
		if record.RunID == id {
			return record, true
		}
	}
	return RunRecord{}, false
}
func (s *Store) HasUnfinishedRun(model control.Model, context map[string]interface{}) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	partition := executionPartitionKey(model, context)
	for _, existing := range s.config.Runs {
		if existing.FamilyID == model.FamilyID && existing.Status == "prepared" &&
			partition == executionPartitionKey(model, existing.Context) {
			return true
		}
	}
	return false
}
func (s *Store) CheckPreparedRunLimit(model control.Model, context map[string]interface{}) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := model.ExecutionPolicy.MaxUnfinishedRuns
	if limit <= 0 {
		return nil
	}
	partition := executionPartitionKey(model, context)
	unfinished := 0
	for _, existing := range s.config.Runs {
		if existing.FamilyID == model.FamilyID && existing.Status == "prepared" &&
			partition == executionPartitionKey(model, existing.Context) {
			unfinished++
		}
	}
	if unfinished >= limit {
		return fmt.Errorf("maximum unfinished runs reached for this partition (%d)", limit)
	}
	return nil
}
func (s *Store) CompleteRun(id, status string, feedback map[string]interface{}, updates map[string]float64, failureCode, failureMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.config.Runs {
		if s.config.Runs[index].RunID == id {
			if s.config.Runs[index].Status != "prepared" {
				return errors.New("run completion was already reported")
			}
			s.config.Runs[index].Status = status
			s.config.Runs[index].Feedback = feedback
			s.config.Runs[index].CompletedAt = time.Now().UTC().Format(time.RFC3339)
			s.config.Runs[index].AdaptiveUpdates = updates
			s.config.Runs[index].FailureCode = failureCode
			s.config.Runs[index].FailureMessage = failureMessage
			return s.persistRun(s.config.Runs[index])
		}
	}
	return errors.New("prepared run not found")
}
func (s *Store) CompleteRunWithFeedbackAdaptive(id string, model control.Model, feedback map[string]interface{}) (map[string]float64, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runIndex := -1
	for index := range s.config.Runs {
		if s.config.Runs[index].RunID == id {
			runIndex = index
			break
		}
	}
	if runIndex < 0 {
		return nil, nil, errors.New("prepared run not found")
	}
	record := &s.config.Runs[runIndex]
	if record.Status != "prepared" {
		return nil, nil, errors.New("run completion was already reported")
	}
	values := map[string]interface{}{}
	for key, value := range record.Parameters {
		values[key] = value
	}
	for key, value := range feedback {
		values[key] = value
	}
	for _, definition := range model.Parameters {
		if definition.Adaptive == nil {
			continue
		}
		if state, exists := s.config.AdaptiveStates[AdaptiveStateKey(model, definition, record.Context)]; exists {
			values[definition.Key] = state.Value
		}
	}
	calculated, err := control.CalculateAdaptiveUpdatesForSource(model, values, "post-run-feedback")
	if err != nil {
		return nil, nil, err
	}
	if s.config.AdaptiveStates == nil {
		s.config.AdaptiveStates = map[string]AdaptiveState{}
	}
	applied := map[string]float64{}
	ignored := []string{}
	for parameter, value := range calculated {
		for _, definition := range model.Parameters {
			if definition.Key != parameter {
				continue
			}
			key := AdaptiveStateKey(model, definition, record.Context)
			state := s.config.AdaptiveStates[key]
			if record.Sequence <= state.LastSequence {
				ignored = append(ignored, parameter)
				break
			}
			s.config.AdaptiveStates[key] = AdaptiveState{
				Value: value, LastRunID: record.RunID, LastSequence: record.Sequence,
				UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			applied[parameter] = value
			break
		}
	}
	record.Status = "completed"
	record.Feedback = feedback
	record.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	record.AdaptiveUpdates = applied
	return applied, ignored, s.persistRun(*record)
}
func (s *Store) RunsForFamily(familyID string, since time.Time) []RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []RunRecord{}
	for _, record := range s.config.Runs {
		prepared, err := time.Parse(time.RFC3339, record.PreparedAt)
		if record.FamilyID == familyID && err == nil && !prepared.Before(since) {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PreparedAt > out[j].PreparedAt })
	return out
}
func (s *Store) RecentRuns(limit int) []RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]RunRecord{}, s.config.Runs...)
	sort.Slice(out, func(i, j int) bool { return out[i].PreparedAt > out[j].PreparedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
func AdaptiveStateKey(model control.Model, definition control.ParameterDefinition, context map[string]interface{}) string {
	return executionPartitionKey(model, context) + "|parameter=" + definition.Key
}

func executionPartitionKey(model control.Model, context map[string]interface{}) string {
	fields := append([]string(nil), model.ExecutionPolicy.PartitionBy...)
	if len(fields) == 0 {
		fields = []string{"machine"}
	}
	sort.Strings(fields)
	parts := []string{model.FamilyID}
	for _, field := range fields {
		parts = append(parts, field+"="+fmt.Sprint(context[field]))
	}
	return strings.Join(parts, "|")
}
func (s *Store) AdaptiveValues(model control.Model, context map[string]interface{}) map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	values := map[string]float64{}
	for _, parameter := range model.Parameters {
		if parameter.Adaptive != nil {
			if state, ok := s.config.AdaptiveStates[AdaptiveStateKey(model, parameter, context)]; ok {
				values[parameter.Key] = state.Value
			}
		}
	}
	return values
}
func (s *Store) CommitAdaptive(model control.Model, context map[string]interface{}, runID string, updates map[string]float64) error {
	if len(updates) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.AdaptiveStates == nil {
		s.config.AdaptiveStates = map[string]AdaptiveState{}
	}
	for parameter, value := range updates {
		var definition control.ParameterDefinition
		for _, candidate := range model.Parameters {
			if candidate.Key == parameter {
				definition = candidate
				break
			}
		}
		key := AdaptiveStateKey(model, definition, context)
		if state, ok := s.config.AdaptiveStates[key]; ok && state.LastRunID == runID {
			continue
		}
		s.config.AdaptiveStates[key] = AdaptiveState{Value: value, LastRunID: runID, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	// Immediate executions persist the run and its adaptive updates together in
	// RecordRun so LAST_RUN_ID always references an existing PCS.RUNS row.
	if s.db != nil {
		return nil
	}
	return s.persist()
}
func (s *Store) persist() error {
	if s.db != nil {
		return nil
	}
	data, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0644)
}
func (s *Store) Model(id string) (control.Model, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.config.Models {
		if m.ID == id {
			return m, true
		}
	}
	return control.Model{}, false
}
func (s *Store) UpdateSource(id string, next Source) (Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.config.Sources {
		if s.config.Sources[index].ID != id {
			continue
		}
		next.ID = id
		if err := normalizeSource(&next); err != nil {
			return Source{}, err
		}
		next.LastTestStatus = s.config.Sources[index].LastTestStatus
		next.LastTestMessage = s.config.Sources[index].LastTestMessage
		next.LastTestAt = s.config.Sources[index].LastTestAt
		next.LastTestMS = s.config.Sources[index].LastTestMS
		next.Location = fmt.Sprintf("%s:%d/%s", next.Host, next.Port, next.Database)
		s.config.Sources[index] = next
		return next, s.persistSource(next)
	}
	return Source{}, errors.New("data source not found")
}

func (s *Store) CreateSource(next Source) (Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next.ID = strings.TrimSpace(next.ID)
	if next.ID == "" {
		return Source{}, errors.New("data source identifier is required")
	}
	for _, existing := range s.config.Sources {
		if strings.EqualFold(existing.ID, next.ID) {
			return Source{}, fmt.Errorf("data source identifier %q already exists", next.ID)
		}
		if strings.EqualFold(existing.Name, next.Name) {
			return Source{}, fmt.Errorf("data source name %q already exists", next.Name)
		}
	}
	if err := normalizeSource(&next); err != nil {
		return Source{}, err
	}
	s.config.Sources = append(s.config.Sources, next)
	if err := s.persistSource(next); err != nil {
		return Source{}, err
	}
	return next, nil
}

func normalizeSource(source *Source) error {
	if source.Name == "" || source.Engine == "" || source.Host == "" || source.Port < 1 || source.Database == "" {
		return errors.New("data source name, engine, host, port, and database are required")
	}
	if source.Engine != "sqlserver" && source.Engine != "postgres" && source.Engine != "mysql" {
		return errors.New("data source engine must be sqlserver, postgres, or mysql")
	}
	if source.Type == "" {
		source.Type = "sql"
	}
	if source.Type != "sql" {
		return errors.New("data source type must be sql")
	}
	if source.TimeoutSeconds < 1 {
		source.TimeoutSeconds = 5
	}
	if source.TLSMode == "" {
		source.TLSMode = "require"
	}
	source.Location = fmt.Sprintf("%s:%d/%s", source.Host, source.Port, source.Database)
	return nil
}
func (s *Store) Source(id string) (Source, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, source := range s.config.Sources {
		if source.ID == id {
			return source, true
		}
	}
	return Source{}, false
}
func (s *Store) RecordSourceTest(id, status, message string, elapsedMS int64) (Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.config.Sources {
		if s.config.Sources[index].ID != id {
			continue
		}
		s.config.Sources[index].LastTestStatus = status
		s.config.Sources[index].LastTestMessage = message
		s.config.Sources[index].LastTestAt = time.Now().UTC().Format(time.RFC3339)
		s.config.Sources[index].LastTestMS = elapsedMS
		return s.config.Sources[index], s.persistSource(s.config.Sources[index])
	}
	return Source{}, errors.New("data source not found")
}
func (s *Store) CreateModel(model control.Model) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	model = normalizeDraftCollections(model)
	if strings.TrimSpace(model.Name) == "" {
		return control.Model{}, errors.New("model name is required")
	}
	if !s.folderExists(model.FolderID) {
		return control.Model{}, errors.New("destination folder not found")
	}
	if model.ChangeNote == "" || model.CreatedBy == "" {
		return control.Model{}, errors.New("change note and author are required")
	}
	if strings.TrimSpace(model.FamilyID) == "" {
		base := modelFamilyID(model.Name)
		model.FamilyID = base
		for suffix := 2; s.familyExists(model.FamilyID); suffix++ {
			model.FamilyID = fmt.Sprintf("%s-%d", base, suffix)
		}
	}
	for _, existing := range s.config.Models {
		if existing.FamilyID == model.FamilyID {
			return control.Model{}, errors.New("model familyId already exists")
		}
	}
	model.Revision = 1
	model.ID = fmt.Sprintf("%s-r1", model.FamilyID)
	model.Status = "draft"
	model.Enabled = false
	model.SubmittedBy = ""
	model.SubmittedAt = ""
	model.RejectedBy = ""
	model.RejectedAt = ""
	model.RejectionReason = ""
	model.ApprovedBy = ""
	model.ApprovedAt = ""
	model.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	s.config.Models = append(s.config.Models, model)
	return model, s.persistModels(model)
}

func modelFamilyID(name string) string {
	var builder strings.Builder
	separator := false
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			builder.WriteRune(char)
			separator = false
		} else if builder.Len() > 0 && !separator {
			builder.WriteByte('-')
			separator = true
		}
	}
	id := strings.Trim(builder.String(), "-")
	if id == "" {
		return "control-model"
	}
	return id
}

func (s *Store) familyExists(familyID string) bool {
	for _, model := range s.config.Models {
		if model.FamilyID == familyID {
			return true
		}
	}
	return false
}

func (s *Store) CreateRevision(baseID string, next control.Model) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next = normalizeDraftCollections(next)
	var base *control.Model
	maxRev := 0
	for i := range s.config.Models {
		if s.config.Models[i].ID == baseID {
			base = &s.config.Models[i]
			break
		}
	}
	if base == nil {
		return control.Model{}, errors.New("base model not found")
	}
	if open, exists := openRevisionForFamily(s.config.Models, base.FamilyID, ""); exists {
		return control.Model{}, fmt.Errorf("model family already has an open revision (revision %d, %s); resolve it before creating another", open.Revision, open.Status)
	}
	if strings.TrimSpace(next.Name) == "" {
		return control.Model{}, errors.New("model name is required")
	}
	if next.ChangeNote == "" || next.CreatedBy == "" {
		return control.Model{}, errors.New("change note and author are required")
	}
	for i := range s.config.Models {
		if s.config.Models[i].FamilyID == base.FamilyID && s.config.Models[i].Revision > maxRev {
			maxRev = s.config.Models[i].Revision
		}
	}
	next.FamilyID = base.FamilyID
	next.FolderID = base.FolderID
	next.Revision = maxRev + 1
	next.ID = fmt.Sprintf("%s-r%d", base.FamilyID, next.Revision)
	next.Status = "draft"
	next.Enabled = false
	next.SubmittedBy = ""
	next.SubmittedAt = ""
	next.RejectedBy = ""
	next.RejectedAt = ""
	next.RejectionReason = ""
	next.ApprovedBy = ""
	next.ApprovedAt = ""
	next.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	s.config.Models = append(s.config.Models, next)
	return next, s.persistModels(next)
}

func openRevisionForFamily(models []control.Model, familyID, exceptID string) (control.Model, bool) {
	for _, model := range models {
		if model.FamilyID == familyID && model.ID != exceptID && (model.Status == "draft" || model.Status == "pending_approval") {
			return model, true
		}
	}
	return control.Model{}, false
}

func (s *Store) DeleteRevision(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, model := range s.config.Models {
		if model.ID != id {
			continue
		}
		if model.Status != "draft" {
			return errors.New("only draft revisions can be deleted")
		}
		if err := s.deleteSQL("MODEL_REVISIONS", "MODEL_ID", id); err != nil {
			return err
		}
		s.config.Models = append(s.config.Models[:index], s.config.Models[index+1:]...)
		return s.persist()
	}
	return errors.New("model revision not found")
}

func (s *Store) UpdateDraft(id string, next control.Model) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next = normalizeDraftCollections(next)
	for index, current := range s.config.Models {
		if current.ID != id {
			continue
		}
		if current.Status != "draft" {
			return control.Model{}, errors.New("only draft revisions can be edited")
		}
		if strings.TrimSpace(next.Name) == "" {
			return control.Model{}, errors.New("model name is required")
		}
		next.ID = current.ID
		next.FamilyID = current.FamilyID
		next.FolderID = current.FolderID
		next.Revision = current.Revision
		next.Status = "draft"
		next.Enabled = false
		next.Owner = current.Owner
		next.CreatedAt = current.CreatedAt
		next.CreatedBy = current.CreatedBy
		next.SubmittedBy = current.SubmittedBy
		next.SubmittedAt = current.SubmittedAt
		next.RejectedBy = current.RejectedBy
		next.RejectedAt = current.RejectedAt
		next.RejectionReason = current.RejectionReason
		next.ApprovedBy = ""
		next.ApprovedAt = ""
		s.config.Models[index] = next
		return next, s.persistModels(next)
	}
	return control.Model{}, errors.New("draft revision not found")
}

func normalizeDraftCollections(model control.Model) control.Model {
	if model.Indexes == nil {
		model.Indexes = map[string]control.IndexValues{}
	}
	if model.ExternalInputs == nil {
		model.ExternalInputs = []control.ExternalInputDefinition{}
	}
	if model.FeedbackInputs == nil {
		model.FeedbackInputs = []control.FeedbackInputDefinition{}
	}
	if model.Parameters == nil {
		model.Parameters = []control.ParameterDefinition{}
	}
	if model.DataSamples == nil {
		model.DataSamples = []control.DataSampleDefinition{}
	}
	if model.Outputs == nil {
		model.Outputs = []string{}
	}
	return model
}

func (s *Store) DeleteFamily(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	familyID := ""
	for _, model := range s.config.Models {
		if model.ID == id {
			familyID = model.FamilyID
			break
		}
	}
	if familyID == "" {
		return errors.New("model not found")
	}
	for _, run := range s.config.Runs {
		if run.FamilyID == familyID {
			return errors.New("this model has execution history and cannot be permanently deleted")
		}
	}
	if err := s.deleteSQL("MODEL_FAMILIES", "FAMILY_ID", familyID); err != nil {
		return err
	}
	kept := s.config.Models[:0]
	for _, model := range s.config.Models {
		if model.FamilyID != familyID {
			kept = append(kept, model)
		}
	}
	s.config.Models = kept
	for key := range s.config.AdaptiveStates {
		if strings.HasPrefix(key, familyID+"|") {
			delete(s.config.AdaptiveStates, key)
		}
	}
	return s.persist()
}

func (s *Store) DeactivateFamily(id string) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	familyID := ""
	for _, model := range s.config.Models {
		if model.ID == id {
			familyID = model.FamilyID
			break
		}
	}
	if familyID == "" {
		return control.Model{}, errors.New("model not found")
	}
	changed := []control.Model{}
	var deactivated control.Model
	for index := range s.config.Models {
		model := &s.config.Models[index]
		if model.FamilyID == familyID && model.Status == "approved" && model.Enabled {
			model.Enabled = false
			deactivated = *model
			changed = append(changed, *model)
		}
	}
	if len(changed) == 0 {
		return control.Model{}, errors.New("model is already inactive")
	}
	if err := s.persistModels(changed...); err != nil {
		return control.Model{}, err
	}
	return deactivated, nil
}

// ReactivateFamily enables the latest approved revision in an inactive model family.
// It revalidates the stored definition and its registered dependencies before making
// the revision eligible for production selection again.
func (s *Store) ReactivateFamily(id string) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	familyID := ""
	for _, model := range s.config.Models {
		if model.ID == id {
			familyID = model.FamilyID
			break
		}
	}
	if familyID == "" {
		return control.Model{}, errors.New("model not found")
	}
	latestApproved := -1
	for index, model := range s.config.Models {
		if model.FamilyID != familyID || model.Status != "approved" {
			continue
		}
		if model.Enabled {
			return control.Model{}, errors.New("model is already active")
		}
		if latestApproved < 0 || model.Revision > s.config.Models[latestApproved].Revision {
			latestApproved = index
		}
	}
	if latestApproved < 0 {
		return control.Model{}, errors.New("model has no approved inactive revision to reactivate")
	}
	candidate := s.config.Models[latestApproved]
	if err := validateProductionReadyModel(candidate, s.config.InputDefinitions, s.config.Sources); err != nil {
		return control.Model{}, fmt.Errorf("cannot reactivate model: %w", err)
	}
	for _, existing := range s.config.Models {
		if existing.FamilyID != familyID && existing.Status == "approved" && existing.Enabled && control.HasAmbiguousRouting(candidate, existing) {
			return control.Model{}, fmt.Errorf("cannot reactivate: routing overlaps equally with active model %q", existing.Name)
		}
	}
	s.config.Models[latestApproved].Enabled = true
	if err := s.persistModels(s.config.Models[latestApproved]); err != nil {
		s.config.Models[latestApproved].Enabled = false
		return control.Model{}, err
	}
	return s.config.Models[latestApproved], nil
}

func validateEnabledDataSources(model control.Model, sources []Source) error {
	registered := map[string]Source{}
	for _, source := range sources {
		registered[strings.ToLower(strings.TrimSpace(source.ID))] = source
	}
	check := func(sourceID, owner string) error {
		source, exists := registered[strings.ToLower(strings.TrimSpace(sourceID))]
		if !exists {
			return fmt.Errorf("%s references unknown data source %q", owner, sourceID)
		}
		if !source.Enabled {
			return fmt.Errorf("%s references disabled data source %q", owner, sourceID)
		}
		return nil
	}
	for _, parameter := range model.Parameters {
		if parameter.Source == "sql" {
			if err := check(parameter.DataSourceID, "parameter "+parameter.Key); err != nil {
				return err
			}
		}
	}
	for _, sample := range model.DataSamples {
		if sample.Source == "sql" {
			if err := check(sample.DataSourceID, "data sample "+sample.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProductionReadyModel(model control.Model, definitions []InputDefinition, sources []Source) error {
	if err := validateRegisteredInputs(model, definitions); err != nil {
		return err
	}
	if err := validateEnabledDataSources(model, sources); err != nil {
		return err
	}
	return control.ValidateModel(model)
}

func (s *Store) DraftValidationIssues(model control.Model) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := validateProductionReadyModel(model, s.config.InputDefinitions, s.config.Sources); err != nil {
		return []string{err.Error()}
	}
	return []string{}
}

func (s *Store) ModelPurgeSummary(id string) (ModelPurgeSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	summary := ModelPurgeSummary{}
	for _, model := range s.config.Models {
		if model.ID == id {
			summary.FamilyID = model.FamilyID
			summary.ModelName = model.Name
			break
		}
	}
	if summary.FamilyID == "" {
		return summary, errors.New("model not found")
	}
	if s.db != nil {
		return s.modelPurgeSummarySQL(summary)
	}
	for _, model := range s.config.Models {
		if model.FamilyID == summary.FamilyID {
			summary.RevisionCount++
		}
	}
	for _, run := range s.config.Runs {
		if run.FamilyID == summary.FamilyID {
			summary.RunCount++
		}
	}
	return summary, nil
}

func (s *Store) PurgeFamily(id string) (ModelPurgeSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	summary := ModelPurgeSummary{}
	for _, model := range s.config.Models {
		if model.ID == id {
			summary.FamilyID = model.FamilyID
			summary.ModelName = model.Name
			break
		}
	}
	if summary.FamilyID == "" {
		return summary, errors.New("model not found")
	}
	var err error
	if s.db != nil {
		summary, err = s.purgeFamilySQL(summary)
		if err != nil {
			return summary, err
		}
	} else {
		for _, model := range s.config.Models {
			if model.FamilyID == summary.FamilyID {
				summary.RevisionCount++
			}
		}
		for _, run := range s.config.Runs {
			if run.FamilyID == summary.FamilyID {
				summary.RunCount++
			}
		}
	}
	models := s.config.Models[:0]
	for _, model := range s.config.Models {
		if model.FamilyID != summary.FamilyID {
			models = append(models, model)
		}
	}
	s.config.Models = models
	runs := s.config.Runs[:0]
	for _, run := range s.config.Runs {
		if run.FamilyID != summary.FamilyID {
			runs = append(runs, run)
		}
	}
	s.config.Runs = runs
	for key := range s.config.AdaptiveStates {
		if strings.HasPrefix(key, summary.FamilyID+"|") {
			delete(s.config.AdaptiveStates, key)
		}
	}
	return summary, s.persist()
}
func (s *Store) SubmitForApproval(id, actor string) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, model := range s.config.Models {
		if model.ID != id {
			continue
		}
		if model.Status != "draft" {
			return control.Model{}, errors.New("only draft revisions can be submitted for approval")
		}
		if open, exists := openRevisionForFamily(s.config.Models, model.FamilyID, model.ID); exists {
			return control.Model{}, fmt.Errorf("cannot submit revision for approval: revision %d is already %s in this model family", open.Revision, open.Status)
		}
		if strings.TrimSpace(actor) == "" {
			return control.Model{}, errors.New("submitter is required")
		}
		if err := validateProductionReadyModel(model, s.config.InputDefinitions, s.config.Sources); err != nil {
			return control.Model{}, fmt.Errorf("cannot submit revision for approval: %w", err)
		}
		s.config.Models[index].Status = "pending_approval"
		s.config.Models[index].Enabled = false
		s.config.Models[index].SubmittedBy = actor
		s.config.Models[index].SubmittedAt = time.Now().UTC().Format(time.RFC3339)
		s.config.Models[index].RejectedBy = ""
		s.config.Models[index].RejectedAt = ""
		s.config.Models[index].RejectionReason = ""
		if err := s.persistModels(s.config.Models[index]); err != nil {
			return control.Model{}, err
		}
		return s.config.Models[index], nil
	}
	return control.Model{}, errors.New("model revision not found")
}

func (s *Store) WithdrawSubmission(id, actor string, allowAdmin bool) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, model := range s.config.Models {
		if model.ID != id {
			continue
		}
		if model.Status != "pending_approval" {
			return control.Model{}, errors.New("only pending revisions can be withdrawn")
		}
		if !allowAdmin && !strings.EqualFold(strings.TrimSpace(model.SubmittedBy), strings.TrimSpace(actor)) {
			return control.Model{}, errors.New("only the submitter or an administrator can withdraw this revision")
		}
		s.config.Models[index].Status = "draft"
		s.config.Models[index].SubmittedBy = ""
		s.config.Models[index].SubmittedAt = ""
		if err := s.persistModels(s.config.Models[index]); err != nil {
			return control.Model{}, err
		}
		return s.config.Models[index], nil
	}
	return control.Model{}, errors.New("model revision not found")
}

func (s *Store) Reject(id, actor, reason string) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return control.Model{}, errors.New("rejection reason is required")
	}
	for index, model := range s.config.Models {
		if model.ID != id {
			continue
		}
		if model.Status != "pending_approval" {
			return control.Model{}, errors.New("only pending revisions can be rejected")
		}
		s.config.Models[index].Status = "draft"
		s.config.Models[index].Enabled = false
		s.config.Models[index].RejectedBy = actor
		s.config.Models[index].RejectedAt = time.Now().UTC().Format(time.RFC3339)
		s.config.Models[index].RejectionReason = reason
		if err := s.persistModels(s.config.Models[index]); err != nil {
			return control.Model{}, err
		}
		return s.config.Models[index], nil
	}
	return control.Model{}, errors.New("model revision not found")
}

func (s *Store) Approve(id, actor string, allowSelfApproval bool) (control.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, m := range s.config.Models {
		if m.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return control.Model{}, errors.New("model revision not found")
	}
	target := s.config.Models[index]
	if target.Status != "pending_approval" {
		return control.Model{}, errors.New("only revisions pending approval can be approved")
	}
	if open, exists := openRevisionForFamily(s.config.Models, target.FamilyID, target.ID); exists {
		return control.Model{}, fmt.Errorf("cannot approve revision: revision %d is also %s in this model family; resolve it first", open.Revision, open.Status)
	}
	if !allowSelfApproval && strings.EqualFold(strings.TrimSpace(target.SubmittedBy), strings.TrimSpace(actor)) {
		return control.Model{}, errors.New("the submitter cannot approve their own revision")
	}
	if err := validateProductionReadyModel(target, s.config.InputDefinitions, s.config.Sources); err != nil {
		return control.Model{}, fmt.Errorf("cannot approve revision: %w", err)
	}
	for _, existing := range s.config.Models {
		if existing.FamilyID != target.FamilyID && existing.Status == "approved" && existing.Enabled && control.HasAmbiguousRouting(target, existing) {
			return control.Model{}, fmt.Errorf("cannot approve: routing overlaps equally with active model %q", existing.Name)
		}
	}
	changed := []control.Model{}
	for i := range s.config.Models {
		if s.config.Models[i].FamilyID == target.FamilyID && s.config.Models[i].Status == "approved" {
			s.config.Models[i].Status = "superseded"
			s.config.Models[i].Enabled = false
			changed = append(changed, s.config.Models[i])
		}
	}
	s.config.Models[index].Status = "approved"
	s.config.Models[index].Enabled = true
	s.config.Models[index].ApprovedBy = actor
	s.config.Models[index].ApprovedAt = time.Now().UTC().Format(time.RFC3339)
	changed = append(changed, s.config.Models[index])
	if err := s.persistModels(changed...); err != nil {
		return control.Model{}, err
	}
	return s.config.Models[index], nil
}
func (s *Store) History(family string) []control.Model {
	c := s.Config()
	out := []control.Model{}
	for _, m := range c.Models {
		if m.FamilyID == family {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision > out[j].Revision })
	return out
}
