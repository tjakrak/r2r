package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"r2r/internal/control"
	"sort"
	"strings"
	"time"
)

const productionSchema = "PCS"

func emptySQLConfig() Config {
	return Config{
		Sources:          []Source{},
		Folders:          []ModelFolder{},
		InputDefinitions: []InputDefinition{},
		Users:            []UserRegistration{},
		Models:           []control.Model{},
		Runs:             []RunRecord{},
		AdaptiveStates:   map[string]AdaptiveState{},
	}
}

// OpenSQL creates the production store. SQL Server is authoritative in this mode;
// data/config.json is neither read nor written.
func OpenSQL(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("SQL database is required")
	}
	s := &Store{db: db, config: emptySQLConfig()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect to production SQL Server: %w", err)
	}
	if err := s.loadSQL(ctx); err != nil {
		return nil, err
	}
	s.seedInputDefinitions()
	return s, nil
}

func (s *Store) IsSQL() bool { return s != nil && s.db != nil }

func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.PingContext(ctx)
}

// Refresh reloads the SQL-backed read model so multiple backend instances see
// each other's committed changes. JSON mode has no external state to refresh.
func (s *Store) Refresh(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := &Store{db: s.db, config: emptySQLConfig()}
	if err := next.loadSQL(ctx); err != nil {
		return err
	}
	next.seedInputDefinitions()
	s.config = next.config
	return nil
}

func sessionHash(token string) []byte { sum := sha256.Sum256([]byte(token)); return sum[:] }

func (s *Store) CreateSession(token, employeeID, clientAddress string, expiresAt time.Time) error {
	if s.db == nil {
		return nil
	}
	result, err := s.db.Exec(`INSERT PCS.USER_SESSIONS (SESSION_ID_HASH,USER_ID,EXPIRES_AT_UTC,CLIENT_ADDRESS)
		SELECT @p1,USER_ID,@p3,NULLIF(@p4,'') FROM PCS.USERS WHERE EMPLOYEE_ID=@p2 AND STATUS='active'`, sessionHash(token), employeeID, expiresAt.UTC(), clientAddress)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("active registered user was not found")
	}
	return nil
}

func (s *Store) SessionUser(token string) (UserRegistration, bool) {
	if s.db == nil {
		return UserRegistration{}, false
	}
	var v UserRegistration
	err := s.db.QueryRow(`SELECT U.EMPLOYEE_ID,U.LOGIN_NAME,U.DISPLAY_NAME,COALESCE(U.EMAIL_ADDRESS,N''),U.ROLE_NAME,U.STATUS
		FROM PCS.USER_SESSIONS S INNER JOIN PCS.USERS U ON U.USER_ID=S.USER_ID
		WHERE S.SESSION_ID_HASH=@p1 AND S.REVOKED_AT_UTC IS NULL AND S.EXPIRES_AT_UTC>SYSUTCDATETIME() AND U.STATUS='active'`, sessionHash(token)).Scan(&v.EmployeeID, &v.LoginID, &v.DisplayName, &v.Email, &v.Role, &v.Status)
	if err != nil {
		return UserRegistration{}, false
	}
	_, _ = s.db.Exec(`UPDATE PCS.USER_SESSIONS SET LAST_SEEN_AT_UTC=SYSUTCDATETIME()
		WHERE SESSION_ID_HASH=@p1 AND LAST_SEEN_AT_UTC<DATEADD(MINUTE,-5,SYSUTCDATETIME())`, sessionHash(token))
	return v, true
}

func (s *Store) RevokeSession(token string) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`UPDATE PCS.USER_SESSIONS SET REVOKED_AT_UTC=SYSUTCDATETIME()
		WHERE SESSION_ID_HASH=@p1 AND REVOKED_AT_UTC IS NULL`, sessionHash(token))
	return err
}

func (s *Store) RecordAudit(actorID, actionName, outcome, entityType, entityID, requestID, clientAddress string, details interface{}) error {
	if s.db == nil {
		return nil
	}
	var detailsValue interface{}
	if details != nil {
		encoded, err := json.Marshal(details)
		if err != nil {
			return err
		}
		detailsValue = string(encoded)
	}
	_, err := s.db.Exec(`INSERT PCS.AUDIT_EVENTS
		(ACTOR_TYPE,ACTOR_ID,ACTION_NAME,OUTCOME,ENTITY_TYPE,ENTITY_ID,REQUEST_ID,DETAILS_JSON,CLIENT_ADDRESS)
		VALUES ('user',@p1,@p2,@p3,@p4,@p5,NULLIF(@p6,N''),@p7,NULLIF(@p8,''))`,
		actorID, actionName, outcome, entityType, entityID, requestID, detailsValue, clientAddress)
	return err
}

