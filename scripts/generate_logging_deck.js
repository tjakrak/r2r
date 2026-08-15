const pptxgen = require('pptxgenjs');

const pptx = new pptxgen();
pptx.layout = 'LAYOUT_WIDE';
pptx.author = 'R2R Process Control';
pptx.company = 'TDK';
pptx.subject = 'Production SQL Server schema';
pptx.title = 'R2R Production SQL Server Tables';
pptx.lang = 'en-US';
pptx.theme = { headFontFace: 'Aptos Display', bodyFontFace: 'Aptos', lang: 'en-US' };

const C = {
  navy: '12344D', blue: '176B9A', cyan: '42A5C6', pale: 'EAF4F8',
  ink: '1F2933', muted: '60717D', line: 'D5E0E5', white: 'FFFFFF',
  green: '23845D', greenPale: 'E5F4EC', amber: 'A86800', amberPale: 'FFF1D6',
  red: 'A84444', redPale: 'FBEAEA', grayPale: 'F4F6F7',
};

function shell(title, subtitle, page) {
  const s = pptx.addSlide();
  s.background = { color: C.white };
  s.addShape(pptx.ShapeType.rect, { x: 0, y: 0, w: 13.333, h: 0.15, fill: { color: C.blue }, line: { color: C.blue } });
  s.addText(subtitle.toUpperCase(), { x: 0.62, y: 0.35, w: 7, h: 0.2, fontSize: 9, bold: true, color: C.blue, charSpacing: 1.3, margin: 0 });
  s.addText(title, { x: 0.62, y: 0.69, w: 12.0, h: 0.52, fontSize: 26, bold: true, color: C.navy, margin: 0, fit: 'shrink' });
  s.addShape(pptx.ShapeType.line, { x: 0.62, y: 1.38, w: 12.05, h: 0, line: { color: C.line } });
  s.addText(String(page), { x: 12.3, y: 7.08, w: 0.35, h: 0.15, fontSize: 8, color: C.muted, align: 'right', margin: 0 });
  return s;
}

function badge(s, text, x, y, kind = 'required', w = 1.3) {
  const p = kind === 'required' ? [C.greenPale, C.green] : kind === 'new' ? [C.amberPale, C.amber] : [C.pale, C.blue];
  s.addShape(pptx.ShapeType.roundRect, { x, y, w, h: 0.3, rectRadius: 0.05, fill: { color: p[0] }, line: { color: p[0] } });
  s.addText(text.toUpperCase(), { x, y: y + 0.01, w, h: 0.2, fontSize: 7.8, bold: true, color: p[1], align: 'center', margin: 0.02, charSpacing: 0.5 });
}

let nextPage = 3;

function inferredExample(name, type) {
  if (name === 'row_version') return '0x00000000000007D3';
  if (name === 'valid_to_utc') return '9999-12-31T23:59:59.9999999Z';
  if (name.endsWith('_at_utc') || name === 'valid_from_utc') return '2026-08-05T22:10:00Z';
  if (name.endsWith('_json')) return '{}';
  if (name.includes('hash')) return '0xA37F…';
  if (name.includes('address')) return '10.20.1.15';
  if (name.endsWith('_by') || name.endsWith('_id')) return 'admin1';
  if (name.includes('status')) return 'active';
  if (name.includes('message')) return null;
  if (name.includes('name')) return 'Example';
  if (type.startsWith('bit')) return false;
  if (type.startsWith('int') || type.startsWith('bigint') || type.startsWith('float')) return 1;
  if (type.startsWith('uniqueidentifier')) return '64B72E4D-0F32-4B62-A9AB-47A0624D00D4';
  return 'example';
}

function displayValue(value) {
  if (value === null) return 'NULL';
  if (typeof value === 'boolean') return value ? '1' : '0';
  return String(value);
}

