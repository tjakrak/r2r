SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRANSACTION;

ALTER TABLE r2r.run_outputs DROP CONSTRAINT CK_run_outputs_value;
ALTER TABLE r2r.run_outputs DROP CONSTRAINT CK_run_outputs_type;

ALTER TABLE r2r.run_outputs ADD boolean_value bit NULL;

ALTER TABLE r2r.run_outputs ADD CONSTRAINT CK_run_outputs_type
    CHECK (data_type IN ('number', 'string', 'boolean'));

ALTER TABLE r2r.run_outputs ADD CONSTRAINT CK_run_outputs_value CHECK (
    (data_type = 'number'
        AND number_value IS NOT NULL AND text_value IS NULL AND boolean_value IS NULL)
    OR
    (data_type = 'string'
        AND number_value IS NULL AND text_value IS NOT NULL AND boolean_value IS NULL)
    OR
    (data_type = 'boolean'
        AND number_value IS NULL AND text_value IS NULL AND boolean_value IS NOT NULL)
);

ALTER TABLE r2r.data_sources DROP CONSTRAINT CK_data_sources_engine;
ALTER TABLE r2r.data_sources ADD CONSTRAINT CK_data_sources_engine
    CHECK (engine IN ('sqlserver', 'postgres', 'mysql'));

ALTER TABLE r2r.data_sources DROP CONSTRAINT CK_data_sources_tls;
ALTER TABLE r2r.data_sources ADD CONSTRAINT CK_data_sources_tls
    CHECK (tls_mode IN ('require', 'strict', 'verify-full', 'disable', 'true'));

INSERT r2r.schema_migrations (version, description)
VALUES (3, N'Add boolean run outputs and external PostgreSQL/MySQL sources');

COMMIT TRANSACTION;