func (s *Store) loadSQL(ctx context.Context) error {
	if err := s.loadInputDefinitions(ctx); err != nil {
		return err
	}
	if err := s.loadUsers(ctx); err != nil {
		return err
	}
	if err := s.loadSources(ctx); err != nil {
		return err
	}
	if err := s.loadFolders(ctx); err != nil {
		return err
	}
	if err := s.loadModels(ctx); err != nil {
		return err
	}
	if err := s.loadRuns(ctx); err != nil {
		return err
	}
	if err := s.loadAdaptiveStates(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) loadFolders(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT FOLDER_ID, NAME, COALESCE(DESCRIPTION,N''), CREATED_BY, CREATED_AT_UTC, UPDATED_BY, UPDATED_AT_UTC FROM PCS.MODEL_FOLDERS ORDER BY NAME`)
	if err != nil {
		return fmt.Errorf("load PCS.MODEL_FOLDERS (apply SQL/19_model_folders.sql): %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var folder ModelFolder
		var created, updated time.Time
		if err := rows.Scan(&folder.ID, &folder.Name, &folder.Description, &folder.CreatedBy, &created, &folder.UpdatedBy, &updated); err != nil {
			return err
		}
		folder.CreatedAt, folder.UpdatedAt = created.UTC().Format(time.RFC3339Nano), updated.UTC().Format(time.RFC3339Nano)
		s.config.Folders = append(s.config.Folders, folder)
	}
	return rows.Err()
}

func (s *Store) loadInputDefinitions(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT INPUT_NAME, DISPLAY_NAME, CATEGORY, DATA_TYPE,
		COALESCE(DESCRIPTION, N''), STATUS, IS_SYSTEM FROM PCS.INPUT_DEFINITIONS ORDER BY INPUT_NAME`)
	if err != nil {
		return fmt.Errorf("load PCS.INPUT_DEFINITIONS: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v InputDefinition
		if err := rows.Scan(&v.Name, &v.DisplayName, &v.Category, &v.DataType, &v.Description, &v.Status, &v.System); err != nil {
			return err
		}
		s.config.InputDefinitions = append(s.config.InputDefinitions, v)
	}
	return rows.Err()
}

func (s *Store) loadUsers(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT EMPLOYEE_ID, LOGIN_NAME, DISPLAY_NAME, COALESCE(EMAIL_ADDRESS, N''), ROLE_NAME, STATUS FROM PCS.USERS ORDER BY LOGIN_NAME`)
	if err != nil {
		return fmt.Errorf("load PCS.USERS: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v UserRegistration
		if err := rows.Scan(&v.EmployeeID, &v.LoginID, &v.DisplayName, &v.Email, &v.Role, &v.Status); err != nil {
			return err
		}
		s.config.Users = append(s.config.Users, v)
	}
	return rows.Err()
}

func (s *Store) loadSources(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT SOURCE_ID, NAME, SOURCE_TYPE, ENGINE, HOST_NAME, PORT, DATABASE_NAME,
		COALESCE(LOGIN_NAME, N''), COALESCE(SECRET_REFERENCE, N''), TLS_MODE, TIMEOUT_SECONDS,
		COALESCE(LOCATION_NAME, N''), ENABLED, COALESCE(LAST_TEST_STATUS, ''), COALESCE(LAST_TEST_MESSAGE, N''),
		LAST_TEST_AT_UTC, COALESCE(LAST_TEST_MS, 0) FROM PCS.DATA_SOURCES ORDER BY NAME`)
	if err != nil {
		return fmt.Errorf("load PCS.DATA_SOURCES: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v Source
		var tested sql.NullTime
		if err := rows.Scan(&v.ID, &v.Name, &v.Type, &v.Engine, &v.Host, &v.Port, &v.Database, &v.Username,
			&v.SecretRef, &v.TLSMode, &v.TimeoutSeconds, &v.Location, &v.Enabled, &v.LastTestStatus,
			&v.LastTestMessage, &tested, &v.LastTestMS); err != nil {
			return err
		}
		if tested.Valid {
			v.LastTestAt = tested.Time.UTC().Format(time.RFC3339Nano)
		}
		s.config.Sources = append(s.config.Sources, v)
	}
	return rows.Err()
}

func (s *Store) loadModels(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT R.MODEL_ID, R.FAMILY_ID, R.REVISION, R.STATUS, R.ENABLED, R.DEFINITION_JSON,
		R.CHANGE_NOTE, R.OWNER_NAME, R.CREATED_BY, R.CREATED_AT_UTC, R.SUBMITTED_BY, R.SUBMITTED_AT_UTC,
		R.APPROVED_BY, R.APPROVED_AT_UTC, R.REJECTED_BY, R.REJECTED_AT_UTC, R.REJECTION_REASON, COALESCE(F.FOLDER_ID,N'')
		FROM PCS.MODEL_REVISIONS R JOIN PCS.MODEL_FAMILIES F ON F.FAMILY_ID=R.FAMILY_ID ORDER BY R.FAMILY_ID, R.REVISION`)
	if err != nil {
		return fmt.Errorf("load PCS.MODEL_REVISIONS: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m control.Model
		var modelID, familyID, status, changeNote, owner, createdBy, folderID string
		var revision int
		var enabled bool
		var definition []byte
		var created time.Time
		var submittedBy, approvedBy, rejectedBy, rejection sql.NullString
		var submittedAt, approvedAt, rejectedAt sql.NullTime
		if err := rows.Scan(&modelID, &familyID, &revision, &status, &enabled, &definition, &changeNote,
			&owner, &createdBy, &created, &submittedBy, &submittedAt, &approvedBy, &approvedAt,
			&rejectedBy, &rejectedAt, &rejection, &folderID); err != nil {
			return err
		}
		if err := json.Unmarshal(definition, &m); err != nil {
			return fmt.Errorf("decode model %s: %w", modelID, err)
		}
		// Relational workflow columns are authoritative over the JSON snapshot.
		m.ID, m.FamilyID, m.Revision, m.Status, m.Enabled = modelID, familyID, revision, status, enabled
		m.FolderID = folderID
		m.ChangeNote, m.Owner, m.CreatedBy = changeNote, owner, createdBy
		m.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		m.SubmittedBy, m.ApprovedBy, m.RejectedBy, m.RejectionReason = submittedBy.String, approvedBy.String, rejectedBy.String, rejection.String
		if submittedAt.Valid {
			m.SubmittedAt = submittedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		if approvedAt.Valid {
			m.ApprovedAt = approvedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		if rejectedAt.Valid {
			m.RejectedAt = rejectedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		s.config.Models = append(s.config.Models, m)
	}
	return rows.Err()
}

func (s *Store) loadRuns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT RUN_ID, RUN_SEQUENCE, FAMILY_ID, MODEL_ID, MODEL_REVISION, STATUS,
		COALESCE(DISPOSITION, ''), PREPARED_AT_UTC, COMPLETED_AT_UTC, CONTEXT_JSON, PARAMETERS_JSON,
		FEEDBACK_JSON, ADAPTIVE_UPDATES_JSON, COALESCE(REQUEST_ID, N''), COALESCE(FAILURE_CODE, N''),
		COALESCE(FAILURE_MESSAGE, N'') FROM PCS.RUNS
		WHERE STATUS='prepared' OR RUN_SEQUENCE IN (SELECT TOP (200) RUN_SEQUENCE FROM PCS.RUNS ORDER BY RUN_SEQUENCE DESC)
		ORDER BY RUN_SEQUENCE`)
	if err != nil {
		return fmt.Errorf("load PCS.RUNS: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r RunRecord
		var prepared time.Time
		var completed sql.NullTime
		var contextJSON, parametersJSON []byte
		var feedbackJSON, adaptiveJSON sql.NullString
		if err := rows.Scan(&r.RunID, &r.Sequence, &r.FamilyID, &r.ModelID, &r.ModelRevision, &r.Status,
			&r.Disposition, &prepared, &completed, &contextJSON, &parametersJSON, &feedbackJSON, &adaptiveJSON,
			&r.RequestID, &r.FailureCode, &r.FailureMessage); err != nil {
			return err
		}
		r.PreparedAt = prepared.UTC().Format(time.RFC3339Nano)
		if completed.Valid {
			r.CompletedAt = completed.Time.UTC().Format(time.RFC3339Nano)
		}
		if err := json.Unmarshal(contextJSON, &r.Context); err != nil {
			return err
		}
		if err := json.Unmarshal(parametersJSON, &r.Parameters); err != nil {
			return err
		}
		if feedbackJSON.Valid {
			_ = json.Unmarshal([]byte(feedbackJSON.String), &r.Feedback)
		}
		if adaptiveJSON.Valid {
			_ = json.Unmarshal([]byte(adaptiveJSON.String), &r.AdaptiveUpdates)
		}
		r.Outputs, err = s.loadRunOutputs(ctx, r.RunID)
		if err != nil {
			return err
		}
		s.config.Runs = append(s.config.Runs, r)
		if r.Sequence > s.config.NextRunSequence {
			s.config.NextRunSequence = r.Sequence
		}
	}
	return rows.Err()
}

func (s *Store) loadRunOutputs(ctx context.Context, runID string) ([]control.OutputValue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT PARAMETER_NAME, DATA_TYPE, IS_NULL, NUMBER_VALUE, TEXT_VALUE, BOOLEAN_VALUE,
		CALCULATED_NUMBER_VALUE, COALESCE(UNIT, N''), LSL, USL, OUT_OF_LIMIT, COALESCE(LIMIT_ACTION, '')
		FROM PCS.RUN_OUTPUTS WHERE RUN_ID = @p1 ORDER BY OUTPUT_ORDINAL`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []control.OutputValue
	for rows.Next() {
		var v control.OutputValue
		var kind, action string
		var isNull bool
		var text sql.NullString
		var number, calculated, lsl, usl sql.NullFloat64
		var boolean sql.NullBool
		if err := rows.Scan(&v.Parameter, &kind, &isNull, &number, &text, &boolean, &calculated, &v.Unit, &lsl, &usl, &v.BoundLimited, &action); err != nil {
			return nil, err
		}
		if isNull {
			v.Value = nil
		} else {
			switch kind {
			case "number":
				v.Value = number.Float64
			case "boolean":
				v.Value = boolean.Bool
			default:
				v.Value = text.String
			}
		}
		if calculated.Valid {
			v.CalculatedValue = calculated.Float64
		}
		if lsl.Valid {
			x := lsl.Float64
			v.LSL = &x
		}
		if usl.Valid {
			x := usl.Float64
			v.USL = &x
		}
		v.LimitAction = action
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) loadAdaptiveStates(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT FAMILY_ID, PARAMETER_NAME, SCOPE_JSON, CURRENT_VALUE,
		COALESCE(LAST_RUN_ID, N''), LAST_RUN_SEQUENCE, UPDATED_AT_UTC FROM PCS.ADAPTIVE_STATES`)
	if err != nil {
		return fmt.Errorf("load PCS.ADAPTIVE_STATES: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var family, parameter, scopeJSON, runID string
		var state AdaptiveState
		var updated time.Time
		if err := rows.Scan(&family, &parameter, &scopeJSON, &state.Value, &runID, &state.LastSequence, &updated); err != nil {
			return err
		}
		var scope map[string]interface{}
		if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
			return err
		}
		state.LastRunID, state.UpdatedAt = runID, updated.UTC().Format(time.RFC3339Nano)
		s.config.AdaptiveStates[canonicalScopeKey(family, parameter, scope)] = state
	}
	return rows.Err()
}

