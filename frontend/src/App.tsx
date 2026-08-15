import { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  BookOpen,
  Check,
  CircleAlert,
  ChevronDown,
  ChevronRight,
  Clock3,
  Code2,
  Copy,
  Database,
  Download,
  FileClock,
  FlaskConical,
  Gauge,
  GitBranch,
  LogOut,
  Menu,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
  Upload,
  UserPlus,
  Users,
  X,
} from 'lucide-react';
import ModelEditor, { DependencyDialog } from './ModelEditor';
import tdkLogo from './assets/tdklogo.png';

type Status = 'approved' | 'draft' | 'pending_approval' | 'superseded';
type Adaptive = {
  initialValue: number;
  updateMethod: 'ewma' | 'formula';
  updateInput?: { source: 'parameter' | 'post-run-feedback'; name: string };
  updateParameter?: string;
  measurementParameter?: string;
  alpha?: number;
  updateFormula?: string;
  deadband?: number;
  maxAdjustment?: number;
};
type LogicCondition = {
  inputParameter: string;
  inputSource?: 'index' | 'external' | 'parameter' | 'data-sample';
  operator:
    'equals' | 'not-equals' | 'greater-than' | 'greater-or-equal' | 'less-than' | 'less-or-equal';
  compareValue: string;
  compareSource?: 'constant' | 'index' | 'external' | 'parameter' | 'data-sample';
  resultValue: string;
  and?: LogicPredicate[];
};
type LogicPredicate = Omit<LogicCondition, 'resultValue' | 'and'>;
type Logic = { conditions: LogicCondition[]; elseValue: string };
type Lookup = {
  sample: string;
  matchColumn: string;
  operator: 'equals' | 'contains';
  inputSource: 'constant' | 'index' | 'external' | 'parameter';
  matchValue: string;
  returnColumn: string;
};
type Parameter = {
  key: string;
  description?: string;
  unit: string;
  dataType?: 'string' | 'number' | 'boolean';
  source:
    'expression' | 'sql' | 'constant' | 'formula' | 'external' | 'data-sample' | 'logic' | 'lookup';
  dataSourceId?: string;
  definition: string;
  lsl?: number;
  usl?: number;
  limitAction?: 'warn' | 'clamp' | 'reject' | 'fallback';
  fallbackValue?: number;
  adaptive?: Adaptive;
  logic?: Logic;
  lookup?: Lookup;
};
type ExternalInput = {
  name: string;
  description?: string;
  type: 'string' | 'number' | 'boolean';
};
type FeedbackInput = ExternalInput & { required: boolean };
type ExecutionPolicy = { maxUnfinishedRuns: number; partitionBy?: string[] };
type DataSampleColumn = { name: string; type: 'string' | 'number' | 'boolean' };
type DataSample = {
  key: string;
  description?: string;
  source: 'sql' | 'static';
  query: string;
  columns?: DataSampleColumn[];
  rows?: Record<string, string | number | boolean>[];
  limit: number;
};
type Source = {
  id: string;
  name: string;
  type: string;
  engine: 'postgres' | 'mysql' | 'sqlserver';
  host: string;
  port: number;
  database: string;
  username?: string;
  secretRef?: string;
  tlsMode: string;
  timeoutSeconds: number;
  location: string;
  enabled: boolean;
  lastTestStatus?: string;
  lastTestMessage?: string;
  lastTestAt?: string;
  lastTestMs?: number;
};
type InputDefinition = {
  name: string;
  displayName: string;
  category: 'index' | 'external';
  dataType: 'string' | 'number' | 'boolean';
  description?: string;
  status: 'active' | 'deprecated';
  system?: boolean;
};
const newSource = (): Source => ({
  id: '',
  name: '',
  type: 'sql',
  engine: 'sqlserver',
  host: '',
  port: 1433,
  database: '',
  username: '',
  secretRef: '',
  tlsMode: 'require',
  timeoutSeconds: 5,
  location: '',
  enabled: false,
});
type Model = {
  id: string;
  familyId: string;
  name: string;
  owner: string;
  revision: number;
  status: Status;
  changeNote: string;
  createdBy: string;
  createdAt: string;
  submittedBy?: string;
  submittedAt?: string;
  rejectedBy?: string;
  rejectedAt?: string;
  rejectionReason?: string;
  approvedBy?: string;
  approvedAt?: string;
  description: string;
  indexes: Record<string, string[]>;
  externalInputs: ExternalInput[];
  feedbackInputs?: FeedbackInput[];
  executionPolicy?: ExecutionPolicy;
  parameters: Parameter[];
  dataSamples: DataSample[];
  outputs: string[];
  enabled: boolean;
};
type ConfigUser = { employeeId: string; loginId: string; role: User['role']; status: 'active' | 'disabled' };
type Config = { models: Model[]; sources: Source[]; inputDefinitions: InputDefinition[]; users?: ConfigUser[] };
type ActivityRow = {
  id: string;
  task: string;
  model: string;
  modelId: string;
  familyId: string;
  reason: string;
  time: string;
  status: string;
  parameters: Record<string, unknown>;
  outputs: { parameter: string; value: unknown; unit?: string }[];
};
type RunRecord = {
  runId: string;
  preparedAt: string;
  completedAt?: string;
  status: string;
  modelRevision: number;
  outputs: { parameter: string; value: unknown }[];
  feedback?: Record<string, unknown>;
};
type ChartSeriesOption = {
  key: string;
  label: string;
  category: 'Output' | 'Post-run feedback';
  points: { label: string; value: number }[];
};
type User = {
  employeeId: string;
  username: string;
  role: 'viewer' | 'engineer' | 'engineering_manager' | 'admin';
  directReports?: string[];
};
type RegisteredUser = User & {
  employeeId: string;
  email?: string;
  status?: 'active' | 'disabled';
};
const newModel = (): Model => ({
  id: '',
  familyId: '',
  name: '',
  owner: '',
  revision: 0,
  status: 'draft',
  changeNote: '',
  createdBy: '',
  createdAt: '',
  description: '',
  indexes: { machine: [], action: [], material: [] },
  externalInputs: [],
  feedbackInputs: [],
  executionPolicy: { maxUnfinishedRuns: 1, partitionBy: ['machine'] },
  parameters: [],
  dataSamples: [],
  outputs: [],
  enabled: false,
});
async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  const data = await response.json();
  if (response.status === 401 && path !== '/api/login' && path !== '/api/session') {
    window.dispatchEvent(new CustomEvent('r2r:session-expired'));
    throw new Error('Your session expired. Please sign in again.');
  }
  if (!response.ok) throw new Error(data.error || 'Request failed');
  return data;
}
const formatDate = (value: string) =>
  new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' }).format(
    new Date(value),
  );

