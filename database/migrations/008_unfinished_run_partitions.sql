SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRANSACTION;

ALTER TABLE r2r.runs ADD
    partition_hash binary(32) NULL,
    partition_key  nvarchar(1000) NULL,
    partition_json nvarchar(max) NULL;

ALTER TABLE r2r.runs ADD CONSTRAINT CK_runs_partition_json
    CHECK (partition_json IS NULL OR ISJSON(partition_json) = 1);

CREATE INDEX IX_runs_unfinished_partition
    ON r2r.runs (family_id, partition_hash, status, prepared_at_utc)
    INCLUDE (run_id, run_sequence, partition_key)
    WHERE status = 'prepared';

INSERT r2r.schema_migrations (version, description)
VALUES (8, N'Add configurable unfinished-run partitions');

COMMIT TRANSACTION;
