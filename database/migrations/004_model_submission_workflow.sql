SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRANSACTION;

ALTER TABLE r2r.model_revisions DROP CONSTRAINT CK_model_revisions_approval;
ALTER TABLE r2r.model_revisions DROP CONSTRAINT CK_model_revisions_status;

ALTER TABLE r2r.model_revisions ADD
    submitted_by       nvarchar(200)  NULL,
    submitted_at_utc   datetime2(3)   NULL,
    rejected_by        nvarchar(200)  NULL,
    rejected_at_utc    datetime2(3)   NULL,
    rejection_reason   nvarchar(2000) NULL;

UPDATE r2r.model_revisions
SET
    submitted_by = COALESCE(approved_by, created_by),
    submitted_at_utc = COALESCE(approved_at_utc, created_at_utc)
WHERE status IN ('approved', 'superseded');

ALTER TABLE r2r.model_revisions ADD CONSTRAINT CK_model_revisions_status
    CHECK (status IN ('draft', 'pending_approval', 'approved', 'superseded'));

ALTER TABLE r2r.model_revisions ADD CONSTRAINT CK_model_revisions_submission
    CHECK (
        (status = 'draft')
        OR
        (status IN ('pending_approval', 'approved', 'superseded')
            AND submitted_by IS NOT NULL AND submitted_at_utc IS NOT NULL)
    );

ALTER TABLE r2r.model_revisions ADD CONSTRAINT CK_model_revisions_approval
    CHECK (
        (status IN ('draft', 'pending_approval')
            AND approved_by IS NULL AND approved_at_utc IS NULL)
        OR
        (status IN ('approved', 'superseded')
            AND approved_by IS NOT NULL AND approved_at_utc IS NOT NULL)
    );

ALTER TABLE r2r.model_revisions ADD CONSTRAINT CK_model_revisions_rejection
    CHECK (
        (rejected_by IS NULL AND rejected_at_utc IS NULL AND rejection_reason IS NULL)
        OR
        (rejected_by IS NOT NULL AND rejected_at_utc IS NOT NULL
            AND NULLIF(LTRIM(RTRIM(rejection_reason)), N'') IS NOT NULL)
    );

INSERT r2r.schema_migrations (version, description)
VALUES (4, N'Add draft submission, approval, and rejection workflow');

COMMIT TRANSACTION;
