SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRANSACTION;

IF NOT EXISTS (SELECT 1 FROM sys.schemas WHERE name = N'monitoring')
    EXEC(N'CREATE SCHEMA monitoring AUTHORIZATION dbo;');

CREATE TABLE r2r.roles (
    role_name          nvarchar(100)  NOT NULL,
    display_name       nvarchar(200)  NOT NULL,
    description        nvarchar(1000) NOT NULL,
    is_system          bit            NOT NULL
        CONSTRAINT DF_roles_is_system DEFAULT (0),
    created_by         nvarchar(200)  NOT NULL,
    created_at_utc     datetime2(3)   NOT NULL
        CONSTRAINT DF_roles_created_at DEFAULT SYSUTCDATETIME(),
    updated_by         nvarchar(200)  NOT NULL,
    updated_at_utc     datetime2(3)   NOT NULL
        CONSTRAINT DF_roles_updated_at DEFAULT SYSUTCDATETIME(),
    row_version        rowversion     NOT NULL,
    CONSTRAINT PK_roles PRIMARY KEY (role_name)
);

INSERT r2r.roles (role_name, display_name, description, is_system, created_by, updated_by)
VALUES
    (N'admin', N'Administrator', N'Manage data sources, input registry, model families, users, roles, and exceptional approvals.', 1, N'system', N'system'),
    (N'engineering_manager', N'Engineering Manager', N'Review, simulate, approve, and reject submitted model revisions.', 1, N'system', N'system'),
    (N'engineer', N'Engineer', N'Create, edit, simulate, and submit model drafts.', 1, N'system', N'system'),
    (N'viewer', N'Viewer', N'Read-only access to models, run history, and documentation.', 1, N'system', N'system');

CREATE TABLE r2r.users (
    user_id             nvarchar(200) NOT NULL,
    login_name          nvarchar(200) NOT NULL,
    display_name        nvarchar(200) NOT NULL,
    email_address       nvarchar(320) NULL,
    role_name           nvarchar(100) NOT NULL,
    status              varchar(20)   NOT NULL
        CONSTRAINT DF_users_status DEFAULT ('active'),
    identity_provider   nvarchar(100) NOT NULL,
    external_subject    nvarchar(300) NOT NULL,
    created_by          nvarchar(200) NOT NULL,
    created_at_utc      datetime2(3)  NOT NULL
        CONSTRAINT DF_users_created_at DEFAULT SYSUTCDATETIME(),
    updated_by          nvarchar(200) NOT NULL,
    updated_at_utc      datetime2(3)  NOT NULL
        CONSTRAINT DF_users_updated_at DEFAULT SYSUTCDATETIME(),
    last_login_at_utc   datetime2(3)  NULL,
    row_version         rowversion    NOT NULL,
    CONSTRAINT PK_users PRIMARY KEY (user_id),
    CONSTRAINT UQ_users_login UNIQUE (login_name),
    CONSTRAINT UQ_users_external_identity UNIQUE (identity_provider, external_subject),
    CONSTRAINT FK_users_role FOREIGN KEY (role_name) REFERENCES r2r.roles (role_name),
    CONSTRAINT CK_users_status CHECK (status IN ('active', 'disabled', 'locked'))
);

CREATE INDEX IX_users_role_status ON r2r.users (role_name, status) INCLUDE (login_name, display_name);

CREATE TABLE r2r.user_sessions (
    session_id_hash     binary(32)    NOT NULL,
    user_id             nvarchar(200) NOT NULL,
    created_at_utc      datetime2(3)  NOT NULL
        CONSTRAINT DF_user_sessions_created_at DEFAULT SYSUTCDATETIME(),
    expires_at_utc      datetime2(3)  NOT NULL,
    revoked_at_utc      datetime2(3)  NULL,
    last_seen_at_utc    datetime2(3)  NOT NULL,
    client_address      varchar(64)   NULL,
    user_agent_hash     binary(32)    NULL,
    CONSTRAINT PK_user_sessions PRIMARY KEY (session_id_hash),
    CONSTRAINT FK_user_sessions_user FOREIGN KEY (user_id) REFERENCES r2r.users (user_id),
    CONSTRAINT CK_user_sessions_expiry CHECK (expires_at_utc > created_at_utc)
);

CREATE INDEX IX_user_sessions_user ON r2r.user_sessions (user_id, expires_at_utc DESC);
CREATE INDEX IX_user_sessions_expiry ON r2r.user_sessions (expires_at_utc) WHERE revoked_at_utc IS NULL;

