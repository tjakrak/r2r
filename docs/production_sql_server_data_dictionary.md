# R2R production SQL Server specification

This document defines the deployed SQL Server 2019 table contract used by the Go backend. All application and operational tables are in the `PCS` schema.

## Database settings

| Setting | Production value | Reason |
|---|---|---|
| Database name | `R2RControl` | Dedicated security, backup, and lifecycle boundary for R2R. Adjust to the company naming standard if needed. |
| SQL Server version | SQL Server 2019 (15.x) or later | Matches the deployed server; audit immutability uses permissions and a trigger rather than SQL Server Ledger. |
| Recovery model | `FULL` | Supports point-in-time recovery when transaction-log backups are scheduled. |
| `READ_COMMITTED_SNAPSHOT` | `ON` | Reduces reader/writer blocking during model execution and administration. |
| `AUTO_CLOSE` | `OFF` | Avoids repeated database shutdown and startup overhead. |
| `AUTO_SHRINK` | `OFF` | Avoids file fragmentation and repeated shrink/growth cycles. |
| Connection encryption | Required | Protects identities, model definitions, parameter values, and manufacturing data in transit. |
| Certificate validation | `TrustServerCertificate=false` | Ensures the backend validates the SQL Server certificate in production. |
| Application timestamps | UTC | Keeps events unambiguous across servers, factories, and daylight-saving changes. |
| Application schema | `PCS` | Contains business, identity, execution, audit, and operational-event data. |
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

GRANT SELECT, INSERT, UPDATE, DELETE ON SCHEMA::PCS TO [r2r_app];
DENY ALTER, CONTROL ON SCHEMA::PCS TO [r2r_app];