export default function App() {
  const [config, setConfig] = useState<Config>();
  const [activity, setActivity] = useState<ActivityRow[]>([]);
  const [view, setView] = useState('overview');
  const [mobile, setMobile] = useState(false);
  const [selected, setSelected] = useState<Model>();
  const [importingModel, setImportingModel] = useState(false);
  const [editingSource, setEditingSource] = useState<Source>();
  const [editingInput, setEditingInput] = useState<InputDefinition>();
  const [user, setUser] = useState<User>();
  const [sessionMessage, setSessionMessage] = useState('');
  const [checkingSession, setCheckingSession] = useState(true);
  const [overviewScope, setOverviewScope] = useState<'relevant' | 'all'>('relevant');
  const [executionModel, setExecutionModel] = useState('');
  const [executionStatus, setExecutionStatus] = useState('');
  const load = async () => {
    setConfig(await api('/api/config'));
    setActivity(await api('/api/runs/recent'));
  };
  useEffect(() => {
    api<User>('/api/session')
      .then((current) => {
        setUser(current);
        return load();
      })
      .catch(() => undefined)
      .finally(() => setCheckingSession(false));
  }, []);
  useEffect(() => {
    const handleSessionExpired = () => {
      setUser(undefined);
      setConfig(undefined);
      setSelected(undefined);
      setEditingSource(undefined);
      setSessionMessage('Your session expired. Sign in again to continue.');
    };
    window.addEventListener('r2r:session-expired', handleSessionExpired);
    return () => window.removeEventListener('r2r:session-expired', handleSessionExpired);
  }, []);
  const approved = useMemo(
    () => config?.models.filter((m) => m.status === 'approved') || [],
    [config],
  );
  const overviewModels = useMemo(() => {
    const models = config?.models || [];
    if (user?.role === 'viewer') return models.filter((model) => model.status === 'approved');
    if (overviewScope === 'all' || user?.role === 'admin') return models;
    const relevantEmployees = new Set([user?.employeeId, ...(user?.directReports || [])].filter(Boolean));
    return models.filter((model) =>
      [model.owner, model.createdBy, model.submittedBy].some((employeeId) =>
        relevantEmployees.has(employeeId),
      ),
    );
  }, [config, overviewScope, user]);
  const overviewFamilyIDs = useMemo(
    () => new Set(overviewModels.map((model) => model.familyId)),
    [overviewModels],
  );
  const overviewActivity = useMemo(
    () =>
      activity.filter(
        (row) =>
          (overviewScope === 'all' || user?.role === 'admin' || overviewFamilyIDs.has(row.familyId)) &&
          (!executionModel || row.familyId === executionModel) &&
          (!executionStatus || row.status === executionStatus),
      ),
    [activity, executionModel, executionStatus, overviewFamilyIDs, overviewScope, user],
  );
  const controlModels = useMemo(() => {
    const models = config?.models || [];
    const byFamily = new Map<string, Model>();
    const priority: Record<Status, number> = {
      approved: 4,
      pending_approval: 3,
      draft: 2,
      superseded: 1,
    };
    for (const model of models) {
      const current = byFamily.get(model.familyId);
      if (
        !current ||
        priority[model.status] > priority[current.status] ||
        (model.status === current.status && model.revision > current.revision)
      ) {
        byFamily.set(model.familyId, model);
      }
    }
    return [...byFamily.values()];
  }, [config]);
  const nav: [string, string, typeof Gauge][] = [
    ['overview', 'Overview', Gauge],
    ['models', 'Control models', GitBranch],
    ['sources', 'Data sources', Database],
    ['inputs', 'Input registry', SlidersHorizontal],
    ...(user?.role === 'admin' ? ([['users', 'User management', Users]] as [string, string, typeof Gauge][]) : []),
    ['simulation', 'Simulation', FlaskConical],
    ['documentation', 'Documentation', BookOpen],
  ];
  if (checkingSession) return <div className="session-loading">Loading workspace…</div>;
  if (!user)
    return (
      <Login
        message={sessionMessage}
        onLogin={async (current) => {
          setSessionMessage('');
          setUser(current);
          await load();
        }}
      />
    );
  return (
    <div className="shell">
      <aside className={mobile ? 'sidebar open' : 'sidebar'}>
        <div className="brand">
          <img className="tdk-logo" src={tdkLogo} alt="TDK" />
          <div>
            R2R<small>PROCESS CONTROL</small>
          </div>
        </div>
        <nav>
          {nav.map(([id, label, Icon]) => (
            <button
              key={id}
              className={view === id ? 'active' : ''}
              onClick={() => {
                setView(id);
                setMobile(false);
              }}
            >
              <Icon size={18} />
              <span>{label}</span>
              {view === id && <ChevronRight size={15} />}
            </button>
          ))}
        </nav>
        <div className="service">
          <span></span>
          <div>
            {user.username}
            <small>{user.role.replaceAll('_', ' ').toUpperCase()}</small>
          </div>
        </div>
      </aside>
      <main>
        <header>
          <button className="menu" onClick={() => setMobile(!mobile)}>
            <Menu />
          </button>
          <div className="env">
            <span></span>Production workspace
          </div>
          <button
            className="session-user"
            title="Sign out"
            onClick={async () => {
              await api('/api/logout', { method: 'POST' });
              setUser(undefined);
              setConfig(undefined);
            }}
          >
            <span>{user.username.slice(0, 2).toUpperCase()}</span>
            <div>
              {user.username}
              <small>{user.role.replaceAll('_', ' ')}</small>
            </div>
            <LogOut />
          </button>
        </header>
        {view === 'overview' && (
          <section className="page">
            <PageTitle
              eyebrow="RUN-TO-RUN CONTROL"
              title="Model execution overview"
              subtitle="Models, revision work, approvals, and executions in one shared workspace."
              action={
                <label className="overview-scope">
                  View
                  <select
                    value={overviewScope}
                    onChange={(event) => setOverviewScope(event.target.value as 'relevant' | 'all')}
                  >
                    <option value="relevant">Relevant to me</option>
                    <option value="all">All available</option>
                  </select>
                </label>
              }
            />
            <div className="metrics">
              <Metric
                label="Active models"
                value={overviewModels.filter((model) => model.status === 'approved' && model.enabled).length}
                detail="Eligible for execution"
                icon={<ShieldCheck />}
              />
              <Metric
                label="Draft revisions"
                value={overviewModels.filter((model) => model.status === 'draft').length}
                detail="Work in progress"
                icon={<FileClock />}
              />
              <Metric
                label="Pending approvals"
                value={overviewModels.filter((model) => model.status === 'pending_approval').length}
                detail="Waiting for review"
                icon={<Clock3 />}
              />
              <Metric
                label="Failed executions"
                value={overviewActivity.filter((row) => row.status.includes('failed')).length}
                detail="Within recent activity"
                icon={<CircleAlert />}
              />
            </div>
            <div className="dashboard-grid">
              <Panel
                title="Recent executions"
                subtitle="Latest model runs with resolved parameter values"
                action={
                  <button className="icon" onClick={load}>
                    <RefreshCw />
                  </button>
                }
              >
                <div className="execution-filters">
                  <select value={executionModel} onChange={(event) => setExecutionModel(event.target.value)}>
                    <option value="">All models</option>
                    {[...new Map(activity.map((row) => [row.familyId, row.model.replace(/ · Rev \d+$/, '')])).entries()].map(
                      ([familyId, name]) => <option key={familyId} value={familyId}>{name}</option>,
                    )}
                  </select>
                  <select value={executionStatus} onChange={(event) => setExecutionStatus(event.target.value)}>
                    <option value="">All statuses</option>
                    <option value="completed">Completed</option>
                    <option value="prepared">Prepared</option>
                    <option value="run_failed">Failed</option>
                    <option value="run_cancelled">Cancelled</option>
                  </select>
                </div>
                {overviewActivity.length ? (
                  <div className="activity-list execution-activity-list">
                    {overviewActivity.map((row) => (
                      <details className="execution-activity" key={row.id}>
                        <summary className="activity-row">
                          <div>
                            <strong>{row.task}</strong>
                            <small>{row.reason} · {formatDate(row.time)}</small>
                          </div>
                          <div className="execution-summary-tags">
                            <span className={`status ${row.status === 'completed' ? 'approved' : row.status.includes('failed') ? 'draft' : 'pending_approval'}`}>
                              {row.status.replace('run_', '').toUpperCase()}
                            </span>
                            <span>{row.model}</span>
                          </div>
                        </summary>
                        <div className="execution-parameters">
                          <div className="execution-parameter-head"><span>Parameter</span><span>Resolved value</span></div>
                          {Object.entries(row.parameters || {}).map(([name, value]) => (
                            <div key={name}><strong>{name}</strong><code>{formatParameterValue(value)}</code></div>
                          ))}
                        </div>
                      </details>
                    ))}
                  </div>
                ) : (
                  <Empty
                    icon={<Activity />}
                    title="No model executions yet"
                    text="Results appear after an external application prepares a run."
                  />
                )}
              </Panel>
              <Panel title="Revision queue" subtitle="Drafts and changes requiring approval">
                {overviewModels.filter((model) => model.status === 'draft' || model.status === 'pending_approval').length ? (
                  <div className="revision-summary">
                    {overviewModels.filter((model) => model.status === 'draft' || model.status === 'pending_approval').map((m) => (
                      <button key={m.id} onClick={() => setSelected(m)}>
                        <span className={`status ${m.status}`}>{m.status === 'draft' ? 'DRAFT' : 'PENDING'}</span>
                        <div>
                          <strong>
                            {m.name} · Rev {m.revision}
                          </strong>
                          <small>{m.changeNote}</small>
                        </div>
                        <ChevronRight />
                      </button>
                    ))}
                  </div>
                ) : (
                  <Empty
                    icon={<Check />}
                    title="No pending revisions"
                    text="All model families are on approved revisions."
                  />
                )}
              </Panel>
            </div>
            <section className="api-flow">
              <div>
                <span>1</span>
                <strong>Receive indexes</strong>
                <p>Machine, action, material, and optional indexes.</p>
              </div>
              <div>
                <span>2</span>
                <strong>Load active revision</strong>
                <p>The most specific approved model is selected.</p>
              </div>
              <div>
                <span>3</span>
                <strong>Resolve parameters</strong>
                <p>Values come from the caller or configured database.</p>
              </div>
              <div>
                <span>4</span>
                <strong>Calculate outputs</strong>
                <p>Every model-defined output is returned.</p>
              </div>
            </section>
          </section>
        )}
        {view === 'models' && (
          <ControlModels
            models={controlModels}
            registeredUsers={config?.users || []}
            canCreate={
              user.role === 'engineer' || user.role === 'engineering_manager' || user.role === 'admin'
            }
            onCreate={() => setSelected(newModel())}
            onImport={() => setImportingModel(true)}
            onOpen={setSelected}
          />
        )}
        {view === 'simulation' && (
          <Simulation models={approved.filter((model) => model.enabled)} />
        )}
        {view === 'documentation' && <Documentation />}
        {view === 'users' && user.role === 'admin' && <UserManagement />}
        {view === 'inputs' && (
          <InputRegistry
            definitions={config?.inputDefinitions || []}
            role={user.role}
            onEdit={setEditingInput}
            onCreate={() =>
              setEditingInput({
                name: '',
                displayName: '',
                category: 'external',
                dataType: 'string',
                description: '',
                status: 'active',
              })
            }
          />
        )}
        {view === 'sources' && (
          <section className="page">
            <PageTitle
              eyebrow="PARAMETER SOURCES"
              title="Data sources"
              subtitle="Preconfigured connections available to SQL parameters."
              action={
                user.role === 'admin' ? (
                  <button className="primary" onClick={() => setEditingSource(newSource())}>
                    <Plus /> Add data source
                  </button>
                ) : undefined
              }
            />
            <div className="source-grid">
              {config?.sources.map((source) => (
                <article className="source" key={source.id}>
                  <Database />
                  <span className={source.enabled ? 'status approved' : 'status superseded'}>
                    {source.enabled ? 'ACTIVE' : 'NOT CONFIGURED'}
                  </span>
                  <h3>{source.name}</h3>
                  <p>{source.location}</p>
                  <div className="source-foot">
                    <small>{source.type.toUpperCase()}</small>
                    {user.role === 'admin' && (
                      <button className="secondary" onClick={() => setEditingSource(source)}>
                        Edit
                      </button>
                    )}
                  </div>
                </article>
              ))}
              {!config?.sources.length && (
                <div className="source-empty">
                  <Database />
                  <strong>No data sources configured</strong>
                  <p>
                    Add the first external SQL connection to make SQL queries available to models.
                  </p>
                </div>
              )}
            </div>
            <div className="notice">
              <Database />
              <div>
                <strong>Database adapters are deployment-specific</strong>
                <p>
                  Connection credentials remain deployment-managed and are not exposed through this
                  interface.
                </p>
              </div>
            </div>
          </section>
        )}
      </main>
      {selected && (
        <ModelDrawer
          model={selected}
          sources={config?.sources || []}
          inputDefinitions={config?.inputDefinitions || []}
          role={user.role}
          onClose={() => setSelected(undefined)}
          onChanged={async () => {
            await load();
            setSelected(undefined);
          }}
        />
      )}
      {importingModel && (
        <ModelImportDialog
          onClose={() => setImportingModel(false)}
          onImported={async (model) => {
            await load();
            setImportingModel(false);
            setSelected(model);
          }}
        />
      )}
      {editingSource && (
        <SourceEditor
          source={editingSource}
          onClose={() => setEditingSource(undefined)}
          onSaved={async () => {
            await load();
            setEditingSource(undefined);
          }}
        />
      )}
      {editingInput && (
        <InputDefinitionEditor
          definition={editingInput}
          onClose={() => setEditingInput(undefined)}
          onSaved={async () => {
            await load();
            setEditingInput(undefined);
          }}
        />
      )}
    </div>
  );
}