function tableSlide({ name, purpose, columns, example, status = 'required', note }) {
  const completeExample = {};
  columns.forEach(([column, type]) => {
    completeExample[column] = Object.prototype.hasOwnProperty.call(example, column)
      ? example[column]
      : inferredExample(column, type);
  });
  const chunks = [];
  for (let i = 0; i < columns.length; i += 6) chunks.push(columns.slice(i, i + 6));

  chunks.forEach((chunk, chunkIndex) => {
    const part = chunks.length > 1 ? ` • columns ${chunkIndex * 6 + 1}–${chunkIndex * 6 + chunk.length} of ${columns.length}` : '';
    const s = shell(name, purpose + part, nextPage++);
    badge(s, status === 'new' ? 'New migration' : status === 'optional' ? 'Conditional' : 'Required', 10.95, 0.4, status, 1.55);
    s.addText('Column definitions', { x: 0.7, y: 1.62, w: 5.7, h: 0.3, fontSize: 17, bold: true, color: C.navy, margin: 0 });
    s.addText('Matching JSON example', { x: 7.0, y: 1.62, w: 5.55, h: 0.3, fontSize: 17, bold: true, color: C.navy, margin: 0 });
    s.addShape(pptx.ShapeType.roundRect, { x: 0.67, y: 2.02, w: 5.95, h: 2.65, rectRadius: 0.04, fill: { color: 'FAFCFD' }, line: { color: C.line } });
    const columnText = chunk.map(([n, t, d]) => `${n}  —  ${t}\n${d}`).join('\n\n');
    s.addText(columnText, { x: 0.91, y: 2.25, w: 5.47, h: 2.18, fontSize: 10.2, color: C.ink, margin: 0, fit: 'shrink', breakLine: false });
    const chunkExample = Object.fromEntries(chunk.map(([n]) => [n, completeExample[n]]));
    s.addShape(pptx.ShapeType.roundRect, { x: 6.97, y: 2.02, w: 5.68, h: 2.65, rectRadius: 0.04, fill: { color: C.navy }, line: { color: C.navy } });
    s.addText(JSON.stringify(chunkExample, null, 2), { x: 7.24, y: 2.25, w: 5.14, h: 2.18, fontFace: 'Aptos Mono', fontSize: 9.2, color: C.white, margin: 0, fit: 'shrink', breakLine: false });

    s.addText('Actual table example — the same columns and values', { x: 0.7, y: 4.9, w: 11.9, h: 0.28, fontSize: 15, bold: true, color: C.navy, margin: 0 });
    const keys = chunk.map(([n]) => n);
    const widths = keys.map(() => 11.95 / keys.length);
    let x = 0.68;
    keys.forEach((key, index) => {
      s.addShape(pptx.ShapeType.rect, { x, y: 5.32, w: widths[index], h: 0.48, fill: { color: C.blue }, line: { color: C.white, width: 0.6 } });
      s.addText(key, { x: x + 0.06, y: 5.46, w: widths[index] - 0.12, h: 0.18, fontSize: 8, bold: true, color: C.white, align: 'center', margin: 0, fit: 'shrink' });
      s.addShape(pptx.ShapeType.rect, { x, y: 5.80, w: widths[index], h: 0.65, fill: { color: C.grayPale }, line: { color: C.line, width: 0.6 } });
      s.addText(displayValue(completeExample[key]), { x: x + 0.06, y: 5.98, w: widths[index] - 0.12, h: 0.25, fontSize: 8.2, color: C.ink, align: 'center', margin: 0, fit: 'shrink' });
      x += widths[index];
    });
    if (note && chunkIndex === chunks.length - 1) s.addText(note, { x: 0.85, y: 6.7, w: 11.65, h: 0.3, fontSize: 10.5, bold: true, color: C.blue, align: 'center', margin: 0, fit: 'shrink' });
  });
}

// 1 — title
{
  const s = pptx.addSlide();
  s.background = { color: C.navy };
  s.addShape(pptx.ShapeType.rect, { x: 0, y: 0, w: 0.2, h: 7.5, fill: { color: C.cyan }, line: { color: C.cyan } });
  s.addText('R2R PROCESS CONTROL', { x: 0.86, y: 1.0, w: 5, h: 0.3, fontSize: 11, bold: true, color: '80C7DF', charSpacing: 2, margin: 0 });
  s.addText('Production SQL Server Tables', { x: 0.86, y: 1.65, w: 9.6, h: 0.78, fontSize: 38, bold: true, color: C.white, margin: 0 });
  s.addText('Required tables, complete column lists, and example records', { x: 0.89, y: 2.73, w: 8.4, h: 0.5, fontSize: 18, color: 'C9D9E2', margin: 0 });
  s.addShape(pptx.ShapeType.roundRect, { x: 9.75, y: 1.55, w: 2.5, h: 2.5, rectRadius: 0.1, fill: { color: '194761' }, line: { color: '4184A5' } });
  s.addText('SQL', { x: 10.15, y: 2.0, w: 1.7, h: 0.58, fontSize: 36, bold: true, color: '80C7DF', align: 'center', margin: 0 });
  s.addText('SERVER', { x: 10.15, y: 2.78, w: 1.7, h: 0.3, fontSize: 13, bold: true, color: C.white, align: 'center', charSpacing: 1.2, margin: 0 });
  s.addText('Proposed final production design • August 2026', { x: 0.89, y: 6.65, w: 5.6, h: 0.25, fontSize: 10, color: '93ADBB', margin: 0 });
}

// 2 — inventory
{
  const s = shell('Production table inventory', '16 application tables + SQL Server-managed history', 2);
  const groups = [
    ['Administration', ['schema_migrations', 'roles', 'users', 'user_sessions']],
    ['Models', ['model_families', 'model_revisions', 'data_sources']],
    ['Execution', ['runs', 'run_parameter_values', 'run_data_samples', 'run_outputs', 'run_feedback_values', 'adaptive_states']],
    ['Governance', ['input_definitions', 'audit_ledger_events', 'operational_events']],
  ];
  groups.forEach((g, i) => {
    const x = 0.62 + i * 3.13;
    s.addShape(pptx.ShapeType.roundRect, { x, y: 1.85, w: 2.78, h: 3.7, rectRadius: 0.06, fill: { color: i === 2 ? C.greenPale : C.pale }, line: { color: i === 2 ? 'B8DDCA' : 'C5DFE8' } });
    s.addText(g[0], { x: x + 0.18, y: 2.15, w: 2.42, h: 0.32, fontSize: 18, bold: true, color: i === 2 ? C.green : C.blue, align: 'center', margin: 0 });
    g[1].forEach((name, n) => s.addText(name, { x: x + 0.2, y: 2.88 + n * 0.56, w: 2.38, h: 0.25, fontFace: 'Aptos Mono', fontSize: 11, color: C.ink, align: 'center', margin: 0, fit: 'shrink' }));
  });
  s.addText('Roles are managed in SQL Server. Passwords are not: authenticate with company identity, then map the authenticated user to an R2R role.', { x: 0.78, y: 5.95, w: 11.8, h: 0.35, fontSize: 11.5, color: C.amber, align: 'center', margin: 0 });
  s.addText('Temporal history tables are maintained automatically by SQL Server for model_revisions, runs, and adaptive_states.', { x: 0.78, y: 6.48, w: 11.8, h: 0.35, fontSize: 12.5, bold: true, color: C.navy, align: 'center', margin: 0 });
}

