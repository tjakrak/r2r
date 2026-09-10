import { createContext, useContext, useEffect, useRef, useState } from 'react';
import dagre from '@dagrejs/dagre';
import {
  ChevronDown,
  Code2,
  Copy,
  Pencil,
  Network,
  Plus,
  RotateCcw,
  Search,
  ShieldCheck,
  Trash2,
  X,
  ZoomIn,
  ZoomOut,
} from 'lucide-react';
import { appPath } from './paths';

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
  or?: LogicPredicate[];
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
type InputDefinition = {
  name: string;
  displayName: string;
  category: 'index' | 'external';
  dataType: 'string' | 'number' | 'boolean';
  description?: string;
  status: 'active' | 'deprecated';
  system?: boolean;
};
type FeedbackInput = ExternalInput & { required: boolean };
type DataSampleColumn = { name: string; type: 'string' | 'number' | 'boolean' };
type DataSample = {
  key: string;
  description?: string;
  source: 'sql' | 'static';
  dataSourceId?: string;
  query: string;
  columns?: DataSampleColumn[];
  rows?: Record<string, string | number | boolean | null>[];
  limit: number;
};
type Source = {
  id: string;
  name: string;
  type: string;
  engine: string;
  host: string;
  port: number;
  database: string;
  schema?: string;
  username?: string;
  secretRef?: string;
  tlsMode: string;
  timeoutSeconds: number;
  location: string;
  enabled: boolean;
};
type DependencyGraph = {
  nodes: {
    id: string;
    key: string;
    kind: 'parameter' | 'sample' | 'external' | 'feedback' | 'index';
    properties: { label: string; value: unknown }[];
  }[];
  edges: { from: string; to: string; reasons?: string[] }[];
  order: string[];
};
type ParameterUsage = {
  section: 'parameter' | 'data-sample' | 'outputs' | 'partition';
  location: string;
  detail: string;
  parameterIndex?: number;
};
const DataSourcesContext = createContext<Source[]>([]);

function refactorModel(form: any) {
  return {
    id: form.id || 'DRAFT_REFACTOR',
    familyId: form.familyId || 'DRAFT_REFACTOR_FAMILY',
    name: form.name || 'Draft model',
    owner: form.owner || 'draft',
    revision: form.revision || 1,
    status: 'draft',
    changeNote: form.note || 'Parameter refactor',
    createdBy: form.createdBy || 'draft',
    createdAt: form.createdAt || '',
    description: form.description || '',
    indexes: form.indexes,
    externalInputs: form.externalInputs,
    feedbackInputs: form.feedbackInputs || [],
    executionPolicy: form.executionPolicy,
    parameters: form.parameters,
    dataSamples: form.dataSamples,
    outputs: form.outputs,
    enabled: false,
  };
}
const emptyParameter = (): Parameter => ({
  key: '',
  description: '',
  unit: '',
  dataType: 'number',
  source: 'expression',
  definition: '0',
});
const emptySample = (): DataSample => ({
  key: '',
  description: '',
  source: 'sql',
  query: '',
  columns: [],
  rows: [],
  limit: 10,
});
const emptyExternalInput = (): ExternalInput => ({
  name: '',
  description: '',
  type: 'string',
});

type SQLReference = {
  name: string;
  source: string;
  dataType?: 'string' | 'number' | 'boolean';
};