GRANT SELECT, INSERT ON OBJECT::PCS.audit_events TO [r2r_app];
DENY UPDATE, DELETE ON OBJECT::PCS.audit_events TO [r2r_app];
```

Prefer a Windows or Active Directory service identity when supported. Secrets must be stored in the deployment secret manager, not in model JSON or database tables.

## Table inventory

| Table | Purpose |
|---|---|
| `PCS.schema_migrations` | Records applied database migrations. |
| `PCS.model_families` | Stable identity shared by all revisions of one model. |
| `PCS.model_revisions` | Immutable/versioned model definitions and approval workflow. |
| `PCS.data_sources` | Metadata for external SQL Server, PostgreSQL, or MySQL sources. |
| `PCS.runs` | Authoritative prepare/complete lifecycle for each production run. |
| `PCS.run_outputs` | Typed output values and limit evaluation for a run. |
| `PCS.run_parameter_values` | Typed index, external-input, and calculated parameter values. |
| `PCS.run_data_samples` | Data-sample snapshot metadata and optional JSON content. |
| `PCS.run_feedback_values` | Typed post-run feedback received at completion. |
| `PCS.adaptive_states` | Current adaptive value for each model, parameter, and execution partition. |
| `PCS.roles` | Application role definitions. |
| `PCS.users` | Registered R2R users linked to corporate employee identity. |
| `PCS.user_sessions` | Hashed login sessions and revocation state. |
| `PCS.input_definitions` | Admin-managed registry of allowed indexes and external inputs. |
| `PCS.audit_events` | Append-only immutable business audit trail. |
| `PCS.operational_events` | HTTP and service diagnostics for PCS. |

## Column dictionary

`NULL` in the Required column means the value is optional. `Generated` means SQL Server supplies or maintains it.

### `PCS.schema_migrations`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `version` | `int` | Yes | Migration number and primary key. |
| `description` | `nvarchar(200)` | Yes | Human-readable migration summary. |
| `applied_at_utc` | `datetime2(3)` | Generated | UTC application time. |

### `PCS.model_families`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `family_id` | `nvarchar(100)` | Yes | Stable model-family primary key. |
| `name` | `nvarchar(200)` | Yes | User-facing model name. |
| `description` | `nvarchar(1000)` | NULL | Business description. |
| `created_by` | `nvarchar(200)` | Yes | Creator identity. |
| `created_at_utc` | `datetime2(3)` | Generated | Creation time. |
| `row_version` | `rowversion` | Generated | Optimistic concurrency token. |

### `PCS.model_revisions`

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

Only one enabled, approved revision may exist per family. SQL Server maintains `PCS.model_revisions_history` automatically.

### `PCS.data_sources`

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

### `PCS.runs`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | R2R-generated run identifier and primary key. |
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
| `partition_hash` | `binary(32)` | Yes | Fixed-size identity of the configured unfinished-run partition. |
| `partition_json` | `nvarchar(max)` | Yes | Selected partition field names and values; defaults to the machine partition when not configured. |
| `row_version` | `rowversion` | Generated | Optimistic concurrency token. |
| `valid_from_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version start. |
| `valid_to_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version end. |

SQL Server maintains `PCS.runs_history` automatically. New reporting should prefer the normalized value tables below over parsing compatibility JSON.

### `PCS.run_outputs`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | Parent run. |
| `output_ordinal` | `int` | Yes | Stable output order within the run. |
| `parameter_name` | `nvarchar(128)` | Yes | Output parameter name. |
| `data_type` | `varchar(10)` | Yes | `number`, `string`, or `boolean`. |
| `is_null` | `bit` | Yes | `1` when the output intentionally resolved to null; all typed value columns must then be `NULL`. |
| `number_value` | `float` | Conditional | Numeric output value. |
| `text_value` | `nvarchar(max)` | Conditional | Text output value. |
| `boolean_value` | `bit` | Conditional | Boolean output value. |
| `calculated_number_value` | `float` | NULL | Unmodified numerical result before limit action. |
| `unit` | `nvarchar(50)` | NULL | Engineering unit. |
| `lsl` | `float` | NULL | Lower specification limit used. |
| `usl` | `float` | NULL | Upper specification limit used. |
| `out_of_limit` | `bit` | Yes | Whether the calculated value exceeded a limit. |
| `limit_action` | `varchar(20)` | NULL | `warn`, `clamp`, `reject`, or `fallback`. |

Exactly one typed value column is populated according to `data_type` when `is_null = 0`. All typed value columns are `NULL` when `is_null = 1`.

### `PCS.run_parameter_values`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | Parent run. |
| `parameter_ordinal` | `int` | Yes | Stable value order within the run. |
| `parameter_name` | `nvarchar(128)` | Yes | Index, external input, or calculated parameter name. |
| `parameter_kind` | `varchar(20)` | Yes | `index`, `external`, or `calculated`. |
| `source_type` | `varchar(30)` | Yes | Expression, SQL, input, or other source classification. |
| `data_type` | `varchar(10)` | Yes | `number`, `string`, or `boolean`. |
| `is_null` | `bit` | Yes | `1` when the parameter intentionally resolved to null; all typed value columns must then be `NULL`. |
| `number_value` | `float` | Conditional | Numeric value. |
| `text_value` | `nvarchar(max)` | Conditional | Text value. |
| `boolean_value` | `bit` | Conditional | Boolean value. |
| `unit` | `nvarchar(50)` | NULL | Engineering unit. |
| `resolved_at_utc` | `datetime2(3)` | Generated | Resolution time. |

### `PCS.run_data_samples`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `run_id` | `nvarchar(200)` | Yes | Parent run. |
| `sample_name` | `nvarchar(128)` | Yes | Data-sample name. |
| `sample_source` | `varchar(10)` | Yes | `sql` or `static`. |
| `data_source_id` | `nvarchar(100)` | NULL | External source used by a SQL sample; NULL for a static sample. |
| `parameterized_query_text` | `nvarchar(max)` | NULL | Parameterized SQL text for a SQL sample; never contains substituted values. |
| `query_hash` | `binary(32)` | NULL | SHA-256 identity for a SQL sample; NULL for a static sample. |
| `row_count` | `int` | Yes | Number of rows returned. |
| `snapshot_json` | `nvarchar(max)` | Conditional | Optional typed sample snapshot for traceability. |
| `captured_at_utc` | `datetime2(3)` | Generated | Capture time. |

SQL samples require a data source, parameterized query, and query hash. Static samples store those three fields as NULL.

### `PCS.run_feedback_values`

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
| `received_at_utc` | `datetime2(3)` | Generated | Completion-feedback time. |

### `PCS.adaptive_states`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `family_id` | `nvarchar(100)` | Yes | Model family. |
| `parameter_name` | `nvarchar(128)` | Yes | Adaptive parameter. |
| `scope_hash` | `binary(32)` | Yes | Fixed-size identity of the model execution partition. |
| `scope_json` | `nvarchar(max)` | Yes | Execution partition names and values. |
| `current_value` | `float` | Yes | Current committed adaptive value. |
| `last_run_id` | `nvarchar(200)` | NULL | Run that last updated the state. |
| `last_run_sequence` | `bigint` | Yes | Prevents stale completion from overwriting newer state. |
| `updated_at_utc` | `datetime2(3)` | Generated/application | Last update time. |
| `row_version` | `rowversion` | Generated | Concurrency protection. |
| `valid_from_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version start. |
| `valid_to_utc` | `datetime2(7)` | Generated/hidden | Temporal row-version end. |

