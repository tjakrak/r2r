SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRANSACTION;

IF NOT EXISTS (SELECT 1 FROM sys.schemas WHERE name = N'r2r')
    EXEC(N'CREATE SCHEMA r2r AUTHORIZATION dbo;');

CREATE TABLE r2r.schema_migrations (
    version             int             NOT NULL,
    description         nvarchar(200)   NOT NULL,
    applied_at_utc      datetime2(3)    NOT NULL
        CONSTRAINT DF_schema_migrations_applied_at DEFAULT SYSUTCDATETIME(),
    CONSTRAINT PK_schema_migrations PRIMARY KEY (version)
);

CREATE TABLE r2r.model_families (
    family_id           nvarchar(100)   NOT NULL,
    name                nvarchar(200)   NOT NULL,
    description         nvarchar(1000)  NULL,
    created_by          nvarchar(200)   NOT NULL,
    created_at_utc      datetime2(3)    NOT NULL
        CONSTRAINT DF_model_families_created_at DEFAULT SYSUTCDATETIME(),
    row_version         rowversion      NOT NULL,
    CONSTRAINT PK_model_families PRIMARY KEY (family_id)
);

CREATE TABLE r2r.model_revisions (
    model_id            nvarchar(150)   NOT NULL,
    family_id           nvarchar(100)   NOT NULL,
    revision            int             NOT NULL,
    status              varchar(20)     NOT NULL,
    enabled             bit             NOT NULL
        CONSTRAINT DF_model_revisions_enabled DEFAULT (0),
    definition_json     nvarchar(max)   NOT NULL,
    change_note         nvarchar(2000)  NOT NULL,
    owner_name          nvarchar(200)   NOT NULL,
    created_by          nvarchar(200)   NOT NULL,
    created_at_utc      datetime2(3)    NOT NULL
        CONSTRAINT DF_model_revisions_created_at DEFAULT SYSUTCDATETIME(),
    approved_by         nvarchar(200)   NULL,
    approved_at_utc     datetime2(3)    NULL,
    row_version         rowversion      NOT NULL,
    CONSTRAINT PK_model_revisions PRIMARY KEY (model_id),
    CONSTRAINT FK_model_revisions_family FOREIGN KEY (family_id)
        REFERENCES r2r.model_families (family_id),
    CONSTRAINT UQ_model_revisions_family_revision UNIQUE (family_id, revision),
    CONSTRAINT CK_model_revisions_revision CHECK (revision > 0),
    CONSTRAINT CK_model_revisions_status
        CHECK (status IN ('draft', 'approved', 'superseded')),
    CONSTRAINT CK_model_revisions_definition_json CHECK (ISJSON(definition_json) = 1),
    CONSTRAINT CK_model_revisions_approval CHECK (
        (status = 'draft' AND approved_by IS NULL AND approved_at_utc IS NULL)
        OR
        (status IN ('approved', 'superseded')
            AND approved_by IS NOT NULL AND approved_at_utc IS NOT NULL)
    )
);

CREATE UNIQUE INDEX UX_model_revisions_one_active
    ON r2r.model_revisions (family_id)
    WHERE status = 'approved' AND enabled = 1;

CREATE INDEX IX_model_revisions_history
    ON r2r.model_revisions (family_id, revision DESC);

CREATE TABLE r2r.data_sources (
    source_id               nvarchar(100)   NOT NULL,
    name                    nvarchar(200)   NOT NULL,
    source_type             varchar(30)     NOT NULL,
    engine                  varchar(20)     NOT NULL,
    host_name               nvarchar(255)   NOT NULL,
    port                    int             NOT NULL,
    database_name           nvarchar(128)   NOT NULL,
    schema_name             nvarchar(128)   NULL,
    login_name              nvarchar(128)   NULL,
    secret_reference        nvarchar(200)   NULL,
    tls_mode                varchar(20)     NOT NULL,
    timeout_seconds         int             NOT NULL,
    location_name           nvarchar(200)   NULL,
    enabled                 bit             NOT NULL
        CONSTRAINT DF_data_sources_enabled DEFAULT (1),
    last_test_status        varchar(20)     NULL,
    last_test_message       nvarchar(2000)  NULL,
    last_test_at_utc        datetime2(3)    NULL,
    last_test_ms            bigint          NULL,
    created_at_utc          datetime2(3)    NOT NULL
        CONSTRAINT DF_data_sources_created_at DEFAULT SYSUTCDATETIME(),
    updated_at_utc          datetime2(3)    NOT NULL
        CONSTRAINT DF_data_sources_updated_at DEFAULT SYSUTCDATETIME(),
    row_version             rowversion      NOT NULL,
    CONSTRAINT PK_data_sources PRIMARY KEY (source_id),
    CONSTRAINT UQ_data_sources_name UNIQUE (name),
    CONSTRAINT CK_data_sources_engine CHECK (engine = 'sqlserver'),
    CONSTRAINT CK_data_sources_port CHECK (port BETWEEN 1 AND 65535),
    CONSTRAINT CK_data_sources_timeout CHECK (timeout_seconds BETWEEN 1 AND 300),
    CONSTRAINT CK_data_sources_tls CHECK (tls_mode IN ('require', 'strict')),
    CONSTRAINT CK_data_sources_test_status
        CHECK (last_test_status IS NULL OR last_test_status IN ('success', 'failed'))
);