func canonicalScopeKey(family, parameter string, scope map[string]interface{}) string {
	keys := make([]string, 0, len(scope))
	for k := range scope {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{family}
	for _, k := range keys {
		parts = append(parts, k+"="+fmt.Sprint(scope[k]))
	}
	return strings.Join(parts, "|") + "|parameter=" + parameter
}

func (s *Store) sqlTransaction(work func(context.Context, *sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := work(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) persistUser(v UserRegistration) error {
	if s.db == nil {
		return s.persist()
	}
	return s.sqlTransaction(func(ctx context.Context, tx *sql.Tx) error { return saveUserSQL(ctx, tx, v) })
}
func (s *Store) persistInput(v InputDefinition) error {
	if s.db == nil {
		return s.persist()
	}
	return s.sqlTransaction(func(ctx context.Context, tx *sql.Tx) error { return saveInputSQL(ctx, tx, v) })
}
func (s *Store) persistSource(v Source) error {
	if s.db == nil {
		return s.persist()
	}
	return s.sqlTransaction(func(ctx context.Context, tx *sql.Tx) error { return saveSourceSQL(ctx, tx, v) })
}
func (s *Store) persistModels(models ...control.Model) error {
	if s.db == nil {
		return s.persist()
	}
	return s.sqlTransaction(func(ctx context.Context, tx *sql.Tx) error {
		for _, m := range models {
			if err := saveModelSQL(ctx, tx, m); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) saveFolderSQL(folder ModelFolder) error {
	if s.db == nil {
		return nil
	}
	return s.sqlTransaction(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `IF EXISTS(SELECT 1 FROM PCS.MODEL_FOLDERS WHERE FOLDER_ID=@p1)
			UPDATE PCS.MODEL_FOLDERS SET NAME=@p2,DESCRIPTION=NULLIF(@p3,N''),UPDATED_BY=@p4,UPDATED_AT_UTC=SYSUTCDATETIME() WHERE FOLDER_ID=@p1
			ELSE INSERT PCS.MODEL_FOLDERS(FOLDER_ID,NAME,DESCRIPTION,CREATED_BY,UPDATED_BY) VALUES(@p1,@p2,NULLIF(@p3,N''),@p4,@p4)`, folder.ID, folder.Name, folder.Description, folder.UpdatedBy)
		return err
	})
}

func (s *Store) deleteFolderSQL(id string) error {
	if s.db == nil {
		return nil
	}
	result, err := s.db.Exec(`DELETE PCS.MODEL_FOLDERS WHERE FOLDER_ID=@p1 AND NOT EXISTS(SELECT 1 FROM PCS.MODEL_FAMILIES WHERE FOLDER_ID=@p1)`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("folder not found or still contains models")
	}
	return nil
}

func (s *Store) moveModelFamilySQL(familyID, folderID string) error {
	if s.db == nil {
		return nil
	}
	result, err := s.db.Exec(`UPDATE PCS.MODEL_FAMILIES SET FOLDER_ID=NULLIF(@p2,N'') WHERE FAMILY_ID=@p1`, familyID, folderID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("model family not found")
	}
	return nil
}
func (s *Store) persistRun(r RunRecord) error {
	if s.db == nil {
		return s.persist()
	}
	return s.sqlTransaction(func(ctx context.Context, tx *sql.Tx) error {
		if r.Status == "prepared" {
			model := findModel(s.config.Models, r.ModelID)
			limit := model.ExecutionPolicy.MaxUnfinishedRuns
			if limit > 0 {
				_, partitionHash := runPartition(r, s.config.Models)
				resource := fmt.Sprintf("PCS:RUN_PARTITION:%s:%x", r.FamilyID, partitionHash)
				var lockResult int
				if err := tx.QueryRowContext(ctx, `DECLARE @result int; EXEC @result=sys.sp_getapplock @Resource=@p1,@LockMode='Exclusive',@LockOwner='Transaction',@LockTimeout=10000; SELECT @result`, resource).Scan(&lockResult); err != nil {
					return err
				}
				if lockResult < 0 {
					return errors.New("could not lock the execution partition")
				}
				var unfinished int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM PCS.RUNS WITH (UPDLOCK,HOLDLOCK) WHERE FAMILY_ID=@p1 AND STATUS='prepared' AND PARTITION_HASH=@p2`, r.FamilyID, partitionHash).Scan(&unfinished); err != nil {
					return err
				}
				if unfinished >= limit {
					return fmt.Errorf("maximum unfinished runs reached for this partition (%d)", limit)
				}
			}
		}
		if err := s.saveRunSQL(ctx, tx, r); err != nil {
			return err
		}
		return s.saveAdaptiveForRunSQL(ctx, tx, r.RunID)
	})
}

func (s *Store) saveAdaptiveForRunSQL(ctx context.Context, tx *sql.Tx, runID string) error {
	for _, m := range s.config.Models {
		for _, p := range m.Parameters {
			if p.Adaptive == nil {
				continue
			}
			prefix, suffix := m.FamilyID+"|", "|parameter="+p.Key
			for key, state := range s.config.AdaptiveStates {
				if state.LastRunID != runID || !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
					continue
				}
				scope := scopeFromKey(key)
				scopeJSON, _ := json.Marshal(scope)
				hash := sha256.Sum256(scopeJSON)
				if state.LastSequence == 0 {
					_ = tx.QueryRowContext(ctx, `SELECT RUN_SEQUENCE FROM PCS.RUNS WHERE RUN_ID=@p1`, runID).Scan(&state.LastSequence)
				}
				_, err := tx.ExecContext(ctx, `UPDATE PCS.ADAPTIVE_STATES SET CURRENT_VALUE=@p5,LAST_RUN_ID=@p6,LAST_RUN_SEQUENCE=@p7,UPDATED_AT_UTC=SYSUTCDATETIME() WHERE FAMILY_ID=@p1 AND PARAMETER_NAME=@p2 AND SCOPE_HASH=@p3 AND LAST_RUN_SEQUENCE<@p7; IF @@ROWCOUNT=0 AND NOT EXISTS(SELECT 1 FROM PCS.ADAPTIVE_STATES WHERE FAMILY_ID=@p1 AND PARAMETER_NAME=@p2 AND SCOPE_HASH=@p3) INSERT PCS.ADAPTIVE_STATES(FAMILY_ID,PARAMETER_NAME,SCOPE_HASH,SCOPE_JSON,CURRENT_VALUE,LAST_RUN_ID,LAST_RUN_SEQUENCE) VALUES(@p1,@p2,@p3,@p4,@p5,@p6,@p7)`, m.FamilyID, p.Key, hash[:], string(scopeJSON), state.Value, state.LastRunID, state.LastSequence)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func saveInputSQL(ctx context.Context, tx *sql.Tx, v InputDefinition) error {
	res, err := tx.ExecContext(ctx, `UPDATE PCS.INPUT_DEFINITIONS SET DISPLAY_NAME=@p2, DATA_TYPE=@p3, DESCRIPTION=@p4,
		STATUS=@p5, UPDATED_BY=N'SYSTEM', UPDATED_AT_UTC=SYSUTCDATETIME() WHERE INPUT_NAME=@p1
		AND (DISPLAY_NAME<>@p2 OR DATA_TYPE<>@p3 OR ISNULL(DESCRIPTION,N'')<>@p4 OR STATUS<>@p5)`, v.Name, v.DisplayName, v.DataType, v.Description, v.Status)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `IF NOT EXISTS (SELECT 1 FROM PCS.INPUT_DEFINITIONS WHERE INPUT_NAME=@p1)
		INSERT PCS.INPUT_DEFINITIONS (INPUT_NAME,DISPLAY_NAME,CATEGORY,DATA_TYPE,DESCRIPTION,STATUS,IS_SYSTEM,CREATED_BY,UPDATED_BY)
		VALUES (@p1,@p2,@p3,@p4,NULLIF(@p5,N''),@p6,@p7,N'SYSTEM',N'SYSTEM')`, v.Name, v.DisplayName, v.Category, v.DataType, v.Description, v.Status, v.System)
	return err
}

func saveUserSQL(ctx context.Context, tx *sql.Tx, v UserRegistration) error {
	userID := "USR-" + v.EmployeeID
	res, err := tx.ExecContext(ctx, `UPDATE PCS.USERS SET LOGIN_NAME=@p3, DISPLAY_NAME=@p4, EMAIL_ADDRESS=NULLIF(@p5,N''),
		ROLE_NAME=@p6, STATUS=@p7, UPDATED_BY=N'SYSTEM', UPDATED_AT_UTC=SYSUTCDATETIME()
		WHERE EMPLOYEE_ID=@p2 AND (LOGIN_NAME<>@p3 OR DISPLAY_NAME<>@p4 OR ISNULL(EMAIL_ADDRESS,N'')<>@p5 OR ROLE_NAME<>@p6 OR STATUS<>@p7)`, userID, v.EmployeeID, v.LoginID, v.DisplayName, v.Email, v.Role, v.Status)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if v.Status != "active" {
			_, err = tx.ExecContext(ctx, `UPDATE PCS.USER_SESSIONS SET REVOKED_AT_UTC=SYSUTCDATETIME() WHERE USER_ID=@p1 AND REVOKED_AT_UTC IS NULL`, userID)
		}
		return err
	}
	_, err = tx.ExecContext(ctx, `IF NOT EXISTS (SELECT 1 FROM PCS.USERS WHERE EMPLOYEE_ID=@p2)
		INSERT PCS.USERS (USER_ID,EMPLOYEE_ID,LOGIN_NAME,DISPLAY_NAME,EMAIL_ADDRESS,ROLE_NAME,STATUS,IDENTITY_PROVIDER,EXTERNAL_SUBJECT,CREATED_BY,UPDATED_BY)
		VALUES (@p1,@p2,@p3,@p4,NULLIF(@p5,N''),@p6,@p7,N'corporate_rest',@p2,N'SYSTEM',N'SYSTEM')`, userID, v.EmployeeID, v.LoginID, v.DisplayName, v.Email, v.Role, v.Status)
	return err
}

func saveSourceSQL(ctx context.Context, tx *sql.Tx, v Source) error {
	res, err := tx.ExecContext(ctx, `UPDATE PCS.DATA_SOURCES SET NAME=@p2,SOURCE_TYPE=@p3,ENGINE=@p4,HOST_NAME=@p5,PORT=@p6,
		DATABASE_NAME=@p7,LOGIN_NAME=NULLIF(@p8,N''),SECRET_REFERENCE=NULLIF(@p9,N''),TLS_MODE=@p10,TIMEOUT_SECONDS=@p11,
		LOCATION_NAME=NULLIF(@p12,N''),ENABLED=@p13,LAST_TEST_STATUS=NULLIF(@p14,''),LAST_TEST_MESSAGE=NULLIF(@p15,N''),
		LAST_TEST_AT_UTC=@p16,LAST_TEST_MS=@p17,UPDATED_AT_UTC=SYSUTCDATETIME() WHERE SOURCE_ID=@p1`,
		v.ID, v.Name, v.Type, v.Engine, v.Host, v.Port, v.Database, v.Username, v.SecretRef, v.TLSMode, v.TimeoutSeconds, v.Location, v.Enabled, v.LastTestStatus, v.LastTestMessage, nullableTime(v.LastTestAt), nullableInt64(v.LastTestAt, v.LastTestMS))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT PCS.DATA_SOURCES (SOURCE_ID,NAME,SOURCE_TYPE,ENGINE,HOST_NAME,PORT,DATABASE_NAME,LOGIN_NAME,SECRET_REFERENCE,TLS_MODE,TIMEOUT_SECONDS,LOCATION_NAME,ENABLED,LAST_TEST_STATUS,LAST_TEST_MESSAGE,LAST_TEST_AT_UTC,LAST_TEST_MS)
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,NULLIF(@p8,N''),NULLIF(@p9,N''),@p10,@p11,NULLIF(@p12,N''),@p13,NULLIF(@p14,''),NULLIF(@p15,N''),@p16,@p17)`,
		v.ID, v.Name, v.Type, v.Engine, v.Host, v.Port, v.Database, v.Username, v.SecretRef, v.TLSMode, v.TimeoutSeconds, v.Location, v.Enabled, v.LastTestStatus, v.LastTestMessage, nullableTime(v.LastTestAt), nullableInt64(v.LastTestAt, v.LastTestMS))
	return err
}

func saveModelSQL(ctx context.Context, tx *sql.Tx, m control.Model) error {
	definition, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `IF NOT EXISTS (SELECT 1 FROM PCS.MODEL_FAMILIES WHERE FAMILY_ID=@p1)
		INSERT PCS.MODEL_FAMILIES (FAMILY_ID,NAME,DESCRIPTION,CREATED_BY,FOLDER_ID) VALUES (@p1,@p2,NULLIF(@p3,N''),@p4,NULLIF(@p5,N''))`, m.FamilyID, m.Name, m.Description, m.CreatedBy, m.FolderID)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE PCS.MODEL_REVISIONS SET STATUS=@p4,ENABLED=@p5,DEFINITION_JSON=@p6,CHANGE_NOTE=@p7,
		OWNER_NAME=@p8,SUBMITTED_BY=@p9,SUBMITTED_AT_UTC=@p10,APPROVED_BY=@p11,APPROVED_AT_UTC=@p12,
		REJECTED_BY=@p13,REJECTED_AT_UTC=@p14,REJECTION_REASON=@p15 WHERE MODEL_ID=@p1
		AND (STATUS<>@p4 OR ENABLED<>@p5 OR DEFINITION_JSON<>@p6 OR CHANGE_NOTE<>@p7 OR OWNER_NAME<>@p8
		OR ISNULL(SUBMITTED_BY,N'')<>ISNULL(@p9,N'') OR ISNULL(APPROVED_BY,N'')<>ISNULL(@p11,N'') OR ISNULL(REJECTED_BY,N'')<>ISNULL(@p13,N'') OR ISNULL(REJECTION_REASON,N'')<>ISNULL(@p15,N''))`,
		m.ID, m.FamilyID, m.Revision, m.Status, m.Enabled, string(definition), m.ChangeNote, m.Owner, nullString(m.SubmittedBy), nullableTime(m.SubmittedAt), nullString(m.ApprovedBy), nullableTime(m.ApprovedAt), nullString(m.RejectedBy), nullableTime(m.RejectedAt), nullString(m.RejectionReason))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `IF NOT EXISTS (SELECT 1 FROM PCS.MODEL_REVISIONS WHERE MODEL_ID=@p1)
		INSERT PCS.MODEL_REVISIONS (MODEL_ID,FAMILY_ID,REVISION,STATUS,ENABLED,DEFINITION_JSON,CHANGE_NOTE,OWNER_NAME,CREATED_BY,CREATED_AT_UTC,SUBMITTED_BY,SUBMITTED_AT_UTC,APPROVED_BY,APPROVED_AT_UTC,REJECTED_BY,REJECTED_AT_UTC,REJECTION_REASON)
		VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,COALESCE(@p10,SYSUTCDATETIME()),@p11,@p12,@p13,@p14,@p15,@p16,@p17)`,
		m.ID, m.FamilyID, m.Revision, m.Status, m.Enabled, string(definition), m.ChangeNote, m.Owner, m.CreatedBy, nullableTime(m.CreatedAt), nullString(m.SubmittedBy), nullableTime(m.SubmittedAt), nullString(m.ApprovedBy), nullableTime(m.ApprovedAt), nullString(m.RejectedBy), nullableTime(m.RejectedAt), nullString(m.RejectionReason))
	return err
}

func (s *Store) saveRunSQL(ctx context.Context, tx *sql.Tx, r RunRecord) error {
	contextJSON, _ := json.Marshal(r.Context)
	parametersJSON, _ := json.Marshal(r.Parameters)
	feedbackJSON := marshalNullable(r.Feedback)
	adaptiveJSON := marshalNullable(r.AdaptiveUpdates)
	partitionJSON, partitionHash := runPartition(r, s.config.Models)
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM PCS.RUNS WHERE RUN_ID=@p1`, r.RunID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists == 0 {
		err = tx.QueryRowContext(ctx, `INSERT PCS.RUNS (RUN_ID,FAMILY_ID,MODEL_ID,MODEL_REVISION,STATUS,DISPOSITION,PREPARED_AT_UTC,COMPLETED_AT_UTC,CONTEXT_JSON,PARAMETERS_JSON,FEEDBACK_JSON,ADAPTIVE_UPDATES_JSON,REQUEST_ID,FAILURE_CODE,FAILURE_MESSAGE,PARTITION_HASH,PARTITION_JSON)
			OUTPUT INSERTED.RUN_SEQUENCE VALUES (@p1,@p2,@p3,@p4,@p5,NULLIF(@p6,''),COALESCE(@p7,SYSUTCDATETIME()),@p8,@p9,@p10,@p11,@p12,NULLIF(@p13,N''),NULLIF(@p14,N''),NULLIF(@p15,N''),@p16,@p17)`,
			r.RunID, r.FamilyID, r.ModelID, r.ModelRevision, r.Status, r.Disposition, nullableTime(r.PreparedAt), nullableTime(r.CompletedAt), string(contextJSON), string(parametersJSON), feedbackJSON, adaptiveJSON, r.RequestID, r.FailureCode, r.FailureMessage, partitionHash, string(partitionJSON)).Scan(&r.Sequence)
		if err != nil {
			return err
		}
	} else if r.Status != "prepared" {
		_, err = tx.ExecContext(ctx, `UPDATE PCS.RUNS SET STATUS=@p2,COMPLETED_AT_UTC=COALESCE(@p3,SYSUTCDATETIME()),FEEDBACK_JSON=@p4,ADAPTIVE_UPDATES_JSON=@p5,FAILURE_CODE=NULLIF(@p6,N''),FAILURE_MESSAGE=NULLIF(@p7,N'') WHERE RUN_ID=@p1 AND STATUS='prepared'`, r.RunID, r.Status, nullableTime(r.CompletedAt), feedbackJSON, adaptiveJSON, r.FailureCode, r.FailureMessage)
		if err != nil {
			return err
		}
	}
	if err := s.saveRunOutputsSQL(ctx, tx, r); err != nil {
		return err
	}
	if err := s.saveRunParametersSQL(ctx, tx, r); err != nil {
		return err
	}
	if err := s.saveRunSamplesSQL(ctx, tx, r); err != nil {
		return err
	}
	return saveRunFeedbackSQL(ctx, tx, r)
}

func (s *Store) saveRunOutputsSQL(ctx context.Context, tx *sql.Tx, r RunRecord) error {
	model := findModel(s.config.Models, r.ModelID)
	for i, v := range r.Outputs {
		dataType := parameterType(model, v.Parameter)
		kind, n, t, b := typedValueAs(v.Value, dataType)
		isNull := v.Value == nil
		limitAction := v.LimitAction
		if v.LSL == nil && v.USL == nil {
			limitAction = ""
		}
		var calc interface{}
		if numeric, ok := numericValue(v.CalculatedValue); ok {
			calc = numeric
		}
		_, err := tx.ExecContext(ctx, `IF NOT EXISTS(SELECT 1 FROM PCS.RUN_OUTPUTS WHERE RUN_ID=@p1 AND OUTPUT_ORDINAL=@p2)
			INSERT PCS.RUN_OUTPUTS (RUN_ID,OUTPUT_ORDINAL,PARAMETER_NAME,DATA_TYPE,IS_NULL,NUMBER_VALUE,TEXT_VALUE,BOOLEAN_VALUE,CALCULATED_NUMBER_VALUE,UNIT,LSL,USL,OUT_OF_LIMIT,LIMIT_ACTION)
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,NULLIF(@p10,N''),@p11,@p12,@p13,NULLIF(@p14,''))`, r.RunID, i, v.Parameter, kind, isNull, n, t, b, calc, v.Unit, v.LSL, v.USL, v.BoundLimited, limitAction)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) saveRunParametersSQL(ctx context.Context, tx *sql.Tx, r RunRecord) error {
	model := findModel(s.config.Models, r.ModelID)
	names := make([]string, 0, len(r.Parameters))
	for k := range r.Parameters {
		names = append(names, k)
	}
	sort.Strings(names)
	for i, name := range names {
		value := r.Parameters[name]
		kind, source, unit := parameterMetadata(model, name)
		typ, n, t, b := typedValueAs(value, parameterType(model, name))
		isNull := value == nil
		_, err := tx.ExecContext(ctx, `IF NOT EXISTS(SELECT 1 FROM PCS.RUN_PARAMETER_VALUES WHERE RUN_ID=@p1 AND PARAMETER_ORDINAL=@p2)
			INSERT PCS.RUN_PARAMETER_VALUES (RUN_ID,PARAMETER_ORDINAL,PARAMETER_NAME,PARAMETER_KIND,SOURCE_TYPE,DATA_TYPE,IS_NULL,NUMBER_VALUE,TEXT_VALUE,BOOLEAN_VALUE,UNIT)
			VALUES (@p1,@p2,@p3,@p4,@p5,@p6,@p7,@p8,@p9,@p10,NULLIF(@p11,N''))`, r.RunID, i, name, kind, source, typ, isNull, n, t, b, unit)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) saveRunSamplesSQL(ctx context.Context, tx *sql.Tx, r RunRecord) error {
	model := findModel(s.config.Models, r.ModelID)
	for name, rows := range r.DataSamples {
		var sampleSource, sourceID, query string
		for _, d := range model.DataSamples {
			if d.Key == name {
				sampleSource, sourceID, query = d.Source, d.DataSourceID, d.Query
				break
			}
		}
		if sampleSource == "" {
			return fmt.Errorf("data sample %q was not found in model %q", name, model.ID)
		}
		var queryHash interface{}
		if sampleSource == "sql" {
			h := sha256.Sum256([]byte(query))
			queryHash = h[:]
		}
		snapshot, _ := json.Marshal(rows)
		_, err := tx.ExecContext(ctx, `IF NOT EXISTS(SELECT 1 FROM PCS.RUN_DATA_SAMPLES WHERE RUN_ID=@p1 AND SAMPLE_NAME=@p2)
			INSERT PCS.RUN_DATA_SAMPLES (RUN_ID,SAMPLE_NAME,SAMPLE_SOURCE,DATA_SOURCE_ID,PARAMETERIZED_QUERY_TEXT,QUERY_HASH,ROW_COUNT,SNAPSHOT_JSON)
			VALUES (@p1,@p2,@p3,NULLIF(@p4,N''),NULLIF(@p5,N''),CONVERT(binary(32),@p6),@p7,@p8)`, r.RunID, name, sampleSource, sourceID, query, queryHash, len(rows), string(snapshot))
		if err != nil {
			return err
		}
	}
	return nil
}

func saveRunFeedbackSQL(ctx context.Context, tx *sql.Tx, r RunRecord) error {
	names := make([]string, 0, len(r.Feedback))
	for k := range r.Feedback {
		names = append(names, k)
	}
	sort.Strings(names)
	for i, name := range names {
		typ, n, t, b := typedValue(r.Feedback[name])
		_, err := tx.ExecContext(ctx, `IF NOT EXISTS(SELECT 1 FROM PCS.RUN_FEEDBACK_VALUES WHERE RUN_ID=@p1 AND FEEDBACK_ORDINAL=@p2) INSERT PCS.RUN_FEEDBACK_VALUES (RUN_ID,FEEDBACK_ORDINAL,FEEDBACK_NAME,DATA_TYPE,NUMBER_VALUE,TEXT_VALUE,BOOLEAN_VALUE,UNIT) VALUES(@p1,@p2,@p3,@p4,@p5,@p6,@p7,NULL)`, r.RunID, i, name, typ, n, t, b)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deleteSQL(table, column, id string) error {
	if s.db == nil {
		return nil
	}
	allowed := map[string]bool{"INPUT_DEFINITIONS": true, "USERS": true, "MODEL_REVISIONS": true, "MODEL_FAMILIES": true}
	if !allowed[table] {
		return errors.New("invalid delete table")
	}
	if table == "MODEL_FAMILIES" {
		tx, e := s.db.Begin()
		if e != nil {
			return e
		}
		defer func() { _ = tx.Rollback() }()
		var runCount int
		if e = tx.QueryRow(`SELECT COUNT_BIG(1) FROM PCS.RUNS WHERE FAMILY_ID=@p1`, id).Scan(&runCount); e != nil {
			return fmt.Errorf("check model execution history: %w", e)
		}
		if runCount > 0 {
			return errors.New("this model has execution history and cannot be permanently deleted")
		}
		if _, e = tx.Exec(`DELETE FROM PCS.ADAPTIVE_STATES WHERE FAMILY_ID=@p1`, id); e != nil {
			return fmt.Errorf("delete model adaptive state: %w", e)
		}
		if _, e = tx.Exec(`DELETE FROM PCS.MODEL_REVISIONS WHERE FAMILY_ID=@p1`, id); e != nil {
			return fmt.Errorf("delete model revisions: %w", e)
		}
		if _, e = tx.Exec(`DELETE FROM PCS.MODEL_FAMILIES WHERE FAMILY_ID=@p1`, id); e != nil {
			return fmt.Errorf("delete model family: %w", e)
		}
		return tx.Commit()
	}
	if table == "USERS" {
		tx, beginErr := s.db.Begin()
		if beginErr != nil {
			return beginErr
		}
		if _, err := tx.Exec(`DELETE FROM PCS.USER_SESSIONS WHERE USER_ID IN (SELECT USER_ID FROM PCS.USERS WHERE EMPLOYEE_ID=@p1); DELETE FROM PCS.USERS WHERE EMPLOYEE_ID=@p1`, id); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	_, err := s.db.Exec(`DELETE FROM PCS.`+table+` WHERE `+column+`=@p1`, id)
	return err
}

func (s *Store) modelPurgeSummarySQL(summary ModelPurgeSummary) (ModelPurgeSummary, error) {
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT_BIG(1) FROM PCS.MODEL_REVISIONS WHERE FAMILY_ID=@p1),
		(SELECT COUNT_BIG(1) FROM PCS.RUNS WHERE FAMILY_ID=@p1)`, summary.FamilyID).
		Scan(&summary.RevisionCount, &summary.RunCount)
	if err != nil {
		return summary, fmt.Errorf("count model purge records: %w", err)
	}
	return summary, nil
}

func (s *Store) purgeFamilySQL(summary ModelPurgeSummary) (ModelPurgeSummary, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return summary, err
	}
	defer func() { _ = tx.Rollback() }()
	var lockedFamilyID string
	if err = tx.QueryRow(`SELECT FAMILY_ID FROM PCS.MODEL_FAMILIES WITH (XLOCK,HOLDLOCK) WHERE FAMILY_ID=@p1`, summary.FamilyID).Scan(&lockedFamilyID); err != nil {
		return summary, fmt.Errorf("lock model family for purge: %w", err)
	}
	if err = tx.QueryRow(`SELECT
		(SELECT COUNT_BIG(1) FROM PCS.MODEL_REVISIONS WHERE FAMILY_ID=@p1),
		(SELECT COUNT_BIG(1) FROM PCS.RUNS WHERE FAMILY_ID=@p1)`, summary.FamilyID).
		Scan(&summary.RevisionCount, &summary.RunCount); err != nil {
		return summary, fmt.Errorf("lock and count model purge records: %w", err)
	}
	statements := []struct {
		name string
		sql  string
	}{
		{"adaptive state", `DELETE FROM PCS.ADAPTIVE_STATES WHERE FAMILY_ID=@p1`},
		{"run outputs", `DELETE FROM PCS.RUN_OUTPUTS WHERE RUN_ID IN (SELECT RUN_ID FROM PCS.RUNS WHERE FAMILY_ID=@p1)`},
		{"run parameter values", `DELETE FROM PCS.RUN_PARAMETER_VALUES WHERE RUN_ID IN (SELECT RUN_ID FROM PCS.RUNS WHERE FAMILY_ID=@p1)`},
		{"run data samples", `DELETE FROM PCS.RUN_DATA_SAMPLES WHERE RUN_ID IN (SELECT RUN_ID FROM PCS.RUNS WHERE FAMILY_ID=@p1)`},
		{"run feedback values", `DELETE FROM PCS.RUN_FEEDBACK_VALUES WHERE RUN_ID IN (SELECT RUN_ID FROM PCS.RUNS WHERE FAMILY_ID=@p1)`},
		{"runs", `DELETE FROM PCS.RUNS WHERE FAMILY_ID=@p1`},
		{"model revisions", `DELETE FROM PCS.MODEL_REVISIONS WHERE FAMILY_ID=@p1`},
		{"model family", `DELETE FROM PCS.MODEL_FAMILIES WHERE FAMILY_ID=@p1`},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(statement.sql, summary.FamilyID); err != nil {
			return summary, fmt.Errorf("purge model %s: %w", statement.name, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return summary, fmt.Errorf("commit model purge: %w", err)
	}
	return summary, nil
}

func typedValue(v interface{}) (string, interface{}, interface{}, interface{}) {
	switch x := v.(type) {
	case bool:
		return "boolean", nil, nil, x
	case string:
		return "string", nil, x, nil
	default:
		if n, ok := numericValue(v); ok {
			return "number", n, nil, nil
		}
		return "string", nil, fmt.Sprint(v), nil
	}
}
func typedValueAs(v interface{}, dataType string) (string, interface{}, interface{}, interface{}) {
	if v == nil {
		if dataType != "number" && dataType != "boolean" && dataType != "string" {
			dataType = "string"
		}
		return dataType, nil, nil, nil
	}
	return typedValue(v)
}
func numericValue(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		n, e := x.Float64()
		return n, e == nil
	}
	return 0, false
}
func nullString(v string) interface{} {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
func nullableTime(v string) interface{} {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	t, e := time.Parse(time.RFC3339Nano, v)
	if e != nil {
		t, e = time.Parse(time.RFC3339, v)
	}
	if e != nil {
		return nil
	}
	return t
}
func nullableInt64(marker string, v int64) interface{} {
	if marker == "" {
		return nil
	}
	return v
}
func marshalNullable(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	if string(b) == "{}" || string(b) == "null" {
		return nil
	}
	return string(b)
}
func findModel(models []control.Model, id string) control.Model {
	for _, m := range models {
		if m.ID == id {
			return m
		}
	}
	return control.Model{}
}
func parameterMetadata(m control.Model, name string) (string, string, string) {
	if _, ok := m.Indexes[name]; ok {
		return "index", "input", ""
	}
	for _, x := range m.ExternalInputs {
		if x.Name == name {
			return "external", "input", ""
		}
	}
	for _, p := range m.Parameters {
		if p.Key == name {
			source := "expression"
			if p.Source == "sql" {
				source = "sql"
			} else if p.Adaptive != nil {
				source = "adaptive"
			}
			return "calculated", source, p.Unit
		}
	}
	return "calculated", "expression", ""
}
func parameterType(m control.Model, name string) string {
	if _, ok := m.Indexes[name]; ok {
		return "string"
	}
	for _, input := range m.ExternalInputs {
		if input.Name == name {
			return input.Type
		}
	}
	for _, parameter := range m.Parameters {
		if parameter.Key == name {
			if parameter.DataType == "" {
				return "number"
			}
			return parameter.DataType
		}
	}
	return "string"
}
func runPartition(r RunRecord, models []control.Model) ([]byte, []byte) {
	m := findModel(models, r.ModelID)
	fields := append([]string(nil), m.ExecutionPolicy.PartitionBy...)
	if len(fields) == 0 {
		fields = []string{"machine"}
	}
	sort.Strings(fields)
	scope := map[string]interface{}{}
	for _, f := range fields {
		scope[f] = r.Context[f]
	}
	b, _ := json.Marshal(scope)
	h := sha256.Sum256(b)
	return b, h[:]
}
func scopeFromKey(key string) map[string]interface{} {
	scope := map[string]interface{}{}
	parts := strings.Split(key, "|")
	for _, part := range parts[1:] {
		if strings.HasPrefix(part, "parameter=") {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) == 2 {
			scope[pair[0]] = pair[1]
		}
	}
	return scope
}