SQL Server maintains `PCS.adaptive_states_history` automatically.

### `PCS.roles`

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

### `PCS.users`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `user_id` | `nvarchar(200)` | Yes | Internal R2R user primary key. |
| `employee_id` | `nvarchar(100)` | Yes | Unique corporate `IDNUM` used for registration and supervisor approval. |
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

### `PCS.user_sessions`

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

### `PCS.input_definitions`

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

### `PCS.audit_events`

| Column | SQL type | Required | Purpose |
|---|---|---|---|
| `event_id` | `uniqueidentifier` | Generated | Audit-event primary key. |
| `occurred_at_utc` | `datetime2(7)` | Generated | High-resolution event time. |
| `actor_type` | `varchar(20)` | Yes | `user`, `service`, or `system`. |
| `actor_id` | `nvarchar(200)` | Yes | Identity that performed the action. |
| `action_name` | `nvarchar(100)` | Yes | Stable action name. |
| `outcome` | `varchar(20)` | Yes | `success`, `failure`, or `denied`. |
| `entity_type` | `nvarchar(100)` | Yes | Type of affected record. |
| `entity_id` | `nvarchar(200)` | Yes | Identifier of affected record. |
| `request_id` | `nvarchar(100)` | NULL | HTTP request correlation. |
| `run_id` | `nvarchar(200)` | NULL | Related R2R run. |
| `correlation_id` | `uniqueidentifier` | NULL | Cross-table business correlation. |
| `details_json` | `nvarchar(max)` | NULL | Structured before/after or action details. |
| `client_address` | `varchar(64)` | NULL | Client address when policy permits. |
This is an append-only application audit table. SQL Server 2019 permissions and a trigger prevent normal updates and deletes.

### `PCS.operational_events`

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

## Representative example records

These examples show one representative record for every production table. They are displayed as JSON
for readability, not as API payloads or SQL insert statements. SQL JSON columns are shown decoded,
binary values are abbreviated, and generated values illustrate what SQL Server supplies.

### `PCS.schema_migrations`

```json
{
  "version": 9,
  "description": "Align production execution tables",
  "applied_at_utc": "2026-08-25T17:30:00.000Z"
}
```

### `PCS.model_families`

```json
{
  "family_id": "b5-rie802-recipe-selection",
  "name": "B5 RIE802 Recipe Selection",
  "description": "Selects an RIE802 recipe from wafer history and workbook rules.",
  "created_by": "USR-000123",
  "created_at_utc": "2026-08-25T17:31:00.000Z",
  "row_version": "0x00000000000007D1"
}
```

