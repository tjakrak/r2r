# SQL Server Production Database

Run migrations against a dedicated, empty R2R database using a deployment
identity with DDL permission. The application identity must not run migrations.

## Required database settings

The DBA should configure:

- SQL Server 2022 or later
- compatibility level approved by the DBA team
- `READ_COMMITTED_SNAPSHOT ON`
- full recovery model
- TLS-encrypted connections with a trusted server certificate
- regular full, differential, and transaction-log backups
- an Availability Group listener when high availability is required
- UTC for every application-supplied timestamp

Example DBA commands, replacing the database name:

```sql
ALTER DATABASE [R2RControl] SET RECOVERY FULL;
ALTER DATABASE [R2RControl] SET READ_COMMITTED_SNAPSHOT ON WITH ROLLBACK IMMEDIATE;
ALTER DATABASE [R2RControl] SET AUTO_CLOSE OFF;
ALTER DATABASE [R2RControl] SET AUTO_SHRINK OFF;
```

`READ_COMMITTED_SNAPSHOT` changes concurrency behavior and the
`WITH ROLLBACK IMMEDIATE` clause disconnects active sessions. Apply it only
during the planned database provisioning window.

## Migration order

Execute:

```text
migrations/001_initial_schema.sql
migrations/002_history_ledger_and_logging.sql
migrations/003_boolean_outputs_and_external_engines.sql
migrations/004_model_submission_workflow.sql
migrations/005_remove_default_schema.sql
migrations/006_production_identity_and_execution_tables.sql
migrations/007_employee_identity_and_supervisor_approval.sql
migrations/008_unfinished_run_partitions.sql
```

Record every later schema change as a new numbered migration. Never edit a
migration after it has run in a shared environment.

Migration 004 adds the draft → submitted → approved/rejected workflow. Migration
005 makes data-source connections database-scoped so model SQL can reference
multiple schemas. Migration 006 adds production roles (including engineering
manager), users and sessions, the input registry, normalized execution-value
tables, and the separate operational-event schema.

Migration 007 adds the corporate employee identifier used to join an R2R user
to the company directory (`IDNUM`) and enforce direct-supervisor approval.

Migration 008 records the configured partition identity used to enforce
unfinished-run limits independently for each selected index/input combination.

## Application permissions

Create the login according to company policy, then create a contained database
user. The initial application needs DML access but no DDL access:

```sql
CREATE USER [r2r_app] FOR LOGIN [r2r_app];

GRANT SELECT, INSERT, UPDATE, DELETE ON SCHEMA::r2r TO [r2r_app];
DENY ALTER, CONTROL ON SCHEMA::r2r TO [r2r_app];
```

Replace broad schema-level DML grants with stored-procedure `EXECUTE`
permissions later if required by the company's database security standard.

Passwords and access tokens are never stored in these tables. `secret_reference`
contains only the name of a deployment-managed secret.

## History and audit integrity

Migration 002 enables system-versioned history for model revisions, runs, and
adaptive states. Query historical state with `FOR SYSTEM_TIME`.

New audit events must be inserted into `r2r.audit_ledger_events`. It is an
append-only SQL Server ledger table; the application must never use the legacy
`r2r.audit_events` table after migration. Configure database digests to be
exported to storage controlled separately from the database administrators and
run scheduled ledger verification.

Grant the runtime identity only the permissions it needs:

```sql
GRANT SELECT, INSERT ON OBJECT::r2r.audit_ledger_events TO [r2r_app];
DENY UPDATE, DELETE ON OBJECT::r2r.audit_ledger_events TO [r2r_app];
```

Operational application logs are emitted as one JSON object per line on stdout.
They may initially be retained for a short period in
`monitoring.operational_events`. The production runtime can forward stdout or
these events to an OpenTelemetry Collector, SIEM, or the organization's
centralized logging platform. Local files are not the authoritative log store.

The R2R application repository itself remains SQL Server-only. Rows in
`r2r.data_sources` describe external calculation sources and may use
`sqlserver`, `postgres`, or `mysql` after migration 003.