// 3 — evaluated design
{
  const s = shell('Columns selected for this project', 'Consistency and scope decisions', nextPage++);
  const decisions = [
    ['No duplicate parameter JSON', 'Inputs and calculated values live in run_parameter_values. Outputs with limits live in run_outputs.'],
    ['Normalize execution evidence', 'Data samples use run_data_samples; feedback uses run_feedback_values; adaptive state uses adaptive_states and temporal history.'],
    ['Use two practical links', 'request_id connects HTTP activity; run_id connects execution records. correlation_id is omitted until a real multi-request workflow requires it.'],
    ['Separate audit from operations', 'Audit ledger records business changes. Operational events record health, timing, and errors.'],
    ['Manage authorization', 'roles and users control application access. Sessions are shared across servers; credentials stay with company identity.'],
  ];
  decisions.forEach((d, i) => {
    const y = 1.68 + i * 0.98;
    s.addShape(pptx.ShapeType.roundRect, { x: 0.72, y, w: 3.1, h: 0.67, rectRadius: 0.04, fill: { color: i === 0 ? C.greenPale : C.pale }, line: { color: i === 0 ? 'B8DDCA' : 'C5DFE8' } });
    s.addText(d[0], { x: 0.9, y: y + 0.21, w: 2.74, h: 0.23, fontSize: 13, bold: true, color: i === 0 ? C.green : C.blue, align: 'center', margin: 0, fit: 'shrink' });
    s.addText(d[1], { x: 4.18, y: y + 0.06, w: 8.0, h: 0.47, fontSize: 12.2, color: C.muted, margin: 0, fit: 'shrink' });
  });
  s.addText('Every example in the following slides is generated from the same column list shown beside it.', { x: 0.95, y: 6.72, w: 11.45, h: 0.28, fontSize: 12.5, bold: true, color: C.navy, align: 'center', margin: 0 });
}

tableSlide({ page: 3, name: 'r2r.schema_migrations', purpose: 'Database version tracking', columns: [
  ['version', 'int', 'Migration number; primary key'], ['description', 'nvarchar(200)', 'What the migration changed'], ['applied_at_utc', 'datetime2(3)', 'When it was applied'],
], example: { version: 4, description: 'Add input registry and typed run parameters', applied_at_utc: '2026-08-05T23:00:00Z' } });

tableSlide({ name: 'r2r.roles', purpose: 'Application roles administrators can assign', status: 'new', columns: [
  ['role_name', 'nvarchar(100)', 'Stable role key; primary key'], ['display_name', 'nvarchar(200)', 'User-facing role name'], ['description', 'nvarchar(1000)', 'Capabilities and intended users'], ['is_system', 'bit', 'Protect built-in roles'], ['created_by', 'nvarchar(200)', 'Creator identity'], ['created_at_utc', 'datetime2(3)', 'Creation time'], ['updated_by', 'nvarchar(200)', 'Last editor'], ['updated_at_utc', 'datetime2(3)', 'Last edit time'], ['row_version', 'rowversion', 'Concurrency protection'],
], example: { role_name: 'engineering_manager', display_name: 'Engineering Manager', description: 'Review, simulate, approve, and reject submitted revisions', is_system: true, created_by: 'system', created_at_utc: '2026-08-05T18:00:00Z', updated_by: 'admin1', updated_at_utc: '2026-08-05T18:00:00Z' } });

tableSlide({ name: 'r2r.users', purpose: 'Authorized R2R users and their assigned role', status: 'new', columns: [
  ['user_id', 'nvarchar(200)', 'Stable user key; primary key'], ['login_name', 'nvarchar(200)', 'Unique company login'], ['display_name', 'nvarchar(200)', 'User-facing name'], ['email_address', 'nvarchar(320)', 'Business email'], ['role_name', 'nvarchar(100)', 'Assigned R2R role; foreign key'], ['status', 'varchar(20)', 'active, disabled, locked'], ['identity_provider', 'nvarchar(100)', 'Company identity source'], ['external_subject', 'nvarchar(300)', 'Identity-provider subject ID'], ['created_by', 'nvarchar(200)', 'Administrator who added user'], ['created_at_utc', 'datetime2(3)', 'Creation time'], ['updated_by', 'nvarchar(200)', 'Last administrator'], ['updated_at_utc', 'datetime2(3)', 'Last update time'], ['last_login_at_utc', 'datetime2(3)', 'Latest successful login'], ['row_version', 'rowversion', 'Concurrency protection'],
], example: { user_id: 'USR-1007', login_name: 'ryan.t', display_name: 'Ryan T.', email_address: 'ryan.t@example.com', role_name: 'engineer', status: 'active', identity_provider: 'company-sso', external_subject: 'aad|91d8…', created_by: 'admin1', created_at_utc: '2026-08-05T18:00:00Z', updated_by: 'admin1', updated_at_utc: '2026-08-05T18:00:00Z', last_login_at_utc: '2026-08-05T21:42:00Z' }, note: 'Store authorization here, not passwords. Authentication remains with the company identity provider.' });