### `PCS.model_revisions`

```json
{
  "model_id": "b5-rie802-recipe-selection-r1",
  "family_id": "b5-rie802-recipe-selection",
  "revision": 1,
  "status": "approved",
  "enabled": true,
  "definition_json": {
    "name": "B5 RIE802 Recipe Selection",
    "indexes": {
      "machine": ["B5_RIE802"],
      "action": ["RECIPE_SELECTION"],
      "material": ["WAFER"]
    },
    "outputs": ["recipe"]
  },
  "change_note": "Initial conversion from the approved workbook",
  "owner_name": "USR-000123",
  "created_by": "USR-000123",
  "created_at_utc": "2026-08-25T17:31:00.000Z",
  "submitted_by": "USR-000123",
  "submitted_at_utc": "2026-08-25T18:00:00.000Z",
  "approved_by": "USR-000100",
  "approved_at_utc": "2026-08-25T18:15:00.000Z",
  "rejected_by": null,
  "rejected_at_utc": null,
  "rejection_reason": null,
  "row_version": "0x00000000000007D2",
  "valid_from_utc": "2026-08-25T18:15:00.0000000Z",
  "valid_to_utc": "9999-12-31T23:59:59.9999999Z"
}
```

### `PCS.data_sources`

```json
{
  "source_id": "wf8parametric",
  "name": "WF8 Parametric Production",
  "source_type": "sql",
  "engine": "sqlserver",
  "host_name": "sql-listener.internal.example",
  "port": 1433,
  "database_name": "WF8PARAMETRIC",
  "login_name": "svc_r2r_reader",
  "secret_reference": "R2R_WF8PARAMETRIC_PASSWORD",
  "tls_mode": "require",
  "timeout_seconds": 15,
  "location_name": "Factory 8",
  "enabled": true,
  "last_test_status": "success",
  "last_test_message": "Connection and authentication succeeded",
  "last_test_at_utc": "2026-08-25T18:20:00.000Z",
  "last_test_ms": 42,
  "created_at_utc": "2026-08-25T17:00:00.000Z",
  "updated_at_utc": "2026-08-25T18:20:00.000Z",
  "row_version": "0x00000000000007D3"
}
```

### `PCS.runs`

```json
{
  "run_id": "r2r_8f31c9240a7e4fd5a3f6",
  "run_sequence": 1042,
  "family_id": "b5-rie802-recipe-selection",
  "model_id": "b5-rie802-recipe-selection-r1",
  "model_revision": 1,
  "status": "prepared",
  "disposition": "ready",
  "prepared_at_utc": "2026-08-25T19:00:00.125Z",
  "completed_at_utc": null,
  "context_json": {
    "machine": "B5_RIE802",
    "action": "RECIPE_SELECTION",
    "material": "WAFER",
    "wafer_id": "W01",
    "operation": "BPI190",
    "product": "700-0016-000"
  },
  "parameters_json": {
    "selection_value": 0.05,
    "recipe": "~PM2/PM3_N2O2_4mTIG_4.7%_12_250W_105S~"
  },
  "feedback_json": null,
  "adaptive_updates_json": null,
  "correlation_id": "6d27a7a6-73ce-4f23-a02d-c23e202eb105",
  "request_id": "req-20260825-00042",
  "failure_code": null,
  "failure_message": null,
  "partition_hash": "0xA6B84C...32_BYTES",
  "partition_json": {
    "wafer_id": "W01"
  },
  "row_version": "0x00000000000007D4",
  "valid_from_utc": "2026-08-25T19:00:00.1250000Z",
  "valid_to_utc": "9999-12-31T23:59:59.9999999Z"
}
```

### `PCS.run_outputs`

