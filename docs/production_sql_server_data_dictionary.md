# R2R production SQL Server specification

This document defines the recommended SQL Server settings and the final table structure after migrations `001` through `008`. The SQL migration files remain the executable source of truth.

## Database settings

| Setting | Production value | Reason |
|---|---|---|
| Database name | `R2RControl` | Dedicated security, backup, and lifecycle boundary for R2R. Adjust to the company naming standard if needed. |
| SQL Server version | SQL Server 2022 or later | Required by migration `002` for append-only SQL Server ledger tables. |
| Recovery model | `FULL` | Supports point-in-time recovery when transaction-log backups are scheduled. |
| `READ_COMMITTED_SNAPSHOT` | `ON` | Reduces reader/writer blocking during model execution and administration. |
| `AUTO_CLOSE` | `OFF` | Avoids repeated database shutdown and startup overhead. |
| `AUTO_SHRINK` | `OFF` | Avoids file fragmentation and repeated shrink/growth cycles. |
| Connection encryption | Required | Protects identities, model definitions, parameter values, and manufacturing data in transit. |
| Certificate validation | `TrustServerCertificate=false` | Ensures the backend validates the SQL Server certificate in production. |
| Application timestamps | UTC | Keeps events unambiguous across servers, factories, and daylight-saving changes. |
| Application schema | `r2r` | Contains authoritative R2R business, identity, execution, and audit data. |
| Operational schema | `monitoring` | Separates short-lived operational telemetry from authoritative business records. |
| Application identity | `r2r_app` or `svc_r2r_backend` | Restricted database user used by the Go backend; this is not a schema. |
| Migration identity | Separate deployment/DBA account | Owns DDL permission and is not used by the running application. |
| Default SQL port | `1433` | Use the company-approved SQL Server endpoint or Availability Group listener. |
| Connection timeout | 5–15 seconds | Prevents requests from hanging indefinitely while allowing normal network or failover delay. |

Initial database settings:

```sql
ALTER DATABASE [R2RControl] SET RECOVERY FULL;
ALTER DATABASE [R2RControl]
    SET READ_COMMITTED_SNAPSHOT ON WITH ROLLBACK IMMEDIATE;
ALTER DATABASE [R2RControl] SET AUTO_CLOSE OFF;
ALTER DATABASE [R2RControl] SET AUTO_SHRINK OFF;
```

`WITH ROLLBACK IMMEDIATE` disconnects active sessions. Use it only during provisioning or a planned maintenance window. `FULL` recovery requires scheduled transaction-log backups; otherwise, the log can continue growing.

## Runtime identity and permissions

The application identity performs normal data operations but must not alter the database structure.

```sql
USE [R2RControl];
GO

CREATE USER [r2r_app] FOR LOGIN [r2r_app];

GRANT SELECT, INSERT, UPDATE, DELETE ON SCHEMA::r2r TO [r2r_app];
DENY ALTER, CONTROL ON SCHEMA::r2r TO [r2r_app];

GRANT SELECT, INSERT ON OBJECT::r2r.audit_ledger_events TO [r2r_app];
DENY UPDATE, DELETE ON OBJECT::r2r.audit_ledger_events TO [r2r_app];
```

Prefer a Windows or Active Directory service identity when supported. Secrets must be stored in the deployment secret manager, not in model JSON or database tables.

## Table inventory

| Table | Purpose |
|---|---|
| `r2r.schema_migrations` | Records applied database migrations. |
| `r2r.model_families` | Stable identity shared by all revisions of one model. |
| `r2r.model_revisions` | Immutable/versioned model definitions and approval workflow. |
| `r2r.data_sources` | Metadata for external SQL Server, PostgreSQL, or MySQL sources. |
| `r2r.runs` | Authoritative prepare/complete lifecycle for each production run. |
| `r2r.run_outputs` | Typed output values and limit evaluation for a run. |
| `r2r.run_parameter_values` | Typed index, external-input, and calculated parameter values. |
| `r2r.run_data_samples` | Data-sample snapshot metadata and optional JSON content. |
| `r2r.run_feedback_values` | Typed post-run feedback received at completion. |
| `r2r.adaptive_states` | Current adaptive value for each model, parameter, and execution partition. |
| `r2r.roles` | Application role definitions. |
| `r2r.users` | Registered R2R users linked to corporate employee identity. |
| `r2r.user_sessions` | Hashed login sessions and revocation state. |
| `r2r.input_definitions` | Admin-managed registry of allowed indexes and external inputs. |
| `r2r.audit_ledger_events` | Append-only immutable business audit trail. |
| `monitoring.operational_events` | HTTP and service diagnostics for monitoring. |
| `r2r.audit_events` | Legacy audit table retained only for migration history; new writes use the ledger table. |