function ControlModels({
  models,
  registeredUsers,
  canCreate,
  onCreate,
  onImport,
  onOpen,
}: {
  models: Model[];
  registeredUsers: ConfigUser[];
  canCreate: boolean;
  onCreate: () => void;
  onImport: () => void;
  onOpen: (model: Model) => void;
}) {
  const [search, setSearch] = useState('');
  const [machine, setMachine] = useState('');
  const [action, setAction] = useState('');
  const [material, setMaterial] = useState('');

  const indexValues = (model: Model, key: string) =>
    Object.entries(model.indexes).find(([name]) => name.toLowerCase() === key)?.[1] || [];
  const optionsFor = (key: string) =>
    [...new Set(models.flatMap((model) => indexValues(model, key)).filter(Boolean))].sort((a, b) =>
      a.localeCompare(b),
    );

  const machines = useMemo(() => optionsFor('machine'), [models]);
  const actions = useMemo(() => optionsFor('action'), [models]);
  const materials = useMemo(() => optionsFor('material'), [models]);
  const filteredModels = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    return models.filter(
      (model) =>
        (!query || model.name.toLocaleLowerCase().includes(query)) &&
        (!machine || indexValues(model, 'machine').includes(machine)) &&
        (!action || indexValues(model, 'action').includes(action)) &&
        (!material || indexValues(model, 'material').includes(material)),
    );
  }, [models, search, machine, action, material]);
  const hasFilters = Boolean(search || machine || action || material);
  const clearFilters = () => {
    setSearch('');
    setMachine('');
    setAction('');
    setMaterial('');
  };

  return (
    <section className="page">
      <PageTitle
        eyebrow="MODEL OWNERSHIP"
        title="Control models"
        subtitle="Calculation logic, processing parameter contracts, outputs, and revisions are owned by each model."
        action={
          canCreate ? (
            <div className="page-title-actions">
              <button className="secondary" onClick={onImport}>
                <Upload />
                Import JSON
              </button>
              <button className="primary" onClick={onCreate}>
                <Plus />
                Create model
              </button>
            </div>
          ) : undefined
        }
      />
      <section className="model-filters" aria-label="Filter control models">
        <label className="model-search">
          <Search aria-hidden="true" />
          <span className="sr-only">Search by model name</span>
          <input
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search model name"
          />
        </label>
        <div className="model-filter-selects">
          <FilterSelect label="Machine" value={machine} options={machines} onChange={setMachine} />
          <FilterSelect label="Action" value={action} options={actions} onChange={setAction} />
          <FilterSelect
            label="Material"
            value={material}
            options={materials}
            onChange={setMaterial}
          />
        </div>
        <div className="model-filter-summary">
          <span>
            {filteredModels.length} of {models.length} models
          </span>
          {hasFilters && (
            <button type="button" onClick={clearFilters}>
              <X />
              Clear filters
            </button>
          )}
        </div>
      </section>
      {filteredModels.length ? (
        <div className="model-list">
          {filteredModels.map((model) => (
            <ModelRow
              key={model.id}
              model={model}
              ownerInactive={
                Boolean(model.owner) &&
                !registeredUsers.some(
                  (registered) =>
                    registered.employeeId.toLowerCase() === model.owner.toLowerCase() &&
                    registered.status === 'active',
                )
              }
              onOpen={() => onOpen(model)}
            />
          ))}
        </div>
      ) : (
        <div className="model-filter-empty">
          <SlidersHorizontal />
          <strong>No matching control models</strong>
          <p>Adjust the model name or index filters to see more results.</p>
          <button className="secondary" onClick={clearFilters}>
            Clear filters
          </button>
        </div>
      )}
    </section>
  );
}

const modelImportExample = `{
  "name": "Imported control model",
  "description": "Created from a JSON definition",
  "indexes": {
    "machine": ["PROCESS-01"],
    "action": ["CALCULATE"],
    "material": ["PRODUCT_A"]
  },
  "parameters": [
    {
      "key": "result",
      "dataType": "number",
      "unit": "nm",
      "source": "expression",
      "definition": "1 + 1"
    }
  ],
  "dataSamples": [],
  "outputs": ["result"],
  "changeNote": "Initial JSON import"
}`;

function ModelImportDialog({
  onClose,
  onImported,
}: {
  onClose: () => void;
  onImported: (model: Model) => Promise<void>;
}) {
  const [definition, setDefinition] = useState(modelImportExample);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const importModel = async () => {
    setError('');
    let parsed: unknown;
    try {
      parsed = JSON.parse(definition);
    } catch (parseError) {
      setError(`Invalid JSON: ${(parseError as Error).message}`);
      return;
    }
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      setError('The imported JSON must contain one model object.');
      return;
    }
    setBusy(true);
    try {
      const created = await api<Model>('/api/models/import', {
        method: 'POST',
        body: JSON.stringify(parsed),
      });
      await onImported(created);
    } catch (requestError) {
      setError((requestError as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="payload-backdrop"
      onMouseDown={(event) => event.target === event.currentTarget && onClose()}
    >
      <section className="model-import-dialog" role="dialog" aria-modal="true">
        <header>
          <div>
            <p>MODEL DEFINITION</p>
            <h3>Import model JSON</h3>
          </div>
          <button className="icon" onClick={onClose} aria-label="Close import dialog">
            <X />
          </button>
        </header>
        <p>
          Paste one model definition. R2R validates it and creates a new draft. IDs, revision,
          ownership, status, and approval fields in the JSON are ignored.
        </p>
        <label>
          Model JSON
          <textarea
            value={definition}
            onChange={(event) => setDefinition(event.target.value)}
            spellCheck={false}
            autoFocus
          />
        </label>
        {error && <p className="error">{error}</p>}
        <footer>
          <button className="secondary" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button className="primary" onClick={importModel} disabled={busy || !definition.trim()}>
            <Upload />
            {busy ? 'Importing…' : 'Import as draft'}
          </button>
        </footer>
      </section>
    </div>
  );
}

const prepareExample = `POST /api/v1/runs/prepare
Content-Type: application/json

{
  "runId": "LOT-2048-W01",
  "context": {
    "machine": "PROCESS-01",
    "action": "CALCULATE",
    "material": "PRODUCT_A",
    "operation": "REWORK",
    "measured_cd": 1.5
  }
}`;

const completeExample = `POST /api/v1/runs/LOT-2048-W01/complete
Content-Type: application/json

{
  "status": "succeeded",
  "feedback": {
    "measured_thickness": 98.4
  }
}`;

function Documentation() {
  const sections = [
    ['getting-started', 'Getting started'],
    ['model-definition', 'Model definition'],
    ['parameters', 'Parameters and data'],
    ['revisions', 'Revisions and simulation'],
    ['execution-api', 'Execution API'],
    ['roles', 'Roles and permissions'],
  ] as const;
  return (
    <section className="page documentation-page">
      <PageTitle
        eyebrow="OPERATOR GUIDE"
        title="Documentation"
        subtitle="Configure, validate, approve, and execute manufacturing control models."
      />
      <div className="documentation-layout">
        <nav className="documentation-nav" aria-label="Documentation sections">
          <strong>Contents</strong>
          {sections.map(([id, label]) => (
            <a key={id} href={`#${id}`}>
              {label}
            </a>
          ))}
        </nav>
        <div className="documentation-content">
          <section id="getting-started">
            <p className="documentation-kicker">WORKFLOW</p>
            <h2>Getting started</h2>
            <ol className="documentation-steps">
              <li>
                <strong>Configure data sources</strong>
                <span>
                  An administrator adds the database connection and verifies it with Test
                  connection. Engineers select an approved connection in their model.
                </span>
              </li>
              <li>
                <strong>Create a control model</strong>
                <span>
                  Define the required machine, action, and material indexes, then add any additional
                  selection indexes.
                </span>
              </li>
              <li>
                <strong>Build the calculation</strong>
                <span>
                  Add data samples, external inputs, parameters, outputs, and post-run feedback as
                  required by the process.
                </span>
              </li>
              <li>
                <strong>Simulate the draft</strong>
                <span>
                  Run representative CSV rows and inspect parameters, outputs, and trends.
                </span>
              </li>
              <li>
                <strong>Approve the revision</strong>
                <span>
                  Approval makes the revision active for model selection. A model family has only
                  one active revision.
                </span>
              </li>
            </ol>
          </section>

          <section id="model-definition">
            <p className="documentation-kicker">MODEL SELECTION</p>
            <h2>Model definition</h2>
            <p>
              Every model requires a name and values for <code>machine</code>, <code>action</code>,
              and <code>material</code>. An index may accept multiple values, such as chambers 1, 2,
              3, and 4. The request must match one allowed value for every index defined by the
              model.
            </p>
            <div className="documentation-note">
              Keep index, external input, data sample, parameter, and feedback names unique. Use the
              dependency graph before approval to find unknown references and circular dependencies.
            </div>
          </section>

          <section id="parameters">
            <p className="documentation-kicker">CALCULATION</p>
            <h2>Parameters and data</h2>
            <div className="documentation-table">
              <div className="documentation-table-head">
                <span>Source</span>
                <span>Purpose</span>
                <span>Example</span>
              </div>
              <div>
                <strong>Expression</strong>
                <span>A typed value, calculation, condition, sample function, or lookup.</span>
                <code>IF(@is_rework, FIRST(@history.adjustment), 0)</code>
              </div>
              <div>
                <strong>SQL query</strong>
                <span>One value from a configured source. Use @name for declared inputs.</span>
                <code>@wafer_id</code>
              </div>
            </div>
            <h3>Data sample expressions</h3>
            <p>
              Numerical columns support <code>MIN</code>, <code>MAX</code>, <code>AVG</code>,{' '}
              <code>MEDIAN</code>, <code>RANGE</code>, <code>STDEV</code>, <code>SUM</code>,{' '}
              <code>COUNT</code>, <code>FIRST</code>, <code>NTH</code>, and <code>LAST</code>. Text
              and boolean columns support the three positional commands.
            </p>
            <ul>
              <li>
                <code>MIN(@sample.column_name)</code>, <code>MAX(@sample.column_name)</code>,{' '}
                <code>AVG(@sample.column_name)</code>, and <code>SUM(@sample.column_name)</code>
              </li>
              <li>
                <code>COUNT(@sample.column_name)</code>
              </li>
              <li>
                <code>FIRST(@sample.column_name)</code> and <code>LAST(@sample.column_name)</code>
              </li>
              <li>
                <code>NTH(@sample.column_name, row)</code>—for example,{' '}
                <code>NTH(@history.result, 2)</code> selects the second row
              </li>
            </ul>
            <p>
              NTH row numbers start at 1. Use a deterministic SQL <code>ORDER BY</code> whenever row
              order matters.
            </p>
            <p>
              <code>CONTAINS(@sample.text_column, "value")</code> returns a boolean and is true when
              any row contains the supplied text. Matching is case-insensitive.
            </p>
            <h3>Typed SQL schemas and lookups</h3>
            <p>
              SQL samples may declare expected text, number, and boolean columns. Every returned or
              simulation-supplied row is validated before calculations execute. Use{' '}
              <code>LOOKUP_ONE</code> when exactly one row must match or <code>LOOKUP_FIRST</code>{' '}
              when the first matching row is intentional. Filters support typed comparisons,
              <code>CONTAINS</code>, <code>AND</code>, and <code>OR</code>.
            </p>
            <h3>Limits and adaptive behavior</h3>
            <p>
              Every numerical parameter may define LSL and USL actions, including parameters that are not model outputs. Limits are checked as soon as the parameter resolves: warn keeps the value, clamp and fallback supply the value used by dependent parameters, and reject stops the run.
              Adaptive parameters preserve state between runs and may update during prepare from a
              resolved numerical parameter or during completion from required numerical feedback.
            </p>
          </section>

          <section id="revisions">
            <p className="documentation-kicker">CHANGE CONTROL</p>
            <h2>Revisions and simulation</h2>
            <p>
              Changes are made in a draft revision. Draft simulation executes that exact revision
              without changing production routing or persisted adaptive state. Sequential simulation
              carries preview adaptive values from one CSV row to the next.
            </p>
            <p>
              After approval, the new revision becomes active and the previous active revision is
              superseded. Use model details to inspect its dependency graph, API payload, and 14-day
              numerical run chart.
            </p>
          </section>

          <section id="execution-api">
            <p className="documentation-kicker">INTEGRATION</p>
            <h2>Execution API</h2>
            <h3>1. Prepare a run</h3>
            <p>
              The caller sends a unique run ID, model-selection indexes, and declared external
              inputs. R2R selects the active revision, resolves parameters, and returns every
              configured output.
            </p>
            <pre>{prepareExample}</pre>
            <h3>2. Complete the run</h3>
            <p>
              Every prepared run must be completed. When the model declares required post-run
              feedback, include it with a successful completion. Failed or cancelled runs may be
              completed without successful feedback.
            </p>
            <pre>{completeExample}</pre>
            <div className="documentation-note">
              A prepare request remains unfinished until completion succeeds. The model execution
              policy controls how many unfinished runs are allowed for each configured partition,
              such as a machine and chamber combination.
            </div>
          </section>

          <section id="roles">
            <p className="documentation-kicker">ACCESS</p>
            <h2>Roles and permissions</h2>
            <div className="documentation-table role-table">
              <div className="documentation-table-head">
                <span>Role</span>
                <span>Capabilities</span>
              </div>
              <div>
                <strong>Viewer</strong>
                <span>View models, run history, charts, and documentation.</span>
              </div>
              <div>
                <strong>Engineer</strong>
                <span>Create, edit, and simulate drafts, then submit them for approval.</span>
              </div>
              <div>
                <strong>Engineering manager</strong>
                <span>
                  Create and submit models; review, simulate, and approve direct reports' revisions.
                </span>
              </div>
              <div>
                <strong>Administrator</strong>
                <span>
                  Register employees and manage roles, data sources, the Input Registry, and model
                  families. Administrators may approve any pending model revision.
                </span>
              </div>
            </div>
          </section>
        </div>
      </div>
    </section>
  );
}

function FilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
}) {
  return (
    <label>
      <span>{label}</span>
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        <option value="">All {label.toLowerCase()}s</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    </label>
  );
}