tableSlide({ page: 4, name: 'r2r.input_definitions', purpose: 'Admin-managed input registry', status: 'new', columns: [
  ['input_name', 'nvarchar(128)', 'Stable key; primary key'], ['display_name', 'nvarchar(200)', 'User-facing label'], ['category', 'varchar(20)', 'index or external'], ['data_type', 'varchar(20)', 'number, string, or boolean'], ['description', 'nvarchar(1000)', 'Business meaning'], ['status', 'varchar(20)', 'active or deprecated'], ['is_system', 'bit', 'Protect built-in inputs'], ['created_by', 'nvarchar(200)', 'Creator identity'], ['created_at_utc', 'datetime2(3)', 'Creation time'], ['updated_by', 'nvarchar(200)', 'Last editor'], ['updated_at_utc', 'datetime2(3)', 'Last edit time'], ['row_version', 'rowversion', 'Concurrency protection'],
], example: { input_name: 'measured_cd', display_name: 'Measured CD', category: 'external', data_type: 'number', description: 'Measured critical dimension', status: 'active', is_system: false, created_by: 'admin' } });

tableSlide({ page: 5, name: 'r2r.model_families', purpose: 'One logical model across all revisions', columns: [
  ['family_id', 'nvarchar(100)', 'Stable family key; primary key'], ['name', 'nvarchar(200)', 'Model name'], ['description', 'nvarchar(1000)', 'Business purpose'], ['created_by', 'nvarchar(200)', 'Creator identity'], ['created_at_utc', 'datetime2(3)', 'Creation time'], ['row_version', 'rowversion', 'Concurrency protection'],
], example: { family_id: 'expression-demo', name: 'Expression Control Example', description: 'Calculates recipe settings', created_by: 'engineer1', created_at_utc: '2026-08-05T18:00:00Z' } });

tableSlide({ page: 6, name: 'r2r.model_revisions', purpose: 'Exact frozen version of a model', columns: [
  ['model_id', 'nvarchar(150)', 'Exact revision ID; primary key'], ['family_id', 'nvarchar(100)', 'Parent family; foreign key'], ['revision', 'int', 'Family-wide revision number'], ['status', 'varchar(20)', 'draft, approved, superseded'], ['enabled', 'bit', 'Available for routing'], ['definition_json', 'nvarchar(max)', 'Complete model including static tables'], ['change_note', 'nvarchar(2000)', 'Reason for the change'], ['owner_name', 'nvarchar(200)', 'Business owner'], ['created_by', 'nvarchar(200)', 'Draft author'], ['created_at_utc', 'datetime2(3)', 'Draft creation time'], ['approved_by', 'nvarchar(200)', 'Approver identity'], ['approved_at_utc', 'datetime2(3)', 'Approval time'], ['valid_from_utc', 'datetime2(7)', 'Temporal-history start'], ['valid_to_utc', 'datetime2(7)', 'Temporal-history end'], ['row_version', 'rowversion', 'Concurrency protection'],
], example: { model_id: 'expression-demo-r3', family_id: 'expression-demo', revision: 3, status: 'approved', enabled: true, definition_json: '{ indexes, inputs, parameters, staticTables, outputs }', change_note: 'Updated lookup range', created_by: 'engineer1', approved_by: 'admin1' }, note: 'Static tabular data is frozen inside definition_json; no separate static-table record is required.' });

tableSlide({ page: 7, name: 'r2r.data_sources', purpose: 'External database connection definitions', columns: [
  ['source_id', 'nvarchar(100)', 'Primary key'], ['name', 'nvarchar(200)', 'Unique display name'], ['source_type', 'varchar(30)', 'Source classification'], ['engine', 'varchar(20)', 'sqlserver, postgres, mysql'], ['host_name', 'nvarchar(255)', 'Database host'], ['port', 'int', 'Network port'], ['database_name', 'nvarchar(128)', 'Database/catalog'], ['login_name', 'nvarchar(128)', 'Login when required'], ['secret_reference', 'nvarchar(200)', 'Secret-manager reference only'], ['tls_mode', 'varchar(20)', 'Encryption setting'], ['timeout_seconds', 'int', 'Query timeout'], ['location_name', 'nvarchar(200)', 'Site/environment'], ['enabled', 'bit', 'Available to models'], ['last_test_status', 'varchar(20)', 'success or failed'], ['last_test_message', 'nvarchar(2000)', 'Safe connection-test result'], ['last_test_at_utc', 'datetime2(3)', 'Latest test time'], ['last_test_ms', 'bigint', 'Latest test duration'], ['created_at_utc', 'datetime2(3)', 'Creation time'], ['updated_at_utc', 'datetime2(3)', 'Last update time'], ['row_version', 'rowversion', 'Concurrency protection'],
], example: { source_id: 'metrology-prod', name: 'Metrology Production', source_type: 'calculation', engine: 'sqlserver', host_name: 'SQL-PROD-01', port: 1433, database_name: 'Metrology', login_name: 'r2r_reader', secret_reference: 'kv/r2r/metrology', tls_mode: 'strict', timeout_seconds: 30, location_name: 'Factory A', enabled: true, last_test_status: 'success', last_test_message: 'Connection successful', last_test_at_utc: '2026-08-05T21:55:00Z', last_test_ms: 82 }, note: 'Use schema-qualified table names in model SQL. Passwords and access tokens never belong in this table.' });

