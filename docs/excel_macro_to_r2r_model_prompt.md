# Prompt template: Excel/VBA macro to R2R model JSON

Use this prompt with an AI that cannot access the R2R source code. Attach the Excel workbook and
the exported VBA source (`.bas`, `.cls`, or plain text). Replace every value in angle brackets before
submitting it.

---

## Copyable prompt

You are converting a manufacturing Excel/VBA control macro into one importable R2R model JSON
object. You do not have access to the R2R application source code, so follow the contract and
expression rules in this prompt exactly.

### Inputs provided

- Excel workbook: `<WORKBOOK_FILENAME.xlsm>`
- Exported VBA/macro source: `<MACRO_FILENAME.bas or pasted macro text>`
- Target macro/procedure: `<PROCEDURE_OR_MODEL_NAME>`
- SQL engine: `<sqlserver | postgres | mysql>`
- Configured R2R data-source identifier: `<DATA_SOURCE_ID>`
- Values supplied by the external application:
  - `<INPUT_NAME>: <string | number | boolean> — <meaning>`
- Required routing indexes:
  - `machine`: `<one or more accepted values>`
  - `action`: `<one or more accepted values>`
  - `material`: `<one or more accepted values>`
- Maximum unfinished runs per execution partition: `<OMIT_FOR_UNLIMITED_OR_POSITIVE_INTEGER>`
- Unfinished-run partition fields: `<index and/or external-input names; use machine if unspecified>`
- Desired output parameter(s): `<OUTPUT_NAMES>`
- Known database tables and columns: `<SCHEMA DETAILS, or "not yet confirmed">`
- Additional business clarification: `<NOTES>`
- Output mode: `<REVIEW_AND_JSON | JSON_ONLY>`

### Objective

Reconstruct the behavior of the specified Excel/VBA macro and produce a single JSON object accepted
by R2R's **Import model JSON** dialog. The imported model will be created as a draft. Preserve the
macro's formulas, branching, thresholds, lookup order, missing-data handling, database filtering,
and returned outputs.

Do not include system-managed fields such as `id`, `familyId`, `owner`, `revision`, `status`,
`enabled`, `createdBy`, approval metadata, or timestamps. R2R generates those fields.

### Required analysis procedure

1. Identify the target procedure's inputs, outputs, local/intermediate values, called procedures,
   worksheet cells, tables, named ranges, SQL statements, and constants.
2. Trace every execution branch, including default/error branches and comparisons at threshold
   boundaries. Distinguish `<` from `<=`, and `>` from `>=` exactly.
3. Resolve worksheet formulas to their real logic. Do not replace a formula with its currently
   displayed value.
4. Determine which values are:
   - routing indexes: stable, low-cardinality values used to choose a model;
   - external inputs: per-run values supplied by the caller;
   - SQL data samples: tabular query results;
   - static data samples: rule/threshold tables embedded in the workbook;
   - parameters: constants or calculated intermediate/final values;
   - outputs: values returned to the caller;
   - feedback inputs: values received only when completing a run.
5. Convert repeated `If`, `Select Case`, lookup tables, recipe tables, and threshold ranges into
   typed static samples and expressions when that is clearer than deeply nested `IF` calls.
6. For SQL results whose row position matters, make ordering explicit with deterministic `ORDER BY`.
   Never assume database row order. Use `ROW_NUMBER()` or an equivalent technique when selecting
   the latest row per group.
7. Parameterize SQL using `@external_input_or_parameter`. Never concatenate caller values into SQL.
8. Declare every SQL result column used by an expression, including its exact R2R type.
9. Build a dependency graph mentally and ensure it is acyclic. Every `@reference` must refer to a
   declared index, external input, parameter, or data sample.
10. Perform the validation checklist below before returning the JSON.

### Stop-and-ask rule

Do not invent database identifiers, worksheet meaning, units, lookup ordering, missing-data
behavior, or ambiguous formulas. If any missing fact can materially change the model result, stop
and ask a short numbered list of precise questions. Cite the relevant macro procedure, cell, formula,
or SQL fragment in each question. Do not produce final JSON until those blocking questions are
answered.

You may make a non-blocking assumption only when it cannot change the calculated result. List every
such assumption when `Output mode` is `REVIEW_AND_JSON`.

### R2R import JSON contract

Return one object with this shape:

