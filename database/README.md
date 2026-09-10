# SQL Server Production Database

Run migrations against a dedicated, empty R2R database using a deployment
identity with DDL permission. The application identity must not run migrations.

## Required database settings

The DBA should configure:

- SQL Server 2019 (15.x) or later
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

## Schema baseline

The deployed baseline uses the single `PCS` schema for business, identity,
execution, audit, and operational-event tables. The numbered migration files in
this prototype predate that consolidated baseline and must not be run against the
manually provisioned production database.

## Application permissions

Create the login according to company policy, then create a contained database
user. The initial application needs DML access but no DDL access:

```sql
CREATE USER [r2r_app] FOR LOGIN [r2r_app];

GRANT SELECT, INSERT, UPDATE, DELETE ON SCHEMA::PCS TO [r2r_app];
DENY ALTER, CONTROL ON SCHEMA::PCS TO [r2r_app];
```

Replace broad schema-level DML grants with stored-procedure `EXECUTE`
permissions later if required by the company's database security standard.

Passwords and access tokens are never stored in these tables. `secret_reference`
contains only the name of a deployment-managed secret.

## History and audit integrity

System-versioned history is enabled for model revisions, runs, and adaptive
states. Query historical state with `FOR SYSTEM_TIME`.

New audit events are inserted into `PCS.AUDIT_EVENTS`. SQL Server 2019 does not
support Ledger tables, so append-only behavior is enforced with database
permissions and the audit-table trigger.

Grant the runtime identity only the permissions it needs:

```sql
GRANT SELECT, INSERT ON OBJECT::PCS.audit_events TO [r2r_app];
DENY UPDATE, DELETE ON OBJECT::PCS.audit_events TO [r2r_app];
```

Operational application logs are emitted as one JSON object per line on stdout
and inserted into `PCS.OPERATIONAL_EVENTS`. The production runtime can forward stdout or
these events to an OpenTelemetry Collector, SIEM, or the organization's
centralized logging platform. Local files are not the authoritative log store.

The R2R application repository itself remains SQL Server-only. Rows in
`PCS.DATA_SOURCES` describe external calculation sources and may use
`sqlserver`, `postgres`, or `mysql`.