tableSlide({ page: 8, name: 'r2r.runs', purpose: 'One record for every model execution', columns: [
  ['run_id', 'nvarchar(200)', 'Run key; primary key'], ['run_sequence', 'bigint identity', 'Ordered internal sequence'], ['family_id', 'nvarchar(100)', 'Model family'], ['model_id', 'nvarchar(150)', 'Exact revision executed'], ['model_revision', 'int', 'Revision number'], ['status', 'varchar(30)', 'prepared, completed, failed, cancelled'], ['disposition', 'varchar(30)', 'normal, warning, rejected'], ['prepared_at_utc', 'datetime2(3)', 'Execution start'], ['completed_at_utc', 'datetime2(3)', 'Completion/feedback time'], ['request_id', 'nvarchar(100)', 'Link to the API request'], ['failure_code', 'nvarchar(100)', 'Stable failure category'], ['failure_message', 'nvarchar(2000)', 'Safe failure explanation'], ['valid_from_utc', 'datetime2(7)', 'Temporal-history start'], ['valid_to_utc', 'datetime2(7)', 'Temporal-history end'], ['row_version', 'rowversion', 'Concurrency protection'],
], example: { run_id: 'RUN-10428', run_sequence: 10428, family_id: 'expression-demo', model_id: 'expression-demo-r3', model_revision: 3, status: 'completed', disposition: 'normal', prepared_at_utc: '2026-08-05T22:10:00Z', completed_at_utc: '2026-08-05T22:10:01Z', request_id: 'REQ-500', failure_code: null, failure_message: null } });

tableSlide({ page: 9, name: 'r2r.run_parameter_values', purpose: 'Typed value for every input and calculated parameter', status: 'new', columns: [
  ['run_id', 'nvarchar(200)', 'Parent run; composite primary key'], ['parameter_ordinal', 'int', 'Evaluation order; composite key'], ['parameter_name', 'nvarchar(128)', 'Parameter name'], ['parameter_kind', 'varchar(20)', 'index, external, calculated'], ['source_type', 'varchar(30)', 'expression, SQL, lookup, external'], ['data_type', 'varchar(10)', 'number, string, boolean'], ['number_value', 'float', 'Number value only'], ['text_value', 'nvarchar(max)', 'Text value only'], ['boolean_value', 'bit', 'Boolean value only'], ['unit', 'nvarchar(50)', 'Engineering unit'], ['protection_class', 'varchar(20)', 'normal, masked, secret'], ['is_redacted', 'bit', 'Value removed or masked'], ['calculated_at_utc', 'datetime2(3)', 'Evaluation time'],
], example: { run_id: 'RUN-10428', parameter_ordinal: 4, parameter_name: 'recipe_time', parameter_kind: 'calculated', source_type: 'expression', data_type: 'number', number_value: 53.75, text_value: null, boolean_value: null, unit: 's', protection_class: 'normal', is_redacted: false } });

tableSlide({ name: 'r2r.run_data_samples', purpose: 'Tabular SQL sample results used by one run', status: 'new', columns: [
  ['run_id', 'nvarchar(200)', 'Parent run; composite primary key'], ['sample_name', 'nvarchar(128)', 'Declared sample; composite primary key'], ['data_source_id', 'nvarchar(100)', 'External source used'], ['query_hash', 'binary(32)', 'Hash of executed query definition'], ['row_count', 'int', 'Number of captured rows'], ['snapshot_json', 'nvarchar(max)', 'Rows and columns used by calculation'], ['protection_class', 'varchar(20)', 'normal, masked, secret'], ['captured_at_utc', 'datetime2(3)', 'Capture time'],
], example: { run_id: 'RUN-10428', sample_name: 'history', data_source_id: 'metrology-prod', query_hash: '0x67A1…', row_count: 2, snapshot_json: '[{"adjustment":1.25},{"adjustment":0.75}]', protection_class: 'normal', captured_at_utc: '2026-08-05T22:10:00Z' }, note: 'Static sample tables remain in model_revisions.definition_json. This table captures changing SQL query results.' });

tableSlide({ page: 10, name: 'r2r.run_outputs', purpose: 'Values returned to the external application', columns: [
  ['run_id', 'nvarchar(200)', 'Parent run; composite primary key'], ['output_ordinal', 'int', 'Output order; composite key'], ['parameter_name', 'nvarchar(128)', 'Output name'], ['data_type', 'varchar(10)', 'number, string, boolean'], ['number_value', 'float', 'Number output'], ['text_value', 'nvarchar(max)', 'Text output'], ['boolean_value', 'bit', 'Boolean output'], ['calculated_number_value', 'float', 'Value before limit action'], ['unit', 'nvarchar(50)', 'Engineering unit'], ['lsl', 'float', 'Lower limit'], ['usl', 'float', 'Upper limit'], ['out_of_limit', 'bit', 'Limit result'], ['limit_action', 'varchar(20)', 'warn, clamp, reject, fallback'],
], example: { run_id: 'RUN-10428', output_ordinal: 0, parameter_name: 'recipe_time', data_type: 'number', number_value: 53.75, text_value: null, boolean_value: null, calculated_number_value: 53.75, unit: 's', lsl: 45, usl: 70, out_of_limit: false, limit_action: 'warn' } });

