SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRANSACTION;

ALTER TABLE r2r.data_sources DROP COLUMN schema_name;

INSERT r2r.schema_migrations (version, description)
VALUES (5, N'Remove unused default schema from data-source connections');

COMMIT TRANSACTION;