function SQLAutocomplete({
  value,
  references,
  placeholder,
  onChange,
}: {
  value: string;
  references: SQLReference[];
  placeholder: string;
  onChange: (value: string) => void;
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [matches, setMatches] = useState<SQLReference[]>([]);
  const [activeIndex, setActiveIndex] = useState(0);
  const updateMatches = (text: string, caret: number) => {
    const token = text.slice(0, caret).match(/@([A-Za-z_][A-Za-z0-9_]*)?$/);
    if (!token) {
      setMatches([]);
      return;
    }
    const query = (token[1] || '').toLowerCase();
    setMatches(
      references.filter((reference) => reference.name.toLowerCase().startsWith(query)).slice(0, 8),
    );
    setActiveIndex(0);
  };
  const insert = (reference: SQLReference) => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    const caret = textarea.selectionStart;
    const before = value.slice(0, caret);
    const token = before.match(/@([A-Za-z_][A-Za-z0-9_]*)?$/);
    if (!token) return;
    const start = caret - token[0].length;
    const inserted = `@${reference.name}`;
    const next = value.slice(0, start) + inserted + value.slice(caret);
    onChange(next);
    setMatches([]);
    requestAnimationFrame(() => {
      textarea.focus();
      textarea.setSelectionRange(start + inserted.length, start + inserted.length);
    });
  };
  return (
    <div className="sql-autocomplete">
      <textarea
        ref={textareaRef}
        className="formula parameter-query"
        value={value}
        onChange={(event) => {
          onChange(event.target.value);
          updateMatches(event.target.value, event.target.selectionStart);
        }}
        onClick={(event) =>
          updateMatches(event.currentTarget.value, event.currentTarget.selectionStart)
        }
        onKeyDown={(event) => {
          if (!matches.length) return;
          if (event.key === 'ArrowDown') {
            event.preventDefault();
            setActiveIndex((current) => (current + 1) % matches.length);
          } else if (event.key === 'ArrowUp') {
            event.preventDefault();
            setActiveIndex((current) => (current - 1 + matches.length) % matches.length);
          } else if (event.key === 'Enter' || event.key === 'Tab') {
            event.preventDefault();
            insert(matches[activeIndex]);
          } else if (event.key === 'Escape') {
            setMatches([]);
          }
        }}
        onBlur={() => window.setTimeout(() => setMatches([]), 120)}
        placeholder={placeholder}
      />
      {matches.length > 0 && (
        <div className="sql-autocomplete-menu">
          {matches.map((reference, index) => (
            <button
              type="button"
              className={index === activeIndex ? 'active' : ''}
              key={`${reference.source}:${reference.name}`}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => insert(reference)}
            >
              <span>@{reference.name}</span>
              <small>{reference.source}</small>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

type SQLQueryTestResult = {
  status: 'success';
  queryType: 'parameter' | 'data_sample';
  value?: unknown;
  dataType?: string;
  columns?: string[];
  rows?: Record<string, unknown>[];
  rowCount?: number;
  truncated?: boolean;
  elapsedMs: number;
};

function SQLQueryTester({
  dataSourceId,
  query,
  queryType,
  dataType,
  columns,
  limit,
  references,
}: {
  dataSourceId?: string;
  query: string;
  queryType: 'parameter' | 'data_sample';
  dataType?: 'string' | 'number' | 'boolean';
  columns?: DataSampleColumn[];
  limit?: number;
  references: SQLReference[];
}) {
  const referencedNames = Array.from(
    new Set(Array.from(query.matchAll(/@([A-Za-z_][A-Za-z0-9_]*)/g), (match) => match[1])),
  );
  const referenced = referencedNames.map(
    (name) =>
      references.find((reference) => reference.name.toLowerCase() === name.toLowerCase()) || {
        name,
        source: 'Query parameter',
        dataType: 'string' as const,
      },
  );
  const referenceKey = referenced
    .map((reference) => `${reference.name}:${reference.dataType || 'string'}`)
    .join('|');
  const [inputs, setInputs] = useState<Record<string, string>>({});
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState<SQLQueryTestResult>();

  useEffect(() => {
    setInputs((current) =>
      Object.fromEntries(referenced.map((reference) => [reference.name, current[reference.name] || ''])),
    );
    setResult(undefined);
    setError('');
  }, [referenceKey]);

  const convertedInputs = () =>
    Object.fromEntries(
      referenced.map((reference) => {
        const raw = inputs[reference.name] || '';
        if (reference.dataType === 'number') return [reference.name, Number(raw)];
        if (reference.dataType === 'boolean') return [reference.name, raw === 'true'];
        return [reference.name, raw];
      }),
    );

  const test = async () => {
    if (!dataSourceId || !query.trim()) return;
    setTesting(true);
    setError('');
    setResult(undefined);
    try {
      const response = await fetch(
        appPath(`/api/sources/${encodeURIComponent(dataSourceId)}/test-query`),
        {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            queryType,
            query,
            inputs: convertedInputs(),
            dataType,
            columns: columns || [],
            limit,
          }),
        },
      );
      const body = await response.json();
      if (!response.ok) throw new Error(body.error || 'Query test failed');
      setResult(body);
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setTesting(false);
    }
  };

  const missingInput = referenced.some(
    (reference) => reference.dataType !== 'boolean' && !(inputs[reference.name] || '').trim(),
  );

  return (
    <div className="sql-query-tester span-three">
      <div className="sql-query-test-heading">
        <div>
          <strong>Test query</strong>
          <small>Runs this read-only query without saving the model.</small>
        </div>
        <button
          type="button"
          className="secondary"
          disabled={testing || !dataSourceId || !query.trim() || missingInput}
          onClick={test}
        >
          <Code2 /> {testing ? 'Testing…' : 'Run test'}
        </button>
      </div>
      {referenced.length > 0 && (
        <div className="sql-query-test-inputs">
          {referenced.map((reference) => (
            <label key={reference.name}>
              @{reference.name}
              {reference.dataType === 'boolean' ? (
                <select
                  value={inputs[reference.name] || 'false'}
                  onChange={(event) =>
                    setInputs({ ...inputs, [reference.name]: event.target.value })
                  }
                >
                  <option value="false">False</option>
                  <option value="true">True</option>
                </select>
              ) : (
                <input
                  type={reference.dataType === 'number' ? 'number' : 'text'}
                  value={inputs[reference.name] || ''}
                  onChange={(event) =>
                    setInputs({ ...inputs, [reference.name]: event.target.value })
                  }
                  placeholder={`${reference.source} · ${reference.dataType || 'string'}`}
                />
              )}
            </label>
          ))}
        </div>
      )}
      {error && <div className="connection-result failed"><strong>Query failed</strong><span>{error}</span></div>}
      {result?.queryType === 'parameter' && (
        <div className="connection-result success">
          <strong>Result: {result.value === null ? 'NULL' : String(result.value)}</strong>
          <span>{result.dataType} · {result.elapsedMs} ms</span>
        </div>
      )}
      {result?.queryType === 'data_sample' && (
        <div className="sql-query-test-result">
          <div><strong>{result.rowCount || 0} rows</strong><span>{result.elapsedMs} ms{result.truncated ? ' · preview limited' : ''}</span></div>
          <div className="sql-query-test-table-wrap">
            <table>
              <thead><tr>{(result.columns || []).map((column) => <th key={column}>{column}</th>)}</tr></thead>
              <tbody>
                {(result.rows || []).map((row, rowIndex) => (
                  <tr key={rowIndex}>{(result.columns || []).map((column) => <td key={column}>{row[column] == null ? 'NULL' : String(row[column])}</td>)}</tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

function highlightExpression(expression: string) {
  const pattern = /"(?:\\.|[^"\\])*"|@[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?|\b(?:AND|OR|true|false)\b|>=|<=|==|!=|[+*/<>-]|\b\d+(?:\.\d+)?\b|[A-Za-z_][A-Za-z0-9_]*|\s+|./gi;
  const tokens = expression.match(pattern) || [];
  let offset = 0;
  return tokens.map((token, index) => {
    const start = offset;
    offset += token.length;
    if (/^"/.test(token)) return <span className="expression-token string" key={index}>{token}</span>;
    if (/^@/.test(token)) {
      const dot = token.indexOf('.');
      return (
        <span className="expression-token reference" key={index}>
          <span>{dot < 0 ? token : token.slice(0, dot)}</span>
          {dot >= 0 && <span className="column">{token.slice(dot)}</span>}
        </span>
      );
    }
    if (/^(AND|OR)$/i.test(token)) return <span className="expression-token keyword" key={index}>{token}</span>;
    if (/^(true|false)$/i.test(token)) return <span className="expression-token boolean" key={index}>{token}</span>;
    if (/^(>=|<=|==|!=|[+*/<>-])$/.test(token)) return <span className="expression-token operator" key={index}>{token}</span>;
    if (/^\d/.test(token)) return <span className="expression-token number" key={index}>{token}</span>;
    if (/^[A-Za-z_]/.test(token) && /^\s*\(/.test(expression.slice(start + token.length)))
      return <span className="expression-token function" key={index}>{token}</span>;
    return token;
  });
}

function ExpressionAutocomplete({
  value,
  references,
  placeholder,
  onChange,
  multiline = false,
}: {
  value: string;
  references: {
    name: string;
    source: string;
    plain?: boolean;
    kind?: 'value' | 'sample' | 'sample-column';
  }[];
  placeholder: string;
  onChange: (value: string) => void;
  multiline?: boolean;
}) {
  const inputRef = useRef<any>(null);
  const highlightRef = useRef<HTMLPreElement>(null);
  const [matches, setMatches] = useState<
    { name: string; source: string; plain?: boolean; kind?: 'value' | 'sample' | 'sample-column' }[]
  >([]);
  const [activeIndex, setActiveIndex] = useState(0);
  const updateMatches = (text: string, caret: number) => {
    const before = text.slice(0, caret);
    const referenceToken = before.match(
      /@([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]*)?)?$/,
    );
    const functionToken = before.match(/(?:^|[^@A-Za-z0-9_])([A-Za-z_][A-Za-z0-9_]*)$/);
    if (referenceToken) {
      const query = (referenceToken[1] || '').toLowerCase();
      const prefix = before.slice(0, before.length - referenceToken[0].length);
      const kind = query.includes('.')
        ? 'sample-column'
        : /(?:CONTAINS|MATCHES|FIRST|NTH|LAST|MIN|MAX|AVG|MEDIAN|RANGE|STDEV|SUM|COUNT)\s*\(\s*$/i.test(
            prefix,
          )
          ? 'sample-column'
          : /LOOKUP_(?:ONE|FIRST)\s*\([^)]*(?:,|\bAND|\bOR)\s*$/i.test(prefix)
            ? 'sample-column'
          : /LOOKUP_(?:ONE|FIRST)\s*\(\s*$/i.test(prefix)
            ? 'sample'
            : 'value';
      setMatches(
        references
          .filter(
            (item) =>
              !item.plain &&
              (item.kind || 'value') === kind &&
              item.name.toLowerCase().startsWith(query),
          )
          .slice(0, 10),
      );
    } else if (functionToken) {
      const query = functionToken[1].toLowerCase();
      setMatches(
        references
          .filter((item) => item.plain && item.name.toLowerCase().startsWith(query))
          .slice(0, 10),
      );
    } else {
      setMatches([]);
    }
    setActiveIndex(0);
  };
  const insert = (reference: {
    name: string;
    source: string;
    plain?: boolean;
    kind?: 'value' | 'sample' | 'sample-column';
  }) => {
    const input = inputRef.current;
    if (!input) return;
    const caret = input.selectionStart ?? value.length;
    const before = value.slice(0, caret);
    const token = reference.plain
      ? before.match(/(?:^|[^@A-Za-z0-9_])([A-Za-z_][A-Za-z0-9_]*)$/)
      : before.match(/@([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]*)?)?$/);
    if (!token) return;
    const start = reference.plain ? caret - token[1].length : caret - token[0].length;
    const inserted = reference.plain ? reference.name : `@${reference.name}`;
    const next = value.slice(0, start) + inserted + value.slice(caret);
    onChange(next);
    const placeholderOffset = reference.plain ? inserted.indexOf('@') : -1;
    const nextCaret =
      placeholderOffset >= 0 ? start + placeholderOffset + 1 : start + inserted.length;
    if (placeholderOffset >= 0) updateMatches(next, nextCaret);
    else setMatches([]);
    requestAnimationFrame(() => {
      input.focus();
      input.setSelectionRange(nextCaret, nextCaret);
    });
  };
  const editorProps: any = {
    ref: inputRef,
    className: 'formula',
    value,
    placeholder,
    onChange: (event: any) => {
      onChange(event.target.value);
      const caret = event.target.selectionStart ?? event.target.value.length;
      updateMatches(event.target.value, caret);
    },
    onClick: (event: any) => {
      const caret = event.currentTarget.selectionStart ?? 0;
      updateMatches(event.currentTarget.value, caret);
    },
    onScroll: (event: any) => {
      if (!highlightRef.current) return;
      highlightRef.current.scrollTop = event.currentTarget.scrollTop;
      highlightRef.current.scrollLeft = event.currentTarget.scrollLeft;
    },
    onKeyDown: (event: any) => {
      if (!matches.length) return;
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setActiveIndex((current) => (current + 1) % matches.length);
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        setActiveIndex((current) => (current - 1 + matches.length) % matches.length);
      } else if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault();
        insert(matches[activeIndex]);
      } else if (event.key === 'Escape') setMatches([]);
    },
    onBlur: () => window.setTimeout(() => setMatches([]), 120),
  };
  return (
    <div className={`sql-autocomplete logic-autocomplete ${multiline ? 'multiline' : ''}`}>
      {multiline && (
        <pre className="expression-highlight" aria-hidden="true" ref={highlightRef}>
          {highlightExpression(value)}
          {'\n'}
        </pre>
      )}
      {multiline ? <textarea {...editorProps} /> : <input {...editorProps} />}
      {matches.length > 0 && (
        <div className="sql-autocomplete-menu">
          {matches.map((item, index) => (
            <button
              type="button"
              className={index === activeIndex ? 'active' : ''}
              key={`${item.source}:${item.name}`}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => insert(item)}
            >
              <span>{item.plain ? item.name : `@${item.name}`}</span>
              <small>{item.source}</small>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

type ExpressionReference = {
  name: string;
  source: string;
  plain?: boolean;
  kind?: 'value' | 'sample' | 'sample-column';
};

const functionHelp: Record<string, { signature: string; description: string }> = {
  IF: {
    signature: 'IF(condition, result_if_true, result_if_false)',
    description: 'Evaluates a condition and returns one of two results.',
  },
  IS_NULL: {
    signature: 'IS_NULL(@value)',
    description: 'Returns true when the supplied parameter or lookup result is null.',
  },
  LOOKUP_ONE: {
    signature: 'LOOKUP_ONE(@sample, filter, @sample.return_column)',
    description: 'Returns null for no match, the selected value for one match, and errors for multiple matches.',
  },
  LOOKUP_FIRST: {
    signature: 'LOOKUP_FIRST(@sample, filter, @sample.return_column)',
    description: 'Returns null for no match or the selected column from the first matching row.',
  },
  CONTAINS: {
    signature: 'CONTAINS(@sample.column, text)',
    description: 'Returns true when any row equals the supplied text, ignoring letter case.',
  },
  MATCHES: {
    signature: 'MATCHES(@sample.column, pattern)',
    description: 'Returns true when any row matches a Go-compatible regular expression.',
  },
  FIRST: { signature: 'FIRST(@sample.column)', description: 'Returns the first row value.' },
  NTH: { signature: 'NTH(@sample.column, row)', description: 'Returns a row by its 1-based position.' },
  LAST: { signature: 'LAST(@sample.column)', description: 'Returns the last row value.' },
};

for (const name of ['AVG', 'MAX', 'MEDIAN', 'MIN', 'RANGE', 'STDEV', 'SUM', 'COUNT']) {
  functionHelp[name] = {
    signature: `${name}(@sample.column)`,
    description: `Calculates ${name.toLowerCase()} over the selected data-sample column.`,
  };
}
for (const name of ['ABS', 'CEIL', 'COS', 'EXP', 'FLOOR', 'LOG', 'ROUND', 'SIN', 'SQRT', 'TAN']) {
  functionHelp[name] = {
    signature: `${name}(@value)`,
    description: `Applies the ${name.toLowerCase()} mathematical function.`,
  };
}
Object.assign(functionHelp, {
  POW: { signature: 'POW(@value, exponent)', description: 'Raises a number to an exponent.' },
  SUBSTR: { signature: 'SUBSTR(@text, start, length)', description: 'Returns part of a text value.' },
  REPLACE: { signature: 'REPLACE(@text, "old", "new")', description: 'Replaces matching text.' },
  LOWER: { signature: 'LOWER(@text)', description: 'Converts text to lowercase.' },
  UPPER: { signature: 'UPPER(@text)', description: 'Converts text to uppercase.' },
  TRIM: { signature: 'TRIM(@text)', description: 'Removes surrounding whitespace.' },
  CONCAT: { signature: 'CONCAT(value, value)', description: 'Joins text values.' },
  LEN: { signature: 'LEN(@text)', description: 'Returns the number of characters.' },
});

function expressionTokens(expression: string) {
  return expression.match(/"(?:\\.|[^"\\])*"|@[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?|>=|<=|==|!=|\bAND\b|\bOR\b|[A-Za-z_][A-Za-z0-9_]*|-?\d+(?:\.\d+)?|[(),+*/<>-]/gi) || [];
}

function formatExpression(expression: string) {
  const tokens = expressionTokens(expression.trim());
  if (!tokens.length) return expression.trim();
  let output = '';
  let depth = 0;
  const functionStack: boolean[] = [];
  const indent = () => '  '.repeat(depth);
  const newline = () => {
    output = `${output.trimEnd()}\n${indent()}`;
  };
  const word = (token: string) => /^[@A-Za-z0-9_".-]/.test(token);
  tokens.forEach((token, index) => {
    const previous = tokens[index - 1] || '';
    if (token === '(') {
      const isFunction = /^[A-Za-z_][A-Za-z0-9_]*$/.test(previous);
      output = output.trimEnd() + '(';
      functionStack.push(isFunction);
      depth++;
      if (isFunction) newline();
    } else if (token === ')') {
      const wasFunction = functionStack.pop() || false;
      depth = Math.max(0, depth - 1);
      if (wasFunction) newline();
      output = output.trimEnd() + ')';
    } else if (token === ',') {
      output = output.trimEnd() + ',';
      if (functionStack[functionStack.length - 1]) newline();
      else output += ' ';
    } else if (/^(AND|OR)$/i.test(token)) {
      newline();
      output += `${token.toUpperCase()} `;
    } else if (/^(==|!=|>=|<=|>|<|\+|-|\*|\/)$/i.test(token)) {
      output = `${output.trimEnd()} ${token} `;
    } else {
      if (output && !/[\s(]$/.test(output) && word(token) && word(previous)) output += ' ';
      output += token;
    }
  });
  return output.trim();
}

function splitExpressionArguments(value: string) {
  const parts: string[] = [];
  let start = 0;
  let depth = 0;
  let quoted = false;
  for (let index = 0; index < value.length; index++) {
    const character = value[index];
    if (character === '"' && value[index - 1] !== '\\') quoted = !quoted;
    if (quoted) continue;
    if (character === '(') depth++;
    else if (character === ')') depth--;
    else if (character === ',' && depth === 0) {
      parts.push(value.slice(start, index).trim());
      start = index + 1;
    }
  }
  parts.push(value.slice(start).trim());
  return parts;
}

function outerExpressionCall(value: string) {
  const trimmed = value.trim();
  const match = trimmed.match(/^([A-Za-z_][A-Za-z0-9_]*)\s*\(/);
  if (!match || !trimmed.endsWith(')')) return undefined;
  return { name: match[1].toUpperCase(), args: splitExpressionArguments(trimmed.slice(match[0].length, -1)) };
}

function readableExpressionValue(value: string): string {
  const call = outerExpressionCall(value);
  if (call?.name === 'IF' && call.args.length === 3)
    return `If ${readableCondition(call.args[0])}, then ${readableExpressionValue(call.args[1])}; otherwise ${readableExpressionValue(call.args[2])}`;
  if ((call?.name === 'LOOKUP_ONE' || call?.name === 'LOOKUP_FIRST') && call.args.length === 3) {
    const mode = call.name === 'LOOKUP_ONE' ? 'the single matching row' : 'the first matching row';
    return `return ${readableReference(call.args[2])} from ${mode} in ${readableReference(call.args[0])} where ${readableCondition(call.args[1])}`;
  }
  if (call && call.args.length)
    return `${call.name.toLowerCase()} of ${call.args.map(readableExpressionValue).join(', ')}`;
  return readableCondition(value);
}

function readableReference(value: string) {
  return value.trim().replace(/^@/, '').replace('.', ' → ').replaceAll('_', ' ');
}

function readableCondition(value: string) {
  return value
    .trim()
    .replace(/@([A-Za-z_][A-Za-z0-9_]*)(?:\.([A-Za-z_][A-Za-z0-9_]*))?/g, (_, sample, column) =>
      column ? `${sample.replaceAll('_', ' ')} → ${column.replaceAll('_', ' ')}` : sample.replaceAll('_', ' '),
    )
    .replace(/\s*==\s*/g, ' equals ')
    .replace(/\s*!=\s*/g, ' does not equal ')
    .replace(/\s*>=\s*/g, ' is at least ')
    .replace(/\s*<=\s*/g, ' is at most ')
    .replace(/\s*>\s*/g, ' is greater than ')
    .replace(/\s*<\s*/g, ' is less than ')
    .replace(/\bAND\b/gi, 'and')
    .replace(/\bOR\b/gi, 'or');
}

function expressionItemDescription(label: string, template: string) {
  const functionName = template.match(/^([A-Za-z_][A-Za-z0-9_]*)\s*\(/)?.[1]?.toUpperCase();
  if (functionName && functionHelp[functionName]) return functionHelp[functionName].description;
  const descriptions: Record<string, string> = {
    Add: 'Adds two numerical values.',
    Subtract: 'Subtracts the right value from the left value.',
    Multiply: 'Multiplies two numerical values.',
    Divide: 'Divides the left value by the right value.',
    Equal: 'Checks whether two values are equal.',
    'Not equal': 'Checks whether two values are different.',
    'Greater than': 'Checks whether the left number is greater.',
    'Less than': 'Checks whether the left number is smaller.',
    AND: 'Requires both boolean conditions to be true.',
    OR: 'Requires at least one boolean condition to be true.',
  };
  return descriptions[label] || '';
}
const expressionGroups = [
  {
    name: 'Operators',
    items: [
      ['Add', '+'],
      ['Subtract', '-'],
      ['Multiply', '*'],
      ['Divide', '/'],
      ['Equal', '=='],
      ['Not equal', '!='],
      ['Greater than', '>'],
      ['Less than', '<'],
      ['AND', ' AND '],
      ['OR', ' OR '],
    ],
  },
  {
    name: 'Conditional statements',
    items: [
      ['IF', 'IF(@boolean_parameter, result_if_true, result_if_false)'],
      ['Is null', 'IS_NULL(@value)'],
    ],
  },
  {
    name: 'Aggregate functions',
    items: [
      ['Average', 'AVG(@sample.column)'],
      ['Maximum', 'MAX(@sample.column)'],
      ['Median', 'MEDIAN(@sample.column)'],
      ['Minimum', 'MIN(@sample.column)'],
      ['Range', 'RANGE(@sample.column)'],
      ['Standard deviation', 'STDEV(@sample.column)'],
      ['Sum', 'SUM(@sample.column)'],
      ['Count', 'COUNT(@sample.column)'],
    ],
  },
  {
    name: 'Math functions',
    items: [
      ['Absolute', 'ABS(@value)'],
      ['Ceiling', 'CEIL(@value)'],
      ['Cosine', 'COS(@value)'],
      ['Exponential', 'EXP(@value)'],
      ['Floor', 'FLOOR(@value)'],
      ['Natural log', 'LOG(@value)'],
      ['Power', 'POW(@value, exponent)'],
      ['Round', 'ROUND(@value)'],
      ['Sine', 'SIN(@value)'],
      ['Square root', 'SQRT(@value)'],
      ['Tangent', 'TAN(@value)'],
    ],
  },
  {
    name: 'Data sample functions',
    items: [
      ['First row', 'FIRST(@sample.column)'],
      ['Specific row', 'NTH(@sample.column, 2)'],
      ['Last row', 'LAST(@sample.column)'],
      ['Column has exact value', 'CONTAINS(@sample.column, "value")'],
      ['Column matches regex', 'MATCHES(@sample.column, "^value.*")'],
      ['Lookup exactly one', 'LOOKUP_ONE(@sample, @sample.column == @value, @sample.return_column)'],
      ['Lookup first', 'LOOKUP_FIRST(@sample, @sample.column == @value, @sample.return_column)'],
    ],
  },
  {
    name: 'String functions',
    items: [
      ['Substring', 'SUBSTR(@text, start, length)'],
      ['Replace', 'REPLACE(@text, "old", "new")'],
      ['Lowercase', 'LOWER(@text)'],
      ['Uppercase', 'UPPER(@text)'],
      ['Trim', 'TRIM(@text)'],
      ['Concatenate', 'CONCAT(@text, "suffix")'],
      ['Length', 'LEN(@text)'],
    ],
  },
] as const;

function ExpressionPopupEditor({
  value,
  references,
  placeholder,
  onChange,
}: {
  value: string;
  references: ExpressionReference[];
  placeholder: string;
  onChange: (value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(value);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [category, setCategory] = useState<string>(expressionGroups[0].name);
  const begin = () => {
    setDraft(value);
    setPreviewOpen(false);
    setOpen(true);
  };
  const insert = (template: string) => {
    const editor = document.querySelector<HTMLTextAreaElement>(
      '.expression-editor-dialog textarea',
    );
    const start = editor?.selectionStart ?? draft.length;
    const end = editor?.selectionEnd ?? start;
    const padding = start > 0 && !/\s|\($/.test(draft.slice(0, start)) ? ' ' : '';
    const inserted = `${padding}${template}`;
    setDraft(draft.slice(0, start) + inserted + draft.slice(end));
    requestAnimationFrame(() => {
      editor?.focus();
      const placeholder = inserted.indexOf('@');
      const caret = start + (placeholder >= 0 ? placeholder + 1 : inserted.length);
      editor?.setSelectionRange(caret, caret);
    });
  };
  const group = expressionGroups.find((item) => item.name === category) || expressionGroups[0];
  const explanation = draft.trim() ? readableExpressionValue(draft) : '';
  return (
    <>
      <div className="expression-field">
        <code>{value || placeholder}</code>
        <button type="button" className="secondary" onClick={begin}>
          <Code2 /> Open expression editor
        </button>
      </div>
      {open && (
        <div
          className="payload-backdrop expression-editor-backdrop"
          onMouseDown={(event) => {
            if (event.target === event.currentTarget) setOpen(false);
          }}
        >
          <section
            className="expression-editor-dialog"
            role="dialog"
            aria-modal="true"
            aria-label="Expression editor"
          >
            <header>
              <div>
                <p>EXPRESSION</p>
                <h3>Expression editor</h3>
                <span>
                  Choose a function to insert its format, then replace placeholders or type @ to
                  reference declared data.
                </span>
              </div>
              <button type="button" className="remove-index" onClick={() => setOpen(false)}>
                <X />
              </button>
            </header>
            <div className="expression-editor-layout">
              <aside>
                <strong>Functions</strong>
                {expressionGroups.map((item) => (
                  <button
                    type="button"
                    className={category === item.name ? 'active' : ''}
                    onClick={() => setCategory(item.name)}
                    key={item.name}
                  >
                    {item.name}
                  </button>
                ))}
              </aside>
              <main>
                <div className="expression-function-list">
                  <h4>{group.name}</h4>
                  {group.items.map(([label, template]) => (
                    <button type="button" key={label} onClick={() => insert(template)}>
                      <strong>{label}</strong>
                      <code>{template}</code>
                      <span>{expressionItemDescription(label, template)}</span>
                    </button>
                  ))}
                </div>
                <div className="expression-workspace">
                  <div className="expression-workspace-heading">
                    <label>Expression</label>
                    <button
                      type="button"
                      className="secondary"
                      onClick={() => {
                        const formatted = formatExpression(draft);
                        setDraft(formatted);
                        requestAnimationFrame(() => {
                          const editor = document.querySelector<HTMLTextAreaElement>(
                            '.expression-editor-dialog textarea',
                          );
                          editor?.focus();
                          editor?.setSelectionRange(formatted.length, formatted.length);
                        });
                      }}
                    >
                      Format expression
                    </button>
                  </div>
                  <ExpressionAutocomplete
                    multiline
                    value={draft}
                    onChange={setDraft}
                    references={references}
                    placeholder={placeholder}
                  />
                  <small>References begin with @. Function names do not.</small>
                  <div className={`expression-readable-preview ${previewOpen ? 'expanded' : ''}`}>
                    <button
                      type="button"
                      className="expression-preview-toggle"
                      aria-expanded={previewOpen}
                      onClick={() => setPreviewOpen((current) => !current)}
                    >
                      <span>
                        <strong>Plain-language preview</strong>
                        <small>Readable explanation of the current expression</small>
                      </span>
                      <ChevronDown />
                    </button>
                    {previewOpen && (
                      <div className="expression-preview-body">
                        <p>{explanation || 'Enter an expression to see a readable explanation.'}</p>
                        <small>This preview is explanatory; the expression above is executed.</small>
                      </div>
                    )}
                  </div>
                </div>
              </main>
            </div>
            <footer>
              <button type="button" className="secondary" onClick={() => setOpen(false)}>
                Cancel
              </button>
              <button
                type="button"
                className="primary"
                onClick={() => {
                  onChange(draft);
                  setOpen(false);
                }}
              >
                Apply expression
              </button>
            </footer>
          </section>
        </div>
      )}
    </>
  );
}
export default function ModelEditor({
  isNew,
  editingDraft,
  form,
  setForm,
  updateInput,
  busy,
  error,
  create,
  back,
}: any) {
  const [showPayload, setShowPayload] = useState(false);
  const [payloadView, setPayloadView] = useState<'request' | 'response' | 'contract'>('request');
  const [showDependencies, setShowDependencies] = useState(false);
  const [expandedParameter, setExpandedParameter] = useState<number | null>(null);
  const [expandedSample, setExpandedSample] = useState<number | null>(null);
  const [pendingParameter, setPendingParameter] = useState<Parameter>();
  const [pendingSample, setPendingSample] = useState<DataSample>();
  const [pendingExternal, setPendingExternal] = useState<ExternalInput>();
  const [pendingFeedback, setPendingFeedback] = useState<FeedbackInput>();
  const [pendingOutput, setPendingOutput] = useState('');
  const [addingOutput, setAddingOutput] = useState(false);
  const [pendingIndex, setPendingIndex] = useState<{ name: string; values: string[] }>();
  const [usageView, setUsageView] = useState<{ name: string; usages: ParameterUsage[] }>();
  const [renameView, setRenameView] = useState<{
    index: number;
    oldName: string;
    newName: string;
    usages: ParameterUsage[];
  }>();
  const [refactorBusy, setRefactorBusy] = useState(false);
  const [refactorError, setRefactorError] = useState('');
  const requestRefactor = async (action: 'find' | 'rename', oldName: string, newName = '') => {
    const response = await fetch(appPath('/api/parameters/refactor'), {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action, oldName, newName, model: refactorModel(form) }),
    });
    const text = await response.text();
    let data: any = {};
    try {
      data = text ? JSON.parse(text) : {};
    } catch {
      throw new Error(`Parameter refactor returned HTTP ${response.status} with an invalid response.`);
    }
    if (!response.ok) throw new Error(data.error || `Parameter refactor failed with HTTP ${response.status}.`);
    return data as { usages: ParameterUsage[]; model?: any };
  };
  const findUsages = async (parameter: Parameter, index: number, rename = false) => {
    setRefactorBusy(true);
    setRefactorError('');
    try {
      const result = await requestRefactor('find', parameter.key);
      if (rename) {
        setRenameView({ index, oldName: parameter.key, newName: parameter.key, usages: result.usages || [] });
      } else {
        setUsageView({ name: parameter.key, usages: result.usages || [] });
      }
    } catch (reason) {
      setRefactorError((reason as Error).message);
    } finally {
      setRefactorBusy(false);
    }
  };
  const openUsage = (usage: ParameterUsage) => {
    setUsageView(undefined);
    if (usage.section === 'parameter' && usage.parameterIndex !== undefined) {
      setExpandedParameter(usage.parameterIndex);
    }
    if (usage.section === 'data-sample') {
      const index = form.dataSamples.findIndex((sample: DataSample) => sample.key === usage.location);
      if (index >= 0) setExpandedSample(index);
    }
    const target =
      usage.section === 'parameter' && usage.parameterIndex !== undefined
        ? `parameter-refactor-${usage.parameterIndex}`
        : usage.section === 'data-sample'
          ? `data-sample-refactor-${form.dataSamples.findIndex((sample: DataSample) => sample.key === usage.location)}`
          : usage.section === 'outputs'
            ? 'model-outputs-refactor'
            : 'model-partition-refactor';
    window.setTimeout(() => document.getElementById(target)?.scrollIntoView({ behavior: 'smooth', block: 'center' }), 0);
  };
  const externalContext = Object.fromEntries(
    form.externalInputs.map((input: ExternalInput) => [
      input.name || 'external_input',
      input.type === 'number' ? 0 : input.type === 'boolean' ? false : 'VALUE',
    ]),
  );
  const adaptiveFeedbackNames = new Set<string>(
    form.parameters
      .map((parameter: Parameter) =>
        parameter.adaptive?.updateInput?.source === 'post-run-feedback'
          ? parameter.adaptive.updateInput.name
          : '',
      )
      .filter((name: string) => Boolean(name)),
  );
  const payload = JSON.stringify(
    {
      indexes: Object.fromEntries(
        Object.entries(form.indexes).map(([key, values]) => [
          key,
          (values as string[])[0] === '*' ? 'VALUE' : (values as string[])[0] || 'VALUE',
        ]),
      ),
      externalInputs: externalContext,
    },
    null,
    2,
  );
  const parameterDefinitions = new Map<string, Parameter>(
    form.parameters.map((parameter: Parameter) => [parameter.key, parameter]),
  );
  const contractPayload = {
    modelId: form.id || 'AVAILABLE_AFTER_SAVE',
    familyId: form.familyId || 'AVAILABLE_AFTER_SAVE',
    revision: form.revision || 1,
    endpoint: '/api/v1/runs/prepare',
    indexes: Object.entries(form.indexes).map(([name, values]) => ({
      name,
      type: 'string',
      required: ['machine', 'action', 'material'].includes(name.toLowerCase()),
      acceptedValues: values,
    })),
    externalInputs: form.externalInputs.map((input: ExternalInput) => ({
      name: input.name,
      type: input.type,
      required: true,
      description: input.description || '',
    })),
    outputs: form.outputs.map((name: string) => ({
      name,
      type: parameterDefinitions.get(name)?.dataType || 'number',
      unit: parameterDefinitions.get(name)?.unit || '',
    })),
  };
  const responsePayload = {
    runId: 'RUN_UNIQUE_ID',
    status: 'prepared',
    disposition: 'ready',
    controlModel: {
      id: form.id || 'MODEL_ID',
      familyId: form.familyId || 'FAMILY_ID',
      name: form.name || 'MODEL_NAME',
      revision: form.revision || 1,
    },
    outputs: contractPayload.outputs.map(
      (output: { name: string; type: string; unit: string }) => ({
        parameter: output.name,
        value: output.type === 'number' ? 0 : output.type === 'boolean' ? false : 'VALUE',
        ...(output.unit ? { unit: output.unit } : {}),
        boundLimited: false,
        limitAction: 'warn',
      }),
    ),
  };
  const displayedPayload =
    payloadView === 'request'
      ? payload
      : JSON.stringify(payloadView === 'response' ? responsePayload : contractPayload, null, 2);
  return (
    <DataSourcesContext.Provider value={form.sources}>
      <div className="revision-form model-editor">
        {!isNew && (
          <button className="back" onClick={back}>
            ← Model details
          </button>
        )}
        <div className="editor-title">
          <div>
            <h3>
              {isNew
                ? 'Create control model'
                : editingDraft
                  ? 'Edit draft revision'
                  : 'Create model revision'}
            </h3>
            <p>
              Configure the model inputs and optional data samples, define how each parameter is
              resolved, then choose which parameters are returned as outputs.
            </p>
          </div>
          <div className="editor-title-actions">
            <button className="secondary" onClick={() => setShowDependencies(true)}>
              <Network />
              View dependencies
            </button>
            <button className="secondary" onClick={() => setShowPayload(true)}>
              <Code2 />
              View API payload
            </button>
          </div>
        </div>
        <EditorHeader title="Model identity" />
        <div className="form-grid">
          <label className="span-two">
            <span className="field-label">
              Model name <b className="required-badge">Required</b>
            </span>
            <input
              required
              aria-invalid={!form.name.trim()}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </label>
          <label className="span-two">
            Description
            <input
              value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
            />
          </label>
        </div>
        <IndexEditor
          indexes={form.indexes}
          onChange={(indexes) => setForm({ ...form, indexes })}
          onAdd={() => setPendingIndex({ name: '', values: [] })}
        />
        <EditorHeader
          title="Data samples"
          action={
            <button className="secondary" onClick={() => setPendingSample(emptySample())}>
              <Plus />
              Add data sample
            </button>
          }
        />
        <p className="field-help">
          Define tabular SQL results before using their columns in parameters, for example
          MAX(datasample1.thickness_nm).
        </p>
        {form.dataSamples.length === 0 && (
          <div className="parameter-empty">No data samples defined.</div>
        )}
        {form.dataSamples.map((sample: DataSample, index: number) => (
          <div id={`data-sample-refactor-${index}`} key={index}>
            <DataSampleCard
              sample={sample}
              parameters={form.parameters}
              externalInputs={form.externalInputs}
              indexes={form.indexes}
              expanded={expandedSample === index}
              onToggle={() => setExpandedSample(expandedSample === index ? null : index)}
              onChange={(next: DataSample) => updateSample(form, setForm, index, next)}
              onRemove={() => {
                setForm({
                  ...form,
                  dataSamples: form.dataSamples.filter((_: unknown, n: number) => n !== index),
                });
                setExpandedSample(null);
              }}
            />
          </div>
        ))}
        <ExternalInputsEditor
          inputs={form.externalInputs}
          onAdd={() => setPendingExternal(emptyExternalInput())}
          onChange={(externalInputs) => setForm({ ...form, externalInputs })}
        />
        <EditorHeader
          title="Parameters"
          action={
            <button className="secondary" onClick={() => setPendingParameter(emptyParameter())}>
              <Plus />
              Add parameter
            </button>
          }
        />
        <p className="field-help">
          Dependencies determine execution order automatically. LSL and USL flag returned values
          outside engineering limits.
        </p>
        {form.parameters.length === 0 && (
          <div className="parameter-empty">No parameters defined.</div>
        )}
        {form.parameters.map((parameter: Parameter, index: number) => (
          <div id={`parameter-refactor-${index}`} key={index}>
            <ParameterCard
              parameter={parameter}
              previous={form.parameters.slice(0, index)}
              allParameters={form.parameters.filter((_: Parameter, n: number) => n !== index)}
              samples={form.dataSamples}
              externalInputs={form.externalInputs}
              feedbackInputs={form.feedbackInputs || []}
              indexes={form.indexes}
              expanded={expandedParameter === index}
              onToggle={() => setExpandedParameter(expandedParameter === index ? null : index)}
              onChange={(next: Parameter) => updateInput(index, next)}
              onFindUsages={() => findUsages(parameter, index)}
              onRename={() => findUsages(parameter, index, true)}
              refactorBusy={refactorBusy}
              onRemove={() => {
                setForm({
                  ...form,
                  parameters: form.parameters.filter((_: unknown, n: number) => n !== index),
                  outputs: form.outputs.filter((key: string) => key !== parameter.key),
                });
                setExpandedParameter(null);
              }}
            />
          </div>
        ))}
        {refactorError && <p className="error">{refactorError}</p>}
        <div id="model-outputs-refactor" />
        <EditorHeader
          title="Model outputs"
          action={
            <button
              className="secondary"
              onClick={() => {
                setPendingOutput('');
                setAddingOutput(true);
              }}
            >
              <Plus />
              Add output
            </button>
          }
        />
        <p className="field-help">
          Add the parameters returned to the calling application. Output calculations belong in
          parameter definitions.
        </p>
        {form.outputs.length === 0 && (
          <div className="parameter-empty">No output parameters selected.</div>
        )}
        <div className="output-selector">
          {form.outputs.map((output: string, index: number) => (
            <div className="output-row" key={index}>
              <label>
                Parameter
                <select
                  value={output}
                  onChange={(e) =>
                    setForm({
                      ...form,
                      outputs: form.outputs.map((key: string, n: number) =>
                        n === index ? e.target.value : key,
                      ),
                    })
                  }
                >
                  <option value="">Select a parameter</option>
                  {form.parameters
                    .filter((parameter: Parameter) => parameter.key)
                    .map((parameter: Parameter) => (
                      <option
                        key={parameter.key}
                        value={parameter.key}
                        disabled={form.outputs.some(
                          (key: string, n: number) => n !== index && key === parameter.key,
                        )}
                      >
                        {parameter.key}
                        {parameter.description ? ` - ${parameter.description}` : ''}
                      </option>
                    ))}
                </select>
              </label>
              <button
                className="remove-index"
                title="Remove output"
                onClick={() =>
                  setForm({
                    ...form,
                    outputs: form.outputs.filter((_: string, n: number) => n !== index),
                  })
                }
              >
                <Trash2 />
              </button>
            </div>
          ))}
        </div>
        <FeedbackEditor
          inputs={form.feedbackInputs || []}
          adaptiveNames={adaptiveFeedbackNames}
          onAdd={() =>
            setPendingFeedback({ name: '', description: '', type: 'number', required: false })
          }
          onChange={(feedbackInputs) => setForm({ ...form, feedbackInputs })}
        />
        <EditorHeader title="Execution policy" />
        <p className="field-help">
          Unfinished runs are unlimited by default. Enable a limit when overlapping prepared runs
          must be controlled within each selected partition.
        </p>
        <div className="execution-policy">
          <label className="execution-limit-toggle">
            <input
              type="checkbox"
              checked={(form.executionPolicy?.maxUnfinishedRuns || 0) > 0}
              onChange={(event) =>
                setForm({
                  ...form,
                  executionPolicy: {
                    ...form.executionPolicy,
                    maxUnfinishedRuns: event.target.checked ? 1 : 0,
                  },
                })
              }
            />
            Limit unfinished runs
          </label>
          <label>
            Maximum unfinished runs
            <input
              type="number"
              min="1"
              step="1"
              disabled={(form.executionPolicy?.maxUnfinishedRuns || 0) === 0}
              value={form.executionPolicy?.maxUnfinishedRuns || 1}
              onChange={(event) =>
                setForm({
                  ...form,
                  executionPolicy: {
                    ...form.executionPolicy,
                    maxUnfinishedRuns: Math.max(1, Number(event.target.value) || 1),
                  },
                })
              }
            />
          </label>
          <fieldset className="execution-partition" id="model-partition-refactor">
            <legend>Run and adaptive-state partition</legend>
            <p>
              Each value combination receives an independent run limit and adaptive state. If no
              fields are selected, the partition defaults to machine.
            </p>
            <div>
              {[
                ...Object.keys(form.indexes),
                ...(form.externalInputs || []).map((input: ExternalInput) => input.name),
              ]
                .filter(Boolean)
                .filter((field, index, fields) => fields.indexOf(field) === index)
                .map((field) => {
                  const selected = form.executionPolicy?.partitionBy || [];
                  return (
                    <label key={field}>
                      <input
                        type="checkbox"
                        checked={selected.includes(field)}
                        onChange={() =>
                          setForm({
                            ...form,
                            executionPolicy: {
                              ...form.executionPolicy,
                              maxUnfinishedRuns: form.executionPolicy?.maxUnfinishedRuns || 0,
                              partitionBy: selected.includes(field)
                                ? selected.filter((item: string) => item !== field)
                                : [...selected, field],
                            },
                          })
                        }
                      />
                      {field}
                    </label>
                  );
                })}
            </div>
          </fieldset>
        </div>
        {form.executionPolicy?.maxUnfinishedRuns > 1 &&
          form.parameters.some((parameter: Parameter) => parameter.adaptive) && (
            <p className="field-help">
              Overlapping runs reuse the current adaptive state. State advances only when no earlier
              run in the same partition remains prepared.
            </p>
          )}
        <label>
          <span className="field-label">
            <span>Change note</span>
            <b className="required-badge">Required</b>
          </span>
          <textarea
            value={form.note}
            onChange={(e) => setForm({ ...form, note: e.target.value })}
          />
        </label>
        {error && <p className="error">{error}</p>}
        {!form.name.trim() && <p className="error">Model name is required.</p>}
        {!form.note.trim() && <p className="error">Change note is required.</p>}
        <button
          className="primary full"
          disabled={busy || !form.name.trim() || !form.note.trim()}
          onClick={create}
        >
          {busy
            ? 'Saving…'
            : isNew
              ? 'Create draft model'
              : editingDraft
                ? 'Save draft revision'
                : 'Create draft revision'}
        </button>
        {pendingIndex && (
          <AddDialog title="Add index" onClose={() => setPendingIndex(undefined)}>
            <div className="form-grid">
              <label>
                <span className="field-label">
                  <span>Index name</span>
                  <b className="required-badge">Required</b>
                </span>
                <select
                  value={pendingIndex.name}
                  onChange={(event) =>
                    setPendingIndex({ ...pendingIndex, name: event.target.value })
                  }
                >
                  <option value="">Select a registered index</option>
                  {(form.inputDefinitions || [])
                    .filter(
                      (definition: InputDefinition) =>
                        definition.category === 'index' &&
                        definition.status === 'active' &&
                        !definition.system &&
                        !(definition.name in form.indexes),
                    )
                    .map((definition: InputDefinition) => (
                      <option value={definition.name} key={definition.name}>
                        {definition.displayName} (@{definition.name})
                      </option>
                    ))}
                </select>
              </label>
              <label>
                <span className="field-label">
                  <span>Accepted values</span>
                  <b className="required-badge">Required</b>
                </span>
                <IndexValuesInput
                  values={pendingIndex.values}
                  onChange={(values) => setPendingIndex({ ...pendingIndex, values })}
                />
              </label>
            </div>
            <DialogActions
              label="Save index"
              disabled={
                !pendingIndex.name.trim() ||
                pendingIndex.values.length === 0 ||
                Object.keys(form.indexes).some(
                  (key) => key.toLowerCase() === pendingIndex.name.trim().toLowerCase(),
                )
              }
              onCancel={() => setPendingIndex(undefined)}
              onSave={() => {
                const name = pendingIndex.name.trim().replace(/\s+/g, '_');
                setForm({ ...form, indexes: { ...form.indexes, [name]: pendingIndex.values } });
                setPendingIndex(undefined);
              }}
            />
          </AddDialog>
        )}
        {pendingExternal && (
          <AddDialog title="Add external input" onClose={() => setPendingExternal(undefined)}>
            <div className="form-grid three">
              <label>
                <span className="field-label">
                  <span>Name</span>
                  <b className="required-badge">Required</b>
                </span>
                <select
                  value={pendingExternal.name}
                  onChange={(event) => {
                    const definition = (form.inputDefinitions || []).find(
                      (item: InputDefinition) => item.name === event.target.value,
                    );
                    if (definition)
                      setPendingExternal({
                        name: definition.name,
                        type: definition.dataType,
                        description: definition.description,
                      });
                  }}
                >
                  <option value="">Select a registered external input</option>
                  {(form.inputDefinitions || [])
                    .filter(
                      (definition: InputDefinition) =>
                        definition.category === 'external' &&
                        definition.status === 'active' &&
                        !form.externalInputs.some(
                          (input: ExternalInput) => input.name === definition.name,
                        ),
                    )
                    .map((definition: InputDefinition) => (
                      <option value={definition.name} key={definition.name}>
                        {definition.displayName} (@{definition.name})
                      </option>
                    ))}
                </select>
              </label>
              <label>
                <span className="field-label">
                  <span>Data type</span>
                  <b className="required-badge">Required</b>
                </span>
                <select
                  disabled
                  value={pendingExternal.type}
                  onChange={(event) =>
                    setPendingExternal({
                      ...pendingExternal,
                      type: event.target.value as ExternalInput['type'],
                    })
                  }
                >
                  <option value="string">Text</option>
                  <option value="number">Number</option>
                  <option value="boolean">Boolean</option>
                </select>
              </label>
              <label>
                Description (optional)
                <input
                  disabled
                  value={pendingExternal.description || ''}
                  onChange={(event) =>
                    setPendingExternal({ ...pendingExternal, description: event.target.value })
                  }
                />
              </label>
            </div>
            <DialogActions
              label="Save external input"
              disabled={!pendingExternal.name.trim()}
              onCancel={() => setPendingExternal(undefined)}
              onSave={() => {
                setForm({
                  ...form,
                  externalInputs: [...form.externalInputs, pendingExternal],
                });
                setPendingExternal(undefined);
              }}
            />
          </AddDialog>
        )}
        {pendingFeedback && (
          <AddDialog title="Add post-run feedback" onClose={() => setPendingFeedback(undefined)}>
            <div className="form-grid three">
              <label>
                <span className="field-label">
                  <span>Name</span>
                  <b className="required-badge">Required</b>
                </span>
                <input
                  value={pendingFeedback.name}
                  onChange={(event) =>
                    setPendingFeedback({ ...pendingFeedback, name: event.target.value })
                  }
                />
              </label>
              <label>
                <span className="field-label">
                  <span>Data type</span>
                  <b className="required-badge">Required</b>
                </span>
                <select
                  value={pendingFeedback.type}
                  onChange={(event) =>
                    setPendingFeedback({
                      ...pendingFeedback,
                      type: event.target.value as FeedbackInput['type'],
                    })
                  }
                >
                  <option value="number">Number</option>
                  <option value="string">Text</option>
                </select>
              </label>
              <label>
                Description (optional)
                <input
                  value={pendingFeedback.description || ''}
                  onChange={(event) =>
                    setPendingFeedback({ ...pendingFeedback, description: event.target.value })
                  }
                />
              </label>
              <label className="feedback-required">
                <input
                  type="checkbox"
                  checked={pendingFeedback.required}
                  onChange={(event) =>
                    setPendingFeedback({ ...pendingFeedback, required: event.target.checked })
                  }
                />
                Required
              </label>
            </div>
            <DialogActions
              label="Save feedback"
              disabled={!pendingFeedback.name.trim()}
              onCancel={() => setPendingFeedback(undefined)}
              onSave={() => {
                setForm({
                  ...form,
                  feedbackInputs: [...(form.feedbackInputs || []), pendingFeedback],
                });
                setPendingFeedback(undefined);
              }}
            />
          </AddDialog>
        )}
        {addingOutput && (
          <AddDialog title="Add model output" onClose={() => setAddingOutput(false)}>
            <label>
              <span className="field-label">
                <span>Parameter</span>
                <b className="required-badge">Required</b>
              </span>
              <select
                value={pendingOutput}
                onChange={(event) => setPendingOutput(event.target.value)}
              >
                <option value="">Select a parameter</option>
                {form.parameters
                  .filter(
                    (parameter: Parameter) =>
                      parameter.key && !form.outputs.includes(parameter.key),
                  )
                  .map((parameter: Parameter) => (
                    <option key={parameter.key} value={parameter.key}>
                      {parameter.key}
                    </option>
                  ))}
              </select>
            </label>
            <DialogActions
              label="Save output"
              disabled={!pendingOutput}
              onCancel={() => setAddingOutput(false)}
              onSave={() => {
                setForm({ ...form, outputs: [...form.outputs, pendingOutput] });
                setAddingOutput(false);
              }}
            />
          </AddDialog>
        )}
        {pendingParameter && (
          <div
            className="payload-backdrop"
            onMouseDown={(event) =>
              event.target === event.currentTarget && setPendingParameter(undefined)
            }
          >
            <section className="add-definition-dialog">
              <header>
                <div>
                  <p>NEW DEFINITION</p>
                  <h3>Add parameter</h3>
                </div>
                <button className="icon" onClick={() => setPendingParameter(undefined)}>
                  <X />
                </button>
              </header>
              <div className="add-definition-body">
                <ParameterCard
                  parameter={pendingParameter}
                  previous={form.parameters}
                  allParameters={form.parameters}
                  samples={form.dataSamples}
                  externalInputs={form.externalInputs}
                  feedbackInputs={form.feedbackInputs || []}
                  indexes={form.indexes}
                  expanded
                  onToggle={() => undefined}
                  onSave={() => {
                    setForm({ ...form, parameters: [...form.parameters, pendingParameter] });
                    setPendingParameter(undefined);
                  }}
                  onChange={setPendingParameter}
                  onRemove={() => setPendingParameter(undefined)}
                />
              </div>
            </section>
          </div>
        )}
        {pendingSample && (
          <div
            className="payload-backdrop"
            onMouseDown={(event) =>
              event.target === event.currentTarget && setPendingSample(undefined)
            }
          >
            <section className="add-definition-dialog">
              <header>
                <div>
                  <p>NEW DEFINITION</p>
                  <h3>Add data sample</h3>
                </div>
                <button className="icon" onClick={() => setPendingSample(undefined)}>
                  <X />
                </button>
              </header>
              <div className="add-definition-body">
                <DataSampleCard
                  sample={pendingSample}
                  parameters={form.parameters}
                  externalInputs={form.externalInputs}
                  indexes={form.indexes}
                  expanded
                  onToggle={() => undefined}
                  onChange={setPendingSample}
                  onRemove={() => setPendingSample(undefined)}
                />
                <div className="add-definition-actions">
                  <button className="secondary" onClick={() => setPendingSample(undefined)}>
                    Cancel
                  </button>
                  <button
                    className="primary"
                    disabled={
                      !pendingSample.key.trim() ||
                      (pendingSample.source === 'sql' &&
                        (!pendingSample.dataSourceId ||
                          !pendingSample.query.trim() ||
                          !pendingSample.columns?.length ||
                          pendingSample.columns.some((column) => !column.name.trim()))) ||
                      (pendingSample.source === 'static' &&
                        (!pendingSample.columns?.length ||
                          pendingSample.columns.some((column) => !column.name.trim()) ||
                          !pendingSample.rows?.length)) ||
                      pendingSample.limit < 1
                    }
                    onClick={() => {
                      setForm({ ...form, dataSamples: [...form.dataSamples, pendingSample] });
                      setPendingSample(undefined);
                    }}
                  >
                    Save data sample
                  </button>
                </div>
              </div>
            </section>
          </div>
        )}
        {usageView && (
          <AddDialog title={`Uses of ${usageView.name}`} onClose={() => setUsageView(undefined)}>
            {usageView.usages.length ? (
              <div className="parameter-usage-list">
                {usageView.usages.map((usage, index) => (
                  <button type="button" key={`${usage.section}-${usage.location}-${index}`} onClick={() => openUsage(usage)}>
                    <strong>{usage.location}</strong>
                    <span>{usage.detail}</span>
                  </button>
                ))}
              </div>
            ) : (
              <div className="parameter-empty">No references found.</div>
            )}
            <div className="add-definition-actions">
              <button className="primary" onClick={() => setUsageView(undefined)}>Close</button>
            </div>
          </AddDialog>
        )}
        {renameView && (
          <AddDialog title="Rename parameter" onClose={() => !refactorBusy && setRenameView(undefined)}>
            <p className="refactor-rename-heading">
              Rename: <code>{renameView.oldName}</code> → <code>{renameView.newName || 'new_name'}</code>
            </p>
            <label>
              <span className="field-label">
                New parameter name <b className="required-badge">Required</b>
              </span>
              <input
                autoFocus
                value={renameView.newName}
                onChange={(event) => {
                  setRefactorError('');
                  setRenameView({ ...renameView, newName: event.target.value.trim() });
                }}
              />
            </label>
            <p className="refactor-dialog-intro">References that will be updated:</p>
            {renameView.usages.length ? (
              <div className="parameter-usage-list compact">
                {renameView.usages.map((usage, index) => (
                  <div key={`${usage.section}-${usage.location}-${index}`}>
                    <strong>{usage.location}</strong>
                    <span>{usage.detail}</span>
                  </div>
                ))}
              </div>
            ) : (
              <div className="parameter-empty">No references need to be updated.</div>
            )}
            {refactorError && <p className="error">{refactorError}</p>}
            <DialogActions
              label={refactorBusy ? 'Validating rename…' : `Rename and update ${renameView.usages.length} reference${renameView.usages.length === 1 ? '' : 's'}`}
              disabled={
                refactorBusy ||
                renameView.newName === renameView.oldName ||
                !/^[A-Za-z_][A-Za-z0-9_]*$/.test(renameView.newName)
              }
              onCancel={() => setRenameView(undefined)}
              onSave={async () => {
                setRefactorBusy(true);
                setRefactorError('');
                try {
                  const result = await requestRefactor('rename', renameView.oldName, renameView.newName);
                  if (!result.model) throw new Error('The backend did not return the renamed model.');
                  setForm({
                    ...form,
                    parameters: result.model.parameters,
                    dataSamples: result.model.dataSamples,
                    outputs: result.model.outputs,
                    executionPolicy: result.model.executionPolicy,
                  });
                  setExpandedParameter(renameView.index);
                  setRenameView(undefined);
                } catch (reason) {
                  setRefactorError((reason as Error).message);
                } finally {
                  setRefactorBusy(false);
                }
              }}
            />
          </AddDialog>
        )}
        {showPayload && (
          <div
            className="payload-backdrop"
            onMouseDown={(e) => e.target === e.currentTarget && setShowPayload(false)}
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
                    ? 'Example response returned by a successful model execution.'
                    : 'Required index and external-input contract for this revision.'}
              </p>
              <pre>{displayedPayload}</pre>
              <button
                className="primary"
                onClick={() => navigator.clipboard.writeText(displayedPayload)}
              >
                <Copy />
                Copy payload
              </button>
            </section>
          </div>
        )}
        {showDependencies && (
          <DependencyDialog
            parameters={form.parameters}
            dataSamples={form.dataSamples}
            externalInputs={form.externalInputs}
            feedbackInputs={form.feedbackInputs || []}
            indexes={form.indexes}
            outputs={form.outputs}
            onClose={() => setShowDependencies(false)}
          />
        )}
      </div>
    </DataSourcesContext.Provider>
  );
}

function AddDialog({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <div
      className="payload-backdrop"
      onMouseDown={(event) => event.target === event.currentTarget && onClose()}
    >
      <section className="add-definition-dialog">
        <header>
          <div>
            <p>NEW DEFINITION</p>
            <h3>{title}</h3>
          </div>
          <button className="icon" onClick={onClose}>
            <X />
          </button>
        </header>
        <div className="add-definition-body">{children}</div>
      </section>
    </div>
  );
}

function DialogActions({
  label,
  disabled,
  onCancel,
  onSave,
}: {
  label: string;
  disabled: boolean;
  onCancel: () => void;
  onSave: () => void;
}) {
  return (
    <div className="add-definition-actions">
      <button className="secondary" onClick={onCancel}>
        Cancel
      </button>
      <button className="primary" disabled={disabled} onClick={onSave}>
        {label}
      </button>
    </div>
  );
}

export function DependencyDialog({
  parameters,
  dataSamples,
  externalInputs,
  feedbackInputs,
  indexes,
  outputs,
  onClose,
}: {
  parameters: Parameter[];
  dataSamples: DataSample[];
  externalInputs: ExternalInput[];
  feedbackInputs: FeedbackInput[];
  indexes: Record<string, string[]>;
  outputs: string[];
  onClose: () => void;
}) {
  const [graph, setGraph] = useState<DependencyGraph>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetch(appPath('/api/dependencies'), {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ parameters, dataSamples, externalInputs, feedbackInputs, indexes }),
    })
      .then(async (response) => {
        const data = await response.json();
        if (!response.ok) throw new Error(data.error || 'Unable to build dependency graph');
        return {
          nodes: data.nodes || [],
          edges: data.edges || [],
          order: data.order || [],
        } as DependencyGraph;
      })
      .then(setGraph)
      .catch((reason: Error) => setError(reason.message))
      .finally(() => setLoading(false));
  }, [parameters, dataSamples, externalInputs, feedbackInputs, indexes]);

  return (
    <div
      className="payload-backdrop dependency-backdrop"
      onMouseDown={(event) => event.target === event.currentTarget && onClose()}
    >
      <section className="dependency-dialog">
        <header>
          <div>
            <p>MODEL EXECUTION</p>
            <h3>Parameter dependencies</h3>
          </div>
          <button className="icon" onClick={onClose}>
            <X />
          </button>
        </header>
        <div className="dependency-legend">
          <span className="parameter">Parameter</span>
          <span className="sample">Data sample</span>
          <span className="external">External input</span>
          <span className="feedback">Post-run feedback</span>
          <span className="index">Model index</span>
          <span className="output">Model output</span>
          {graph && (
            <small>
              {graph.nodes.length} nodes · {graph.edges.length} dependencies
            </small>
          )}
        </div>
        <div className="dependency-canvas">
          {loading && <div className="dependency-message">Building dependency graph…</div>}
          {error && (
            <div className="dependency-message error">
              <Network />
              <strong>Dependency error</strong>
              <span>{error}</span>
            </div>
          )}
          {graph && graph.nodes.length === 0 && (
            <div className="dependency-message">
              Add a parameter or data sample to build a graph.
            </div>
          )}
          {graph && graph.nodes.length > 0 && (
            <DependencyGraphView graph={graph} outputs={outputs} />
          )}
        </div>
      </section>
    </div>
  );
}

function DependencyGraphView({ graph, outputs }: { graph: DependencyGraph; outputs: string[] }) {
  const [zoom, setZoom] = useState(1);
  const [selectedNodeID, setSelectedNodeID] = useState<string>();
  const selectedNode = graph.nodes.find((node) => node.id === selectedNodeID);
  const nodeWidth = 180;
  const nodeHeight = 60;
  const layout = new dagre.graphlib.Graph();
  layout.setDefaultEdgeLabel(() => ({}));
  layout.setGraph({
    rankdir: 'LR',
    align: 'UL',
    nodesep: 48,
    ranksep: 115,
    edgesep: 24,
    marginx: 80,
    marginy: 24,
  });
  graph.nodes.forEach((node) => layout.setNode(node.id, { width: nodeWidth, height: nodeHeight }));
  graph.edges.forEach((edge) => layout.setEdge(edge.from, edge.to));
  dagre.layout(layout);

  const incoming: Record<string, number[]> = {};
  const outgoing: Record<string, number[]> = {};
  graph.edges.forEach((edge, index) => {
    incoming[edge.to] ||= [];
    outgoing[edge.from] ||= [];
    incoming[edge.to].push(index);
    outgoing[edge.from].push(index);
  });
  const graphWidth = Math.max(
    760,
    ...graph.nodes.map((node) => layout.node(node.id).x + nodeWidth / 2 + 60),
  );
  const graphHeight = Math.max(
    480,
    ...graph.nodes.map((node) => layout.node(node.id).y + nodeHeight / 2 + 70),
  );
  const edgeOffset = (edgeIndex: number, nodeID: string, direction: 'in' | 'out') => {
    const edges = direction === 'in' ? incoming[nodeID] : outgoing[nodeID];
    const position = edges.indexOf(edgeIndex);
    return ((position + 1) / (edges.length + 1)) * nodeHeight;
  };
  return (
    <>
      <div className="dependency-zoom-controls">
        <button
          type="button"
          aria-label="Zoom out"
          title="Zoom out"
          disabled={zoom <= 0.6}
          onClick={() => setZoom((value) => Math.max(0.6, value - 0.1))}
        >
          <ZoomOut />
        </button>
        <span>{Math.round(zoom * 100)}%</span>
        <button
          type="button"
          aria-label="Zoom in"
          title="Zoom in"
          disabled={zoom >= 1.6}
          onClick={() => setZoom((value) => Math.min(1.6, value + 0.1))}
        >
          <ZoomIn />
        </button>
        <button
          type="button"
          aria-label="Reset zoom"
          title="Reset zoom"
          disabled={zoom === 1}
          onClick={() => setZoom(1)}
        >
          <RotateCcw />
        </button>
      </div>
      <div className="dependency-static-viewport">
        <div
          className="dependency-scaled-area"
          style={{ width: graphWidth * zoom, height: graphHeight * zoom }}
        >
          <div
            className="dependency-static-stage"
            style={{
              width: graphWidth,
              height: graphHeight,
              transform: `scale(${zoom})`,
            }}
          >
            <svg width={graphWidth} height={graphHeight} aria-hidden="true">
              <defs>
                <marker
                  id="dependency-arrow"
                  viewBox="0 0 10 10"
                  refX="9"
                  refY="5"
                  markerWidth="7"
                  markerHeight="7"
                  orient="auto-start-reverse"
                >
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="#52665c" />
                </marker>
              </defs>
              {graph.edges.map((edge, index) => {
                const source = layout.node(edge.from);
                const target = layout.node(edge.to);
                const sourceX = source.x + nodeWidth / 2;
                const sourceY = source.y - nodeHeight / 2 + edgeOffset(index, edge.from, 'out');
                const targetX = target.x - nodeWidth / 2;
                const targetY = target.y - nodeHeight / 2 + edgeOffset(index, edge.to, 'in');
                const curve = Math.max(50, (targetX - sourceX) * 0.45);
                return (
                  <g key={`${edge.from}-${edge.to}`}>
                    <path
                      d={`M ${sourceX} ${sourceY} C ${sourceX + curve} ${sourceY}, ${targetX - curve} ${targetY}, ${targetX} ${targetY}`}
                      className="dependency-edge"
                      markerEnd="url(#dependency-arrow)"
                    />
                  </g>
                );
              })}
            </svg>
            {graph.nodes.map((node) => {
              const position = layout.node(node.id);
              const source =
                node.kind === 'sample'
                  ? node.properties.some(
                      (property) => property.label === 'Source' && property.value === 'static',
                    )
                    ? 'Static data sample'
                    : 'SQL data sample'
                  : node.kind === 'external'
                    ? 'External app'
                    : node.kind === 'feedback'
                      ? 'Run completion'
                      : node.kind === 'index'
                        ? 'Model selection'
                        : String(node.properties[0]?.value || 'Parameter');
              const isOutput = node.kind === 'parameter' && outputs.includes(node.key);
              const isLogic =
                node.kind === 'parameter' &&
                node.properties.some(
                  (property) => property.label === 'Source' && property.value === 'logic',
                );
              return (
                <div
                  className={`dependency-node-card ${node.kind} ${isOutput ? 'output' : ''} ${selectedNodeID === node.id ? 'selected' : ''}`}
                  key={node.id}
                  onClick={() =>
                    setSelectedNodeID((current) => (current === node.id ? undefined : node.id))
                  }
                  style={{
                    left: position.x - nodeWidth / 2,
                    top: position.y - nodeHeight / 2,
                  }}
                >
                  <span className="dependency-node-source">{source}</span>
                  {isOutput && <span className="dependency-output-badge">OUTPUT</span>}
                  {isLogic && <span className="dependency-logic-badge">LOGIC</span>}
                  <div className="dependency-node-name">{node.key || 'Unnamed parameter'}</div>
                  <div className="dependency-tooltip" role="tooltip">
                    <header>
                      <strong>{node.key}</strong>
                      <span>
                        {isOutput
                          ? 'MODEL OUTPUT'
                          : node.kind === 'sample'
                            ? 'DATA SAMPLE'
                            : node.kind === 'external'
                              ? 'EXTERNAL INPUT'
                              : node.kind === 'feedback'
                                ? 'POST-RUN FEEDBACK'
                                : node.kind === 'index'
                                  ? 'MODEL INDEX'
                                  : 'PARAMETER'}
                      </span>
                    </header>
                    <dl>
                      {node.properties.map((property) => (
                        <div key={property.label}>
                          <dt>{property.label}</dt>
                          <dd>{formatDependencyProperty(property.value)}</dd>
                        </div>
                      ))}
                    </dl>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>
      {selectedNode && (
        <aside className="dependency-detail-panel">
          <header>
            <div>
              <small>DEPENDENCY DETAILS</small>
              <strong>{selectedNode.key}</strong>
            </div>
            <button type="button" className="icon" onClick={() => setSelectedNodeID(undefined)}>
              <X />
            </button>
          </header>
          <dl>
            {selectedNode.properties.map((property) => (
              <div key={property.label}>
                <dt>{property.label}</dt>
                <dd>{formatDependencyProperty(property.value)}</dd>
              </div>
            ))}
          </dl>
          {graph.edges.filter((edge) => edge.to === selectedNode.id && edge.reasons?.length)
            .length > 0 && (
            <section>
              <strong>Why these dependencies exist</strong>
              {graph.edges
                .filter((edge) => edge.to === selectedNode.id && edge.reasons?.length)
                .map((edge) => (
                  <div key={edge.from}>
                    <b>{graph.nodes.find((node) => node.id === edge.from)?.key || edge.from}</b>
                    <span>{edge.reasons?.join(' · ')}</span>
                  </div>
                ))}
            </section>
          )}
        </aside>
      )}
    </>
  );
}

function formatDependencyProperty(value: unknown) {
  if (Array.isArray(value)) return value.join(', ');
  if (value === null || value === undefined || value === '') return 'Not configured';
  return String(value);
}

function ExternalInputsEditor({
  inputs,
  onAdd,
  onChange,
}: {
  inputs: ExternalInput[];
  onAdd: () => void;
  onChange: (inputs: ExternalInput[]) => void;
}) {
  return (
    <>
      <EditorHeader
        title="External inputs"
        action={
          <button className="secondary" onClick={onAdd}>
            <Plus />
            Add input
          </button>
        }
      />
      <p className="field-help">
        Define values the calling application must provide. Names can be used in SQL, formulas, and
        conditional logic.
      </p>
      {inputs.length === 0 ? (
        <div className="parameter-empty">No external inputs defined.</div>
      ) : (
        <div className="external-inputs">
          <div className="external-input-head">
            <span>NAME</span>
            <span>DATA TYPE</span>
            <span>DESCRIPTION</span>
            <span />
          </div>
          {inputs.map((input, index) => (
            <div className="external-input-row" key={index}>
              <input
                disabled
                required
                aria-label={`External input ${index + 1} name`}
                aria-invalid={!input.name.trim()}
                value={input.name}
                onChange={(event) =>
                  onChange(
                    inputs.map((item, itemIndex) =>
                      itemIndex === index ? { ...item, name: event.target.value } : item,
                    ),
                  )
                }
                placeholder="wafer_id"
              />
              <select
                disabled
                aria-label={`External input ${index + 1} data type`}
                value={input.type}
                onChange={(event) =>
                  onChange(
                    inputs.map((item, itemIndex) =>
                      itemIndex === index
                        ? { ...item, type: event.target.value as ExternalInput['type'] }
                        : item,
                    ),
                  )
                }
              >
                <option value="string">Text</option>
                <option value="number">Number</option>
                <option value="boolean">Boolean</option>
              </select>
              <input
                disabled
                aria-label={`External input ${index + 1} description`}
                value={input.description || ''}
                onChange={(event) =>
                  onChange(
                    inputs.map((item, itemIndex) =>
                      itemIndex === index ? { ...item, description: event.target.value } : item,
                    ),
                  )
                }
                placeholder="Optional description"
              />
              <button
                className="remove-index"
                title={`Remove ${input.name || 'external input'}`}
                onClick={() => onChange(inputs.filter((_, itemIndex) => itemIndex !== index))}
              >
                <Trash2 />
              </button>
            </div>
          ))}
        </div>
      )}
    </>
  );
}

function FeedbackEditor({
  inputs,
  adaptiveNames,
  onAdd,
  onChange,
}: {
  inputs: FeedbackInput[];
  adaptiveNames: Set<string>;
  onAdd: () => void;
  onChange: (inputs: FeedbackInput[]) => void;
}) {
  return (
    <>
      <EditorHeader
        title="Post-run feedback"
        action={
          <button className="secondary" onClick={onAdd}>
            <Plus />
            Add feedback field
          </button>
        }
      />
      <p className="field-help">
        Required values must be included when the calling application completes a successful run.
        Without them, the run remains prepared.
      </p>
      {inputs.length === 0 && <div className="parameter-empty">No feedback fields defined.</div>}
      {inputs.map((input, index) => (
        <div className="feedback-input-row" key={index}>
          <input
            value={input.name}
            onChange={(event) =>
              onChange(
                inputs.map((item, itemIndex) =>
                  itemIndex === index ? { ...item, name: event.target.value } : item,
                ),
              )
            }
            placeholder="measured_thickness"
          />
          <select
            value={input.type}
            onChange={(event) =>
              onChange(
                inputs.map((item, itemIndex) =>
                  itemIndex === index
                    ? { ...item, type: event.target.value as FeedbackInput['type'] }
                    : item,
                ),
              )
            }
          >
            <option value="number">Number</option>
            <option value="string">Text</option>
            <option value="boolean">Boolean</option>
          </select>
          <input
            value={input.description || ''}
            onChange={(event) =>
              onChange(
                inputs.map((item, itemIndex) =>
                  itemIndex === index ? { ...item, description: event.target.value } : item,
                ),
              )
            }
            placeholder="Description"
          />
          <label className="feedback-required">
            <input
              type="checkbox"
              checked={input.required || adaptiveNames.has(input.name)}
              disabled={adaptiveNames.has(input.name)}
              onChange={(event) =>
                onChange(
                  inputs.map((item, itemIndex) =>
                    itemIndex === index ? { ...item, required: event.target.checked } : item,
                  ),
                )
              }
            />
            Required
          </label>
          <button
            className="remove-index"
            onClick={() => onChange(inputs.filter((_, itemIndex) => itemIndex !== index))}
          >
            <Trash2 />
          </button>
        </div>
      ))}
    </>
  );
}

function StaticDataSampleEditor({
  sample,
  onChange,
}: {
  sample: DataSample;
  onChange: (next: DataSample) => void;
}) {
  const columns = sample.columns || [];
  const rows = sample.rows || [];
  const defaultValue = (type: DataSampleColumn['type']) =>
    type === 'boolean' ? false : null;
  const columnInputWidth = (columnName: string) =>
    `${Math.min(
      48,
      Math.max(
        12,
        ...rows.map((row) => String(row[columnName] === null ? 'NULL' : row[columnName] ?? '').length + 3),
      ),
    )}ch`;

  const updateColumns = (nextColumns: DataSampleColumn[], nextRows = rows) =>
    onChange({ ...sample, columns: nextColumns, rows: nextRows });
  return (
    <div className="static-sample-editor span-three">
      <div className="adaptive-heading">
        <div>
          <strong>Static table</strong>
          <small>Stored with this model revision.</small>
        </div>
        <div className="static-sample-actions">
          <button
            type="button"
            className="secondary"
            onClick={() => {
              const name = `column_${columns.length + 1}`;
              updateColumns(
                [...columns, { name, type: 'string' }],
                rows.map((row) => ({ ...row, [name]: '' })),
              );
            }}
          >
            <Plus />
            Add column
          </button>
          <button
            type="button"
            className="secondary"
            disabled={columns.length === 0}
            onClick={() =>
              onChange({
                ...sample,
                rows: [
                  ...rows,
                  Object.fromEntries(
                    columns.map((column) => [column.name, defaultValue(column.type)]),
                  ),
                ],
              })
            }
          >
            <Plus />
            Add row
          </button>
        </div>
      </div>
      {columns.length === 0 ? (
        <div className="parameter-empty">Add a column to define the static table.</div>
      ) : (
        <div className="static-sample-table-wrap">
          <table className="static-sample-table">
            <thead>
              <tr>
                {columns.map((column, columnIndex) => (
                  <th key={columnIndex}>
                    <input
                      aria-label={`Column ${columnIndex + 1} name`}
                      value={column.name}
                      onChange={(event) => {
                        const oldName = column.name;
                        const name = event.target.value;
                        updateColumns(
                          columns.map((item, index) =>
                            index === columnIndex ? { ...item, name } : item,
                          ),
                          rows.map((row) => {
                            const next = { ...row, [name]: row[oldName] };
                            if (oldName !== name) delete next[oldName];
                            return next;
                          }),
                        );
                      }}
                      placeholder="column_name"
                    />
                    <select
                      aria-label={`${column.name || `Column ${columnIndex + 1}`} type`}
                      value={column.type}
                      onChange={(event) => {
                        const type = event.target.value as DataSampleColumn['type'];
                        updateColumns(
                          columns.map((item, index) =>
                            index === columnIndex ? { ...item, type } : item,
                          ),
                          rows.map((row) => ({
                            ...row,
                            [column.name]: defaultValue(type),
                          })),
                        );
                      }}
                    >
                      <option value="string">Text</option>
                      <option value="number">Number</option>
                      <option value="boolean">Boolean</option>
                    </select>
                    <button
                      type="button"
                      className="remove-index"
                      title={`Remove ${column.name || 'column'}`}
                      onClick={() => {
                        updateColumns(
                          columns.filter((_, index) => index !== columnIndex),
                          rows.map((row) => {
                            const next = { ...row };
                            delete next[column.name];
                            return next;
                          }),
                        );
                      }}
                    >
                      <Trash2 />
                    </button>
                  </th>
                ))}
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((row, rowIndex) => (
                <tr key={rowIndex}>
                  {columns.map((column) => (
                    <td key={column.name}>
                      {column.type === 'boolean' ? (
                        <select
                          value={row[column.name] === null ? 'null' : String(row[column.name] ?? false)}
                          onChange={(event) =>
                            onChange({
                              ...sample,
                              rows: rows.map((item, index) =>
                                index === rowIndex
                                  ? {
                                      ...item,
                                      [column.name]:
                                        event.target.value === 'null'
                                          ? null
                                          : event.target.value === 'true',
                                    }
                                  : item,
                              ),
                            })
                          }
                        >
                          <option value="false">False</option>
                          <option value="true">True</option>
                          <option value="null">NULL</option>
                        </select>
                      ) : (
                        <div className="static-sample-cell">
                          {row[column.name] === null ? (
                            <input
                              type={column.type === 'number' ? 'number' : 'text'}
                              className="static-null-value"
                              value=""
                              placeholder="NULL"
                              aria-label={`${column.name} NULL value`}
                              style={{ width: columnInputWidth(column.name) }}
                              onFocus={() =>
                                onChange({
                                  ...sample,
                                  rows: rows.map((item, index) =>
                                    index === rowIndex
                                      ? { ...item, [column.name]: '' }
                                     : item,
                                  ),
                                })
                              }
                            />
                          ) : (
                            <>
                              <input
                                type={column.type === 'number' ? 'number' : 'text'}
                                step={column.type === 'number' ? 'any' : undefined}
                                value={String(row[column.name] ?? '')}
                            style={{
                              width: columnInputWidth(column.name),
                            }}
                                onChange={(event) =>
                                  onChange({
                                    ...sample,
                                    rows: rows.map((item, index) =>
                                      index === rowIndex
                                        ? { ...item, [column.name]: event.target.value }
                                        : item,
                                    ),
                                  })
                                }
                                onBlur={(event) => {
                                  const entered = event.target.value.trim();
                                  if (entered === '') {
                                    onChange({
                                      ...sample,
                                      rows: rows.map((item, index) =>
                                        index === rowIndex
                                          ? { ...item, [column.name]: null }
                                          : item,
                                      ),
                                    });
                                    return;
                                  }
                                  if (column.type !== 'number') return;
                                  const number = Number(entered);
                                  if (!Number.isFinite(number)) return;
                                  onChange({
                                    ...sample,
                                    rows: rows.map((item, index) =>
                                      index === rowIndex
                                        ? { ...item, [column.name]: number }
                                        : item,
                                    ),
                                  });
                                }}
                              />
                              <button
                                type="button"
                                className="static-set-null"
                                onClick={() =>
                                  onChange({
                                    ...sample,
                                    rows: rows.map((item, index) =>
                                      index === rowIndex
                                        ? { ...item, [column.name]: null }
                                        : item,
                                    ),
                                  })
                                }
                              >
                                Set NULL
                              </button>
                            </>
                          )}
                        </div>
                      )}
                    </td>
                  ))}
                  <td>
                    <button
                      type="button"
                      className="remove-index"
                      title={`Remove row ${rowIndex + 1}`}
                      onClick={() =>
                        onChange({ ...sample, rows: rows.filter((_, index) => index !== rowIndex) })
                      }
                    >
                      <Trash2 />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {rows.length === 0 && <div className="parameter-empty">Add at least one row.</div>}
        </div>
      )}
    </div>
  );
}

function SQLSampleSchemaEditor({
  sample,
  onChange,
}: {
  sample: DataSample;
  onChange: (next: DataSample) => void;
}) {
  const columns = sample.columns || [];
  return (
    <div className="sql-sample-schema span-three">
      <div className="adaptive-heading">
        <div>
          <span className="field-label">
            <strong>Expected result columns</strong>
            <b className="required-badge">Required</b>
          </span>
          <small>Returned SQL rows are validated against this typed contract.</small>
        </div>
        <button
          type="button"
          className="secondary"
          onClick={() =>
            onChange({
              ...sample,
              columns: [...columns, { name: '', type: 'string' }],
            })
          }
        >
          <Plus />
          Add column
        </button>
      </div>
      {columns.length === 0 ? (
        <div className="parameter-empty">
          No schema declared. Add each SQL result column that an expression or lookup will use.
        </div>
      ) : (
        <div className="sql-sample-column-list">
          {columns.map((column, index) => (
            <div key={index}>
              <input
                aria-label={`SQL column ${index + 1} name`}
                value={column.name}
                onChange={(event) =>
                  onChange({
                    ...sample,
                    columns: columns.map((item, itemIndex) =>
                      itemIndex === index ? { ...item, name: event.target.value } : item,
                    ),
                  })
                }
                placeholder="column_name"
              />
              <select
                aria-label={`${column.name || `SQL column ${index + 1}`} type`}
                value={column.type}
                onChange={(event) =>
                  onChange({
                    ...sample,
                    columns: columns.map((item, itemIndex) =>
                      itemIndex === index
                        ? { ...item, type: event.target.value as DataSampleColumn['type'] }
                        : item,
                    ),
                  })
                }
              >
                <option value="string">Text</option>
                <option value="number">Number</option>
                <option value="boolean">Boolean</option>
              </select>
              <button
                type="button"
                className="remove-index"
                title={`Remove ${column.name || 'column'}`}
                onClick={() =>
                  onChange({
                    ...sample,
                    columns: columns.filter((_, itemIndex) => itemIndex !== index),
                  })
                }
              >
                <Trash2 />
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function DataSampleCard({
  sample,
  parameters,
  externalInputs,
  indexes,
  expanded,
  onToggle,
  onChange,
  onRemove,
}: {
  sample: DataSample;
  parameters: Parameter[];
  externalInputs: ExternalInput[];
  indexes: Record<string, string[]>;
  expanded: boolean;
  onToggle: () => void;
  onChange: (next: DataSample) => void;
  onRemove: () => void;
}) {
  const sources = useContext(DataSourcesContext);
  return (
    <section className={`definition accordion-card ${expanded ? 'expanded' : ''}`}>
      <div className="accordion-summary">
        <button className="accordion-toggle" onClick={onToggle}>
          <ChevronDown />
          <span>
            <strong>{sample.key || 'Untitled data sample'}</strong>
            <small>
              {sample.source === 'static'
                ? `Static table · ${sample.rows?.length || 0} rows`
                : `SQL query · ${sample.limit} records`}
              {sample.description ? ` · ${sample.description}` : ''}
            </small>
          </span>
        </button>
        <button className="remove-index" title="Remove data sample" onClick={onRemove}>
          <Trash2 />
        </button>
      </div>
      {expanded && (
        <div className="accordion-body">
          <div className="form-grid three">
            <label>
              <span className="field-label">
                <span>Key</span>
                <b className="required-badge">Required</b>
              </span>
              <input
                value={sample.key}
                onChange={(e) => onChange({ ...sample, key: e.target.value })}
                placeholder="datasample1"
              />
            </label>
            <label>
              <span className="field-label">
                <span>Source type</span>
                <b className="required-badge">Required</b>
              </span>
              <select
                value={sample.source}
                onChange={(event) => {
                  const source = event.target.value as DataSample['source'];
                  onChange({
                    ...sample,
                    source,
                    dataSourceId: source === 'sql' ? sample.dataSourceId : undefined,
                    query: source === 'sql' ? sample.query : '',
                    columns:
                      source === 'static' && !sample.columns?.length
                        ? [{ name: 'column_name', type: 'string' }]
                        : sample.columns,
                    rows:
                      source === 'static' && !sample.rows?.length
                        ? [{ column_name: '' }]
                        : sample.rows,
                  });
                }}
              >
                <option value="sql">SQL query</option>
                <option value="static">Static table</option>
              </select>
            </label>
            <label>
              <span className="field-label">
                <span>Record limit</span>
                <b className="required-badge">Required</b>
              </span>
              <input
                type="number"
                min="1"
                value={sample.limit}
                onChange={(e) => onChange({ ...sample, limit: +e.target.value })}
              />
            </label>
            <label className="span-three">
              Description (optional)
              <input
                value={sample.description || ''}
                onChange={(e) => onChange({ ...sample, description: e.target.value })}
              />
            </label>
            {sample.source === 'sql' ? (
              <>
                <label>
                  <span className="field-label">
                    <span>Data source</span>
                    <b className="required-badge">Required</b>
                  </span>
                  <select
                    value={sample.dataSourceId || ''}
                    onChange={(e) => onChange({ ...sample, dataSourceId: e.target.value })}
                  >
                    <option value="">Select a configured data source</option>
                    {sources
                      .filter((source) => source.type === 'sql')
                      .map((source) => (
                        <option key={source.id} value={source.id}>
                          {source.name} · {source.location}
                        </option>
                      ))}
                  </select>
                </label>
                <label className="span-three">
                  <span className="field-label">
                    <span>SQL query</span>
                    <b className="required-badge">Required</b>
                  </span>
                  <SQLAutocomplete
                    value={sample.query}
                    onChange={(query) => onChange({ ...sample, query })}
                    references={[
                      ...externalInputs
                        .filter((input) => input.name)
                        .map((input) => ({
                          name: input.name,
                          source: 'External input' as const,
                          dataType: input.type,
                        })),
                      ...parameters
                        .filter((parameter) => parameter.key)
                        .map((parameter) => ({
                          name: parameter.key,
                          source: 'Parameter' as const,
                          dataType: parameter.dataType || ('number' as const),
                        })),
                      ...Object.keys(indexes).map((name) => ({
                        name,
                        source: 'Model index',
                        dataType: 'string' as const,
                      })),
                    ]}
                    placeholder="SELECT TOP (10) thickness_nm FROM process_history WHERE wafer_id = @wafer_id"
                  />
                </label>
                {(externalInputs.some((input) => input.name) ||
                  parameters.some((parameter) => parameter.key)) && (
                  <div className="sql-parameter-references span-three">
                    <span>Available query parameters</span>
                    <div>
                      {externalInputs
                        .filter((input) => input.name)
                        .map((input) => (
                          <button
                            type="button"
                            key={`external-${input.name}`}
                            onClick={() =>
                              onChange({
                                ...sample,
                                query: `${sample.query}${sample.query ? ' ' : ''}@${input.name}`,
                              })
                            }
                          >
                            @{input.name}
                          </button>
                        ))}
                      {parameters
                        .filter((parameter) => parameter.key)
                        .map((parameter) => (
                          <button
                            type="button"
                            key={parameter.key}
                            onClick={() =>
                              onChange({
                                ...sample,
                                query: `${sample.query}${sample.query ? ' ' : ''}@${parameter.key}`,
                              })
                            }
                          >
                            @{parameter.key}
                          </button>
                        ))}
                    </div>
                  </div>
                )}
                <SQLSampleSchemaEditor sample={sample} onChange={onChange} />
                <SQLQueryTester
                  dataSourceId={sample.dataSourceId}
                  query={sample.query}
                  queryType="data_sample"
                  columns={sample.columns}
                  limit={sample.limit}
                  references={[
                    ...externalInputs
                      .filter((input) => input.name)
                      .map((input) => ({ name: input.name, source: 'External input', dataType: input.type })),
                    ...parameters
                      .filter((parameter) => parameter.key)
                      .map((parameter) => ({ name: parameter.key, source: 'Parameter', dataType: parameter.dataType || ('number' as const) })),
                    ...Object.keys(indexes).map((name) => ({ name, source: 'Model index', dataType: 'string' as const })),
                  ]}
                />
              </>
            ) : (
              <StaticDataSampleEditor sample={sample} onChange={onChange} />
            )}
          </div>
        </div>
      )}
    </section>
  );
}

function ParameterCard({
  parameter,
  previous,
  allParameters,
  samples,
  externalInputs,
  feedbackInputs,
  indexes,
  expanded,
  onToggle,
  onSave,
  onChange,
  onRemove,
  onFindUsages,
  onRename,
  refactorBusy,
}: {
  parameter: Parameter;
  previous: Parameter[];
  allParameters: Parameter[];
  samples: DataSample[];
  externalInputs: ExternalInput[];
  feedbackInputs: FeedbackInput[];
  indexes: Record<string, string[]>;
  expanded: boolean;
  onToggle: () => void;
  onSave?: () => void;
  onChange: (next: Parameter) => void;
  onRemove: () => void;
  onFindUsages?: () => void;
  onRename?: () => void;
  refactorBusy?: boolean;
}) {
  const sourceName = parameter.adaptive
    ? 'Adaptive'
    : parameter.source === 'data-sample'
      ? 'Data sample'
      : parameter.source === 'lookup'
        ? 'Data sample lookup'
        : parameter.source === 'logic'
          ? 'Conditional logic'
          : parameter.source.replace('-', ' ');
  const firstCondition = parameter.logic?.conditions?.[0];
  const sourceSummary = parameter.adaptive
    ? `${parameter.adaptive.updateMethod.toUpperCase()} · Initial ${parameter.adaptive.initialValue}`
    : parameter.source === 'logic' && firstCondition
      ? `${parameter.logic?.conditions.length} rules · If ${firstCondition.inputParameter} ${firstCondition.operator} ${firstCondition.compareValue}`
      : parameter.source === 'lookup' && parameter.lookup
        ? `${parameter.lookup.sample}.${parameter.lookup.matchColumn} → ${parameter.lookup.returnColumn}`
        : parameter.definition || 'Not configured';
  const formulaError = formulaValidationError(parameter, allParameters, externalInputs, samples);
  const complete = parameterIsComplete(parameter) && !formulaError;
  return (
    <section className={`definition accordion-card ${expanded ? 'expanded' : ''}`}>
      <div className="accordion-summary">
        <button className="accordion-toggle" onClick={onToggle}>
          <ChevronDown />
          <span>
            <strong>{parameter.key || 'Untitled parameter'}</strong>
            <small>
              {sourceName}
              {parameter.unit ? ` · ${parameter.unit}` : ''} · {sourceSummary}
            </small>
          </span>
        </button>
        <div className="parameter-card-actions">
          {onFindUsages && (
            <button className="remove-index" title="Find usages" disabled={refactorBusy} onClick={onFindUsages}>
              <Search />
            </button>
          )}
          {onRename && (
            <button className="remove-index" title="Rename parameter" disabled={refactorBusy} onClick={onRename}>
              <Pencil />
            </button>
          )}
          <button className="remove-index" title="Remove parameter" onClick={onRemove}>
            <Trash2 />
          </button>
        </div>
      </div>
      {expanded && (
        <div className="accordion-body">
          <SectionLabel title="Basic" />
          <div className="form-grid three">
            <label>
              <span className="field-label">
                Key <b className="required-badge">Required</b>
              </span>
              <input
                required
                aria-invalid={!parameter.key.trim()}
                value={parameter.key}
                onChange={(e) => onChange({ ...parameter, key: e.target.value })}
              />
            </label>
            <label>
              <span className="field-label">
                <span>Data type</span>
                <b className="required-badge">Required</b>
              </span>
              <select
                value={parameter.dataType || 'number'}
                onChange={(e) => {
                  const dataType = e.target.value as NonNullable<Parameter['dataType']>;
                  const incompatible = dataType !== 'number' && parameter.adaptive != null;
                  onChange({
                    ...parameter,
                    dataType,
                    source: incompatible ? 'expression' : parameter.source,
                    definition: incompatible
                      ? dataType === 'boolean'
                        ? 'false'
                        : '""'
                      : parameter.definition,
                    lsl: dataType !== 'number' ? undefined : parameter.lsl,
                    usl: dataType !== 'number' ? undefined : parameter.usl,
                    adaptive: incompatible ? undefined : parameter.adaptive,
                    logic:
                      parameter.logic && dataType === 'boolean'
                        ? {
                            ...parameter.logic,
                            conditions: parameter.logic.conditions.map((condition) => ({
                              ...condition,
                              resultValue: 'true',
                            })),
                            elseValue: 'false',
                          }
                        : parameter.logic,
                    lookup: parameter.lookup
                      ? {
                          ...parameter.lookup,
                          returnColumn:
                            samples
                              .find((sample) => sample.key === parameter.lookup?.sample)
                              ?.columns?.find((column) => column.type === dataType)?.name || '',
                        }
                      : undefined,
                  });
                }}
              >
                <option value="number">Number</option>
                <option value="string">Text</option>
                <option value="boolean">Boolean</option>
              </select>
            </label>
            {(parameter.dataType || 'number') === 'number' && (
              <label>
                Unit
                <input
                  value={parameter.unit}
                  onChange={(e) => onChange({ ...parameter, unit: e.target.value })}
                  placeholder="nm"
                />
              </label>
            )}
            <label>
              <span className="field-label">
                <span>Parameter behavior</span>
                <b className="required-badge">Required</b>
              </span>
              <select
                value={parameter.adaptive ? 'adaptive' : 'standard'}
                disabled={(parameter.dataType || 'number') !== 'number'}
                onChange={(event) => {
                  if (event.target.value === 'adaptive') {
                    const initialValue = Number(parameter.definition) || 3;
                    onChange({
                      ...parameter,
                      source: 'expression',
                      definition: String(initialValue),
                      logic: undefined,
                      adaptive: {
                        initialValue,
                        updateMethod: 'ewma',
                        updateInput: { source: 'parameter', name: '' },
                        alpha: 0.25,
                      },
                    });
                  } else {
                    onChange({ ...parameter, adaptive: undefined });
                  }
                }}
              >
                <option value="standard">Standard</option>
                <option value="adaptive">Adaptive</option>
              </select>
            </label>
            {!parameter.adaptive && (
              <label>
                <span className="field-label">
                  <span>Value source</span>
                  <b className="required-badge">Required</b>
                </span>
                <select
                  value={parameter.source}
                  onChange={(e) => {
                    const source = e.target.value as Parameter['source'];
                    onChange({
                      ...parameter,
                      source,
                      definition:
                        source === 'expression'
                          ? (parameter.dataType || 'number') === 'number'
                            ? '0'
                            : parameter.dataType === 'boolean'
                              ? 'false'
                              : '""'
                          : '',
                      lsl: parameter.lsl,
                      usl: parameter.usl,
                      logic:
                        source === 'logic'
                          ? parameter.logic || {
                              conditions: [
                                {
                                  inputParameter: '',
                                  inputSource: 'parameter',
                                  operator: 'equals',
                                  compareValue: '',
                                  resultValue:
                                    parameter.dataType === 'boolean'
                                      ? 'true'
                                      : parameter.dataType === 'string'
                                        ? ''
                                        : '1',
                                },
                              ],
                              elseValue:
                                parameter.dataType === 'boolean'
                                  ? 'false'
                                  : parameter.dataType === 'string'
                                    ? ''
                                    : '2',
                            }
                          : undefined,
                      lookup:
                        source === 'lookup'
                          ? parameter.lookup || {
                              sample: samples[0]?.key || '',
                              matchColumn: samples[0]?.columns?.[0]?.name || '',
                              operator: 'equals',
                              inputSource: 'constant',
                              matchValue: '',
                              returnColumn:
                                samples[0]?.columns?.find(
                                  (column) => column.type === (parameter.dataType || 'number'),
                                )?.name || '',
                            }
                          : undefined,
                    });
                  }}
                >
                  <option value="expression">Expression</option>
                  <option value="sql">SQL query</option>
                </select>
              </label>
            )}
            <label className="span-three">
              Description (optional)
              <input
                value={parameter.description || ''}
                onChange={(e) => onChange({ ...parameter, description: e.target.value })}
              />
            </label>
          </div>
          <SectionLabel title="Value configuration" />
          <ParameterSourceEditor
            parameter={parameter}
            previous={previous}
            resultParameters={allParameters}
            samples={samples}
            externalInputs={externalInputs}
            feedbackInputs={feedbackInputs}
            indexes={indexes}
            onChange={onChange}
          />
          {(parameter.dataType || 'number') === 'number' && parameter.source !== 'constant' && (
            <>
              <SectionLabel title="Specification limits" />
              <div className="form-grid">
                <label>
                  LSL (optional)
                  <input
                    type="number"
                    value={parameter.lsl ?? ''}
                    onChange={(e) =>
                      onChange({
                        ...parameter,
                        lsl: e.target.value === '' ? undefined : +e.target.value,
                      })
                    }
                  />
                </label>
                <label>
                  USL (optional)
                  <input
                    type="number"
                    value={parameter.usl ?? ''}
                    onChange={(e) =>
                      onChange({
                        ...parameter,
                        usl: e.target.value === '' ? undefined : +e.target.value,
                      })
                    }
                  />
                </label>
                <label>
                  Out-of-limit action
                  <select
                    value={parameter.limitAction || 'warn'}
                    onChange={(event) =>
                      onChange({
                        ...parameter,
                        limitAction: event.target.value as NonNullable<Parameter['limitAction']>,
                        fallbackValue:
                          event.target.value === 'fallback' ? parameter.fallbackValue : undefined,
                      })
                    }
                  >
                    <option value="warn">Warn and return value</option>
                    <option value="clamp">Clamp to LSL or USL</option>
                    <option value="reject">Reject run</option>
                    <option value="fallback">Use fallback value</option>
                  </select>
                </label>
                {parameter.limitAction === 'fallback' && (
                  <label>
                    Fallback value
                    <input
                      type="number"
                      value={parameter.fallbackValue ?? ''}
                      onChange={(event) =>
                        onChange({
                          ...parameter,
                          fallbackValue:
                            event.target.value === '' ? undefined : +event.target.value,
                        })
                      }
                    />
                  </label>
                )}
              </div>
            </>
          )}
          <div className={`parameter-save ${complete ? 'complete' : 'incomplete'}`}>
            <span>
              {complete
                ? 'Ready to save'
                : formulaError || 'Key and Value configuration are required.'}
            </span>
            <button className="primary" disabled={!complete} onClick={onSave || onToggle}>
              Save parameter
            </button>
          </div>
        </div>
      )}
    </section>
  );
}

function parameterIsComplete(parameter: Parameter) {
  if (!parameter.key.trim()) return false;
  if (parameter.lsl !== undefined && parameter.usl !== undefined && parameter.lsl > parameter.usl) {
    return false;
  }
  if (parameter.limitAction === 'fallback' && parameter.fallbackValue === undefined) {
    return false;
  }
  if (parameter.source === 'sql') return !!parameter.dataSourceId && !!parameter.definition.trim();
  if (parameter.adaptive) {
    const value = parameter.adaptive;
    if (!(value.updateInput?.name || value.updateParameter || value.measurementParameter)) {
      return false;
    }
    return (
      !!value && (value.updateMethod === 'ewma' ? !!value.alpha : !!value.updateFormula?.trim())
    );
  }
  if (parameter.source === 'logic') {
    const value = parameter.logic;
    return (
      !!value &&
      value.conditions.length > 0 &&
      value.conditions.every(
        (rule) =>
          rule.inputParameter &&
          rule.compareValue.trim() &&
          rule.resultValue.trim() &&
          (rule.and || []).every(
            (predicate) => predicate.inputParameter && predicate.compareValue.trim(),
          ) &&
          (rule.or || []).every(
            (predicate) => predicate.inputParameter && predicate.compareValue.trim(),
          ),
      ) &&
      !!value.elseValue.trim()
    );
  }
  if (parameter.source === 'lookup') {
    const lookup = parameter.lookup;
    return Boolean(
      lookup?.sample && lookup.matchColumn && lookup.matchValue.trim() && lookup.returnColumn,
    );
  }
  return !!parameter.definition.trim();
}

function formulaValidationError(
  parameter: Parameter,
  parameters: Parameter[],
  externalInputs: ExternalInput[],
  samples: DataSample[],
) {
  if (parameter.source !== 'formula') return '';
  const expression = parameter.definition.trim();
  if (!expression) return 'Formula is required.';
  if (/["'`]/.test(expression)) return 'Formula cannot contain text values.';
  const numericNames = new Set([
    ...parameters
      .filter((item) => (item.dataType || 'number') === 'number')
      .map((item) => item.key),
    ...externalInputs.filter((item) => item.type === 'number').map((item) => item.name),
  ]);
  const sampleNames = new Set(samples.map((sample) => sample.key));
  const functions = new Set(['min', 'max', 'abs', 'avg', 'sum', 'count', 'first', 'nth', 'last']);
  const identifierExpression = expression.replace(/\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/g, '');
  const identifiers = identifierExpression.match(/[A-Za-z_][A-Za-z0-9_]*/g) || [];
  for (const identifier of identifiers) {
    const offset = identifierExpression.indexOf(identifier);
    const before = offset > 0 ? identifierExpression[offset - 1] : '';
    const after = identifierExpression[offset + identifier.length] || '';
    if (
      functions.has(identifier.toLowerCase()) ||
      sampleNames.has(identifier) ||
      before === '.' ||
      after === '.'
    )
      continue;
    if (!numericNames.has(identifier)) {
      return `"${identifier}" is not a declared numerical parameter or external input.`;
    }
  }
  return '';
}
function SectionLabel({ title }: { title: string }) {
  return <h5 className="section-label">{title}</h5>;
}

function LogicPredicateEditor({
  predicate,
  indexes,
  externalInputs,
  parameters,
  samples,
  onChange,
}: {
  predicate: LogicPredicate;
  indexes: Record<string, string[]>;
  externalInputs: ExternalInput[];
  parameters: Parameter[];
  samples: DataSample[];
  onChange: (patch: Partial<LogicPredicate>) => void;
}) {
  const source = predicate.inputSource || 'parameter';
  const selectedType = logicOperandDataType(
    source,
    predicate.inputParameter,
    indexes,
    externalInputs,
    parameters,
    samples,
  );
  const operators =
    selectedType !== 'number'
      ? [
          ['equals', 'Equals'],
          ['not-equals', 'Does not equal'],
        ]
      : [
          ['equals', 'Equals'],
          ['not-equals', 'Does not equal'],
          ['greater-than', 'Greater than'],
          ['greater-or-equal', 'Greater than or equal'],
          ['less-than', 'Less than'],
          ['less-or-equal', 'Less than or equal'],
        ];
  return (
    <>
      <label>
        Input value
        <LogicOperandInput
          value={predicate.inputParameter}
          source={source}
          indexes={indexes}
          externalInputs={externalInputs}
          parameters={parameters}
          samples={samples}
          onChange={(inputSource, inputParameter) => {
            const nextType = logicOperandDataType(
              inputSource,
              inputParameter,
              indexes,
              externalInputs,
              parameters,
              samples,
            );
            onChange({
              inputSource: inputSource as LogicPredicate['inputSource'],
              inputParameter,
              operator:
                nextType !== 'number' && !['equals', 'not-equals'].includes(predicate.operator)
                  ? 'equals'
                  : predicate.operator,
            });
          }}
        />
      </label>
      <label>
        Operator
        <select
          value={predicate.operator}
          onChange={(event) =>
            onChange({ operator: event.target.value as LogicPredicate['operator'] })
          }
        >
          {operators.map(([value, label]) => (
            <option value={value} key={value}>
              {label}
            </option>
          ))}
        </select>
      </label>
      <label>
        Comparison value
        <LogicOperandInput
          value={predicate.compareValue}
          source={predicate.compareSource || 'constant'}
          expectedType={selectedType}
          allowConstant
          indexes={indexes}
          externalInputs={externalInputs}
          parameters={parameters}
          samples={samples}
          onChange={(compareSource, compareValue) => onChange({ compareSource, compareValue })}
        />
      </label>
    </>
  );
}

type LogicOperandSource = 'constant' | 'index' | 'external' | 'parameter' | 'data-sample';
type LogicOperandSuggestion = {
  name: string;
  source: LogicOperandSource;
  label: string;
  dataType: 'string' | 'number' | 'boolean';
  kind?: 'value' | 'sample-column' | 'function';
};

function logicOperandDataType(
  source: string,
  value: string,
  indexes: Record<string, string[]>,
  externalInputs: ExternalInput[],
  parameters: Parameter[],
  samples: DataSample[],
) {
  const reference = value.replace(/^@/, '');
  if (source === 'index') return Object.keys(indexes).includes(reference) ? 'string' : undefined;
  if (source === 'external') return externalInputs.find((item) => item.name === reference)?.type;
  if (source === 'parameter')
    return parameters.find((item) => item.key === value.replace(/^@/, ''))?.dataType || 'number';
  if (source === 'data-sample') {
    const match = reference.match(/^([A-Z]+)\(@?([^.(),]+)\.([^) ,]+)(?:,\s*\d+)?\)$/i);
    const column =
      match &&
      samples
        .find((sample) => sample.key === match[2])
        ?.columns?.find((item) => item.name === match[3]);
    return match?.[1].toUpperCase() === 'COUNT' ? 'number' : column?.type;
  }
  return undefined;
}

function LogicOperandInput({
  value,
  source,
  expectedType,
  allowConstant = false,
  indexes,
  externalInputs,
  parameters,
  samples,
  onChange,
}: {
  value: string;
  source: LogicOperandSource;
  expectedType?: 'string' | 'number' | 'boolean';
  allowConstant?: boolean;
  indexes: Record<string, string[]>;
  externalInputs: ExternalInput[];
  parameters: Parameter[];
  samples: DataSample[];
  onChange: (source: LogicOperandSource, value: string) => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [matches, setMatches] = useState<LogicOperandSuggestion[]>([]);
  const [activeIndex, setActiveIndex] = useState(0);
  const suggestions: LogicOperandSuggestion[] = [
    ...parameters
      .filter((item) => item.key)
      .map((item) => ({
        name: `@${item.key}`,
        source: 'parameter' as const,
        label: 'Parameter',
        dataType: item.dataType || 'number',
      })),
    ...samples.flatMap((sample) =>
      (sample.columns || []).map((column) => ({
        name: `@${sample.key}.${column.name}`,
        source: 'data-sample' as const,
        label: 'Data sample column',
        dataType: column.type,
        kind: 'sample-column' as const,
      })),
    ),
    {
      name: 'FIRST(@)',
      source: 'data-sample' as const,
      label: 'Data sample function',
      dataType: expectedType || 'number',
      kind: 'function' as const,
    },
    {
      name: 'NTH(@, 2)',
      source: 'data-sample' as const,
      label: 'Data sample function',
      dataType: expectedType || 'number',
      kind: 'function' as const,
    },
    {
      name: 'LAST(@)',
      source: 'data-sample' as const,
      label: 'Data sample function',
      dataType: expectedType || 'number',
      kind: 'function' as const,
    },
    ...(!expectedType || expectedType === 'number'
      ? [
          {
            name: 'MIN(@)',
            source: 'data-sample' as const,
            label: 'Data sample function',
            dataType: 'number' as const,
            kind: 'function' as const,
          },
          {
            name: 'MAX(@)',
            source: 'data-sample' as const,
            label: 'Data sample function',
            dataType: 'number' as const,
            kind: 'function' as const,
          },
          {
            name: 'AVG(@)',
            source: 'data-sample' as const,
            label: 'Data sample function',
            dataType: 'number' as const,
            kind: 'function' as const,
          },
          {
            name: 'SUM(@)',
            source: 'data-sample' as const,
            label: 'Data sample function',
            dataType: 'number' as const,
            kind: 'function' as const,
          },
          {
            name: 'COUNT(@)',
            source: 'data-sample' as const,
            label: 'Data sample function',
            dataType: 'number' as const,
            kind: 'function' as const,
          },
        ]
      : []),
    ...externalInputs
      .filter((item) => item.name)
      .map((item) => ({
        name: `@${item.name}`,
        source: 'external' as const,
        label: 'External input',
        dataType: item.type,
      })),
    ...Object.keys(indexes).map((name) => ({
      name: `@${name}`,
      source: 'index' as const,
      label: 'Model index',
      dataType: 'string' as const,
    })),
    ...(allowConstant && expectedType === 'boolean'
      ? [
          {
            name: 'true',
            source: 'constant' as const,
            label: 'Boolean value',
            dataType: 'boolean' as const,
          },
          {
            name: 'false',
            source: 'constant' as const,
            label: 'Boolean value',
            dataType: 'boolean' as const,
          },
        ]
      : []),
  ].filter((item) => !expectedType || item.dataType === expectedType);
  const updateMatches = (text: string) => {
    const referenceToken = text.match(/@([A-Za-z_][A-Za-z0-9_]*)?$/);
    const functionToken = text.match(/(?:^|[^@A-Za-z0-9_])([A-Za-z_][A-Za-z0-9_]*)$/);
    if (referenceToken) {
      const query = (referenceToken[1] || '').toLowerCase();
      const prefix = text.slice(0, text.length - referenceToken[0].length);
      const insideSampleFunction = /(?:FIRST|NTH|LAST|MIN|MAX|AVG|SUM|COUNT)\s*\(\s*$/i.test(
        prefix,
      );
      setMatches(
        suggestions
          .filter((item) =>
            insideSampleFunction
              ? item.kind === 'sample-column' && item.name.slice(1).toLowerCase().startsWith(query)
              : item.kind !== 'function' &&
                item.kind !== 'sample-column' &&
                item.source !== 'constant' &&
                item.name.slice(1).toLowerCase().startsWith(query),
          )
          .slice(0, 16),
      );
    } else if (functionToken) {
      const query = functionToken[1].toLowerCase();
      setMatches(
        suggestions
          .filter((item) => item.kind === 'function' && item.name.toLowerCase().startsWith(query))
          .slice(0, 16),
      );
    } else {
      setMatches([]);
    }
    setActiveIndex(0);
  };
  const select = (suggestion: LogicOperandSuggestion) => {
    const input = inputRef.current;
    const caret = input?.selectionStart ?? value.length;
    const before = value.slice(0, caret);
    const token =
      suggestion.kind === 'function'
        ? before.match(/(?:^|[^@A-Za-z0-9_])([A-Za-z_][A-Za-z0-9_]*)$/)
        : before.match(/@([A-Za-z_][A-Za-z0-9_]*)?$/);
    const start = token
      ? caret - (suggestion.kind === 'function' ? token[1].length : token[0].length)
      : 0;
    const next = value.slice(0, start) + suggestion.name + value.slice(caret);
    onChange(suggestion.source, next);
    const placeholder = suggestion.kind === 'function' ? suggestion.name.indexOf('@') : -1;
    const nextCaret = placeholder >= 0 ? start + placeholder + 1 : start + suggestion.name.length;
    if (placeholder >= 0) updateMatches(next.slice(0, nextCaret));
    else setMatches([]);
    requestAnimationFrame(() => {
      input?.focus();
      input?.setSelectionRange(nextCaret, nextCaret);
    });
  };
  return (
    <div className="sql-autocomplete logic-autocomplete">
      <input
        ref={inputRef}
        value={value}
        className={source === 'data-sample' ? 'formula' : ''}
        placeholder={
          allowConstant ? 'Enter a value or select a reference' : 'Select or type a reference'
        }
        onChange={(event) => {
          const next = event.target.value;
          const exact = suggestions.find(
            (item) => item.name.toLowerCase() === next.trim().toLowerCase(),
          );
          onChange(exact?.source || (allowConstant ? 'constant' : source), next);
          updateMatches(next);
        }}
        onFocus={() => updateMatches(value)}
        onClick={() => updateMatches(value)}
        onKeyDown={(event) => {
          if (!matches.length) return;
          if (event.key === 'ArrowDown') {
            event.preventDefault();
            setActiveIndex((current) => (current + 1) % matches.length);
          } else if (event.key === 'ArrowUp') {
            event.preventDefault();
            setActiveIndex((current) => (current - 1 + matches.length) % matches.length);
          } else if (event.key === 'Enter' || event.key === 'Tab') {
            event.preventDefault();
            select(matches[activeIndex]);
          } else if (event.key === 'Escape') setMatches([]);
        }}
        onBlur={() => window.setTimeout(() => setMatches([]), 120)}
      />
      {matches.length > 0 && (
        <div className="sql-autocomplete-menu">
          {matches.map((item, index) => (
            <button
              type="button"
              className={index === activeIndex ? 'active' : ''}
              key={`${item.source}:${item.name}`}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => select(item)}
            >
              <span>{item.name}</span>
              <small>
                {item.label} · {item.dataType === 'string' ? 'text' : item.dataType}
              </small>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function LogicResultInput({
  value,
  dataType,
  previous,
  externalInputs,
  indexes,
  samples,
  onChange,
}: {
  value: string;
  dataType: NonNullable<Parameter['dataType']>;
  previous: Parameter[];
  externalInputs: ExternalInput[];
  indexes: Record<string, string[]>;
  samples: DataSample[];
  onChange: (value: string) => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [matches, setMatches] = useState<
    { name: string; source: string; kind?: 'value' | 'sample-column' | 'function' }[]
  >([]);
  const [activeIndex, setActiveIndex] = useState(0);
  const compatible = previous.filter(
    (item) => item.key && (item.dataType || 'number') === dataType,
  );
  const sampleValues = samples.flatMap((sample) =>
    (sample.columns || [])
      .filter((column) => column.type === dataType)
      .flatMap((column) =>
        (dataType === 'number'
          ? ['FIRST', 'NTH', 'LAST', 'MIN', 'MAX', 'AVG', 'SUM']
          : ['FIRST', 'NTH', 'LAST']
        ).map((fn) => ({
          name:
            fn === 'NTH'
              ? `NTH(@${sample.key}.${column.name}, 2)`
              : `${fn}(@${sample.key}.${column.name})`,
          source: 'Data sample',
        })),
      ),
  );
  if (dataType === 'boolean') {
    return (
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        <option value="true">True</option>
        <option value="false">False</option>
        {compatible.length > 0 && (
          <optgroup label="Boolean parameters">
            {compatible.map((item) => (
              <option key={item.key} value={`@${item.key}`}>
                @{item.key}
              </option>
            ))}
          </optgroup>
        )}
        {externalInputs.some((item) => item.name && item.type === 'boolean') && (
          <optgroup label="Boolean external inputs">
            {externalInputs
              .filter((item) => item.name && item.type === 'boolean')
              .map((item) => (
                <option key={item.name} value={`@${item.name}`}>
                  @{item.name}
                </option>
              ))}
          </optgroup>
        )}
        {sampleValues.length > 0 && (
          <optgroup label="Boolean data samples">
            {sampleValues.map((item) => (
              <option key={item.name} value={item.name}>
                {item.name}
              </option>
            ))}
          </optgroup>
        )}
      </select>
    );
  }
  const suggestions: {
    name: string;
    source: string;
    kind?: 'value' | 'sample-column' | 'function';
  }[] = [
    ...compatible.map((item) => ({ name: `@${item.key}`, source: 'Declared parameter' })),
    ...externalInputs
      .filter((item) => item.name && item.type === dataType)
      .map((item) => ({ name: `@${item.name}`, source: 'External input' })),
    ...(dataType === 'string'
      ? Object.keys(indexes).map((name) => ({ name: `@${name}`, source: 'Model index' }))
      : []),
    ...samples.flatMap((sample) =>
      (sample.columns || [])
        .filter((column) => column.type === dataType)
        .map((column) => ({
          name: `@${sample.key}.${column.name}`,
          source: 'Data sample column',
          kind: 'sample-column' as const,
        })),
    ),
    { name: 'FIRST(@)', source: 'Data sample function', kind: 'function' as const },
    { name: 'NTH(@, 2)', source: 'Data sample function', kind: 'function' as const },
    { name: 'LAST(@)', source: 'Data sample function', kind: 'function' as const },
    ...(dataType === 'number'
      ? ['MIN', 'MAX', 'AVG', 'SUM'].map((fn) => ({
          name: `${fn}(@)`,
          source: 'Data sample function',
          kind: 'function' as const,
        }))
      : []),
  ];
  const updateMatches = (text: string, caret: number) => {
    const before = text.slice(0, caret);
    const referenceToken = before.match(/@([A-Za-z_][A-Za-z0-9_]*)?$/);
    const functionToken = before.match(/(?:^|[^@A-Za-z0-9_])([A-Za-z_][A-Za-z0-9_]*)$/);
    if (referenceToken) {
      const query = (referenceToken[1] || '').toLowerCase();
      const prefix = before.slice(0, before.length - referenceToken[0].length);
      const insideSampleFunction = /(?:FIRST|NTH|LAST|MIN|MAX|AVG|SUM|COUNT)\s*\(\s*$/i.test(
        prefix,
      );
      setMatches(
        suggestions
          .filter((item) =>
            insideSampleFunction
              ? item.kind === 'sample-column' && item.name.slice(1).toLowerCase().startsWith(query)
              : item.kind !== 'function' &&
                item.kind !== 'sample-column' &&
                item.name.slice(1).toLowerCase().startsWith(query),
          )
          .slice(0, 8),
      );
    } else if (functionToken) {
      const query = functionToken[1].toLowerCase();
      setMatches(
        suggestions
          .filter((item) => item.kind === 'function' && item.name.toLowerCase().startsWith(query))
          .slice(0, 8),
      );
    } else {
      setMatches([]);
    }
    setActiveIndex(0);
  };
  const insert = (suggestion: {
    name: string;
    source: string;
    kind?: 'value' | 'sample-column' | 'function';
  }) => {
    const input = inputRef.current;
    if (!input) return;
    const caret = input.selectionStart ?? value.length;
    const before = value.slice(0, caret);
    const token =
      suggestion.kind === 'function'
        ? before.match(/(?:^|[^@A-Za-z0-9_])([A-Za-z_][A-Za-z0-9_]*)$/)
        : before.match(/@([A-Za-z_][A-Za-z0-9_]*)?$/);
    const start = token
      ? caret - (suggestion.kind === 'function' ? token[1].length : token[0].length)
      : caret;
    const next = value.slice(0, start) + suggestion.name + value.slice(caret);
    onChange(next);
    const placeholder = suggestion.kind === 'function' ? suggestion.name.indexOf('@') : -1;
    const nextCaret = placeholder >= 0 ? start + placeholder + 1 : start + suggestion.name.length;
    if (placeholder >= 0) updateMatches(next, nextCaret);
    else setMatches([]);
    requestAnimationFrame(() => {
      input.focus();
      input.setSelectionRange(nextCaret, nextCaret);
    });
  };
  return (
    <div className="sql-autocomplete logic-autocomplete">
      <input
        ref={inputRef}
        className={dataType === 'number' ? 'formula' : ''}
        value={value}
        onChange={(event) => {
          onChange(event.target.value);
          updateMatches(
            event.target.value,
            event.target.selectionStart ?? event.target.value.length,
          );
        }}
        onFocus={(event) =>
          updateMatches(event.currentTarget.value, event.currentTarget.selectionStart ?? 0)
        }
        onClick={(event) =>
          updateMatches(
            event.currentTarget.value,
            event.currentTarget.selectionStart ?? event.currentTarget.value.length,
          )
        }
        onKeyDown={(event) => {
          if (!matches.length) return;
          if (event.key === 'ArrowDown') {
            event.preventDefault();
            setActiveIndex((current) => (current + 1) % matches.length);
          } else if (event.key === 'ArrowUp') {
            event.preventDefault();
            setActiveIndex((current) => (current - 1 + matches.length) % matches.length);
          } else if (event.key === 'Enter' || event.key === 'Tab') {
            event.preventDefault();
            insert(matches[activeIndex]);
          } else if (event.key === 'Escape') {
            setMatches([]);
          }
        }}
        onBlur={() => window.setTimeout(() => setMatches([]), 120)}
        placeholder={
          dataType === 'number'
            ? 'Enter a number, formula, or parameter'
            : 'Enter text or a parameter'
        }
      />
      {matches.length > 0 && (
        <div className="sql-autocomplete-menu">
          {matches.map((suggestion, index) => (
            <button
              type="button"
              className={index === activeIndex ? 'active' : ''}
              key={`${suggestion.source}:${suggestion.name}`}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => insert(suggestion)}
            >
              <span>{suggestion.name}</span>
              <small>{suggestion.source}</small>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function ParameterSourceEditor({
  parameter,
  previous,
  resultParameters,
  samples,
  externalInputs,
  feedbackInputs,
  indexes,
  onChange,
}: {
  parameter: Parameter;
  previous: Parameter[];
  resultParameters: Parameter[];
  samples: DataSample[];
  externalInputs: ExternalInput[];
  feedbackInputs: FeedbackInput[];
  indexes: Record<string, string[]>;
  onChange: (next: Parameter) => void;
}) {
  const sources = useContext(DataSourcesContext);
  if (parameter.source === 'lookup') {
    const lookup = parameter.lookup || {
      sample: samples[0]?.key || '',
      matchColumn: '',
      operator: 'equals' as const,
      inputSource: 'constant' as const,
      matchValue: '',
      returnColumn: '',
    };
    const sample = samples.find((item) => item.key === lookup.sample);
    const columns = sample?.columns || [];
    const matchType =
      columns.find((column) => column.name === lookup.matchColumn)?.type || 'string';
    const update = (patch: Partial<Lookup>) =>
      onChange({ ...parameter, lookup: { ...lookup, ...patch } });
    const namedInputs =
      lookup.inputSource === 'index'
        ? Object.keys(indexes).map((name) => ({ name, type: 'string' as const }))
        : lookup.inputSource === 'external'
          ? externalInputs
          : resultParameters.map((item) => ({
              name: item.key,
              type: item.dataType || ('number' as const),
            }));
    return (
      <div className="lookup-settings span-three">
        <div className="adaptive-heading">
          <div>
            <strong>Typed table lookup</strong>
            <small>Exactly one row must match; zero or multiple matches stop execution.</small>
          </div>
        </div>
        <div className="form-grid three">
          <label>
            Data sample
            <select
              value={lookup.sample}
              onChange={(event) => {
                const nextSample = samples.find((item) => item.key === event.target.value);
                update({
                  sample: event.target.value,
                  matchColumn: nextSample?.columns?.[0]?.name || '',
                  returnColumn:
                    nextSample?.columns?.find(
                      (column) => column.type === (parameter.dataType || 'number'),
                    )?.name || '',
                });
              }}
            >
              <option value="">Select sample</option>
              {samples.map((item) => (
                <option key={item.key} value={item.key}>
                  {item.key}
                </option>
              ))}
            </select>
          </label>
          <label>
            Match column
            <select
              value={lookup.matchColumn}
              onChange={(event) =>
                update({ matchColumn: event.target.value, matchValue: '', operator: 'equals' })
              }
            >
              <option value="">Select column</option>
              {columns.map((column) => (
                <option key={column.name} value={column.name}>
                  {column.name} · {column.type}
                </option>
              ))}
            </select>
          </label>
          <label>
            Match operator
            <select
              value={lookup.operator}
              onChange={(event) => update({ operator: event.target.value as Lookup['operator'] })}
            >
              <option value="equals">Equals</option>
              <option value="contains" disabled={matchType !== 'string'}>
                Contains
              </option>
            </select>
          </label>
          <label>
            Match value source
            <select
              value={lookup.inputSource}
              onChange={(event) =>
                update({
                  inputSource: event.target.value as Lookup['inputSource'],
                  matchValue: '',
                })
              }
            >
              <option value="constant">Fixed value</option>
              <option value="index">Model index</option>
              <option value="external">External input</option>
              <option value="parameter">Parameter</option>
            </select>
          </label>
          <label>
            Match value
            {lookup.inputSource === 'constant' ? (
              matchType === 'boolean' ? (
                <select
                  value={lookup.matchValue}
                  onChange={(event) => update({ matchValue: event.target.value })}
                >
                  <option value="">Select value</option>
                  <option value="true">True</option>
                  <option value="false">False</option>
                </select>
              ) : (
                <input
                  type={matchType === 'number' ? 'number' : 'text'}
                  value={lookup.matchValue}
                  onChange={(event) => update({ matchValue: event.target.value })}
                />
              )
            ) : (
              <select
                value={lookup.matchValue}
                onChange={(event) => update({ matchValue: event.target.value })}
              >
                <option value="">Select value</option>
                {namedInputs
                  .filter((item) => item.name && item.type === matchType)
                  .map((item) => (
                    <option key={item.name} value={item.name}>
                      {item.name}
                    </option>
                  ))}
              </select>
            )}
          </label>
          <label>
            Return column
            <select
              value={lookup.returnColumn}
              onChange={(event) => update({ returnColumn: event.target.value })}
            >
              <option value="">Select column</option>
              {columns
                .filter((column) => column.type === (parameter.dataType || 'number'))
                .map((column) => (
                  <option key={column.name} value={column.name}>
                    {column.name} · {column.type}
                  </option>
                ))}
            </select>
          </label>
        </div>
      </div>
    );
  }
  if (parameter.source === 'logic') {
    const availableParameters = resultParameters.filter((item) => item.key !== parameter.key);
    const logic = parameter.logic || {
      conditions: [
        {
          inputParameter: '',
          inputSource: 'parameter' as const,
          operator: 'equals' as const,
          compareValue: '',
          resultValue: parameter.dataType === 'boolean' ? 'true' : '1',
        },
      ],
      elseValue: parameter.dataType === 'boolean' ? 'false' : '2',
    };
    const update = (patch: Partial<Logic>) =>
      onChange({ ...parameter, logic: { ...logic, ...patch } });
    const updateRule = (index: number, patch: Partial<LogicCondition>) =>
      update({
        conditions: logic.conditions.map((rule, n) => (n === index ? { ...rule, ...patch } : rule)),
      });
    return (
      <div className="logic-settings span-three">
        <div className="adaptive-heading">
          <strong>Conditional logic</strong>
          <button
            className="secondary"
            onClick={() =>
              update({
                conditions: [
                  ...logic.conditions,
                  {
                    inputParameter: '',
                    inputSource: 'parameter',
                    operator: 'equals',
                    compareValue: '',
                    resultValue: parameter.dataType === 'boolean' ? 'true' : '1',
                  },
                ],
              })
            }
          >
            <Plus />
            Add condition
          </button>
        </div>
        {logic.conditions.map((rule, index) => (
          <div className="logic-rule" key={index}>
            <div className="logic-rule-title">
              <strong>
                {index === 0 ? 'If' : 'Else if'} condition {index + 1}
              </strong>
              {logic.conditions.length > 1 && (
                <button
                  className="remove-index"
                  onClick={() =>
                    update({ conditions: logic.conditions.filter((_, n) => n !== index) })
                  }
                >
                  <Trash2 />
                </button>
              )}
            </div>
            <div className="logic-condition logic-primary">
              <LogicPredicateEditor
                predicate={rule}
                indexes={indexes}
                externalInputs={externalInputs}
                parameters={availableParameters}
                samples={samples}
                onChange={(patch) => updateRule(index, patch)}
              />
            </div>
            {(rule.and || []).map((predicate, predicateIndex) => (
              <div className="logic-and" key={predicateIndex}>
                <strong>AND</strong>
                <div className="logic-condition">
                  <LogicPredicateEditor
                    predicate={predicate}
                    indexes={indexes}
                    externalInputs={externalInputs}
                    parameters={availableParameters}
                    samples={samples}
                    onChange={(patch) =>
                      updateRule(index, {
                        and: (rule.and || []).map((item, itemIndex) =>
                          itemIndex === predicateIndex ? { ...item, ...patch } : item,
                        ),
                      })
                    }
                  />
                  <button
                    className="remove-index"
                    title="Remove AND condition"
                    onClick={() =>
                      updateRule(index, {
                        and: (rule.and || []).filter(
                          (_, itemIndex) => itemIndex !== predicateIndex,
                        ),
                      })
                    }
                  >
                    <Trash2 />
                  </button>
                </div>
              </div>
            ))}
            {(rule.or || []).map((predicate, predicateIndex) => (
              <div className="logic-and logic-or" key={`or-${predicateIndex}`}>
                <strong>OR</strong>
                <div className="logic-condition">
                  <LogicPredicateEditor
                    predicate={predicate}
                    indexes={indexes}
                    externalInputs={externalInputs}
                    parameters={availableParameters}
                    samples={samples}
                    onChange={(patch) =>
                      updateRule(index, {
                        or: (rule.or || []).map((item, itemIndex) =>
                          itemIndex === predicateIndex ? { ...item, ...patch } : item,
                        ),
                      })
                    }
                  />
                  <button
                    className="remove-index"
                    title="Remove OR condition"
                    onClick={() =>
                      updateRule(index, {
                        or: (rule.or || []).filter((_, itemIndex) => itemIndex !== predicateIndex),
                      })
                    }
                  >
                    <Trash2 />
                  </button>
                </div>
              </div>
            ))}
            <div className="logic-condition-actions">
              <button
                className="secondary logic-add-and"
                onClick={() =>
                  updateRule(index, {
                    and: [
                      ...(rule.and || []),
                      {
                        inputParameter: '',
                        inputSource: 'parameter',
                        operator: 'equals',
                        compareValue: '',
                      },
                    ],
                  })
                }
              >
                <Plus />
                Add AND condition
              </button>
              <button
                className="secondary logic-add-and"
                onClick={() =>
                  updateRule(index, {
                    or: [
                      ...(rule.or || []),
                      {
                        inputParameter: '',
                        inputSource: 'parameter',
                        operator: 'equals',
                        compareValue: '',
                      },
                    ],
                  })
                }
              >
                <Plus />
                Add OR condition
              </button>
            </div>
            <div className="logic-result">
              <label>
                Result value
                <LogicResultInput
                  dataType={parameter.dataType || 'number'}
                  previous={availableParameters}
                  externalInputs={externalInputs}
                  indexes={indexes}
                  samples={samples}
                  value={rule.resultValue}
                  onChange={(resultValue) => updateRule(index, { resultValue })}
                />
              </label>
            </div>
          </div>
        ))}
        <div className="logic-else">
          <label>
            Else value
            <LogicResultInput
              dataType={parameter.dataType || 'number'}
              previous={availableParameters}
              externalInputs={externalInputs}
              indexes={indexes}
              samples={samples}
              value={logic.elseValue}
              onChange={(elseValue) => update({ elseValue })}
            />
          </label>
        </div>
      </div>
    );
  }
  if (parameter.adaptive) {
    const adaptive = parameter.adaptive || { initialValue: 3, updateMethod: 'ewma', alpha: 0.25 };
    const update = (patch: Partial<Adaptive>) =>
      onChange({ ...parameter, adaptive: { ...adaptive, ...patch } });
    return (
      <div className="adaptive-settings span-three">
        <div className="adaptive-heading">
          <strong>Adaptive settings</strong>
          <small>The current value is updated during prepare and retained for the next run.</small>
        </div>
        <div className="form-grid three">
          <label>
            Initial value
            <input
              type="number"
              value={adaptive.initialValue}
              onChange={(e) => {
                const initialValue = +e.target.value;
                onChange({
                  ...parameter,
                  definition: String(initialValue),
                  adaptive: { ...adaptive, initialValue },
                });
              }}
            />
          </label>
          <label>
            Update method
            <select
              value={adaptive.updateMethod}
              onChange={(e) => update({ updateMethod: e.target.value as Adaptive['updateMethod'] })}
            >
              <option value="ewma">EWMA</option>
              <option value="formula">Custom formula</option>
            </select>
          </label>
          <label className="span-three">
            Update input
            <select
              value={
                adaptive.updateInput?.name
                  ? `${adaptive.updateInput.source}:${adaptive.updateInput.name}`
                  : adaptive.updateParameter || adaptive.measurementParameter
                    ? `parameter:${adaptive.updateParameter || adaptive.measurementParameter}`
                    : ''
              }
              onChange={(e) => {
                const [source, ...nameParts] = e.target.value.split(':');
                update({
                  updateInput: e.target.value
                    ? {
                        source: source as 'parameter' | 'post-run-feedback',
                        name: nameParts.join(':'),
                      }
                    : { source: 'parameter', name: '' },
                  updateParameter: undefined,
                  measurementParameter: undefined,
                });
              }}
            >
              <option value="">Select update input</option>
              <optgroup label="During prepare - model parameters">
                {previous
                  .filter((item) => item.key && (item.dataType || 'number') === 'number')
                  .map((item) => (
                    <option key={`parameter:${item.key}`} value={`parameter:${item.key}`}>
                      parameter.{item.key}
                    </option>
                  ))}
              </optgroup>
              <optgroup label="During completion - post-run feedback">
                {feedbackInputs
                  .filter((item) => item.name && item.type === 'number')
                  .map((item) => (
                    <option
                      key={`post-run-feedback:${item.name}`}
                      value={`post-run-feedback:${item.name}`}
                    >
                      feedback.{item.name}
                    </option>
                  ))}
              </optgroup>
            </select>
          </label>
          {adaptive.updateMethod === 'ewma' ? (
            <>
              <label>
                Alpha
                <input
                  type="number"
                  min=".01"
                  max="1"
                  step=".01"
                  value={adaptive.alpha ?? 0.25}
                  onChange={(e) => update({ alpha: +e.target.value })}
                />
              </label>
              <p className="adaptive-preview span-three">
                next = alpha × measurement + (1 - alpha) × previous value
              </p>
            </>
          ) : (
            <label className="span-three">
              Update formula
              <ExpressionAutocomplete
                value={adaptive.updateFormula || ''}
                onChange={(updateFormula) => update({ updateFormula })}
                references={[
                  { name: 'previous_value', source: 'Current adaptive value' },
                  { name: 'current_value', source: 'Current adaptive value' },
                  ...resultParameters
                    .filter(
                      (item) =>
                        item.key &&
                        item.key !== parameter.key &&
                        (item.dataType || 'number') === 'number',
                    )
                    .map((item) => ({ name: item.key, source: 'Parameter' })),
                  ...externalInputs
                    .filter((item) => item.name && item.type === 'number')
                    .map((item) => ({ name: item.name, source: 'External input' })),
                  ...feedbackInputs
                    .filter((item) => item.name && item.type === 'number')
                    .map((item) => ({ name: item.name, source: 'Post-run feedback' })),
                ]}
                placeholder="@previous_value + (@measurement * 0.1)"
              />
            </label>
          )}
          <label>
            Deadband
            <input
              type="number"
              min="0"
              value={adaptive.deadband ?? ''}
              onChange={(e) =>
                update({ deadband: e.target.value === '' ? undefined : +e.target.value })
              }
            />
            <small>Ignore proposed changes at or below this amount.</small>
          </label>
          <label>
            Maximum adjustment per run
            <input
              type="number"
              min="0.000001"
              value={adaptive.maxAdjustment ?? ''}
              onChange={(e) =>
                update({ maxAdjustment: e.target.value === '' ? undefined : +e.target.value })
              }
            />
            <small>Limit how far the retained value can move in one update.</small>
          </label>
        </div>
      </div>
    );
  }
  if (parameter.source === 'sql')
    return (
      <div className="span-three sql-source-settings">
        <label>
          <span className="field-label">
            <span>Data source</span>
            <b className="required-badge">Required</b>
          </span>
          <select
            value={parameter.dataSourceId || ''}
            onChange={(e) => onChange({ ...parameter, dataSourceId: e.target.value })}
          >
            <option value="">Select a configured data source</option>
            {sources
              .filter((source) => source.type === 'sql')
              .map((source) => (
                <option key={source.id} value={source.id}>
                  {source.name} · {source.location}
                </option>
              ))}
          </select>
        </label>
        <label>
          <span className="field-label">
            <span>SQL query</span>
            <b className="required-badge">Required</b>
          </span>
          <SQLAutocomplete
            value={parameter.definition}
            onChange={(definition) => onChange({ ...parameter, definition })}
            references={[
              ...externalInputs
                .filter((item) => item.name)
                .map((item) => ({ name: item.name, source: 'External input' as const, dataType: item.type })),
              ...previous
                .filter((item) => item.key)
                .map((item) => ({ name: item.key, source: 'Parameter' as const, dataType: item.dataType || ('number' as const) })),
              ...Object.keys(indexes).map((name) => ({ name, source: 'Model index', dataType: 'string' as const })),
            ]}
            placeholder="SELECT measured_value FROM metrology WHERE wafer_id = @wafer_id"
          />
        </label>
        {(externalInputs.some((item) => item.name) || previous.some((item) => item.key)) && (
          <div className="sql-parameter-references">
            <span>Available query parameters</span>
            <div>
              {externalInputs
                .filter((item) => item.name)
                .map((item) => (
                  <button
                    type="button"
                    key={`external-${item.name}`}
                    onClick={() =>
                      onChange({
                        ...parameter,
                        definition: `${parameter.definition}${parameter.definition ? ' ' : ''}@${item.name}`,
                      })
                    }
                  >
                    @{item.name}
                  </button>
                ))}
              {previous
                .filter((item) => item.key)
                .map((item) => (
                  <button
                    type="button"
                    key={item.key}
                    onClick={() =>
                      onChange({
                        ...parameter,
                        definition: `${parameter.definition}${parameter.definition ? ' ' : ''}@${item.key}`,
                      })
                    }
                  >
                    @{item.key}
                  </button>
                ))}
            </div>
          </div>
        )}
        <SQLQueryTester
          dataSourceId={parameter.dataSourceId}
          query={parameter.definition}
          queryType="parameter"
          dataType={parameter.dataType || 'number'}
          references={[
            ...externalInputs
              .filter((item) => item.name)
              .map((item) => ({ name: item.name, source: 'External input', dataType: item.type })),
            ...previous
              .filter((item) => item.key)
              .map((item) => ({ name: item.key, source: 'Parameter', dataType: item.dataType || ('number' as const) })),
            ...Object.keys(indexes).map((name) => ({ name, source: 'Model index', dataType: 'string' as const })),
          ]}
        />
      </div>
    );
  return (
    <label className="span-three">
      <span className="field-label">
        <span>{sourceLabel(parameter.source, parameter.dataType || 'number')}</span>
        <b className="required-badge">Required</b>
      </span>
      {parameter.source === 'expression' ||
      parameter.source === 'formula' ||
      parameter.source === 'data-sample' ? (
        <ExpressionPopupEditor
          value={parameter.definition}
          onChange={(definition) => onChange({ ...parameter, definition })}
          references={[
            ...resultParameters
              .filter((item) => item.key && item.key !== parameter.key)
              .map((item) => ({ name: item.key, source: 'Parameter' })),
            ...externalInputs
              .filter((item) => item.name)
              .map((item) => ({ name: item.name, source: 'External input' })),
            ...Object.keys(indexes).map((name) => ({ name, source: 'Model index' })),
            { name: 'true', source: 'Boolean value', plain: true },
            { name: 'false', source: 'Boolean value', plain: true },
            ...samples.map((sample) => ({
              name: sample.key,
              source: 'Data sample',
              kind: 'sample' as const,
            })),
            ...samples.flatMap((sample) =>
              (sample.columns || [])
                .filter((column) => {
                  if ((parameter.dataType || 'number') === 'number')
                    return column.type === 'number';
                  return column.type === (parameter.dataType || 'number');
                })
                .map((column) => ({
                  name: `${sample.key}.${column.name}`,
                  source: `Data sample column · ${column.type}`,
                  kind: 'sample-column' as const,
                })),
            ),
            ...[
              { name: 'CONTAINS(@, "value")', source: 'Data sample function', plain: true },
              { name: 'MATCHES(@, "^value.*")', source: 'Data sample function', plain: true },
              { name: 'FIRST(@)', source: 'Data sample function', plain: true },
              { name: 'NTH(@, 2)', source: 'Data sample function', plain: true },
              { name: 'LAST(@)', source: 'Data sample function', plain: true },
            ],
            ...[
              {
                name: 'IS_NULL(@value)',
                source: 'Conditional expression',
                plain: true,
              },
              { name: 'MEDIAN(@)', source: 'Aggregate function', plain: true },
              { name: 'RANGE(@)', source: 'Aggregate function', plain: true },
              { name: 'STDEV(@)', source: 'Aggregate function', plain: true },
              { name: 'ABS(@value)', source: 'Math function', plain: true },
              { name: 'CEIL(@value)', source: 'Math function', plain: true },
              { name: 'COS(@value)', source: 'Math function', plain: true },
              { name: 'EXP(@value)', source: 'Math function', plain: true },
              { name: 'FLOOR(@value)', source: 'Math function', plain: true },
              { name: 'LOG(@value)', source: 'Math function', plain: true },
              { name: 'POW(@value, exponent)', source: 'Math function', plain: true },
              { name: 'ROUND(@value)', source: 'Math function', plain: true },
              { name: 'SIN(@value)', source: 'Math function', plain: true },
              { name: 'SQRT(@value)', source: 'Math function', plain: true },
              { name: 'TAN(@value)', source: 'Math function', plain: true },
              { name: 'SUBSTR(@text, start, length)', source: 'String function', plain: true },
              { name: 'REPLACE(@text, "old", "new")', source: 'String function', plain: true },
              { name: 'LOWER(@text)', source: 'String function', plain: true },
              { name: 'UPPER(@text)', source: 'String function', plain: true },
              { name: 'TRIM(@text)', source: 'String function', plain: true },
              { name: 'CONCAT(@text, "suffix")', source: 'String function', plain: true },
              { name: 'LEN(@text)', source: 'String function', plain: true },
            ],
            ...((parameter.dataType || 'number') === 'number'
              ? [
                  { name: 'MIN(@)', source: 'Data sample function', plain: true },
                  { name: 'MAX(@)', source: 'Data sample function', plain: true },
                  { name: 'AVG(@)', source: 'Data sample function', plain: true },
                  { name: 'SUM(@)', source: 'Data sample function', plain: true },
                  { name: 'COUNT(@)', source: 'Data sample function', plain: true },
                ]
              : []),
            ...[
              {
                name: 'IF(@boolean_parameter, result_if_true, result_if_false)',
                source: 'Conditional expression',
                plain: true,
              },
              {
                name: 'LOOKUP_ONE(@sample, @sample.column == @value, @sample.return_column)',
                source: 'Lookup formula',
                plain: true,
              },
              {
                name: 'LOOKUP_FIRST(@sample, @sample.column == @value, @sample.return_column)',
                source: 'Lookup formula',
                plain: true,
              },
            ],
          ]}
          placeholder={sourcePlaceholder(
            parameter.source,
            previous,
            samples,
            parameter.dataType || 'number',
          )}
        />
      ) : (
        <input
          className=""
          value={parameter.definition}
          onChange={(e) => onChange({ ...parameter, definition: e.target.value })}
          placeholder={sourcePlaceholder(
            parameter.source,
            previous,
            samples,
            parameter.dataType || 'number',
          )}
        />
      )}
    </label>
  );
}
function sourceLabel(
  source: Parameter['source'],
  dataType: NonNullable<Parameter['dataType']> = 'number',
) {
  return source === 'sql'
    ? 'SQL query'
    : source === 'expression'
      ? 'Expression'
      : source === 'constant'
        ? 'Constant value'
        : source === 'formula'
          ? 'Formula using previous parameters'
          : source === 'data-sample'
            ? dataType !== 'number'
              ? 'Data sample value'
              : 'Data sample aggregate'
            : source === 'lookup'
              ? 'Typed data sample lookup'
              : 'Context field from external app';
}
function sourcePlaceholder(
  source: Parameter['source'],
  previous: Parameter[],
  samples: DataSample[],
  dataType: NonNullable<Parameter['dataType']> = 'number',
) {
  if (source === 'external') return 'waferId';
  if (source === 'expression') {
    if (dataType === 'boolean') return 'IF(@temperature > 100, true, false)';
    if (dataType === 'string') return 'IF(@is_rework, "rework", "standard")';
    return 'FIRST(@sample.measurement) * 3';
  }
  if (source === 'constant') return dataType === 'boolean' ? 'false' : '0';
  if (source === 'data-sample') {
    const sample = samples[0]?.key || 'datasample1';
    return dataType !== 'number'
      ? `FIRST(@${sample}.operation_name)`
      : `MAX(@${sample}.thickness_nm)`;
  }
  return (
    previous
      .filter((item) => (item.dataType || 'number') === 'number')
      .map((item) => `@${item.key}`)
      .filter(Boolean)
      .join(' + ') || '@previous_parameter * 1.0'
  );
}
function updateSample(form: any, setForm: any, index: number, next: DataSample) {
  setForm({
    ...form,
    dataSamples: form.dataSamples.map((item: DataSample, n: number) => (n === index ? next : item)),
  });
}
function IndexEditor({
  indexes,
  onAdd,
  onChange,
}: {
  indexes: Record<string, string[]>;
  onAdd: () => void;
  onChange: (indexes: Record<string, string[]>) => void;
}) {
  const required = ['machine', 'action', 'material'];
  const additional = Object.entries(indexes).filter(([key]) => !required.includes(key));
  const setValues = (key: string, values: string[]) => onChange({ ...indexes, [key]: values });
  return (
    <>
      <EditorHeader
        title="Model selection indexes"
        action={
          <button className="secondary" onClick={onAdd}>
            <Plus />
            Add index
          </button>
        }
      />
      <p className="field-help">
        Machine, action, and material are required. Add more indexes for a narrower process context.
        Use <code>*</code> as the only accepted value to match any non-empty value; an exact model
        takes priority over a wildcard model.
      </p>
      <div className="index-editor">
        <div className="index-table-head">
          <span>INDEX</span>
          <span>ACCEPTED VALUES</span>
          <span></span>
        </div>
        {required.map((key) => (
          <div className="index-form-row required" key={key}>
            <div>
              <strong>{key}</strong>
              <small>Required</small>
            </div>
            <IndexValuesInput
              values={indexes[key] || []}
              onChange={(values) => setValues(key, values)}
            />
            <ShieldCheck />
          </div>
        ))}
        {additional.map(([key, values]) => (
          <div className="index-form-row" key={key}>
            <div>
              <strong>{key}</strong>
              <small>Additional</small>
            </div>
            <IndexValuesInput values={values} onChange={(next) => setValues(key, next)} />
            <button
              className="remove-index"
              onClick={() => {
                const next = { ...indexes };
                delete next[key];
                onChange(next);
              }}
            >
              <Trash2 />
            </button>
          </div>
        ))}
      </div>
    </>
  );
}

function IndexValuesInput({
  values,
  onChange,
}: {
  values: string[];
  onChange: (values: string[]) => void;
}) {
  const [draft, setDraft] = useState('');
  const addValue = () => {
    const next = draft.trim();
    if (!next || values.some((value) => value.toLowerCase() === next.toLowerCase())) {
      setDraft('');
      return;
    }
    if (next === '*') onChange(['*']);
    else if (values.includes('*')) onChange([next]);
    else onChange([...values, next]);
    setDraft('');
  };
  return (
    <div className="index-values">
      {values.map((value) => (
        <span key={value}>
          {value}
          <button
            type="button"
            title={`Remove ${value}`}
            onClick={() => onChange(values.filter((item) => item !== value))}
          >
            <X />
          </button>
        </span>
      ))}
      <input
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={addValue}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ',') {
            event.preventDefault();
            addValue();
          }
        }}
        placeholder={values.length ? 'Add value' : 'Type value and press Enter'}
      />
    </div>
  );
}
function EditorHeader({ title, action }: { title: string; action?: React.ReactNode }) {
  return (
    <div className="editor-head">
      <h4>{title}</h4>
      {action}
    </div>
  );
}