CREATE TABLE r2r.runs (
    run_id                  nvarchar(200)   NOT NULL,
    run_sequence            bigint          IDENTITY(1,1) NOT NULL,
    family_id               nvarchar(100)   NOT NULL,
    model_id                nvarchar(150)   NOT NULL,
    model_revision          int             NOT NULL,
    status                  varchar(30)     NOT NULL,
    disposition             varchar(30)     NULL,
    prepared_at_utc         datetime2(3)    NOT NULL
        CONSTRAINT DF_runs_prepared_at DEFAULT SYSUTCDATETIME(),
    completed_at_utc        datetime2(3)    NULL,
    context_json            nvarchar(max)   NOT NULL,
    parameters_json         nvarchar(max)   NOT NULL,
    feedback_json           nvarchar(max)   NULL,
    adaptive_updates_json   nvarchar(max)   NULL,
    correlation_id          uniqueidentifier NOT NULL
        CONSTRAINT DF_runs_correlation_id DEFAULT NEWSEQUENTIALID(),
    row_version             rowversion      NOT NULL,
    CONSTRAINT PK_runs PRIMARY KEY (run_id),
    CONSTRAINT UQ_runs_sequence UNIQUE (run_sequence),
    CONSTRAINT FK_runs_family FOREIGN KEY (family_id)
        REFERENCES r2r.model_families (family_id),
    CONSTRAINT FK_runs_model FOREIGN KEY (model_id)
        REFERENCES r2r.model_revisions (model_id),
    CONSTRAINT CK_runs_model_revision CHECK (model_revision > 0),
    CONSTRAINT CK_runs_status CHECK (
        status IN ('prepared', 'completed', 'run_failed', 'run_cancelled')
    ),
    CONSTRAINT CK_runs_context_json CHECK (ISJSON(context_json) = 1),
    CONSTRAINT CK_runs_parameters_json CHECK (ISJSON(parameters_json) = 1),
    CONSTRAINT CK_runs_feedback_json
        CHECK (feedback_json IS NULL OR ISJSON(feedback_json) = 1),
    CONSTRAINT CK_runs_adaptive_json
        CHECK (adaptive_updates_json IS NULL OR ISJSON(adaptive_updates_json) = 1),
    CONSTRAINT CK_runs_completion_time CHECK (
        (status = 'prepared' AND completed_at_utc IS NULL)
        OR
        (status <> 'prepared' AND completed_at_utc IS NOT NULL)
    )
);

CREATE INDEX IX_runs_family_prepared
    ON r2r.runs (family_id, prepared_at_utc DESC)
    INCLUDE (status, model_id, model_revision, disposition);

CREATE INDEX IX_runs_unfinished
    ON r2r.runs (family_id, status, prepared_at_utc)
    INCLUDE (run_sequence, context_json)
    WHERE status = 'prepared';

