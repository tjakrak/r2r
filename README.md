# R2R Manufacturing Control

R2R selects and executes versioned manufacturing control models. An external application submits process indexes, external context, SQL-backed parameter values, and tabular data samples. R2R finds the most-specific active revision, resolves its declared parameters in order, and calculates next-run outputs.

## Technology

- React 18 and TypeScript frontend
- Vite 4 build tooling
- Go 1.19 HTTP API and model execution engine
- JSON persistence in `data/config.json` for the prototype

## Run

```bash
npm install
npm run build
cp data/config.example.json data/config.json
npm start
```

On Windows PowerShell, use `Copy-Item data/config.example.json data/config.json` instead. The local `data/config.json` file is intentionally excluded from Git because the running prototype writes users, models, runs, and source metadata to it.

Open [http://localhost:4173](http://localhost:4173). The web interface manages models and revisions; model execution is performed by external applications through the API.

### Development login

The prototype employee directory contains `IDNUM`, `LOGINID`, `EMAIL`, and `SUPID`. These employees are pre-registered in R2R for development:

- `viewer` / `viewer`: read-only access
- `engineer` / `engineer`: create models and revisions
- `manager` / `manager`: create and submit models, and review or approve revisions submitted by the test engineer, whose `SUPID` is the manager's `IDNUM`
- `admin` / `admin`: register employees and manage roles, data sources, the Input Registry, and model families

Override the development passwords with `R2R_VIEWER_PASSWORD`, `R2R_ENGINEER_PASSWORD`, `R2R_MANAGER_PASSWORD`, and `R2R_ADMIN_PASSWORD`. The password map is the test substitute for the corporate authentication REST API. Production should replace only this verifier; employee lookup, R2R pre-registration, and supervisor authorization remain separate checks.

For testing Add User, employee `E301` (`engineer2` / `engineer2`) exists in the placeholder corporate directory but is not initially registered in R2R. An administrator can register `E301`, assign a role, and then use that login.

## Execution workflow

1. The caller sends one actual value for required `machine`, `action`, and `material` indexes plus any optional model indexes and external application values. A model may accept multiple values for each index.
2. R2R selects the most-specific enabled model whose revision status is `approved`.
3. Parameters are resolved in dependency order from either a scalar SQL query or a typed expression. Expressions cover constants, references, calculations, conditions, sample functions, and typed table lookups.
4. Named SQL or static data samples resolve after their dependencies and provide bounded, typed tabular inputs. Production callers cannot replace those rows.
5. The configured output list returns selected resolved parameters without another calculation layer.

```bash
curl -X POST http://localhost:4173/api/v1/runs/prepare \
  -H "Content-Type: application/json" \
  -d '{
    "runId": "RUN-2026-00042",
    "context": {
      "lotId": "LOT-042",
      "machine": "PROCESS-01",
      "action": "CALCULATE",
      "material": "PRODUCT_A",
      "operation": "REWORK",
      "measured_cd": 1.5
    }
  }'
```

The included draft example demonstrates all expression forms. References use `@name`; functions do not. The popup expression editor groups operators, conditional statements, aggregates, math functions, data-sample functions, and string functions. Expressions support typed literals, comparisons, `IF(condition, result, else_result)`, numerical operators, common math functions, `MIN`, `MAX`, `AVG`, `MEDIAN`, `RANGE`, `STDEV`, `SUM`, and `COUNT` aggregates, positional `FIRST`, `NTH`, and `LAST`, typed lookups, and string functions such as `SUBSTR`, `REPLACE`, `CONCAT`, and `TRIM`. `CONTAINS(@sample.text_column, "value")` performs a case-insensitive search across the column and returns a boolean. `NTH(@sample.column, 2)` selects the second row using a 1-based position. Use a deterministic SQL `ORDER BY` whenever row order matters.

## Model definition

Each model revision owns:

- Required and additional selection indexes with one or more accepted values
- Any number of ordered parameters with optional descriptions
- Parameter value source: `sql` or `expression`
- Typed conditional expressions with `IF`, comparisons, `AND`, and `OR`
- Adaptive parameters with persisted current values, EWMA or custom update rules, optional deadband, and maximum adjustment per run
- Optional LSL and USL engineering limits on every numerical parameter, evaluated before dependent parameters (warn, clamp, reject, or fallback)
- Named tabular SQL data samples with a record limit
- Revision-owned static tabular data samples with typed text, number, and boolean columns
- Declared text, number, and boolean result schemas for SQL data samples
- Typed table lookups with equals or contains matching and a typed return column
- Scalar SQL queries or typed expressions referencing parameters, external inputs, model indexes, and data samples
- Any number of output parameter references
- Author, change note, revision number, and lifecycle status

New revisions begin as editable drafts. Submitting a draft locks it in `pending_approval`. An engineering manager may approve a direct report's revision, while an administrator may approve any pending revision. Approval supersedes the previously approved revision; submitting does not affect the active model. Only approved and enabled revisions can execute.

The Control Models screen can create a new model family as revision 1 or create a new revision from an existing family. Both workflows use forms for required and optional indexes, data samples, ordered parameters, engineering limits, and output selection. The developer payload is available behind the **View API payload** button.

Parameters and data samples form one validated dependency graph. SQL data samples bind declared values with names such as `@wafer_id`, while expressions reference sample columns such as `MAX(@history.thickness_nm)`. The backend derives execution order automatically and rejects unknown references, type mismatches, duplicate names, and circular dependencies. The model editor's **View dependencies** dialog displays the same backend-validated graph.

A data-source definition represents one database and security boundary, not one schema. Queries may access multiple authorized schemas by using qualified names such as `metrology.measurements` and `reference.targets`.

SQL parameters select a preconfigured connection from the Data Sources registry. Admin and Engineer roles can edit registry metadata and availability; Viewer access is read-only. Credentials remain deployment-managed and are never returned in the browser configuration.

The R2R production repository uses SQL Server. External calculation data may come from PostgreSQL, MySQL, or SQL Server. Data Sources define the external engine, server, port, database, optional schema, username, TLS mode, timeout, and a deployment secret reference. Queries use model references such as `@wafer_id`; R2R rewrites them to the target driver's parameter format. The Test connection action records the latest result and response time. A password entered in the editor is write-only and used only for that test; it is never stored in `config.json`.

## API

- `POST /api/login` resolves `LOGINID`, requires an active R2R registration for its `IDNUM`, and then invokes the placeholder corporate credential verifier.
- `GET /api/users` lists R2R employee registrations for administrators.
- `POST /api/users` registers an existing corporate `employeeId` with an R2R role; the prototype persists this in `data/config.json`.
- `PUT /api/users/{employeeId}` changes an R2R role or enables/disables access. Disabling revokes the employee's active sessions while preserving model history.
- `DELETE /api/users/{employeeId}` permanently removes an unused R2R registration without modifying the corporate employee directory. Registrations referenced by model history must be disabled instead. An administrator cannot remove their own registration or the last active administrator.
- `POST /api/models` creates revision 1 of a new model family as a draft.
- `POST /api/models/{id}/revisions` creates a draft revision from an existing model.
- `POST /api/models/{id}/submit` submits and locks a draft for approval.
- `POST /api/models/{id}/transfer` lets an administrator transfer an open draft to an active engineer or administrator while preserving its original creator.
- `POST /api/models/{id}/approve` activates a pending revision when the approver is the submitter's direct supervisor with an approval role, or when the approver is an administrator.
- `POST /api/models/{id}/reject` returns a pending revision to draft with a required reason.
- `GET /api/models/{id}/history` returns immutable revision history.
- `POST /api/v1/runs/prepare` selects the active revision, resolves values, and records a prepared run.
- `POST /api/v1/runs/{runId}/complete` records completion feedback and post-run adaptive updates.
- `POST /api/simulate` runs each row of an uploaded CSV through the active model without committing adaptive updates.

The Simulation page follows a model-first workflow: select an active model, download its generated CSV template, populate and upload the file, then run the simulation. After a run, **View JSON exchange** shows the exact simulation request and response. Draft simulation provides the same template and exchange viewer for the selected revision.

Scalar SQL parameters and tabular data samples execute one read-only `SELECT` against their configured external source. Values are bound through `database/sql` and are never interpolated into query text. Declared SQL sample schemas normalize and validate returned column names and types before expressions or lookups execute. Data-sample results are limited to the configured record count. The source's `secretRef` names the environment variable containing its password. Production callers may override scalar SQL parameters through `parameterValues`; model data samples always come from static definitions or configured SQL sources. Caller-supplied sample rows are confined to the Simulation API.

Static data samples store their typed column definitions and rows directly with the model revision. They resolve without a database connection and support the same aggregate and positional expressions as SQL samples.

Models with adaptive parameters require a unique `runId`. The current value is used for the run, the next value is calculated after all parameters resolve, and the update is persisted only after successful output evaluation. The response exposes the committed next values in `adaptiveUpdates`.

After calculating a proposed adaptive value, R2R compares its change with the configured deadband. Changes at or below the deadband are ignored. A larger change is limited to the configured maximum adjustment per run before it is persisted.

The model execution policy defines one `partitionBy` list for both unfinished-run limits and adaptive state. For example, `partitionBy: ["machine", "chamber"]` gives each machine/chamber combination an independent unfinished-run limit and independent adaptive values. If omitted, the partition defaults to `machine`. Revisions in the same family continue using the same partitioned adaptive state.

The Simulation screen accepts CSV headers for model indexes, external values, and scalar parameters. A dotted header such as `datasample1.thickness_nm` supplies a one-row named data sample. Simulation is limited to 500 rows per request and previews adaptive updates without changing persisted controller state.

## Verify

```bash
npm run build
npm test
```

Production deployment still requires authenticated roles, independent ME approval, a transactional database, immutable audit storage, managed secrets, and equipment-write safeguards.
