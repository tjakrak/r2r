SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRANSACTION;

ALTER TABLE r2r.users ADD
    employee_id nvarchar(100) NULL;

CREATE UNIQUE INDEX UQ_users_employee_id
    ON r2r.users (employee_id)
    WHERE employee_id IS NOT NULL;

UPDATE r2r.roles
SET description = CASE role_name
    WHEN N'admin' THEN N'Manage data sources, input registry, model families, users, and roles; approve any pending model revision.'
    WHEN N'engineering_manager' THEN N'Create, edit, simulate, and submit models; review and approve or reject revisions submitted by direct reports.'
    ELSE description
END,
updated_by = N'system',
updated_at_utc = SYSUTCDATETIME()
WHERE role_name IN (N'admin', N'engineering_manager');

INSERT r2r.schema_migrations (version, description)
VALUES (7, N'Add corporate employee identity for registration and supervisor approval');

COMMIT TRANSACTION;