CREATE TABLE r2r.run_outputs (
    run_id                  nvarchar(200)   NOT NULL,
    output_ordinal          int             NOT NULL,
    parameter_name          nvarchar(128)   NOT NULL,
    data_type               varchar(10)     NOT NULL,
    number_value            float           NULL,
    text_value              nvarchar(max)   NULL,
    calculated_number_value float           NULL,
    unit                    nvarchar(50)    NULL,
    lsl                     float           NULL,
    usl                     float           NULL,
    out_of_limit            bit             NOT NULL
        CONSTRAINT DF_run_outputs_out_of_limit DEFAULT (0),
    limit_action            varchar(20)     NULL,
    CONSTRAINT PK_run_outputs PRIMARY KEY (run_id, output_ordinal),
    CONSTRAINT FK_run_outputs_run FOREIGN KEY (run_id)
        REFERENCES r2r.runs (run_id) ON DELETE CASCADE,
    CONSTRAINT CK_run_outputs_ordinal CHECK (output_ordinal >= 0),
    CONSTRAINT CK_run_outputs_type CHECK (data_type IN ('number', 'string')),
    CONSTRAINT CK_run_outputs_value CHECK (
        (data_type = 'number' AND number_value IS NOT NULL AND text_value IS NULL)
        OR
        (data_type = 'string' AND number_value IS NULL AND text_value IS NOT NULL)
    ),
    CONSTRAINT CK_run_outputs_limits CHECK (lsl IS NULL OR usl IS NULL OR lsl <= usl),
    CONSTRAINT CK_run_outputs_action CHECK (
        limit_action IS NULL OR limit_action IN ('warn', 'clamp', 'reject', 'fallback')
    )
);

CREATE INDEX IX_run_outputs_parameter
    ON r2r.run_outputs (parameter_name, run_id)
    INCLUDE (number_value, out_of_limit, limit_action);

CREATE TABLE r2r.adaptive_states (
    family_id               nvarchar(100)   NOT NULL,
    parameter_name          nvarchar(128)   NOT NULL,
    scope_hash              binary(32)      NOT NULL,
    scope_key               nvarchar(1000)  NOT NULL,
    scope_json              nvarchar(max)   NOT NULL,
    current_value           float           NOT NULL,
    last_run_id             nvarchar(200)   NULL,
    last_run_sequence       bigint          NOT NULL
        CONSTRAINT DF_adaptive_states_sequence DEFAULT (0),
    updated_at_utc          datetime2(3)    NOT NULL
        CONSTRAINT DF_adaptive_states_updated_at DEFAULT SYSUTCDATETIME(),
    row_version             rowversion      NOT NULL,
    CONSTRAINT PK_adaptive_states
        PRIMARY KEY (family_id, parameter_name, scope_hash),
    CONSTRAINT FK_adaptive_states_family FOREIGN KEY (family_id)
        REFERENCES r2r.model_families (family_id),
    CONSTRAINT FK_adaptive_states_run FOREIGN KEY (last_run_id)
        REFERENCES r2r.runs (run_id),
    CONSTRAINT CK_adaptive_states_scope_json CHECK (ISJSON(scope_json) = 1),
    CONSTRAINT CK_adaptive_states_sequence CHECK (last_run_sequence >= 0)
);

CREATE INDEX IX_adaptive_states_last_run
    ON r2r.adaptive_states (last_run_sequence DESC)
    INCLUDE (family_id, parameter_name, current_value, updated_at_utc);

CREATE TABLE r2r.audit_events (
    audit_id                bigint          IDENTITY(1,1) NOT NULL,
    occurred_at_utc         datetime2(3)    NOT NULL
        CONSTRAINT DF_audit_events_occurred_at DEFAULT SYSUTCDATETIME(),
    actor_type              varchar(20)     NOT NULL,
    actor_id                nvarchar(200)   NOT NULL,
    action_name             nvarchar(100)   NOT NULL,
    entity_type             nvarchar(100)   NOT NULL,
    entity_id               nvarchar(200)   NOT NULL,
    correlation_id          uniqueidentifier NULL,
    before_json             nvarchar(max)   NULL,
    after_json              nvarchar(max)   NULL,
    client_address          varchar(64)     NULL,
    CONSTRAINT PK_audit_events PRIMARY KEY (audit_id),
    CONSTRAINT CK_audit_events_actor_type
        CHECK (actor_type IN ('user', 'service', 'system')),
    CONSTRAINT CK_audit_events_before_json
        CHECK (before_json IS NULL OR ISJSON(before_json) = 1),
    CONSTRAINT CK_audit_events_after_json
        CHECK (after_json IS NULL OR ISJSON(after_json) = 1)
);

CREATE INDEX IX_audit_events_entity
    ON r2r.audit_events (entity_type, entity_id, occurred_at_utc DESC);

CREATE INDEX IX_audit_events_actor
    ON r2r.audit_events (actor_id, occurred_at_utc DESC);

INSERT r2r.schema_migrations (version, description)
VALUES (1, N'Initial R2R production schema');

COMMIT TRANSACTION;