CREATE TABLE r2r.input_definitions (
    input_name          nvarchar(128)  NOT NULL,
    display_name        nvarchar(200)  NOT NULL,
    category            varchar(20)    NOT NULL,
    data_type           varchar(20)    NOT NULL,
    description         nvarchar(1000) NULL,
    status              varchar(20)    NOT NULL
        CONSTRAINT DF_input_definitions_status DEFAULT ('active'),
    is_system           bit            NOT NULL
        CONSTRAINT DF_input_definitions_is_system DEFAULT (0),
    created_by          nvarchar(200)  NOT NULL,
    created_at_utc      datetime2(3)   NOT NULL
        CONSTRAINT DF_input_definitions_created_at DEFAULT SYSUTCDATETIME(),
    updated_by          nvarchar(200)  NOT NULL,
    updated_at_utc      datetime2(3)   NOT NULL
        CONSTRAINT DF_input_definitions_updated_at DEFAULT SYSUTCDATETIME(),
    row_version         rowversion     NOT NULL,
    CONSTRAINT PK_input_definitions PRIMARY KEY (input_name),
    CONSTRAINT CK_input_definitions_category CHECK (category IN ('index', 'external')),
    CONSTRAINT CK_input_definitions_type CHECK (data_type IN ('number', 'string', 'boolean')),
    CONSTRAINT CK_input_definitions_status CHECK (status IN ('active', 'deprecated'))
);

INSERT r2r.input_definitions
    (input_name, display_name, category, data_type, description, status, is_system, created_by, updated_by)
VALUES
    (N'machine', N'Machine', 'index', 'string', N'Required routing machine index.', 'active', 1, N'system', N'system'),
    (N'action', N'Action', 'index', 'string', N'Required routing action index.', 'active', 1, N'system', N'system'),
    (N'material', N'Material', 'index', 'string', N'Required routing material index.', 'active', 1, N'system', N'system');

ALTER TABLE r2r.runs ADD
    request_id       nvarchar(100)  NULL,
    failure_code     nvarchar(100)  NULL,
    failure_message  nvarchar(2000) NULL;

CREATE INDEX IX_runs_request ON r2r.runs (request_id) WHERE request_id IS NOT NULL;

CREATE TABLE r2r.run_parameter_values (
    run_id              nvarchar(200) NOT NULL,
    parameter_ordinal   int           NOT NULL,
    parameter_name      nvarchar(128) NOT NULL,
    parameter_kind      varchar(20)   NOT NULL,
    source_type         varchar(30)   NOT NULL,
    data_type           varchar(10)   NOT NULL,
    number_value        float         NULL,
    text_value          nvarchar(max) NULL,
    boolean_value       bit           NULL,
    unit                nvarchar(50)  NULL,
    protection_class    varchar(20)   NOT NULL
        CONSTRAINT DF_run_parameter_values_protection DEFAULT ('normal'),
    is_redacted         bit           NOT NULL
        CONSTRAINT DF_run_parameter_values_redacted DEFAULT (0),
    calculated_at_utc   datetime2(3)  NOT NULL
        CONSTRAINT DF_run_parameter_values_calculated DEFAULT SYSUTCDATETIME(),
    CONSTRAINT PK_run_parameter_values PRIMARY KEY (run_id, parameter_ordinal),
    CONSTRAINT FK_run_parameter_values_run FOREIGN KEY (run_id) REFERENCES r2r.runs (run_id) ON DELETE CASCADE,
    CONSTRAINT CK_run_parameter_values_ordinal CHECK (parameter_ordinal >= 0),
    CONSTRAINT CK_run_parameter_values_kind CHECK (parameter_kind IN ('index', 'external', 'calculated')),
    CONSTRAINT CK_run_parameter_values_type CHECK (data_type IN ('number', 'string', 'boolean')),
    CONSTRAINT CK_run_parameter_values_protection CHECK (protection_class IN ('normal', 'masked', 'secret')),
    CONSTRAINT CK_run_parameter_values_value CHECK (
        (protection_class = 'secret' AND is_redacted = 1
            AND number_value IS NULL AND text_value IS NULL AND boolean_value IS NULL)
        OR
        (protection_class <> 'secret' AND
            ((data_type = 'number' AND number_value IS NOT NULL AND text_value IS NULL AND boolean_value IS NULL)
            OR (data_type = 'string' AND number_value IS NULL AND text_value IS NOT NULL AND boolean_value IS NULL)
            OR (data_type = 'boolean' AND number_value IS NULL AND text_value IS NULL AND boolean_value IS NOT NULL)))
    )
);

CREATE INDEX IX_run_parameter_values_name ON r2r.run_parameter_values (parameter_name, run_id);

CREATE TABLE r2r.run_data_samples (
    run_id              nvarchar(200) NOT NULL,
    sample_name         nvarchar(128) NOT NULL,
    data_source_id      nvarchar(100) NOT NULL,
    query_hash          binary(32)    NOT NULL,
    row_count           int           NOT NULL,
    snapshot_json       nvarchar(max) NULL,
    protection_class    varchar(20)   NOT NULL
        CONSTRAINT DF_run_data_samples_protection DEFAULT ('normal'),
    is_redacted         bit           NOT NULL
        CONSTRAINT DF_run_data_samples_redacted DEFAULT (0),
    captured_at_utc     datetime2(3)  NOT NULL
        CONSTRAINT DF_run_data_samples_captured DEFAULT SYSUTCDATETIME(),
    CONSTRAINT PK_run_data_samples PRIMARY KEY (run_id, sample_name),
    CONSTRAINT FK_run_data_samples_run FOREIGN KEY (run_id) REFERENCES r2r.runs (run_id) ON DELETE CASCADE,
    CONSTRAINT FK_run_data_samples_source FOREIGN KEY (data_source_id) REFERENCES r2r.data_sources (source_id),
    CONSTRAINT CK_run_data_samples_rows CHECK (row_count >= 0),
    CONSTRAINT CK_run_data_samples_protection CHECK (protection_class IN ('normal', 'masked', 'secret')),
    CONSTRAINT CK_run_data_samples_json CHECK (
        (is_redacted = 1 AND snapshot_json IS NULL)
        OR (is_redacted = 0 AND snapshot_json IS NOT NULL AND ISJSON(snapshot_json) = 1)
    )
);