tableSlide({ name: 'r2r.run_feedback_values', purpose: 'Typed post-run measurements received later', status: 'new', columns: [
  ['run_id', 'nvarchar(200)', 'Parent run; composite primary key'], ['feedback_ordinal', 'int', 'Declared order; composite key'], ['feedback_name', 'nvarchar(128)', 'Feedback field name'], ['data_type', 'varchar(10)', 'number, string, boolean'], ['number_value', 'float', 'Number value only'], ['text_value', 'nvarchar(max)', 'Text value only'], ['boolean_value', 'bit', 'Boolean value only'], ['unit', 'nvarchar(50)', 'Engineering unit'], ['protection_class', 'varchar(20)', 'normal, masked, secret'], ['is_redacted', 'bit', 'Value removed or masked'], ['received_at_utc', 'datetime2(3)', 'Feedback receipt time'],
], example: { run_id: 'RUN-10428', feedback_ordinal: 0, feedback_name: 'measured_result', data_type: 'number', number_value: 53.6, text_value: null, boolean_value: null, unit: 's', protection_class: 'normal', is_redacted: false, received_at_utc: '2026-08-05T22:30:00Z' } });

tableSlide({ page: 11, name: 'r2r.adaptive_states', purpose: 'Current learned value for each model scope', columns: [
  ['family_id', 'nvarchar(100)', 'Model family; composite key'], ['parameter_name', 'nvarchar(128)', 'Adaptive parameter; composite key'], ['scope_hash', 'binary(32)', 'Scope hash; composite key'], ['scope_key', 'nvarchar(1000)', 'Readable scope key'], ['scope_json', 'nvarchar(max)', 'Scope indexes'], ['current_value', 'float', 'Current learned value'], ['last_run_id', 'nvarchar(200)', 'Run that last changed it'], ['last_run_sequence', 'bigint', 'Ordering guard'], ['updated_at_utc', 'datetime2(3)', 'Last update time'], ['valid_from_utc', 'datetime2(7)', 'Temporal-history start'], ['valid_to_utc', 'datetime2(7)', 'Temporal-history end'], ['row_version', 'rowversion', 'Concurrency protection'],
], example: { family_id: 'expression-demo', parameter_name: 'offset', scope_hash: '0xA37F…', scope_key: 'machine=PROCESS-01|material=PRODUCT_A', scope_json: '{ machine: PROCESS-01, material: PRODUCT_A }', current_value: 1.25, last_run_id: 'RUN-10428', last_run_sequence: 10428 } });

tableSlide({ page: 12, name: 'r2r.audit_ledger_events', purpose: 'Protected receipt for important business actions', columns: [
  ['event_id', 'uniqueidentifier', 'Audit event key; primary key'], ['occurred_at_utc', 'datetime2(7)', 'Event time'], ['actor_type', 'varchar(20)', 'user, service, system'], ['actor_id', 'nvarchar(200)', 'Authenticated identity'], ['action_name', 'nvarchar(100)', 'Action such as model.approved'], ['entity_type', 'nvarchar(100)', 'Affected entity category'], ['entity_id', 'nvarchar(200)', 'Affected record ID'], ['request_id', 'nvarchar(100)', 'Link to HTTP activity'], ['run_id', 'nvarchar(200)', 'Related run when applicable'], ['details_json', 'nvarchar(max)', 'Safe reason and before/after summary'], ['client_address', 'varchar(64)', 'Origin subject to retention'],
], example: { event_id: '8C3A…', occurred_at_utc: '2026-08-05T22:00:00Z', actor_type: 'user', actor_id: 'admin1', action_name: 'model.approved', entity_type: 'model_revision', entity_id: 'expression-demo-r3', request_id: 'REQ-492', run_id: null, details_json: '{ change_note: Updated lookup range }' }, note: 'Append-only SQL Server ledger: the application may insert and read, but may not update or delete.' });

tableSlide({ page: 13, name: 'monitoring.operational_events', purpose: 'HTTP health, performance, and application errors', status: 'new', columns: [
  ['event_id', 'uniqueidentifier', 'Operational event key; primary key'], ['occurred_at_utc', 'datetime2(7)', 'Event time'], ['severity', 'varchar(10)', 'INFO, WARN, ERROR, FATAL'], ['event_name', 'nvarchar(100)', 'Event category'], ['service_instance_id', 'nvarchar(200)', 'Which application server'], ['application_version', 'nvarchar(50)', 'Deployed build'], ['request_id', 'nvarchar(100)', 'HTTP request link'], ['trace_id', 'varchar(64)', 'Optional OpenTelemetry trace link'], ['run_id', 'nvarchar(200)', 'Nullable model-run link'], ['actor_id', 'nvarchar(200)', 'Resolved user/service'], ['http_method', 'varchar(10)', 'GET, POST, PUT, DELETE'], ['route', 'nvarchar(500)', 'Sanitized route template'], ['status_code', 'int', 'HTTP result'], ['duration_ms', 'bigint', 'Request duration'], ['response_bytes', 'bigint', 'Response size'], ['error_code', 'nvarchar(100)', 'Stable error category'], ['error_message', 'nvarchar(2000)', 'Safe redacted message'], ['client_address', 'varchar(64)', 'Origin subject to retention'],
], example: { event_id: '0F12…', occurred_at_utc: '2026-08-05T22:10:01Z', severity: 'INFO', event_name: 'http_request_completed', service_instance_id: 'r2r-server-02', application_version: '2.0.0', request_id: 'REQ-500', trace_id: '4bf92f3577b34da6a3ce929d0e0e4736', run_id: 'RUN-10428', actor_id: 'engineer1', http_method: 'POST', route: '/api/v1/runs/prepare', status_code: 200, duration_ms: 14, response_bytes: 842, error_code: null, error_message: null, client_address: '10.20.1.15' } });