## Column dictionary

`NULL` in the Required column means the value is optional. `Generated` means SQL Server supplies or maintains it.

### `r2r.schema_migrations`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `version` | `int` | Yes | Migration number and primary key. |
| `description` | `nvarchar(200)` | Yes | Human-readable migration summary. |
| `applied_at_utc` | `datetime2(3)` | Generated | UTC application time. |

### `r2r.model_families`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `family_id` | `nvarchar(100)` | Yes | Stable model-family primary key. |
| `name` | `nvarchar(200)` | Yes | User-facing model name. |
| `description` | `nvarchar(1000)` | NULL | Business description. |
| `created_by` | `nvarchar(200)` | Yes | Creator identity. |
| `created_at_utc` | `datetime2(3)` | Generated | Creation time. |
| `row_version` | `rowversion` | Generated | Optimistic concurrency token. |

### `r2r.model_revisions`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `model_id` | `nvarchar(150)` | Yes | Revision primary key. |
| `family_id` | `nvarchar(100)` | Yes | Parent model family. |
| `revision` | `int` | Yes | Positive revision number, unique within a family. |
| `status` | `varchar(20)` | Yes | `draft`, `pending_approval`, `approved`, or `superseded`. |
| `enabled` | `bit` | Yes | Whether the approved revision participates in production selection. |
| `definition_json` | `nvarchar(max)` | Yes | Complete validated model definition. |
| `change_note` | `nvarchar(2000)` | Yes | Reason for the revision. |
| `owner_name` | `nvarchar(200)` | Yes | Current draft/model owner. |
| `created_by` | `nvarchar(200)` | Yes | Original creator; ownership transfer does not rewrite it. |
| `created_at_utc` | `datetime2(3)` | Generated | Creation time. |
| `submitted_by` | `nvarchar(200)` | NULL | Employee who requested approval. |
| `submitted_at_utc` | `datetime2(3)` | NULL | Submission time. |
| `approved_by` | `nvarchar(200)` | NULL | Supervisor or administrator who approved. |
| `approved_at_utc` | `datetime2(3)` | NULL | Approval time. |
| `rejected_by` | `nvarchar(200)` | NULL | Reviewer who rejected the submission. |
| `rejected_at_utc` | `datetime2(3)` | NULL | Rejection time. |
| `rejection_reason` | `nvarchar(2000)` | NULL | Required explanation when rejection metadata exists. |
| `row_version` | `rowversion` | Generated | Optimistic concurrency token. |
| `valid_from_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version start. |
| `valid_to_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version end. |

Only one enabled, approved revision may exist per family. SQL Server maintains `r2r.model_revisions_history` automatically.

### `r2r.data_sources`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `source_id` | `nvarchar(100)` | Yes | Source primary key. |
| `name` | `nvarchar(200)` | Yes | Unique display name. |
| `source_type` | `varchar(30)` | Yes | Source classification. |
| `engine` | `varchar(20)` | Yes | `sqlserver`, `postgres`, or `mysql`. |
| `host_name` | `nvarchar(255)` | Yes | Database host or listener. |
| `port` | `int` | Yes | TCP port, 1–65535. |
| `database_name` | `nvarchar(128)` | Yes | External database name. |
| `login_name` | `nvarchar(128)` | NULL | External database login name. |
| `secret_reference` | `nvarchar(200)` | NULL | Name of a deployment-managed secret; never the password. |
| `tls_mode` | `varchar(20)` | Yes | Engine-specific TLS mode. |
| `timeout_seconds` | `int` | Yes | Query timeout, 1–300 seconds. |
| `location_name` | `nvarchar(200)` | NULL | Site or environment label. |
| `enabled` | `bit` | Yes | Whether models may use this source. |
| `last_test_status` | `varchar(20)` | NULL | `success` or `failed`. |
| `last_test_message` | `nvarchar(2000)` | NULL | Sanitized connection-test result. |
| `last_test_at_utc` | `datetime2(3)` | NULL | Last test time. |
| `last_test_ms` | `bigint` | NULL | Last test duration. |
| `created_at_utc` | `datetime2(3)` | Generated | Creation time. |
| `updated_at_utc` | `datetime2(3)` | Generated/application | Last update time. |
| `row_version` | `rowversion` | Generated | Optimistic concurrency token. |