```json
{
  "run_id": "r2r_8f31c9240a7e4fd5a3f6",
  "output_ordinal": 0,
  "parameter_name": "recipe",
  "data_type": "string",
  "is_null": false,
  "number_value": null,
  "text_value": "~PM2/PM3_N2O2_4mTIG_4.7%_12_250W_105S~",
  "boolean_value": null,
  "calculated_number_value": null,
  "unit": null,
  "lsl": null,
  "usl": null,
  "out_of_limit": false,
  "limit_action": null
}
```

### `PCS.run_parameter_values`

```json
{
  "run_id": "r2r_8f31c9240a7e4fd5a3f6",
  "parameter_ordinal": 4,
  "parameter_name": "selection_value",
  "parameter_kind": "calculated",
  "source_type": "expression",
  "data_type": "number",
  "is_null": false,
  "number_value": 0.05,
  "text_value": null,
  "boolean_value": null,
  "unit": null,
  "resolved_at_utc": "2026-08-25T19:00:00.120Z"
}
```

### `PCS.run_data_samples`

```json
{
  "run_id": "r2r_8f31c9240a7e4fd5a3f6",
  "sample_name": "wafer_history",
  "sample_source": "sql",
  "data_source_id": "wf8parametric",
  "parameterized_query_text": "SELECT ... WHERE ContainerName = @wafer_id ORDER BY MoveInTimestamp DESC",
  "query_hash": "0x5F46A1...32_BYTES",
  "row_count": 1,
  "snapshot_json": [
    {
      "operation": "BPI170",
      "metric_name": "CD_Average",
      "measurement": 0.05
    }
  ],
  "captured_at_utc": "2026-08-25T19:00:00.118Z"
}
```

For a static sample, `sample_source` is `static` and `data_source_id`,
`parameterized_query_text`, and `query_hash` are all NULL.

### `PCS.run_feedback_values`

```json
{
  "run_id": "r2r_8f31c9240a7e4fd5a3f6",
  "feedback_ordinal": 0,
  "feedback_name": "measured_cd",
  "data_type": "number",
  "number_value": 0.0498,
  "text_value": null,
  "boolean_value": null,
  "unit": null,
  "received_at_utc": "2026-08-25T20:10:00.000Z"
}
```

This table has rows only when the completed model declares and receives feedback inputs.

### `PCS.adaptive_states`

```json
{
  "family_id": "adaptive-cd-control",
  "parameter_name": "cd_offset",
  "scope_hash": "0xD3019B...32_BYTES",
  "scope_json": {
    "machine": "ETCH-01"
  },
  "current_value": 0.0125,
  "last_run_id": "r2r_8f31c9240a7e4fd5a3f6",
  "last_run_sequence": 1042,
  "updated_at_utc": "2026-08-25T20:10:00.010Z",
  "row_version": "0x00000000000007D5",
  "valid_from_utc": "2026-08-25T20:10:00.0100000Z",
  "valid_to_utc": "9999-12-31T23:59:59.9999999Z"
}
```

### `PCS.roles`

```json
{
  "role_name": "engineering_manager",
  "display_name": "Engineering Manager",
  "description": "Create models and review or approve submitted revisions.",
  "is_system": true,
  "created_by": "SYSTEM",
  "created_at_utc": "2026-08-25T16:00:00.000Z",
  "updated_by": "SYSTEM",
  "updated_at_utc": "2026-08-25T16:00:00.000Z",
  "row_version": "0x00000000000007D6"
}
```

### `PCS.users`

```json
{
  "user_id": "USR-000123",
  "employee_id": "000123",
  "login_name": "engineer1",
  "display_name": "Example Engineer",
  "email_address": "engineer1@example.internal",
  "role_name": "engineer",
  "status": "active",
  "identity_provider": "corporate_rest",
  "external_subject": "000123",
  "created_by": "USR-000001",
  "created_at_utc": "2026-08-25T16:30:00.000Z",
  "updated_by": "USR-000001",
  "updated_at_utc": "2026-08-25T16:30:00.000Z",
  "last_login_at_utc": "2026-08-25T18:45:00.000Z",
  "row_version": "0x00000000000007D7"
}
```