tableSlide({ page: 14, name: 'r2r.user_sessions', purpose: 'Shared sessions for multiple application servers', status: 'new', columns: [
  ['session_id_hash', 'binary(32)', 'Hash of token; primary key'], ['user_id', 'nvarchar(200)', 'Authenticated R2R user'], ['created_at_utc', 'datetime2(3)', 'Session creation'], ['expires_at_utc', 'datetime2(3)', 'Automatic expiry'], ['revoked_at_utc', 'datetime2(3)', 'Manual revocation'], ['last_seen_at_utc', 'datetime2(3)', 'Last activity'], ['client_address', 'varchar(64)', 'Optional security context'], ['user_agent_hash', 'binary(32)', 'Optional client fingerprint'],
], example: { session_id_hash: '0x91AB…', user_id: 'USR-1007', created_at_utc: '2026-08-05T20:00:00Z', expires_at_utc: '2026-08-05T22:00:00Z', revoked_at_utc: null, last_seen_at_utc: '2026-08-05T21:42:00Z', client_address: '10.20.1.15', user_agent_hash: '0x733B…' }, note: 'Required for the current cookie-session design across two servers. Omit only if company identity supplies stateless tokens. Never store the raw token.' });

// Role management
{
  const s = shell('How user-role management works', 'Production authorization', nextPage++);
  const flow = [
    ['Company identity', 'Authenticates the person'], ['r2r.users', 'Maps login to one role'], ['r2r.roles', 'Defines four protected roles'], ['Audit ledger', 'Records assignments and changes'],
  ];
  flow.forEach((f, i) => {
    const x = 0.6 + i * 3.13;
    s.addShape(pptx.ShapeType.roundRect, { x, y: 1.75, w: 2.65, h: 1.1, rectRadius: 0.05, fill: { color: i === 2 ? C.greenPale : C.pale }, line: { color: i === 2 ? 'B8DDCA' : 'C5DFE8' } });
    s.addText(f[0], { x: x + 0.12, y: 2.02, w: 2.41, h: 0.26, fontSize: 14, bold: true, color: i === 2 ? C.green : C.blue, align: 'center', margin: 0 });
    s.addText(f[1], { x: x + 0.12, y: 2.46, w: 2.41, h: 0.2, fontSize: 9.5, color: C.muted, align: 'center', margin: 0, fit: 'shrink' });
    if (i < flow.length - 1) s.addShape(pptx.ShapeType.chevron, { x: x + 2.71, y: 2.12, w: 0.3, h: 0.34, fill: { color: C.cyan }, line: { color: C.cyan } });
  });
  const matrix = [
    ['Role', 'View', 'Create/edit draft', 'Simulate', 'Approve', 'Manage users/registry'],
    ['viewer', 'Yes', 'No', 'No', 'No', 'No'],
    ['engineer', 'Yes', 'Yes', 'Yes', 'No', 'No'],
    ['engineering_manager', 'Yes', 'No', 'Yes', 'Yes', 'No'],
    ['admin', 'Yes', 'Yes', 'Yes', 'Yes', 'Yes'],
  ];
  s.addTable(matrix, { x: 0.92, y: 3.55, w: 11.45, h: 1.75, border: { type: 'solid', color: C.line, pt: 0.7 }, fill: C.white, color: C.ink, fontSize: 10.5, margin: 0.08, align: 'center', valign: 'mid', rowH: 0.42 });
  s.addShape(pptx.ShapeType.roundRect, { x: 1.0, y: 6.1, w: 11.25, h: 0.65, rectRadius: 0.04, fill: { color: C.amberPale }, line: { color: 'E9CF9C' } });
  s.addText('Administrators change users.role_name. Every assignment, removal, disable, and re-enable operation must create an audit_ledger_events record.', { x: 1.27, y: 6.27, w: 10.72, h: 0.3, fontSize: 11.5, bold: true, color: C.amber, align: 'center', margin: 0, fit: 'shrink' });
}

// 15 — temporal history
{
  const s = shell('SQL Server-managed temporal history tables', 'Automatic prior versions', nextPage++);
  const tables = [
    ['r2r.model_revisions_history', 'Mirrors model_revisions', 'Shows how a revision row changed over time'],
    ['r2r.runs_history', 'Mirrors runs', 'Shows status and feedback transitions'],
    ['r2r.adaptive_states_history', 'Mirrors adaptive_states', 'Shows every learned-value change'],
  ];
  tables.forEach((t, i) => {
    const y = 1.78 + i * 1.28;
    s.addShape(pptx.ShapeType.roundRect, { x: 0.78, y, w: 3.55, h: 0.82, rectRadius: 0.04, fill: { color: C.pale }, line: { color: 'C5DFE8' } });
    s.addText(t[0], { x: 0.95, y: y + 0.27, w: 3.2, h: 0.23, fontFace: 'Aptos Mono', fontSize: 12, bold: true, color: C.blue, align: 'center', margin: 0, fit: 'shrink' });
    s.addText(t[1], { x: 4.7, y: y + 0.05, w: 2.25, h: 0.27, fontSize: 12, bold: true, color: C.navy, margin: 0 });
    s.addText(t[2], { x: 7.08, y: y + 0.05, w: 5.2, h: 0.42, fontSize: 12, color: C.muted, margin: 0, fit: 'shrink' });
  });
  s.addShape(pptx.ShapeType.roundRect, { x: 1.0, y: 5.93, w: 11.2, h: 0.75, rectRadius: 0.04, fill: { color: C.greenPale }, line: { color: 'B8DDCA' } });
  s.addText('Example query: SELECT * FROM r2r.runs FOR SYSTEM_TIME ALL WHERE run_id = N\'RUN-10428\';', { x: 1.32, y: 6.12, w: 10.55, h: 0.25, fontFace: 'Aptos Mono', fontSize: 11.5, bold: true, color: C.green, align: 'center', margin: 0, fit: 'shrink' });
  s.addText('SQL Server creates and maintains these history rows. The application should not write to them directly.', { x: 1.1, y: 6.82, w: 11.1, h: 0.24, fontSize: 11.5, color: C.muted, align: 'center', margin: 0 });
}