There is intentionally no default schema column. Model SQL uses qualified object names when it must access different schemas in one external database.

### `r2r.runs`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | Caller-supplied idempotency key and primary key. |
| `run_sequence` | `bigint IDENTITY` | Generated | Monotonic internal execution order. |
| `family_id` | `nvarchar(100)` | Yes | Selected model family. |
| `model_id` | `nvarchar(150)` | Yes | Exact selected revision. |
| `model_revision` | `int` | Yes | Revision number copied for reporting. |
| `status` | `varchar(30)` | Yes | `prepared`, `completed`, `run_failed`, or `run_cancelled`. |
| `disposition` | `varchar(30)` | NULL | Output/limit disposition. |
| `prepared_at_utc` | `datetime2(3)` | Generated | Prepare time. |
| `completed_at_utc` | `datetime2(3)` | NULL | Completion, failure, or cancellation time. |
| `context_json` | `nvarchar(max)` | Yes | Model-selection indexes and external request context. |
| `parameters_json` | `nvarchar(max)` | Yes | Compatibility snapshot of resolved parameters. |
| `feedback_json` | `nvarchar(max)` | NULL | Compatibility snapshot of completion feedback. |
| `adaptive_updates_json` | `nvarchar(max)` | NULL | Compatibility snapshot of adaptive updates. |
| `correlation_id` | `uniqueidentifier` | Generated | Correlates business records and audit events. |
| `request_id` | `nvarchar(100)` | NULL | HTTP/trace request identifier. |
| `failure_code` | `nvarchar(100)` | NULL | Machine-readable failure category. |
| `failure_message` | `nvarchar(2000)` | NULL | Sanitized failure details. |
| `partition_hash` | `binary(32)` | NULL | Fixed-size identity of the configured unfinished-run partition. |
| `partition_key` | `nvarchar(1000)` | NULL | Canonical readable partition key. |
| `partition_json` | `nvarchar(max)` | NULL | Selected partition field names and values. |
| `row_version` | `rowversion` | Generated | Optimistic concurrency token. |
| `valid_from_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version start. |
| `valid_to_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version end. |

SQL Server maintains `r2r.runs_history` automatically. New reporting should prefer the normalized value tables below over parsing compatibility JSON.

### `r2r.run_outputs`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | Parent run. |
| `output_ordinal` | `int` | Yes | Stable output order within the run. |
| `parameter_name` | `nvarchar(128)` | Yes | Output parameter name. |
| `data_type` | `varchar(10)` | Yes | `number`, `string`, or `boolean`. |
| `number_value` | `float` | Conditional | Numeric output value. |
| `text_value` | `nvarchar(max)` | Conditional | Text output value. |
| `boolean_value` | `bit` | Conditional | Boolean output value. |
| `calculated_number_value` | `float` | NULL | Unmodified numerical result before limit action. |
| `unit` | `nvarchar(50)` | NULL | Engineering unit. |
| `lsl` | `float` | NULL | Lower specification limit used. |
| `usl` | `float` | NULL | Upper specification limit used. |
| `out_of_limit` | `bit` | Yes | Whether the calculated value exceeded a limit. |
| `limit_action` | `varchar(20)` | NULL | `warn`, `clamp`, `reject`, or `fallback`. |

Exactly one typed value column is populated according to `data_type`.

### `r2r.run_parameter_values`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | Parent run. |
| `parameter_ordinal` | `int` | Yes | Stable value order within the run. |
| `parameter_name` | `nvarchar(128)` | Yes | Index, external input, or calculated parameter name. |
| `parameter_kind` | `varchar(20)` | Yes | `index`, `external`, or `calculated`. |
| `source_type` | `varchar(30)` | Yes | Expression, SQL, input, or other source classification. |
| `data_type` | `varchar(10)` | Yes | `number`, `string`, or `boolean`. |
| `number_value` | `float` | Conditional | Numeric value. |
| `text_value` | `nvarchar(max)` | Conditional | Text value. |
| `boolean_value` | `bit` | Conditional | Boolean value. |
| `unit` | `nvarchar(50)` | NULL | Engineering unit. |
| `protection_class` | `varchar(20)` | Yes | `normal`, `masked`, or `secret`. |
| `is_redacted` | `bit` | Yes | Whether the stored value has been removed. |
| `calculated_at_utc` | `datetime2(3)` | Generated | Resolution time. |