```json
{
  "name": "Descriptive model name",
  "description": "Business purpose and supported process cases.",
  "changeNote": "Initial conversion from <WORKBOOK_FILENAME.xlsm>",
  "indexes": {
    "machine": ["MACHINE_VALUE"],
    "action": ["ACTION_VALUE"],
    "material": ["MATERIAL_VALUE"]
  },
  "externalInputs": [
    {
      "name": "wafer_id",
      "description": "Wafer identifier supplied by the external application.",
      "type": "string"
    }
  ],
  "feedbackInputs": [],
  "executionPolicy": {
    "maxUnfinishedRuns": 1,
    "partitionBy": ["wafer_id"]
  },
  "parameters": [
    {
      "key": "calculated_value",
      "description": "What this parameter represents.",
      "unit": "",
      "dataType": "number",
      "source": "expression",
      "definition": "IF(@condition, @value_a, @value_b)"
    }
  ],
  "dataSamples": [
    {
      "key": "history",
      "description": "Typed rows read from the process-history database.",
      "source": "sql",
      "dataSourceId": "<DATA_SOURCE_ID>",
      "query": "SELECT ... WHERE WAFER_ID = @wafer_id ORDER BY ...",
      "columns": [
        { "name": "operation", "type": "string" },
        { "name": "measurement", "type": "number" }
      ],
      "limit": 100
    },
    {
      "key": "recipe_rules",
      "description": "Rules transcribed from the workbook.",
      "source": "static",
      "columns": [
        { "name": "operation", "type": "string" },
        { "name": "upper_bound", "type": "number" },
        { "name": "recipe", "type": "string" }
      ],
      "rows": [
        { "operation": "ARI170", "upper_bound": 1.5, "recipe": "RECIPE_A" }
      ],
      "limit": 1
    }
  ],
  "outputs": ["calculated_value"]
}
```

Field names are exact and case-sensitive. In particular, every item in `dataSamples` must use
`"key"`; `"name"` is invalid for a data sample. Do not rename, pluralize, or substitute any field in
the contract. Before responding, inspect the generated object key-by-key against the contract rather
than relying on semantic similarity.

Use only these value types:

- `string`
- `number`
- `boolean`

Names must be non-empty and unique without regard to letter case. Prefer stable `snake_case` names.
Every output must name a declared parameter.

The required routing indexes are `machine`, `action`, and `material`. Each index value must be an
array of one or more strings. Do not put high-cardinality per-run values such as wafer IDs or lot IDs
in routing indexes; use external inputs for those.

Every external input is required at prepare time. Only declare inputs that the model actually needs.
Fields in `partitionBy` must name a declared index or external input. Partition fields define which
runs share the unfinished-run limit and adaptive state.

### Parameter contract

New models should normally use one of these parameter sources:

1. Expression parameter:

```json
{
  "key": "result",
  "description": "Calculated result.",
  "unit": "nm",
  "dataType": "number",
  "source": "expression",
  "definition": "@base_value + 2"
}
```

2. SQL scalar parameter, only when a scalar query is genuinely clearer than a typed SQL data
sample:

```json
{
  "key": "latest_value",
  "description": "Latest database value.",
  "unit": "nm",
  "dataType": "number",
  "source": "sql",
  "dataSourceId": "<DATA_SOURCE_ID>",
  "definition": "SELECT ..."
}
```

Prefer SQL data samples when multiple columns or rows are needed. Do not use legacy parameter
sources such as `constant`, `formula`, `logic`, `lookup`, `external`, or `data-sample` in newly
generated JSON; their behavior is covered by typed expressions and data samples.

For numerical parameters, specification-limit behavior may be declared as:

```json
{
  "lsl": 0,
  "usl": 10,
  "limitAction": "warn"
}
```

Allowed actions are `warn`, `clamp`, `reject`, and `fallback`. A `fallback` action also requires a
numeric `fallbackValue`. Add limits only when they exist in the workbook or supplied requirements;
do not infer engineering limits.

### Expression language

References always use `@`; function names never use `@`.
Data samples use `@sample_name`, and their columns must always use the fully qualified
`@sample_name.column_name` form, including lookup filters and return columns.

Correct:

```text
IF(@latest_cd > 1, 3, 0)
FIRST(@history.measurement)
LOOKUP_ONE(@rules, @rules.operation == @operation, @rules.recipe)
```

Incorrect:

```text
@IF(...)
FIRST(history.measurement)
LOOKUP_ONE(rules, ...)
```

Supported literals and operators:

- Numbers: `1`, `-2.5`, `0.02`
- Strings: `"text"`
- Booleans: `true`, `false`
- Arithmetic: `+`, `-`, `*`, `/`
- Comparison: `==`, `!=`, `>`, `>=`, `<`, `<=`
- Boolean: `AND`, `OR`
- Conditional: `IF(condition, result_if_true, result_if_false)`

Supported aggregate functions:

```text
AVG(@sample.column)
MAX(@sample.column)
MEDIAN(@sample.column)
MIN(@sample.column)
RANGE(@sample.column)
STDEV(@sample.column)
SUM(@sample.column)
COUNT(@sample.column)
```

Supported positional and sample functions:

```text
FIRST(@sample.column)
NTH(@sample.column, 2)
LAST(@sample.column)
CONTAINS(@sample.column, "text")
MATCHES(@sample.column, "^pattern$")
```

`NTH` is one-based. `CONTAINS` returns a boolean and performs a case-insensitive exact-value check across the
specified column.

Supported typed lookups:

```text
LOOKUP_ONE(@sample, @sample.column_a == @value_a AND @sample.column_b < 2, @sample.return_column)
LOOKUP_FIRST(@sample, @sample.column_a == "A" AND @sample.column_b >= @minimum, @sample.return_column)
```