### `PCS.user_sessions`

```json
{
  "session_id_hash": "0x927CA4...32_BYTES",
  "user_id": "USR-000123",
  "created_at_utc": "2026-08-25T18:45:00.000Z",
  "expires_at_utc": "2026-08-26T02:45:00.000Z",
  "revoked_at_utc": null,
  "last_seen_at_utc": "2026-08-25T19:05:00.000Z",
  "client_address": "192.0.2.25",
  "user_agent_hash": "0x047B2E...32_BYTES"
}
```

### `PCS.input_definitions`

```json
{
  "input_name": "wafer_id",
  "display_name": "Wafer ID",
  "category": "external",
  "data_type": "string",
  "description": "Wafer identifier supplied by the calling application.",
  "status": "active",
  "is_system": false,
  "created_by": "USR-000001",
  "created_at_utc": "2026-08-25T16:35:00.000Z",
  "updated_by": "USR-000001",
  "updated_at_utc": "2026-08-25T16:35:00.000Z",
  "row_version": "0x00000000000007D8"
}
```

### `PCS.audit_events`

```json
{
  "event_id": "a59a9aab-3e74-4208-89da-10ff731f6ce1",
  "occurred_at_utc": "2026-08-25T18:15:00.1234567Z",
  "actor_type": "user",
  "actor_id": "USR-000100",
  "action_name": "model_revision_approved",
  "outcome": "success",
  "entity_type": "model_revision",
  "entity_id": "b5-rie802-recipe-selection-r1",
  "request_id": "req-20260825-00040",
  "run_id": null,
  "correlation_id": "f471e8f2-e347-4501-b006-b00a043f9503",
  "details_json": {
    "previous_status": "pending_approval",
    "new_status": "approved"
  },
  "client_address": "192.0.2.25"
}
```

### `PCS.operational_events`

```json
{
  "event_id": "6c903a0d-70cd-4d77-8623-1a7a015f63b5",
  "occurred_at_utc": "2026-08-25T19:00:00.1400000Z",
  "severity": "INFO",
  "event_name": "http_request_completed",
  "service_instance_id": "R2R-SERVER-01",
  "application_version": "2.0.0",
  "request_id": "req-20260825-00042",
  "trace_id": null,
  "run_id": "r2r_8f31c9240a7e4fd5a3f6",
  "actor_id": null,
  "http_method": "POST",
  "route": "/api/v1/runs/prepare",
  "status_code": 200,
  "duration_ms": 34,
  "response_bytes": 1250,
  "error_code": null,
  "error_message": null,
  "client_address": "192.0.2.40"
}
```

## SQL Server-managed companion objects

The following are created or managed automatically and are not normal application write targets:

| Object | Managed by | Purpose |
|---|---|---|
| `PCS.model_revisions_history` | System-versioning | Previous model-revision row versions. |
| `PCS.runs_history` | System-versioning | Previous run row versions. |
| `PCS.adaptive_states_history` | System-versioning | Previous adaptive-state row versions. |

Each later schema change must use a new numbered migration. Never edit a migration after it has run in a shared environment.

## Backup and retention baseline

- Full database backup: daily.
- Differential backup: every few hours, according to recovery objectives.
- Transaction-log backup: every 5–15 minutes.
- Restore test: scheduled and documented.
- Audit events: retain according to the company's audit and manufacturing-record policy.
- Run/value data: retain according to process traceability requirements.
- Operational events: use a shorter, capacity-based retention period.
- Audit archive: export according to the company's immutable-record and retention policy.

## Current implementation note

When `R2R_DATABASE_CONNECTION_STRING` is configured, SQL Server is authoritative for models, users,
sessions, input definitions, data sources, runs, normalized run values, adaptive state, audit events,
and operational events. The backend does not read or write `data/config.json` in this mode. JSON mode
remains available only for local development when the production connection string is absent.