### `r2r.run_data_samples`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | Parent run. |
| `sample_name` | `nvarchar(128)` | Yes | Data-sample name. |
| `data_source_id` | `nvarchar(100)` | Yes | External source used by the sample. |
| `query_hash` | `binary(32)` | Yes | SHA-256-style query identity without storing SQL text here. |
| `row_count` | `int` | Yes | Number of rows returned. |
| `snapshot_json` | `nvarchar(max)` | Conditional | Optional typed sample snapshot for traceability. |
| `protection_class` | `varchar(20)` | Yes | `normal`, `masked`, or `secret`. |
| `is_redacted` | `bit` | Yes | Whether the snapshot is intentionally omitted. |
| `captured_at_utc` | `datetime2(3)` | Generated | Capture time. |

Static samples do not have a natural external `data_source_id`; before production implementation, migration design should either allow this column to be nullable or introduce an explicit source-kind/system-source convention.

### `r2r.run_feedback_values`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | Parent run. |
| `feedback_ordinal` | `int` | Yes | Stable feedback order within the run. |
| `feedback_name` | `nvarchar(128)` | Yes | Feedback field name. |
| `data_type` | `varchar(10)` | Yes | `number`, `string`, or `boolean`. |
| `number_value` | `float` | Conditional | Numeric feedback. |
| `text_value` | `nvarchar(max)` | Conditional | Text feedback. |
| `boolean_value` | `bit` | Conditional | Boolean feedback. |
| `unit` | `nvarchar(50)` | NULL | Engineering unit. |
| `protection_class` | `varchar(20)` | Yes | `normal`, `masked`, or `secret`. |
| `is_redacted` | `bit` | Yes | Whether the stored value is redacted. |
| `received_at_utc` | `datetime2(3)` | Generated | Completion-feedback time. |

### `r2r.adaptive_states`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `family_id` | `nvarchar(100)` | Yes | Model family. |
| `parameter_name` | `nvarchar(128)` | Yes | Adaptive parameter. |
| `scope_hash` | `binary(32)` | Yes | Fixed-size identity of the model execution partition. |
| `scope_key` | `nvarchar(1000)` | Yes | Canonical readable partition key. |
| `scope_json` | `nvarchar(max)` | Yes | Execution partition names and values. |
| `current_value` | `float` | Yes | Current committed adaptive value. |
| `last_run_id` | `nvarchar(200)` | NULL | Run that last updated the state. |
| `last_run_sequence` | `bigint` | Yes | Prevents stale completion from overwriting newer state. |
| `updated_at_utc` | `datetime2(3)` | Generated/application | Last update time. |
| `row_version` | `rowversion` | Generated | Concurrency protection. |
| `valid_from_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version start. |
| `valid_to_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version end. |

SQL Server maintains `r2r.adaptive_states_history` automatically.

### `r2r.roles`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `role_name` | `nvarchar(100)` | Yes | Role primary key. |
| `display_name` | `nvarchar(200)` | Yes | User-facing role name. |
| `description` | `nvarchar(1000)` | Yes | Permission summary. |
| `is_system` | `bit` | Yes | Protects built-in roles from deletion or incompatible changes. |
| `created_by` | `nvarchar(200)` | Yes | Creator identity. |
| `created_at_utc` | `datetime2(3)` | Generated | Creation time. |
| `updated_by` | `nvarchar(200)` | Yes | Last editor. |
| `updated_at_utc` | `datetime2(3)` | Generated/application | Last edit time. |
| `row_version` | `rowversion` | Generated | Concurrency protection. |

Built-in roles are `admin`, `engineering_manager`, `engineer`, and `viewer`.