- `LOOKUP_ONE` returns null for zero matches, requires exactly one matching row, and fails for multiple matches.
- `LOOKUP_FIRST` returns null for zero matches and returns the first row when one or more rows match.
- Use `IS_NULL(@parameter)` to test a null lookup result and provide fallback behavior where needed.
- Lookup column names are written without `@` inside the predicate and return-column position.
- Numerical ordering operators may only compare numerical values.
- A lookup predicate supports only comparisons joined by `AND` or `OR`. Do not call `IF` or any
  other function inside a lookup predicate. When lookup behavior differs by a condition, put the
  `IF` outside and return one complete lookup call from each branch, for example:

```text
IF(@mode == "A",
   LOOKUP_FIRST(@rules, @rules.upper_bound >= @value, @rules.result),
   LOOKUP_FIRST(@rules, @rules.upper_bound > @value, @rules.result))
```

Supported math functions:

```text
ABS, CEIL, COS, EXP, FLOOR, LOG, POW, ROUND, SIN, SQRT, TAN
```

Supported string functions:

```text
SUBSTR(@text, start, length)
REPLACE(@text, "old", "new")
LOWER(@text)
UPPER(@text)
TRIM(@text)
CONCAT(@text, "suffix")
LEN(@text)
```

`SUBSTR` uses a one-based start position. Expression result types must match the parameter's
`dataType`, and both branches of an `IF` must be compatible with that result type.

### Data-sample rules

SQL sample:

- `source` must be `sql`.
- `dataSourceId` must match an existing R2R data-source identifier.
- `query` must contain parameterized SQL appropriate for the specified database engine.
- `columns` must declare every returned column used by expressions.
- Query aliases must match declared column names.
- Returned values must match declared types.
- `limit` must be large enough for intended logic but bounded.
- Add deterministic ordering when using `FIRST`, `NTH`, `LAST`, or `LOOKUP_FIRST`.
- Never invent a tie-breaker column or add a value-based tie-breaker that was not specified. If the
  supplied timestamp is not unique and no stable row identifier is documented, ask for one.
- Do not filter conversion failures out of a ranked history query unless the original macro does so.
  Filtering invalid newest values can silently select an older value and change the control result.

Static sample:

- `source` must be `static`.
- Every row must follow the declared column schema.
- Preserve workbook row ordering when first-match semantics matter.
- Preserve decimal values as numbers, not strings.
- Set `limit` to at least the number of required rows.

Do not place credentials, passwords, connection strings, macro passwords, or secret values in the
model JSON.

### Validation checklist

Before returning the model, verify all of the following:

1. The object is valid JSON with no comments or trailing commas.
2. `machine`, `action`, and `material` are present and non-empty.
3. The model contains at least one parameter and one output.
4. Every output names an existing parameter.
5. All names are unique case-insensitively.
6. Every type is `string`, `number`, or `boolean`.
7. Every `@reference` exists and has the correct type.
8. Every referenced sample and column exists.
9. Dependencies contain no cycle.
10. SQL is parameterized and uses the requested SQL dialect.
11. SQL aliases exactly match the declared sample columns.
12. Ordered functions have deterministic row order.
13. Static table thresholds and their inclusive/exclusive boundaries match the workbook exactly.
14. Missing-row, duplicate-row, zero-sentinel, and unsupported-case behavior matches the macro.
15. No system-managed metadata or secrets are included.
16. No formula, table, column, unit, or business rule was invented.
17. Every data-sample object uses `key`, never `name`.
18. Every lookup predicate consists only of supported comparisons joined by `AND`/`OR`.
19. Manually evaluate every supplied acceptance case against the final generated rows and expressions;
    do not return JSON if any expected value differs.

### Required response

If blocking facts are missing, return only the numbered clarification questions.

If `Output mode` is `JSON_ONLY`, return only the final JSON object in a JSON code block, with no
additional prose.

If `Output mode` is `REVIEW_AND_JSON`, return:

1. A concise mapping table from macro/workbook concepts to R2R elements.
2. Any non-blocking assumptions.
3. A short list of boundary and representative test cases with expected results.
4. The final importable JSON object in one JSON code block.

Do not claim the model was validated by R2R itself. State that final acceptance requires importing
the JSON as a draft, running R2R validation, configuring the referenced data source and input
registry entries, and testing it against known workbook cases.

---

## Recommended attachments

For the most reliable conversion, provide:

- the original `.xlsm` workbook;
- exported VBA modules as text;
- workbook/macro password when legally and operationally appropriate;
- database table and column definitions;
- several known input/output examples;
- the R2R data-source identifier;
- the intended machine, action, and material routing values;
- definitions for caller-supplied inputs and completion feedback.

If the AI cannot inspect binary Excel files, export relevant worksheets or named tables to CSV and
provide formulas separately. Never rely only on displayed cell values when formulas drive the macro.
