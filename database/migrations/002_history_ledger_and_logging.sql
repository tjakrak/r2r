SET NOCOUNT ON;
SET XACT_ABORT ON;

IF CAST(SERVERPROPERTY('ProductMajorVersion') AS int) < 16
    THROW 50001, 'Migration 002 requires SQL Server 2022 or later for ledger support.', 1;

BEGIN TRANSACTION;

ALTER TABLE r2r.model_revisions ADD
    valid_from_utc datetime2(7) GENERATED ALWAYS AS ROW START HIDDEN
        CONSTRAINT DF_model_revisions_valid_from DEFAULT SYSUTCDATETIME(),
    valid_to_utc datetime2(7) GENERATED ALWAYS AS ROW END HIDDEN
        CONSTRAINT DF_model_revisions_valid_to DEFAULT CONVERT(datetime2(7), '9999-12-31 23:59:59.9999999'),
    PERIOD FOR SYSTEM_TIME (valid_from_utc, valid_to_utc);

ALTER TABLE r2r.model_revisions
    SET (SYSTEM_VERSIONING = ON (
        HISTORY_TABLE = r2r.model_revisions_history,
        DATA_CONSISTENCY_CHECK = ON
    ));

ALTER TABLE r2r.runs ADD
    valid_from_utc datetime2(7) GENERATED ALWAYS AS ROW START HIDDEN
        CONSTRAINT DF_runs_valid_from DEFAULT SYSUTCDATETIME(),
    valid_to_utc datetime2(7) GENERATED ALWAYS AS ROW END HIDDEN
        CONSTRAINT DF_runs_valid_to DEFAULT CONVERT(datetime2(7), '9999-12-31 23:59:59.9999999'),
    PERIOD FOR SYSTEM_TIME (valid_from_utc, valid_to_utc);

ALTER TABLE r2r.runs
    SET (SYSTEM_VERSIONING = ON (
        HISTORY_TABLE = r2r.runs_history,
        DATA_CONSISTENCY_CHECK = ON
    ));

ALTER TABLE r2r.adaptive_states ADD
    valid_from_utc datetime2(7) GENERATED ALWAYS AS ROW START HIDDEN
        CONSTRAINT DF_adaptive_states_valid_from DEFAULT SYSUTCDATETIME(),
    valid_to_utc datetime2(7) GENERATED ALWAYS AS ROW END HIDDEN
        CONSTRAINT DF_adaptive_states_valid_to DEFAULT CONVERT(datetime2(7), '9999-12-31 23:59:59.9999999'),
    PERIOD FOR SYSTEM_TIME (valid_from_utc, valid_to_utc);

ALTER TABLE r2r.adaptive_states
    SET (SYSTEM_VERSIONING = ON (
        HISTORY_TABLE = r2r.adaptive_states_history,
        DATA_CONSISTENCY_CHECK = ON
    ));

CREATE TABLE r2r.audit_ledger_events (
    event_id                uniqueidentifier NOT NULL
        CONSTRAINT DF_audit_ledger_events_id DEFAULT NEWSEQUENTIALID(),
    occurred_at_utc         datetime2(7)     NOT NULL
        CONSTRAINT DF_audit_ledger_events_occurred DEFAULT SYSUTCDATETIME(),
    actor_type              varchar(20)      NOT NULL,
    actor_id                nvarchar(200)    NOT NULL,
    action_name             nvarchar(100)    NOT NULL,
    entity_type             nvarchar(100)    NOT NULL,
    entity_id               nvarchar(200)    NOT NULL,
    request_id              nvarchar(100)    NULL,
    run_id                  nvarchar(200)    NULL,
    correlation_id          uniqueidentifier NULL,
    details_json            nvarchar(max)    NULL,
    client_address          varchar(64)      NULL,
    legacy_audit_id         bigint           NULL,
    CONSTRAINT PK_audit_ledger_events PRIMARY KEY (event_id),
    CONSTRAINT CK_audit_ledger_events_actor_type
        CHECK (actor_type IN ('user', 'service', 'system')),
    CONSTRAINT CK_audit_ledger_events_details_json
        CHECK (details_json IS NULL OR ISJSON(details_json) = 1)
) WITH (
    LEDGER = ON,
    APPEND_ONLY = ON
);

CREATE INDEX IX_audit_ledger_events_entity
    ON r2r.audit_ledger_events (entity_type, entity_id, occurred_at_utc DESC);

CREATE INDEX IX_audit_ledger_events_actor
    ON r2r.audit_ledger_events (actor_id, occurred_at_utc DESC);

INSERT r2r.audit_ledger_events (
    occurred_at_utc,
    actor_type,
    actor_id,
    action_name,
    entity_type,
    entity_id,
    correlation_id,
    details_json,
    client_address,
    legacy_audit_id
)
SELECT
    occurred_at_utc,
    actor_type,
    actor_id,
    action_name,
    entity_type,
    entity_id,
    correlation_id,
    (
        SELECT
            JSON_QUERY(before_json) AS [before],
            JSON_QUERY(after_json) AS [after]
        FOR JSON PATH, WITHOUT_ARRAY_WRAPPER
    ),
    client_address,
    audit_id
FROM r2r.audit_events;

INSERT r2r.schema_migrations (version, description)
VALUES (2, N'Add temporal business history and append-only audit ledger');

COMMIT TRANSACTION;