### `r2r.users`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `user_id` | `nvarchar(200)` | Yes | Internal R2R user primary key. |
| `employee_id` | `nvarchar(100)` | NULL | Unique corporate `IDNUM` used for registration and supervisor approval. |
| `login_name` | `nvarchar(200)` | Yes | Corporate `LOGINID`. |
| `display_name` | `nvarchar(200)` | Yes | User-facing name. |
| `email_address` | `nvarchar(320)` | NULL | Corporate email address. |
| `role_name` | `nvarchar(100)` | Yes | Assigned R2R role. |
| `status` | `varchar(20)` | Yes | `active`, `disabled`, or `locked`. |
| `identity_provider` | `nvarchar(100)` | Yes | Corporate authentication provider identifier. |
| `external_subject` | `nvarchar(300)` | Yes | Stable subject supplied by the identity provider. |
| `created_by` | `nvarchar(200)` | Yes | Administrator who registered the user. |
| `created_at_utc` | `datetime2(3)` | Generated | Registration time. |
| `updated_by` | `nvarchar(200)` | Yes | Last administrator to modify the registration. |
| `updated_at_utc` | `datetime2(3)` | Generated/application | Last modification time. |
| `last_login_at_utc` | `datetime2(3)` | NULL | Most recent successful login. |
| `row_version` | `rowversion` | Generated | Concurrency protection. |

Supervisor identity remains authoritative in the corporate employee directory (`IDNUM`, `LOGINID`, `EMAIL`, `SUPID`) and is not duplicated as a permanent relationship in R2R.

### `r2r.user_sessions`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `session_id_hash` | `binary(32)` | Yes | Hash of the session token; raw tokens are never stored. |
| `user_id` | `nvarchar(200)` | Yes | Registered R2R user. |
| `created_at_utc` | `datetime2(3)` | Yes | Session creation time. |
| `expires_at_utc` | `datetime2(3)` | Yes | Absolute expiration time. |
| `revoked_at_utc` | `datetime2(3)` | NULL | Logout, deletion, disablement, or administrative revocation time. |
| `last_seen_at_utc` | `datetime2(3)` | Yes | Most recent accepted request. |
| `client_address` | `varchar(64)` | NULL | IPv4 or IPv6 client address when policy permits. |
| `user_agent_hash` | `binary(32)` | NULL | Privacy-conscious client fingerprint for investigation. |

### `r2r.input_definitions`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `input_name` | `nvarchar(128)` | Yes | Stable registry key. |
| `display_name` | `nvarchar(200)` | Yes | User-facing label. |
| `category` | `varchar(20)` | Yes | `index` or `external`. |
| `data_type` | `varchar(20)` | Yes | `number`, `string`, or `boolean`. |
| `description` | `nvarchar(1000)` | NULL | Business meaning. |
| `status` | `varchar(20)` | Yes | `active` or `deprecated`. |
| `is_system` | `bit` | Yes | Protects built-in routing indexes. |
| `created_by` | `nvarchar(200)` | Yes | Creator identity. |
| `created_at_utc` | `datetime2(3)` | Generated | Creation time. |
| `updated_by` | `nvarchar(200)` | Yes | Last editor. |
| `updated_at_utc` | `datetime2(3)` | Generated/application | Last edit time. |
| `row_version` | `rowversion` | Generated | Concurrency protection. |

Built-in system indexes are `machine`, `action`, and `material`.

### `r2r.audit_ledger_events`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `event_id` | `uniqueidentifier` | Generated | Audit-event primary key. |
| `occurred_at_utc` | `datetime2(7)` | Generated | High-resolution event time. |
| `actor_type` | `varchar(20)` | Yes | `user`, `service`, or `system`. |
| `actor_id` | `nvarchar(200)` | Yes | Identity that performed the action. |
| `action_name` | `nvarchar(100)` | Yes | Stable action name. |
| `entity_type` | `nvarchar(100)` | Yes | Type of affected record. |
| `entity_id` | `nvarchar(200)` | Yes | Identifier of affected record. |
| `request_id` | `nvarchar(100)` | NULL | HTTP request correlation. |
| `run_id` | `nvarchar(200)` | NULL | Related R2R run. |
| `correlation_id` | `uniqueidentifier` | NULL | Cross-table business correlation. |
| `details_json` | `nvarchar(max)` | NULL | Structured before/after or action details. |
| `client_address` | `varchar(64)` | NULL | Client address when policy permits. |
| `legacy_audit_id` | `bigint` | NULL | Source identifier for records migrated from `audit_events`. |

This is an append-only SQL Server ledger table. The application must never update or delete its rows.