function simulationCSVTemplate(model?: Model) {
  if (!model) return '';
  return [
    ...Object.keys(model.indexes),
    ...(model.externalInputs || []).map((input) => input.name),
    ...model.parameters.filter((parameter) => parameter.source === 'sql').map((parameter) => parameter.key),
    ...(model.dataSamples || [])
      .filter((sample) => sample.source === 'sql')
      .flatMap((sample) => (sample.columns || []).map((column) => `${sample.key}.${column.name}`)),
  ].join(',');
}

function Simulation({
  model,
  models = [],
  modelId = '',
  draftLabel = '',
}: {
  model?: Model;
  models?: Model[];
  modelId?: string;
  draftLabel?: string;
}) {
  const [selectedModelId, setSelectedModelId] = useState(model?.id || modelId);
  const selectedModel = model || models.find((candidate) => candidate.id === selectedModelId);
  const template = simulationCSVTemplate(selectedModel);
  const [csv, setCSV] = useState('');
  const [fileName, setFileName] = useState('');
  const [results, setResults] = useState<any[]>();
  const [exchange, setExchange] = useState<{ request: unknown; response: unknown }>();
  const [showExchange, setShowExchange] = useState(false);
  const [sequential, setSequential] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [showChart, setShowChart] = useState(false);
  const [notice, setNotice] = useState('');
  const chartSeries = useMemo<ChartSeriesOption[]>(() => {
    if (!results) return [];
    const outputNames = Array.from(
      new Set(
        results.flatMap((row) =>
          (row.outputs || [])
            .filter((output: any) => finiteNumber(output.value) !== undefined)
            .map((output: any) => String(output.parameter)),
        ),
      ),
    );
    const feedbackNames = Array.from(
      new Set(
        results.flatMap((row) =>
          Object.entries(row.feedback || {})
            .filter(([, value]) => finiteNumber(value) !== undefined)
            .map(([name]) => name),
        ),
      ),
    );
    return [
      ...outputNames.map((name) => ({
        key: `output:${name}`,
        label: name,
        category: 'Output' as const,
        points: results.flatMap((row) => {
          const output = (row.outputs || []).find((item: any) => item.parameter === name);
          const value = finiteNumber(output?.value);
          return value !== undefined ? [{ label: `Row ${row.row}`, value }] : [];
        }),
      })),
      ...feedbackNames.map((name) => ({
        key: `feedback:${name}`,
        label: name,
        category: 'Post-run feedback' as const,
        points: results.flatMap((row) =>
          finiteNumber(row.feedback?.[name]) !== undefined
            ? [{ label: `Row ${row.row}`, value: finiteNumber(row.feedback[name])! }]
            : [],
        ),
      })),
    ];
  }, [results]);
  const run = async () => {
    setBusy(true);
    setError('');
    try {
      const requestPayload = { csv, sequential, modelId: selectedModel?.id || modelId || undefined };
      const response = await api<{ rows: any[]; count: number; committed: boolean; mode: string }>('/api/simulate', {
        method: 'POST',
        body: JSON.stringify(requestPayload),
      });
      setResults(response.rows);
      setExchange({ request: requestPayload, response });
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const upload = (file?: File) => {
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      setCSV(String(reader.result || ''));
      setFileName(file.name);
      setResults(undefined);
      setExchange(undefined);
    };
    reader.readAsText(file);
  };
  const downloadTemplate = () => {
    if (!selectedModel) return;
    const blob = new Blob([`${template}\n`], { type: 'text/csv;charset=utf-8' });
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `${selectedModel.familyId || 'simulation'}-simulation-template.csv`;
    link.click();
    URL.revokeObjectURL(link.href);
  };
  return (
    <section className="page">
      <PageTitle
        eyebrow="MODEL VALIDATION"
        title={modelId ? `Draft simulation · ${draftLabel}` : 'CSV simulation'}
        subtitle={
          modelId
            ? 'Test this exact draft revision without changing production routing or adaptive state.'
            : 'Test production routing and calculations without committing adaptive state.'
        }
      />
      {!modelId && (
        <section className="simulation-workflow">
          <label>
            <span>1</span>
            <div>
              <strong>Select model to simulate</strong>
              <select
                value={selectedModelId}
                onChange={(event) => {
                  setSelectedModelId(event.target.value);
                  setCSV('');
                  setFileName('');
                  setResults(undefined);
                  setExchange(undefined);
                }}
              >
                <option value="">Select an active model…</option>
                {models.map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>
                    {candidate.name} · Rev {candidate.revision}
                  </option>
                ))}
              </select>
            </div>
          </label>
          <div className={!selectedModel ? 'disabled' : ''}>
            <span>2</span>
            <div>
              <strong>Download and complete the CSV template</strong>
              <button className="secondary" disabled={!selectedModel} onClick={downloadTemplate}>
                <Download /> Download template
              </button>
            </div>
          </div>
          <div className={!selectedModel ? 'disabled' : ''}>
            <span>3</span>
            <div>
              <strong>Upload the completed CSV and simulate</strong>
              <small>Required columns are generated from the selected model.</small>
            </div>
          </div>
        </section>
      )}
      <div className="simulation-layout">
        <article className="panel simulation-input">
          <div className="panel-head">
            <div>
              <h2>Simulation data</h2>
              <p>
                {!selectedModel
                  ? 'Select a model before uploading simulation data.'
                  : sequential
                  ? 'Each row continues from the preceding adaptive result.'
                  : 'Each row starts from the same persisted adaptive state.'}
              </p>
            </div>
            <div className="simulation-upload-actions">
              {modelId && (
                <button className="secondary" onClick={downloadTemplate}>
                  <Download /> Template
                </button>
              )}
              {selectedModel ? (
                <label className="secondary upload-button">
                  <Upload />
                  Upload CSV
                  <input
                    type="file"
                    accept=".csv,text/csv"
                    onChange={(e) => upload(e.target.files?.[0])}
                  />
                </label>
              ) : (
                <button className="secondary" onClick={() => setNotice('Please select a model before uploading a simulation CSV.')}>
                  <Upload /> Upload CSV
                </button>
              )}
            </div>
          </div>
          {modelId && (
            <p className="simulation-draft-note">
              This simulation uses the selected draft directly and does not change production
              routing or persisted adaptive state.
            </p>
          )}
          <div className="simulation-file-state">
            <div>
              <small>EXPECTED CSV COLUMNS</small>
              <code>{template || 'Select a model to generate the required columns.'}</code>
            </div>
            <div className={fileName ? 'uploaded' : ''}>
              <Upload />
              <strong>{fileName || 'No CSV uploaded'}</strong>
              <small>{fileName ? `${Math.max(csv.split(/\r?\n/).filter(Boolean).length - 1, 0)} data rows detected` : 'Use the provided template and keep the column headers unchanged.'}</small>
            </div>
          </div>
          <div className="simulation-actions">
            {error && <span>{error}</span>}
            <label className="simulation-mode">
              <input
                type="checkbox"
                checked={sequential}
                onChange={(event) => setSequential(event.target.checked)}
              />
              <span>
                Sequential simulation
                <small>Carry adaptive values between rows</small>
              </span>
            </label>
            <button className="primary" disabled={busy || !selectedModel || !fileName || !csv.trim()} onClick={run}>
              <FlaskConical />
              {busy ? 'Running…' : 'Run simulation'}
            </button>
          </div>
        </article>
        <article className="panel simulation-results">
          <div className="panel-head">
            <div>
              <h2>Results</h2>
              <p className="simulation-preview-note">Adaptive updates are previews and are not saved.</p>
            </div>
            {results && (
              <div className="simulation-result-actions">
                <span className="status approved">
                  {results.filter((row) => row.status === 'completed').length}/{results.length} PASSED
                </span>
              </div>
            )}
          </div>
          {!results ? (
            <Empty
              icon={<FlaskConical />}
              title="No simulation results"
              text="Select a model, complete its CSV template, upload it, and run the simulation."
            />
          ) : (
            <>
              <div className="simulation-result-toolbar">
                <button
                  className="secondary chart-open-button simulation-toolbar-button"
                  disabled={!chartSeries.length}
                  onClick={() => setShowChart(true)}
                >
                  <Activity />
                  View trend chart
                </button>
                {exchange && (
                  <button className="secondary simulation-toolbar-button" onClick={() => setShowExchange(true)}>
                    <Code2 /> View JSON exchange
                  </button>
                )}
              </div>
              <div className="simulation-table">
                {results.map((row) => (
                  <div className={`simulation-row ${row.status}`} key={row.row}>
                    <div>
                      <strong>Row {row.row}</strong>
                      <small>{row.model || row.error}</small>
                    </div>
                    <span className={`status ${row.status === 'completed' ? 'approved' : 'draft'}`}>
                      {row.status}
                    </span>
                    {row.outputs && (
                      <code>
                        {row.outputs
                          .map((output: any) => `${output.parameter}=${output.value}`)
                          .join(' · ')}
                      </code>
                    )}
                    {row.parameters && (
                      <details className="simulation-parameters">
                        <summary>
                          <span>View all parameters</span>
                          <ChevronDown />
                        </summary>
                        <div className="simulation-parameter-table">
                          <div className="simulation-parameter-head">
                            <span>Parameter</span>
                            <span>Resolved value</span>
                          </div>
                          {Object.entries(row.parameters).map(([name, value]) => (
                            <div className="simulation-parameter-value" key={name}>
                              <strong>{name}</strong>
                              <code>{formatParameterValue(value)}</code>
                            </div>
                          ))}
                        </div>
                      </details>
                    )}
                  </div>
                ))}
              </div>
              {showChart && (
                <ChartSeriesDialog
                  title="Simulation trend"
                  options={chartSeries}
                  onClose={() => setShowChart(false)}
                />
              )}
            </>
          )}
        </article>
      </div>
      {showExchange && exchange && (
        <div className="payload-backdrop" onMouseDown={(event) => event.target === event.currentTarget && setShowExchange(false)}>
          <section className="payload-dialog simulation-exchange-dialog">
            <header>
              <div><p>SIMULATION SEQUENCE</p><h3>Row-by-row JSON</h3></div>
              <button className="icon" onClick={() => setShowExchange(false)}><X /></button>
            </header>
            <p>
              The CSV is sent in one batch request so sequential adaptive values can carry between
              rows. The sequence below shows the input and result for each processed row.
            </p>
            <div className="simulation-exchange-sequence simulation-json-timeline">
              {((exchange.response as { rows?: any[] }).rows || []).map((row, index) => {
                const { request, ...response } = row;
                return (
                  <section className="simulation-timeline-row" key={row.row || index}>
                    <h4>
                      <span>{index + 1}</span>
                      Row {row.row || index + 1}
                      <small className={`status ${row.status === 'completed' ? 'approved' : 'draft'}`}>
                        {String(row.status || 'processed').toUpperCase()}
                      </small>
                    </h4>
                    <div className="simulation-timeline-event input-event">
                      <i />
                      <div>
                        <label>INPUT RECEIVED</label>
                        <small>CSV row converted into typed simulation values</small>
                        <pre>{JSON.stringify(request || {}, null, 2)}</pre>
                      </div>
                    </div>
                    <div className="simulation-timeline-event result-event">
                      <i />
                      <div>
                        <label>RESULT RETURNED</label>
                        <small>Resolved parameters, adaptive preview, and model outputs</small>
                        <pre>{JSON.stringify(response, null, 2)}</pre>
                      </div>
                    </div>
                  </section>
                );
              })}
            </div>
            <button className="primary" onClick={() => navigator.clipboard.writeText(JSON.stringify((exchange.response as { rows?: any[] }).rows || [], null, 2))}>
              <Copy /> Copy sequence
            </button>
          </section>
        </div>
      )}
      {notice && (
        <div className="payload-backdrop action-error-backdrop" onMouseDown={(event) => event.target === event.currentTarget && setNotice('')}>
          <section className="action-error-dialog" role="alertdialog" aria-modal="true">
            <div className="action-error-icon"><CircleAlert /></div>
            <div><p>SIMULATION SETUP</p><h3>Select a model first</h3><span>{notice}</span></div>
            <button className="primary" onClick={() => setNotice('')} autoFocus>OK</button>
          </section>
        </div>
      )}
    </section>
  );
}