// 16 — relationships
{
  const s = shell('How the main tables connect', 'Relationship overview', nextPage++);
  const nodes = [
    [0.55, 1.85, 2.15, 'input_definitions', 'Allowed inputs'],
    [3.12, 1.85, 2.15, 'model_families', 'Logical model'],
    [5.7, 1.85, 2.15, 'model_revisions', 'Frozen revision'],
    [8.28, 1.85, 2.15, 'runs', 'Execution'],
    [10.85, 1.85, 2.0, 'run_outputs', 'Returned values'],
    [8.28, 4.15, 2.15, 'run_parameter_values', 'All parameter values'],
    [5.7, 4.15, 2.15, 'adaptive_states', 'Learned values'],
    [3.12, 4.15, 2.15, 'audit_ledger_events', 'Protected receipts'],
    [0.55, 4.15, 2.15, 'operational_events', 'Health + performance'],
  ];
  nodes.forEach((n, i) => {
    s.addShape(pptx.ShapeType.roundRect, { x: n[0], y: n[1], w: n[2], h: 1.05, rectRadius: 0.05, fill: { color: i === 3 ? C.navy : i > 4 ? C.greenPale : C.pale }, line: { color: i === 3 ? C.navy : i > 4 ? 'B8DDCA' : 'C5DFE8' } });
    s.addText(n[3], { x: n[0] + 0.1, y: n[1] + 0.23, w: n[2] - 0.2, h: 0.26, fontFace: 'Aptos Mono', fontSize: 10.5, bold: true, color: i === 3 ? C.white : i > 4 ? C.green : C.blue, align: 'center', margin: 0, fit: 'shrink' });
    s.addText(n[4], { x: n[0] + 0.1, y: n[1] + 0.65, w: n[2] - 0.2, h: 0.18, fontSize: 9, color: i === 3 ? 'C9D9E2' : C.muted, align: 'center', margin: 0 });
  });
  [[2.72,2.2],[5.30,2.2],[7.88,2.2],[10.45,2.2]].forEach(a => s.addShape(pptx.ShapeType.chevron, { x:a[0], y:a[1], w:0.32, h:0.35, fill:{color:C.cyan}, line:{color:C.cyan} }));
  s.addShape(pptx.ShapeType.line, { x: 9.35, y: 2.95, w: 0, h: 1.15, line: { color: C.green, width: 1.5, endArrowType: 'triangle' } });
  s.addShape(pptx.ShapeType.line, { x: 8.27, y: 4.67, w: -0.37, h: 0, line: { color: C.green, width: 1.5, endArrowType: 'triangle' } });
  s.addText('request_id and run_id connect operational events, business records, and audit receipts without copying sensitive values into every table.', { x: 0.95, y: 6.45, w: 11.45, h: 0.47, fontSize: 13, bold: true, color: C.navy, align: 'center', margin: 0, fit: 'shrink' });
}

// 17 — implementation status
{
  const s = shell('What remains before this is production-ready', 'Implementation status', nextPage++);
  const rows = [
    ['Already defined', 'schema_migrations, model_families, model_revisions, data_sources, runs, run_outputs, adaptive_states, audit_ledger_events'],
    ['New migration', 'roles, users, user_sessions, input_definitions, run_parameter_values, run_data_samples, run_feedback_values, runs.request_id, and monitoring.operational_events'],
    ['Backend work', 'Replace data/config.json and in-memory users/sessions with SQL Server; write business + audit records in one transaction'],
    ['Operations work', 'Backups, retention, least privilege, encryption, monitoring, alerting, and scheduled ledger verification'],
  ];
  rows.forEach((r, i) => {
    const y = 1.78 + i * 1.16;
    badge(s, r[0], 0.78, y + 0.1, i === 0 ? 'required' : i === 1 ? 'new' : 'optional', 1.8);
    s.addText(r[1], { x: 3.0, y, w: 9.1, h: 0.62, fontSize: 12.5, color: C.muted, margin: 0, fit: 'shrink' });
  });
  s.addShape(pptx.ShapeType.roundRect, { x: 0.9, y: 6.43, w: 11.55, h: 0.57, rectRadius: 0.04, fill: { color: C.redPale }, line: { color: 'E9BABA' } });
  s.addText('Applying the SQL migrations is not sufficient by itself—the Go backend must actually use the tables and enforce the same transaction, validation, and security rules.', { x: 1.18, y: 6.58, w: 11.0, h: 0.3, fontSize: 11.5, bold: true, color: C.red, align: 'center', margin: 0, fit: 'shrink' });
}

pptx.writeFile({ fileName: 'docs/R2R_Production_SQL_Server_Tables.pptx' });