CREATE TABLE r2r.run_feedback_values (
    run_id              nvarchar(200) NOT NULL,
    feedback_ordinal    int           NOT NULL,
    feedback_name       nvarchar(128) NOT NULL,
    data_type           varchar(10)   NOT NULL,
    number_value        float         NULL,
    text_value          nvarchar(max) NULL,
    boolean_value       bit           NULL,
    unit                nvarchar(50)  NULL,
    protection_class    varchar(20)   NOT NULL
        CONSTRAINT DF_run_feedback_values_protection DEFAULT ('normal'),
    is_redacted         bit           NOT NULL
        CONSTRAINT DF_run_feedback_values_redacted DEFAULT (0),
    received_at_utc     datetime2(3)  NOT NULL
        CONSTRAINT DF_run_feedback_values_received DEFAULT SYSUTCDATETIME(),
    CONSTRAINT PK_run_feedback_values PRIMARY KEY (run_id, feedback_ordinal),
    CONSTRAINT FK_run_feedback_values_run FOREIGN KEY (run_id) REFERENCES r2r.runs (run_id) ON DELETE CASCADE,
    CONSTRAINT CK_run_feedback_values_ordinal CHECK (feedback_ordinal >= 0),
    CONSTRAINT CK_run_feedback_values_type CHECK (data_type IN ('number', 'string', 'boolean')),
    CONSTRAINT CK_run_feedback_values_protection CHECK (protection_class IN ('normal', 'masked', 'secret')),
    CONSTRAINT CK_run_feedback_values_value CHECK (
        (protection_class = 'secret' AND is_redacted = 1
            AND number_value IS NULL AND text_value IS NULL AND boolean_value IS NULL)
        OR
        (protection_class <> 'secret' AND
            ((data_type = 'number' AND number_value IS NOT NULL AND text_value IS NULL AND boolean_value IS NULL)
            OR (data_type = 'string' AND number_value IS NULL AND text_value IS NOT NULL AND boolean_value IS NULL)
            OR (data_type = 'boolean' AND number_value IS NULL AND text_value IS NULL AND boolean_value IS NOT NULL)))
    )
);

CREATE TABLE monitoring.operational_events (
    event_id              uniqueidentifier NOT NULL
        CONSTRAINT DF_operational_events_id DEFAULT NEWSEQUENTIALID(),
    occurred_at_utc       datetime2(7)     NOT NULL
        CONSTRAINT DF_operational_events_occurred DEFAULT SYSUTCDATETIME(),
    severity              varchar(10)      NOT NULL,
    event_name            nvarchar(100)    NOT NULL,
    service_instance_id   nvarchar(200)    NOT NULL,
    application_version   nvarchar(50)     NOT NULL,
    request_id            nvarchar(100)    NULL,
    trace_id              varchar(64)      NULL,
    run_id                nvarchar(200)    NULL,
    actor_id              nvarchar(200)    NULL,
    http_method           varchar(10)      NULL,
    route                 nvarchar(500)    NULL,
    status_code           int              NULL,
    duration_ms           bigint           NULL,
    response_bytes        bigint           NULL,
    error_code            nvarchar(100)    NULL,
    error_message         nvarchar(2000)   NULL,
    client_address        varchar(64)      NULL,
    CONSTRAINT PK_operational_events PRIMARY KEY (event_id),
    CONSTRAINT CK_operational_events_severity CHECK (severity IN ('INFO', 'WARN', 'ERROR', 'FATAL')),
    CONSTRAINT CK_operational_events_status CHECK (status_code IS NULL OR status_code BETWEEN 100 AND 599),
    CONSTRAINT CK_operational_events_duration CHECK (duration_ms IS NULL OR duration_ms >= 0),
    CONSTRAINT CK_operational_events_bytes CHECK (response_bytes IS NULL OR response_bytes >= 0)
);

CREATE INDEX IX_operational_events_time ON monitoring.operational_events (occurred_at_utc DESC);
CREATE INDEX IX_operational_events_request ON monitoring.operational_events (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX IX_operational_events_run ON monitoring.operational_events (run_id) WHERE run_id IS NOT NULL;
CREATE INDEX IX_operational_events_severity_time
    ON monitoring.operational_events (severity, occurred_at_utc DESC);

INSERT r2r.schema_migrations (version, description)
VALUES (6, N'Add production identity, role management, input registry, normalized execution values, and operational events');

COMMIT TRANSACTION;