function formatParameterValue(value: unknown) {
  if (value === null) return 'null';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

function finiteNumber(value: unknown) {
  if (typeof value === 'number') return Number.isFinite(value) ? value : undefined;
  if (typeof value !== 'string' || !value.trim()) return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function NumericHistoryChart({
  title,
  series,
}: {
  title: string;
  series: { label: string; value: number }[];
}) {
  if (!series.length) {
    return (
      <div className="history-chart empty-chart">
        <strong>{title}</strong>
        <span>No numerical run data available.</span>
      </div>
    );
  }
  const values = series.map((point) => point.value);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const points = series
    .map((point, index) => {
      const x = series.length === 1 ? 50 : (index / (series.length - 1)) * 100;
      const y = 88 - ((point.value - min) / range) * 72;
      return `${x},${y}`;
    })
    .join(' ');
  return (
    <div className="history-chart">
      <header>
        <strong>{title}</strong>
        <span>
          {series.length} runs · {min.toFixed(2)}–{max.toFixed(2)}
        </span>
      </header>
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" aria-label={title}>
        <line x1="0" y1="88" x2="100" y2="88" />
        <polyline points={points} />
      </svg>
      <div>
        <span>{series[0].label}</span>
        <span>{series[series.length - 1].label}</span>
      </div>
    </div>
  );
}

function ChartSeriesDialog({
  title,
  options,
  onClose,
}: {
  title: string;
  options: ChartSeriesOption[];
  onClose: () => void;
}) {
  const [selectedKey, setSelectedKey] = useState(options[0]?.key || '');
  useEffect(() => {
    if (!options.some((option) => option.key === selectedKey)) {
      setSelectedKey(options[0]?.key || '');
    }
  }, [options, selectedKey]);
  const selected = options.find((option) => option.key === selectedKey);
  return (
    <div
      className="payload-backdrop chart-dialog-backdrop"
      onMouseDown={(event) => event.target === event.currentTarget && onClose()}
    >
      <section className="chart-dialog" role="dialog" aria-modal="true" aria-label={title}>
        <header>
          <div>
            <p>RUN DATA</p>
            <h3>{title}</h3>
          </div>
          <button className="icon" title="Close chart" onClick={onClose}>
            <X />
          </button>
        </header>
        <label className="chart-series-select">
          Numerical series
          <select value={selectedKey} onChange={(event) => setSelectedKey(event.target.value)}>
            {options.map((option) => (
              <option key={option.key} value={option.key}>
                {option.category} · {option.label}
              </option>
            ))}
          </select>
        </label>
        <NumericHistoryChart
          title={selected ? `${selected.category} · ${selected.label}` : 'Run history'}
          series={selected?.points || []}
        />
      </section>
    </div>
  );
}

function InputRegistry({
  definitions,
  role,
  onCreate,
  onEdit,
}: {
  definitions: InputDefinition[];
  role: User['role'];
  onCreate: () => void;
  onEdit: (definition: InputDefinition) => void;
}) {
  return (
    <section className="page">
      <PageTitle
        eyebrow="GOVERNANCE"
        title="Input registry"
        subtitle="Approved names and types available to control models."
        action={
          role === 'admin' ? (
            <button className="primary" onClick={onCreate}>
              <Plus /> Add definition
            </button>
          ) : undefined
        }
      />
      <div className="panel input-registry-panel">
        <div className="input-registry-table">
          <div className="input-registry-head">
            <span>Definition</span>
            <span>Category</span>
            <span>Data type</span>
            <span>Status</span>
            <span />
          </div>
          {definitions.map((definition) => (
            <div className="input-registry-row" key={definition.name}>
              <div className="input-registry-name">
                <strong>{definition.displayName}</strong>
                <code>@{definition.name}</code>
                {definition.description && <small>{definition.description}</small>}
              </div>
              <span>
                {definition.category === 'index'
                  ? definition.system
                    ? 'Core index'
                    : 'Additional index'
                  : 'External input'}
              </span>
              <span className="input-registry-type">
                {definition.dataType === 'string'
                  ? 'Text'
                  : definition.dataType === 'number'
                    ? 'Number'
                    : 'Boolean'}
              </span>
              <span>
                <span
                  className={`status ${definition.status === 'active' ? 'approved' : 'superseded'}`}
                >
                  {definition.status.toUpperCase()}
                </span>
              </span>
              <span className="input-registry-action">
                {role === 'admin' && (
                  <button className="secondary" onClick={() => onEdit(definition)}>
                    Edit
                  </button>
                )}
              </span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function UserManagement() {
  const [users, setUsers] = useState<RegisteredUser[]>([]);
  const [adding, setAdding] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const loadUsers = async () => {
    setLoading(true);
    setError('');
    try {
      setUsers(await api<RegisteredUser[]>('/api/users'));
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    loadUsers();
  }, []);

  const removeUser = async (registered: RegisteredUser) => {
    if (!window.confirm(`Remove ${registered.employeeId} (${registered.username}) from R2R? Their corporate employee record will not be deleted.`)) return;
    setError('');
    try {
      await api(`/api/users/${encodeURIComponent(registered.employeeId)}`, { method: 'DELETE' });
      await loadUsers();
    } catch (reason) {
      setError((reason as Error).message);
    }
  };

  const setUserStatus = async (registered: RegisteredUser, status: 'active' | 'disabled') => {
    const verb = status === 'disabled' ? 'Disable' : 'Enable';
    if (!window.confirm(`${verb} ${registered.employeeId} (${registered.username})?`)) return;
    setError('');
    try {
      await api(`/api/users/${encodeURIComponent(registered.employeeId)}`, {
        method: 'PUT',
        body: JSON.stringify({ role: registered.role, status }),
      });
      await loadUsers();
    } catch (reason) {
      setError((reason as Error).message);
    }
  };

  return (
    <section className="page">
      <PageTitle
        eyebrow="ACCESS CONTROL"
        title="User management"
        subtitle="Register corporate employee IDs and assign their R2R application roles."
        action={
          <button className="primary" onClick={() => setAdding(true)}>
            <UserPlus /> Add user
          </button>
        }
      />
      {error && <p className="user-management-error">{error}</p>}
      <div className="panel input-registry-panel">
        <div className="input-registry-table user-registry-table">
          <div className="input-registry-head user-registry-grid">
            <span>Employee</span>
            <span>Login</span>
            <span>Email</span>
            <span>R2R role</span>
            <span>Status</span>
            <span />
          </div>
          {!loading &&
            users.map((registered) => (
              <div className="input-registry-row user-registry-grid" key={registered.employeeId}>
                <div className="input-registry-name">
                  <strong>{registered.employeeId}</strong>
                  <small>Corporate IDNUM</small>
                </div>
                <span>{registered.username}</span>
                <span>{registered.email || '—'}</span>
                <span>{registered.role.replaceAll('_', ' ')}</span>
                <span>
                  <span className={`status ${registered.status === 'disabled' ? 'superseded' : 'approved'}`}>
                    {(registered.status || 'active').toUpperCase()}
                  </span>
                </span>
                <span className="user-registry-action">
                  <button
                    className="secondary"
                    onClick={() =>
                      setUserStatus(
                        registered,
                        registered.status === 'disabled' ? 'active' : 'disabled',
                      )
                    }
                  >
                    {registered.status === 'disabled' ? 'Enable' : 'Disable'}
                  </button>
                  <button className="danger" onClick={() => removeUser(registered)}>
                    <Trash2 /> Remove
                  </button>
                </span>
              </div>
            ))}
          {loading && <div className="user-registry-message">Loading registered users…</div>}
          {!loading && !users.length && (
            <div className="user-registry-message">No employees are registered.</div>
          )}
        </div>
      </div>
      {adding && (
        <AddUserDialog
          onClose={() => setAdding(false)}
          onSaved={async () => {
            setAdding(false);
            await loadUsers();
          }}
        />
      )}
    </section>
  );
}

function AddUserDialog({ onClose, onSaved }: { onClose: () => void; onSaved: () => Promise<void> }) {
  const [employeeId, setEmployeeId] = useState('');
  const [role, setRole] = useState<User['role']>('engineer');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const save = async () => {
    setBusy(true);
    setError('');
    try {
      await api<RegisteredUser>('/api/users', {
        method: 'POST',
        body: JSON.stringify({ employeeId: employeeId.trim(), role }),
      });
      await onSaved();
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="payload-backdrop" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <section className="add-user-dialog" role="dialog" aria-modal="true">
        <header>
          <div>
            <p>ACCESS CONTROL</p>
            <h3>Register employee</h3>
          </div>
          <button className="icon" onClick={onClose} aria-label="Close user dialog">
            <X />
          </button>
        </header>
        <p>
          Enter an existing corporate IDNUM. R2R retrieves LOGINID and EMAIL from the employee directory.
        </p>
        <label>
          Employee ID
          <input value={employeeId} onChange={(event) => setEmployeeId(event.target.value)} placeholder="E300" autoFocus />
        </label>
        <label>
          R2R role
          <select value={role} onChange={(event) => setRole(event.target.value as User['role'])}>
            <option value="viewer">Viewer</option>
            <option value="engineer">Engineer</option>
            <option value="engineering_manager">Engineering manager</option>
            <option value="admin">Administrator</option>
          </select>
        </label>
        {error && <p className="error">{error}</p>}
        <footer>
          <button className="secondary" onClick={onClose} disabled={busy}>Cancel</button>
          <button className="primary" onClick={save} disabled={busy || !employeeId.trim()}>
            <UserPlus /> {busy ? 'Registering…' : 'Register user'}
          </button>
        </footer>
      </section>
    </div>
  );
}

function InputDefinitionEditor({
  definition,
  onClose,
  onSaved,
}: {
  definition: InputDefinition;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const isNew = !definition.name;
  const [form, setForm] = useState(definition);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    setError('');
    try {
      await api(isNew ? '/api/input-definitions/' : `/api/input-definitions/${definition.name}`, {
        method: isNew ? 'POST' : 'PUT',
        body: JSON.stringify(form),
      });
      await onSaved();
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (
      !window.confirm(
        `Permanently delete ${definition.displayName}? This is allowed only when no model revision references it.`,
      )
    )
      return;
    setBusy(true);
    setError('');
    try {
      await api(`/api/input-definitions/${definition.name}`, { method: 'DELETE' });
      await onSaved();
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div
      className="drawer-backdrop"
      onMouseDown={(event) => event.target === event.currentTarget && onClose()}
    >
      <aside className="drawer source-drawer">
        <header>
          <div>
            <p>INPUT REGISTRY</p>
            <h2>{isNew ? 'Add definition' : definition.displayName}</h2>
          </div>
          <button className="icon" onClick={onClose}>
            <X />
          </button>
        </header>
        <div className="revision-form">
          <div className="form-grid">
            <label>
              Internal name
              <input
                disabled={!isNew}
                value={form.name}
                onChange={(event) =>
                  setForm({
                    ...form,
                    name: event.target.value.toLowerCase().replace(/[^a-z0-9_]/g, '_'),
                  })
                }
                placeholder="run_type"
              />
            </label>
            <label>
              Display name
              <input
                value={form.displayName}
                onChange={(event) => setForm({ ...form, displayName: event.target.value })}
              />
            </label>
            <label>
              Category
              <select
                disabled={!isNew || form.system}
                value={form.category}
                onChange={(event) =>
                  setForm({
                    ...form,
                    category: event.target.value as InputDefinition['category'],
                    dataType: event.target.value === 'index' ? 'string' : form.dataType,
                  })
                }
              >
                <option value="index">Additional index</option>
                <option value="external">External input</option>
              </select>
            </label>
            <label>
              Data type
              <select
                disabled={form.category === 'index' || form.system}
                value={form.dataType}
                onChange={(event) =>
                  setForm({ ...form, dataType: event.target.value as InputDefinition['dataType'] })
                }
              >
                <option value="string">Text</option>
                <option value="number">Number</option>
                <option value="boolean">Boolean</option>
              </select>
            </label>
            <label className="span-two">
              Description
              <input
                value={form.description || ''}
                onChange={(event) => setForm({ ...form, description: event.target.value })}
              />
            </label>
            <label>
              Status
              <select
                disabled={form.system}
                value={form.status}
                onChange={(event) =>
                  setForm({ ...form, status: event.target.value as InputDefinition['status'] })
                }
              >
                <option value="active">Active</option>
                <option value="deprecated">Deprecated</option>
              </select>
            </label>
          </div>
          {error && <p className="error">{error}</p>}
          <div className="source-actions">
            {!isNew && !definition.system && (
              <button className="danger" disabled={busy} onClick={remove}>
                <Trash2 /> Delete permanently
              </button>
            )}
            <button
              className="primary"
              disabled={busy || !form.name || !form.displayName}
              onClick={save}
            >
              {busy ? 'Saving…' : 'Save definition'}
            </button>
          </div>
        </div>
      </aside>
    </div>
  );
}

function SourceEditor({
  source,
  onClose,
  onSaved,
}: {
  source: Source;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const isNew = !source.id;
  const [form, setForm] = useState({ ...source });
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState('');
  const [testResult, setTestResult] = useState<{
    status: string;
    message: string;
    elapsedMs: number;
  }>();
  const save = async () => {
    setBusy(true);
    setError('');
    try {
      await api(isNew ? '/api/sources/' : `/api/sources/${source.id}`, {
        method: isNew ? 'POST' : 'PUT',
        body: JSON.stringify(form),
      });
      await onSaved();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const test = async () => {
    setTesting(true);
    setError('');
    setTestResult(undefined);
    try {
      const result = await api<{ status: string; message: string; elapsedMs: number }>(
        `/api/sources/${source.id}/test`,
        { method: 'POST', body: JSON.stringify({ source: form, password }) },
      );
      setTestResult(result);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setTesting(false);
    }
  };
  const setEngine = (engine: Source['engine']) =>
    setForm({
      ...form,
      engine,
      port: engine === 'postgres' ? 5432 : engine === 'mysql' ? 3306 : 1433,
      tlsMode: engine === 'mysql' ? 'true' : 'require',
    });
  return (
    <div className="drawer-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <aside className="drawer source-drawer">
        <header>
          <div>
            <p>DATA SOURCE</p>
            <h2>{isNew ? 'Add data source' : source.name}</h2>
          </div>
          <button className="icon" onClick={onClose}>
            <X />
          </button>
        </header>
        <div className="revision-form">
          <SectionTitle text="Connection" />
          <div className="form-grid">
            <label>
              Identifier
              <input
                value={form.id}
                disabled={!isNew}
                onChange={(e) =>
                  setForm({
                    ...form,
                    id: e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, '-'),
                  })
                }
                placeholder="production-history"
              />
            </label>
            <label>
              Name
              <input
                value={form.name}
                onChange={(e) => {
                  const name = e.target.value;
                  setForm({
                    ...form,
                    name,
                    id:
                      isNew && !form.id
                        ? name
                            .toLowerCase()
                            .trim()
                            .replace(/[^a-z0-9_-]+/g, '-')
                        : form.id,
                  });
                }}
              />
            </label>
            <label>
              Database type
              <select
                value={form.engine}
                onChange={(e) => setEngine(e.target.value as Source['engine'])}
              >
                <option value="sqlserver">SQL Server</option>
                <option value="postgres">PostgreSQL</option>
                <option value="mysql">MySQL</option>
              </select>
            </label>
            <label>
              Server / host
              <input
                value={form.host}
                onChange={(e) => setForm({ ...form, host: e.target.value })}
              />
            </label>
            <label>
              Port
              <input
                type="number"
                value={form.port}
                onChange={(e) => setForm({ ...form, port: +e.target.value })}
              />
            </label>
            <label>
              Database name
              <input
                value={form.database}
                onChange={(e) => setForm({ ...form, database: e.target.value })}
              />
            </label>
            <div className="source-schema-guidance span-two">
              <strong>Using multiple schemas</strong>
              <span>
                Configure one connection per database and security boundary. In model SQL, use
                schema-qualified names such as <code>metrology.measurements</code> or{' '}
                <code>reference.targets</code>.
              </span>
            </div>
          </div>
          <SectionTitle text="Authentication" />
          <div className="form-grid">
            <label>
              Username
              <input
                value={form.username || ''}
                onChange={(e) => setForm({ ...form, username: e.target.value })}
              />
            </label>
            <label>
              Password
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Write-only; not saved in config"
              />
            </label>
            <label className="span-two">
              Secret environment reference
              <input
                value={form.secretRef || ''}
                onChange={(e) => setForm({ ...form, secretRef: e.target.value })}
                placeholder="R2R_METROLOGY_PASSWORD"
              />
            </label>
          </div>
          <SectionTitle text="Security and timeout" />
          <div className="form-grid">
            <label>
              TLS mode
              <select
                value={form.tlsMode}
                onChange={(e) => setForm({ ...form, tlsMode: e.target.value })}
              >
                <option value="require">Require</option>
                <option value="verify-full">Verify certificate</option>
                <option value="disable">Disable</option>
                {form.engine === 'mysql' && <option value="true">Enabled</option>}
              </select>
            </label>
            <label>
              Timeout (seconds)
              <input
                type="number"
                min="1"
                max="60"
                value={form.timeoutSeconds}
                onChange={(e) => setForm({ ...form, timeoutSeconds: +e.target.value })}
              />
            </label>
          </div>
          <label className="source-toggle">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            />
            <span>Available for model parameters</span>
          </label>
          {testResult && (
            <div className="connection-result success">
              <strong>Connection successful</strong>
              <span>
                {testResult.message} · {testResult.elapsedMs} ms
              </span>
            </div>
          )}
          {error && (
            <div className="connection-result failed">
              <strong>Connection failed</strong>
              <span>{error}</span>
            </div>
          )}
          <div className="source-actions">
            <button
              className="secondary"
              disabled={isNew || testing || !form.host || !form.database}
              onClick={test}
              title={isNew ? 'Save the data source before testing its connection' : undefined}
            >
              {testing ? 'Testing…' : isNew ? 'Save before testing' : 'Test connection'}
            </button>
            <button
              className="primary"
              disabled={
                busy || !form.id || !form.name || !form.host || !form.port || !form.database
              }
              onClick={save}
            >
              {busy ? 'Saving…' : isNew ? 'Add data source' : 'Save data source'}
            </button>
          </div>
        </div>
      </aside>
    </div>
  );
}
function SectionTitle({ text }: { text: string }) {
  return <h3 className="source-section-title">{text}</h3>;
}
function Login({ onLogin, message }: { onLogin: (user: User) => Promise<void>; message?: string }) {
  const [username, setUsername] = useState('engineer');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      const current = await api<User>('/api/login', {
        method: 'POST',
        body: JSON.stringify({ username, password }),
      });
      await onLogin(current);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="login-page">
      <section className="login-panel">
        <div className="brand login-brand">
          <img className="tdk-logo" src={tdkLogo} alt="TDK" />
          <div>
            R2R<small>PROCESS CONTROL</small>
          </div>
        </div>
        <div className="login-heading">
          <h1>Sign in</h1>
          <span>Use your assigned account to access the production workspace.</span>
        </div>
        <form onSubmit={submit}>
          <label>
            Username
            <input
              autoFocus
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              required
            />
          </label>
          <label>
            Password
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </label>
          {(error || message) && <p className="login-error">{error || message}</p>}
          <button className="primary full" disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </section>
    </main>
  );
}
function PageTitle(p: {
  eyebrow: string;
  title: string;
  subtitle: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="page-title">
      <div>
        <p>{p.eyebrow}</p>
        <h1>{p.title}</h1>
        <span>{p.subtitle}</span>
      </div>
      {p.action}
    </div>
  );
}
function Metric(p: { label: string; value: number; detail: string; icon: React.ReactNode }) {
  return (
    <article className="metric">
      <div>
        <span>{p.label}</span>
        <i>{p.icon}</i>
      </div>
      <strong>{p.value}</strong>
      <small>{p.detail}</small>
    </article>
  );
}
function Panel(p: {
  title: string;
  subtitle: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <article className="panel">
      <div className="panel-head">
        <div>
          <h2>{p.title}</h2>
          <p>{p.subtitle}</p>
        </div>
        {p.action}
      </div>
      {p.children}
    </article>
  );
}
function Empty(p: { icon: React.ReactNode; title: string; text: string }) {
  return (
    <div className="empty">
      <i>{p.icon}</i>
      <strong>{p.title}</strong>
      <p>{p.text}</p>
    </div>
  );
}
function ModelRow({ model, ownerInactive, onOpen }: { model: Model; ownerInactive: boolean; onOpen: () => void }) {
  return (
    <article className="model-row">
      <div className="model-main">
        <div className="model-icon">
          <Settings2 />
        </div>
        <div>
          <div className="model-title">
            <h3>{model.name}</h3>
            <span className={`status ${model.status}`}>
              REV {model.revision}{' '}
              {model.status === 'approved'
                ? 'ACTIVE'
                : model.status === 'pending_approval'
                  ? 'PENDING APPROVAL'
                  : model.status.toUpperCase()}
            </span>
            {ownerInactive && <span className="status owner-inactive">OWNER INACTIVE</span>}
          </div>
          <p>{model.description}</p>
          <div className="chips">
            {Object.entries(model.indexes).map(([k, values]) => (
              <span key={k}>
                <b>{k}</b>
                {values.join(', ')}
              </span>
            ))}
          </div>
        </div>
      </div>
      <div className="model-values">
        <div>
          <small>PARAMETERS</small>
          <strong>{model.parameters.length}</strong>
        </div>
        <div>
          <small>OUTPUTS</small>
          <strong>{model.outputs.length}</strong>
        </div>
        <button className="secondary" onClick={onOpen}>
          <Clock3 />
          Model details
        </button>
      </div>
    </article>
  );
}

function modelEditorForm(
  model: Model,
  sources: Source[],
  inputDefinitions: InputDefinition[] = [],
) {
  return {
    name: model.name,
    description: model.description,
    indexes: { ...model.indexes },
    externalInputs: (model.externalInputs || []).map((item) => ({ ...item })),
    feedbackInputs: (model.feedbackInputs || []).map((item) => ({ ...item })),
    executionPolicy: {
      maxUnfinishedRuns: model.executionPolicy?.maxUnfinishedRuns || 1,
      partitionBy: model.executionPolicy?.partitionBy || [],
    },
    parameters: model.parameters.map((item): Parameter => ({
      ...item,
      adaptive: item.adaptive
        ? {
            ...item.adaptive,
            updateParameter:
              item.adaptive.updateParameter || item.adaptive.measurementParameter || '',
            updateInput:
              item.adaptive.updateInput ||
              ({
                source: 'parameter',
                name: item.adaptive.updateParameter || item.adaptive.measurementParameter || '',
              } as const),
            measurementParameter: undefined,
          }
        : undefined,
    })),
    dataSamples: (model.dataSamples || []).map((item) => ({ ...item })),
    outputs: [...model.outputs],
    sources,
    inputDefinitions,
    note: model.status === 'draft' ? model.changeNote : '',
  };
}

function ModelDrawer({
  model,
  sources,
  inputDefinitions,
  role,
  onClose,
  onChanged,
}: {
  model: Model;
  sources: Source[];
  inputDefinitions: InputDefinition[];
  role: User['role'];
  onClose: () => void;
  onChanged: () => Promise<void>;
}) {
  const isNew = !model.id;
  const [history, setHistory] = useState<Model[]>([]);
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [editing, setEditing] = useState(isNew);
  const [editTarget, setEditTarget] = useState(model);
  const [simulationDraft, setSimulationDraft] = useState<Model>();
  const [showDependencies, setShowDependencies] = useState(false);
  const [showPayload, setShowPayload] = useState(false);
  const [payloadView, setPayloadView] = useState<'request' | 'response' | 'contract'>('request');
  const [showHistoryChart, setShowHistoryChart] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [form, setForm] = useState(() => modelEditorForm(model, sources, inputDefinitions));
  const currentModel = history.find((item) => item.id === model.id) || model;
  const currentModelStatus = currentModel.status;
  const canDevelop = role === 'engineer' || role === 'engineering_manager' || role === 'admin';
  const canApprove = role === 'engineering_manager' || role === 'admin';
  const historyChartSeries = useMemo<ChartSeriesOption[]>(() => {
    const parameters = new Map(model.parameters.map((parameter) => [parameter.key, parameter]));
    const outputOptions = model.outputs
      .filter((name) => (parameters.get(name)?.dataType || 'number') === 'number')
      .map((name) => ({
        key: `output:${name}`,
        label: name,
        category: 'Output' as const,
        points: runs.flatMap((run) => {
          const output = (run.outputs || []).find((item) => item.parameter === name);
          const value = finiteNumber(output?.value);
          return value !== undefined
            ? [
                {
                  label: new Date(run.preparedAt).toLocaleDateString(),
                  value,
                },
              ]
            : [];
        }),
      }));
    const feedbackOptions = (model.feedbackInputs || [])
      .filter((input) => input.type === 'number')
      .map((input) => ({
        key: `feedback:${input.name}`,
        label: input.name,
        category: 'Post-run feedback' as const,
        points: runs.flatMap((run) => {
          const value = finiteNumber(run.feedback?.[input.name]);
          return value !== undefined
            ? [
                {
                  label: new Date(run.completedAt || run.preparedAt).toLocaleDateString(),
                  value,
                },
              ]
            : [];
        }),
      }));
    return [...outputOptions, ...feedbackOptions];
  }, [model.feedbackInputs, model.outputs, model.parameters, runs]);
  const refreshHistory = async () => {
    if (model.id) setHistory(await api<Model[]>(`/api/models/${model.id}/history`));
  };
  useEffect(() => {
    if (model.id) {
      refreshHistory();
      api<RunRecord[]>(`/api/models/${model.id}/runs`).then(setRuns);
    }
  }, [model.id]);
  const updateInput = (i: number, next: Parameter) =>
    setForm({ ...form, parameters: form.parameters.map((v, n) => (n === i ? next : v)) });
  const create = async () => {
    setBusy(true);
    setError('');
    try {
      const adaptiveFeedbackNames = new Set(
        form.parameters
          .map((parameter: Parameter) =>
            parameter.adaptive?.updateInput?.source === 'post-run-feedback'
              ? parameter.adaptive.updateInput.name
              : '',
          )
          .filter(Boolean),
      );
      const payload = {
        ...editTarget,
        id: '',
        name: form.name,
        owner: '',
        description: form.description,
        indexes: form.indexes,
        externalInputs: form.externalInputs,
        feedbackInputs: form.feedbackInputs.map((input: FeedbackInput) => ({
          ...input,
          required: input.required || adaptiveFeedbackNames.has(input.name),
        })),
        executionPolicy: form.executionPolicy,
        revision: 0,
        status: 'draft',
        enabled: false,
        parameters: form.parameters,
        dataSamples: form.dataSamples,
        outputs: form.outputs,
        changeNote: form.note,
        createdBy: '',
      };
      const editingDraft = !isNew && editTarget.status === 'draft';
      await api(
        isNew
          ? '/api/models'
          : editingDraft
            ? `/api/models/${editTarget.id}/revision`
            : `/api/models/${editTarget.id}/revisions`,
        {
          method: editingDraft ? 'PUT' : 'POST',
          body: JSON.stringify(payload),
        },
      );
      await onChanged();
      await refreshHistory();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const approve = async (id: string) => {
    setBusy(true);
    setActionError('');
    try {
      await api(`/api/models/${id}/approve`, { method: 'POST' });
      await onChanged();
      await refreshHistory();
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const submitForApproval = async (id: string) => {
    if (!window.confirm('Submit this revision for approval? It will be locked from editing.'))
      return;
    setBusy(true);
    setActionError('');
    try {
      await api(`/api/models/${id}/submit`, { method: 'POST' });
      await onChanged();
      await refreshHistory();
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const reject = async (id: string) => {
    const reason = window.prompt('Why is this revision being rejected?');
    if (reason === null) return;
    if (!reason.trim()) {
      setActionError('A rejection reason is required.');
      return;
    }
    setBusy(true);
    setActionError('');
    try {
      await api(`/api/models/${id}/reject`, {
        method: 'POST',
        body: JSON.stringify({ reason: reason.trim() }),
      });
      await onChanged();
      await refreshHistory();
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const deleteRevision = async (id: string) => {
    if (!window.confirm('Delete this draft revision? This action cannot be undone.')) return;
    setBusy(true);
    setActionError('');
    try {
      await api(`/api/models/${id}/revision`, { method: 'DELETE' });
      await onChanged();
      if (id === model.id) setHistory((current) => current.filter((item) => item.id !== id));
      else await refreshHistory();
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const transferDraft = async (id: string) => {
    const employeeId = window.prompt('Enter the active engineer or administrator employee ID:');
    if (employeeId === null) return;
    if (!employeeId.trim()) {
      setActionError('A new owner employee ID is required.');
      return;
    }
    setBusy(true);
    setActionError('');
    try {
      await api(`/api/models/${id}/transfer`, {
        method: 'POST',
        body: JSON.stringify({ employeeId: employeeId.trim() }),
      });
      await onChanged();
    } catch (reason) {
      setActionError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const deleteFamily = async () => {
    if (
      !window.confirm(
        `Delete the entire ${model.name} model family, including all revisions and adaptive state?`,
      )
    )
      return;
    setBusy(true);
    setActionError('');
    try {
      await api(`/api/models/${model.id}`, { method: 'DELETE' });
      await onChanged();
    } catch (e) {
      setActionError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div
      className={`drawer-backdrop ${editing ? 'editor-backdrop' : ''}`}
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <aside className={`drawer wide ${editing ? 'editing' : ''}`}>
        <header>
          <div>
            <p>MODEL FAMILY</p>
            <h2>{model.name || 'Create control model'}</h2>
          </div>
          <button className="icon" onClick={onClose}>
            <X />
          </button>
        </header>
        {!editing ? (
          <>
            <div className="drawer-actions">
              {canDevelop && currentModelStatus !== 'pending_approval' && (
                <button
                  className="primary model-action"
                  onClick={() => {
                    setEditTarget(currentModel);
                    setForm(modelEditorForm(currentModel, sources, inputDefinitions));
                    setEditing(true);
                  }}
                >
                  <Plus />
                  {currentModelStatus === 'draft' ? 'Edit draft' : 'Create revision'}
                </button>
              )}
              {role === 'admin' && (
                <button className="danger model-action" disabled={busy} onClick={deleteFamily}>
                  <Trash2 />
                  Delete model
                </button>
              )}
            </div>
            <div className="model-detail-tools">
              <button className="secondary" onClick={() => setShowDependencies(true)}>
                <GitBranch />
                View dependencies
              </button>
              <button className="secondary" onClick={() => setShowPayload(true)}>
                <Code2 />
                View API payload
              </button>
            </div>
            <section className="contract">
              <h3>Active model contract</h3>
              {model.status === 'approved' && (
                <button
                  className="secondary chart-open-button"
                  disabled={!historyChartSeries.length}
                  onClick={() => setShowHistoryChart(true)}
                >
                  <Activity />
                  View 14-day run chart
                </button>
              )}
              {model.dataSamples?.length > 0 && (
                <>
                  <h4>Data samples</h4>
                  {model.dataSamples.map((sample) => (
                    <div className="contract-row" key={sample.key}>
                      <div>
                        <strong>{sample.key}</strong>
                        <code>{sample.description || 'No description'}</code>
                      </div>
                      <span>{sample.limit} rows</span>
                      <small>
                        {sample.source === 'static'
                          ? `${sample.rows?.length || 0} static rows`
                          : sample.query}
                      </small>
                    </div>
                  ))}
                </>
              )}
              <h4>Parameters</h4>
              {model.parameters.map((input) => (
                <div className="contract-row" key={input.key}>
                  <div>
                    <strong>{input.key}</strong>
                    <code>{input.description || 'No description'}</code>
                  </div>
                  <span>{input.source}</span>
                  <small>
                    {input.definition}
                    {input.lsl !== undefined ? ` · LSL ${input.lsl}` : ''}
                    {input.usl !== undefined ? ` · USL ${input.usl}` : ''}
                  </small>
                </div>
              ))}
              <h4>Outputs</h4>
              {model.outputs.map((output) => (
                <div className="contract-row" key={output}>
                  <div>
                    <strong>{output}</strong>
                    <code>Parameter reference</code>
                  </div>
                  <span>parameter</span>
                  <small>Returned without additional output calculation</small>
                </div>
              ))}
            </section>
            <div className="timeline compact">
              {history.map((item) => (
                <div className="timeline-item" key={item.id}>
                  <i className={item.status}></i>
                  <div>
                    <div className="timeline-title">
                      <strong>Revision {item.revision}</strong>
                      <span className={`status ${item.status}`}>{item.status.toUpperCase()}</span>
                    </div>
                    <p>{item.changeNote}</p>
                    <small>
                      {item.createdBy} · {formatDate(item.createdAt)}
                    </small>
                    {item.submittedAt && (
                      <small>
                        Submitted by {item.submittedBy} · {formatDate(item.submittedAt)}
                      </small>
                    )}
                    {item.status === 'draft' && item.rejectionReason && (
                      <p className="revision-rejection">
                        Rejected by {item.rejectedBy}: {item.rejectionReason}
                      </p>
                    )}
                    {item.status === 'draft' && (
                      <div className="revision-actions">
                        {canDevelop && (
                          <button
                            className="secondary model-action"
                            disabled={busy}
                            onClick={() => {
                              setEditTarget(item);
                              setForm(modelEditorForm(item, sources, inputDefinitions));
                              setEditing(true);
                            }}
                          >
                            <Settings2 />
                            Edit draft
                          </button>
                        )}
                        {role === 'admin' && (
                          <button
                            className="secondary model-action"
                            disabled={busy}
                            onClick={() => transferDraft(item.id)}
                          >
                            <Users />
                            Transfer owner
                          </button>
                        )}
                        {canDevelop && (
                          <button
                            className="secondary model-action"
                            disabled={busy}
                            onClick={() => setSimulationDraft(item)}
                          >
                            <FlaskConical />
                            Simulate draft
                          </button>
                        )}
                        {canDevelop && (
                          <button
                            className="secondary model-action"
                            disabled={busy}
                            onClick={() => submitForApproval(item.id)}
                          >
                            <Upload />
                            Submit approval
                          </button>
                        )}
                        {canDevelop && (
                          <button
                            className="danger model-action"
                            disabled={busy}
                            onClick={() => deleteRevision(item.id)}
                          >
                            <Trash2 />
                            Delete draft
                          </button>
                        )}
                      </div>
                    )}
                    {item.status === 'pending_approval' && canApprove && (
                      <div className="revision-actions">
                        <button
                          className="secondary model-action"
                          disabled={busy}
                          onClick={() => setSimulationDraft(item)}
                        >
                          <FlaskConical />
                          Simulate revision
                        </button>
                        <button
                          className="approve model-action"
                          disabled={busy}
                          onClick={() => approve(item.id)}
                        >
                          <ShieldCheck />
                          Approve revision
                        </button>
                        <button
                          className="danger model-action"
                          disabled={busy}
                          onClick={() => reject(item.id)}
                        >
                          <X />
                          Reject revision
                        </button>
                      </div>
                    )}
                    {item.status === 'superseded' && canDevelop && (
                      <div className="revision-actions">
                        <button
                          className="secondary model-action"
                          disabled={busy}
                          onClick={() => {
                            setEditTarget(item);
                            setForm(modelEditorForm(item, sources, inputDefinitions));
                            setEditing(true);
                          }}
                        >
                          <GitBranch />
                          Create from this
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              ))}
            </div>
          </>
        ) : (
          <ModelEditor
            isNew={isNew}
            editingDraft={editTarget.status === 'draft'}
            model={editTarget}
            form={form}
            setForm={setForm}
            updateInput={updateInput}
            busy={busy}
            error={error}
            create={create}
            back={() => setEditing(false)}
          />
        )}
      </aside>
      {simulationDraft && (
        <div
          className="payload-backdrop draft-simulation-backdrop"
          onMouseDown={(event) =>
            event.target === event.currentTarget && setSimulationDraft(undefined)
          }
        >
          <section className="draft-simulation-dialog">
            <button
              className="icon draft-simulation-close"
              title="Close simulation"
              onClick={() => setSimulationDraft(undefined)}
            >
              <X />
            </button>
            <Simulation
              model={simulationDraft}
              modelId={simulationDraft.id}
              draftLabel={`${simulationDraft.name} · Rev ${simulationDraft.revision}`}
            />
          </section>
        </div>
      )}
      {showDependencies && (
        <DependencyDialog
          parameters={model.parameters}
          dataSamples={model.dataSamples || []}
          externalInputs={model.externalInputs || []}
          feedbackInputs={model.feedbackInputs || []}
          indexes={model.indexes}
          outputs={model.outputs}
          onClose={() => setShowDependencies(false)}
        />
      )}
      {showHistoryChart && (
        <ChartSeriesDialog
          title={`${model.name} · Last 14 days`}
          options={historyChartSeries}
          onClose={() => setShowHistoryChart(false)}
        />
      )}
      {showPayload && (
        <div
          className="payload-backdrop"
          onMouseDown={(event) => event.target === event.currentTarget && setShowPayload(false)}
        >
          <section className="payload-dialog">
            <header>
              <div>
                <p>DEVELOPER REFERENCE</p>
                <h3>Model API contract</h3>
              </div>
              <button className="icon" onClick={() => setShowPayload(false)}>
                <X />
              </button>
            </header>
            <div className="payload-tabs">
              {(['request', 'response', 'contract'] as const).map((view) => (
                <button
                  type="button"
                  className={payloadView === view ? 'active' : ''}
                  onClick={() => setPayloadView(view)}
                  key={view}
                >
                  {view === 'request'
                    ? 'External app request'
                    : view === 'response'
                      ? 'Model response'
                      : 'Expected inputs'}
                </button>
              ))}
            </div>
            <p>
              {payloadView === 'request'
                ? 'Example body the external application sends to prepare a run.'
                : payloadView === 'response'
                  ? 'Example response returned after the model resolves successfully.'
                  : `Available from GET /api/models/${model.id}/contract.`}
            </p>
            <pre>{modelAPIPayload(model, payloadView)}</pre>
            <button
              className="primary"
              onClick={() => navigator.clipboard.writeText(modelAPIPayload(model, payloadView))}
            >
              <Copy />
              Copy payload
            </button>
          </section>
        </div>
      )}
      {actionError && (
        <div
          className="payload-backdrop action-error-backdrop"
          onMouseDown={(event) => event.target === event.currentTarget && setActionError('')}
        >
          <section className="action-error-dialog" role="alertdialog" aria-modal="true">
            <div className="action-error-icon">
              <CircleAlert />
            </div>
            <div>
              <p>MODEL ACTION</p>
              <h3>Unable to complete the action</h3>
              <span>{actionError}</span>
            </div>
            <button className="primary" onClick={() => setActionError('')} autoFocus>
              OK
            </button>
          </section>
        </div>
      )}
    </div>
  );
}

function exampleValue(type?: string) {
  return type === 'boolean' ? false : type === 'string' ? 'VALUE' : 0;
}

function modelRequest(model: Model) {
  return {
    runId: 'RUN_UNIQUE_ID',
    context: {
      ...Object.fromEntries(
        Object.entries(model.indexes).map(([key, values]) => [key, values[0] || 'VALUE']),
      ),
      ...Object.fromEntries(
        (model.externalInputs || []).map((input) => [input.name, exampleValue(input.type)]),
      ),
    },
    parameterValues: Object.fromEntries(
      model.parameters
        .filter((parameter) => parameter.source === 'sql')
        .map((parameter) => [parameter.key, exampleValue(parameter.dataType || 'number')]),
    ),
  };
}

function modelContractPayload(model: Model) {
  const definitions = new Map(model.parameters.map((parameter) => [parameter.key, parameter]));
  return {
    modelId: model.id,
    familyId: model.familyId,
    revision: model.revision,
    endpoint: '/api/v1/runs/prepare',
    indexes: Object.entries(model.indexes).map(([name, values]) => ({
      name,
      type: 'string',
      required: true,
      acceptedValues: values,
    })),
    externalInputs: (model.externalInputs || []).map((input) => ({
      name: input.name,
      type: input.type,
      required: true,
      description: input.description || '',
    })),
    optionalParameterOverrides: model.parameters
      .filter((parameter) => parameter.source === 'sql')
      .map((parameter) => ({
        name: parameter.key,
        type: parameter.dataType || 'number',
        required: false,
        description: parameter.description || '',
      })),
    outputs: model.outputs.map((name) => ({
      name,
      type: definitions.get(name)?.dataType || 'number',
      unit: definitions.get(name)?.unit || '',
    })),
  };
}

function modelResponsePayload(model: Model) {
  const contract = modelContractPayload(model);
  return {
    runId: 'RUN_UNIQUE_ID',
    status: 'prepared',
    disposition: 'ready',
    controlModel: {
      id: model.id,
      familyId: model.familyId,
      name: model.name,
      revision: model.revision,
    },
    outputs: contract.outputs.map((output) => ({
      parameter: output.name,
      value: exampleValue(output.type),
      ...(output.unit ? { unit: output.unit } : {}),
      boundLimited: false,
      limitAction: 'warn',
    })),
  };
}

function modelAPIPayload(model: Model, view: 'request' | 'response' | 'contract') {
  const payload =
    view === 'request'
      ? modelRequest(model)
      : view === 'response'
        ? modelResponsePayload(model)
        : modelContractPayload(model);
  return JSON.stringify(payload, null, 2);
}