### `monitoring.operational_events`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `event_id` | `uniqueidentifier` | Generated | Operational-event primary key. |
| `occurred_at_utc` | `datetime2(7)` | Generated | Event time. |
| `severity` | `varchar(10)` | Yes | `INFO`, `WARN`, `ERROR`, or `FATAL`. |
| `event_name` | `nvarchar(100)` | Yes | Stable event category. |
| `service_instance_id` | `nvarchar(200)` | Yes | Identifies the backend server/process instance. |
| `application_version` | `nvarchar(50)` | Yes | Deployed application version. |
| `request_id` | `nvarchar(100)` | NULL | HTTP request correlation. |
| `trace_id` | `varchar(64)` | NULL | OpenTelemetry distributed trace identifier. |
| `run_id` | `nvarchar(200)` | NULL | Related R2R run. |
| `actor_id` | `nvarchar(200)` | NULL | Authenticated user/service. |
| `http_method` | `varchar(10)` | NULL | HTTP method. |
| `route` | `nvarchar(500)` | NULL | Matched API route. |
| `status_code` | `int` | NULL | HTTP status, 100–599. |
| `duration_ms` | `bigint` | NULL | Request or operation duration. |
| `response_bytes` | `bigint` | NULL | Response size. |
| `error_code` | `nvarchar(100)` | NULL | Stable diagnostic code. |
| `error_message` | `nvarchar(2000)` | NULL | Sanitized diagnostic message. |
| `client_address` | `varchar(64)` | NULL | Client address when policy permits. |

Operational events have a shorter retention policy than audit and run records. They may be forwarded to an OpenTelemetry collector or centralized logging platform.

### `r2r.audit_events` (legacy)

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `audit_id` | `bigint IDENTITY` | Generated | Legacy primary key. |
| `occurred_at_utc` | `datetime2(3)` | Generated | Event time. |
| `actor_type` | `varchar(20)` | Yes | `user`, `service`, or `system`. |
| `actor_id` | `nvarchar(200)` | Yes | Actor identity. |
| `action_name` | `nvarchar(100)` | Yes | Action name. |
| `entity_type` | `nvarchar(100)` | Yes | Entity type. |
| `entity_id` | `nvarchar(200)` | Yes | Entity identifier. |
| `correlation_id` | `uniqueidentifier` | NULL | Business correlation. |
| `before_json` | `nvarchar(max)` | NULL | Previous state. |
| `after_json` | `nvarchar(max)` | NULL | New state. |
| `client_address` | `varchar(64)` | NULL | Client address. |

Migration `002` copies existing rows into `r2r.audit_ledger_events`. Do not write new production events to this legacy table.

## SQL Server-managed companion objects

The following are created or managed automatically and are not normal application write targets:

| Object | Managed by | Purpose |
|---|---|---|
| `r2r.model_revisions_history` | System-versioning | Previous model-revision row versions. |
| `r2r.runs_history` | System-versioning | Previous run row versions. |
| `r2r.adaptive_states_history` | System-versioning | Previous adaptive-state row versions. |
| Ledger history/view/digest metadata | SQL Server ledger | Cryptographic audit-verification support. |

## Migration order

Run migrations using a deployment identity with DDL permission. The application identity must not execute them.

1. `001_initial_schema.sql`
2. `002_history_ledger_and_logging.sql`
3. `003_boolean_outputs_and_external_engines.sql`
4. `004_model_submission_workflow.sql`
5. `005_remove_default_schema.sql`
6. `006_production_identity_and_execution_tables.sql`
7. `007_employee_identity_and_supervisor_approval.sql`
8. `008_unfinished_run_partitions.sql`

Each later schema change must use a new numbered migration. Never edit a migration after it has run in a shared environment.

## Backup and retention baseline

- Full database backup: daily.
- Differential backup: every few hours, according to recovery objectives.
- Transaction-log backup: every 5–15 minutes.
- Restore test: scheduled and documented.
- Audit ledger: retain according to the company's audit and manufacturing-record policy.
- Run/value data: retain according to process traceability requirements.
- Operational events: use a shorter, capacity-based retention period.
- Ledger digest: export to storage controlled separately from the database administrators.

## Current implementation note

The migrations describe the intended production database. The current MVP backend still uses `data/config.json` and in-memory sessions for parts of its runtime state. The Go repository layer must be connected to these SQL Server tables before the application is production-persistent.
