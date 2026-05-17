import { FormEvent, ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import { ApiError, api, errorLabel, fetchArtifactContent, isAuthError, setCsrfToken } from './api';
import type {
  Approval,
  ApprovalDecision,
  Diagnostics,
  Health,
  Issue,
  IssueRefSummary,
  IssueState,
  ReviewPacketArtifact,
  ReviewPacketSummary,
  RunAttempt,
  RunEvent,
  WorkflowRenderPreviewResponse,
  WorkflowResponse,
  WorkflowValidateResponse
} from './types';

export const BOARD_STATES: IssueState[] = ['Inbox', 'Ready', 'Working', 'Rework', 'Blocked', 'Human Review', 'Done', 'Cancelled', 'Duplicate'];

export const dashboardActions = [
  'create issue',
  'update issue',
  'transition issue',
  'dispatch eligible issue',
  'dispatch pause issue',
  'dispatch resume issue',
  'approve once',
  'approve for run',
  'approve for session',
  'deny approval',
  'cancel run',
  'send to rework',
  'mark done',
  'workflow validate',
  'workflow reload',
  'diagnostics export'
] as const;

const activeRunStatuses = new Set(['pending', 'preparing_workspace', 'rendering_prompt', 'starting_agent', 'running']);
const transitionTargets: IssueState[] = ['Inbox', 'Ready', 'Blocked', 'Cancelled', 'Duplicate'];
const approvalDecisions: Array<{ value: ApprovalDecision; label: string; helper: string }> = [
  { value: 'approve_once', label: 'Approve once', helper: 'Approve only this request.' },
  { value: 'approve_for_run', label: 'Approve for run', helper: 'Approve matching requests for the current run.' },
  { value: 'approve_for_session', label: 'Approve for session', helper: 'Approve matching requests for this local session.' },
  { value: 'deny', label: 'Deny current action', helper: 'Decline this approval request only.' },
  { value: 'cancel_run', label: 'Cancel run', helper: 'Cancel the whole run with operator_cancelled side effects.' }
];

type PageKey = 'overview' | 'board' | 'issue' | 'run' | 'approvals' | 'review' | 'workflow' | 'diagnostics';

interface RouteState {
  page: PageKey;
  issueRef?: string;
  runId?: string;
}

interface DashboardData {
  health: Health | null;
  issues: Issue[];
  runs: RunAttempt[];
  approvals: Approval[];
  workflow: WorkflowResponse | null;
  diagnostics: Diagnostics | null;
  events: RunEvent[];
}

const emptyData: DashboardData = {
  health: null,
  issues: [],
  runs: [],
  approvals: [],
  workflow: null,
  diagnostics: null,
  events: []
};

function parseRoute(): RouteState {
  const raw = window.location.hash.replace(/^#\/?/, '');
  const [page = 'overview', id] = raw.split('/').map(decodeURIComponent);
  if (page === 'issue' && id) return { page: 'issue', issueRef: id };
  if (page === 'run' && id) return { page: 'run', runId: id };
  if (page === 'review') return { page: 'review', issueRef: id };
  if (['overview', 'board', 'approvals', 'workflow', 'diagnostics'].includes(page)) return { page: page as PageKey };
  return { page: 'overview' };
}

function navigate(route: RouteState): void {
  if (route.page === 'issue' && route.issueRef) {
    window.location.hash = `#/issue/${encodeURIComponent(route.issueRef)}`;
    return;
  }
  if (route.page === 'run' && route.runId) {
    window.location.hash = `#/run/${encodeURIComponent(route.runId)}`;
    return;
  }
  if (route.page === 'review' && route.issueRef) {
    window.location.hash = `#/review/${encodeURIComponent(route.issueRef)}`;
    return;
  }
  window.location.hash = `#/${route.page}`;
}

function formatDate(value: string | null | undefined): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function compactJson(value: unknown): string {
  return JSON.stringify(value, null, 2);
}

function isDispatchable(issue: Issue): boolean {
  return (issue.state === 'Ready' || issue.state === 'Rework') && !issue.dispatch_paused && !issue.active_run_id;
}

function issueByIdOrRef(issues: Issue[], ref: string | null | undefined): Issue | undefined {
  if (!ref) return undefined;
  return issues.find((issue) => issue.id === ref || issue.identifier === ref);
}

function refLabel(ref: IssueRefSummary | null | undefined): string {
  if (!ref) return '—';
  return `${ref.identifier} · ${ref.title}`;
}

function useRoute(): RouteState {
  const [route, setRoute] = useState<RouteState>(() => parseRoute());
  useEffect(() => {
    const onHash = () => setRoute(parseRoute());
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);
  return route;
}

function useDashboardData() {
  const [data, setData] = useState<DashboardData>(emptyData);
  const [loading, setLoading] = useState(true);
  const [mutating, setMutating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authError, setAuthError] = useState(false);
  const [daemonUnavailable, setDaemonUnavailable] = useState(false);
  const [sseState, setSseState] = useState<'connecting' | 'connected' | 'reconnecting' | 'disabled'>('connecting');

  const loadAll = useCallback(async () => {
    setError(null);
    setAuthError(false);
    try {
      const health = await api.health();
      setDaemonUnavailable(false);
      const session = await api.session().catch(() => null);
      if (session?.csrf_token || session?.csrf) setCsrfToken(session.csrf_token || session.csrf || null);
      const [issuesPage, runs, approvals, workflow, diagnostics, events] = await Promise.all([
        api.issues('?limit=200'),
        api.runs(),
        api.approvals(),
        api.workflow(),
        api.diagnostics(),
        api.events()
      ]);
      setData({
        health,
        issues: issuesPage.items || [],
        runs,
        approvals,
        workflow,
        diagnostics,
        events
      });
    } catch (err) {
      if (isAuthError(err)) {
        setAuthError(true);
      } else if (err instanceof TypeError) {
        setDaemonUnavailable(true);
      } else {
        setError(errorLabel(err));
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadAll();
    const interval = window.setInterval(() => void loadAll(), 12_000);
    return () => window.clearInterval(interval);
  }, [loadAll]);

  useEffect(() => {
    if (!('EventSource' in window)) {
      setSseState('disabled');
      return undefined;
    }
    setSseState('connecting');
    const source = new EventSource('/api/v1/events/stream');
    source.onopen = () => setSseState('connected');
    source.onmessage = () => {
      setSseState('connected');
      void loadAll();
    };
    source.addEventListener('run.claimed', () => {
      setSseState('connected');
      void loadAll();
    });
    source.onerror = () => {
      setSseState('reconnecting');
    };
    return () => source.close();
  }, [loadAll]);

  const runMutation = useCallback(async <T,>(operation: () => Promise<T>): Promise<T | null> => {
    setMutating(true);
    setError(null);
    setAuthError(false);
    try {
      const result = await operation();
      await loadAll();
      return result;
    } catch (err) {
      if (isAuthError(err)) {
        setAuthError(true);
      }
      setError(errorLabel(err));
      return null;
    } finally {
      setMutating(false);
    }
  }, [loadAll]);

  return { data, loading, mutating, error, authError, daemonUnavailable, sseState, reload: loadAll, runMutation };
}

function Section({ title, children, actions }: { title: string; children: ReactNode; actions?: ReactNode }) {
  return (
    <section className="section">
      <div className="section-header">
        <h2>{title}</h2>
        {actions ? <div className="section-actions">{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

function EmptyState({ title, body, action }: { title: string; body: string; action?: ReactNode }) {
  return (
    <div className="empty-state" role="status">
      <strong>{title}</strong>
      <p>{body}</p>
      {action}
    </div>
  );
}

function Banner({ kind, children }: { kind: 'error' | 'warning' | 'info' | 'success'; children: ReactNode }) {
  return <div className={`banner banner-${kind}`}>{children}</div>;
}

function Pill({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'good' | 'warning' | 'danger' | 'muted' }) {
  return <span className={`pill pill-${tone}`}>{children}</span>;
}

function KeyValue({ rows }: { rows: Array<[string, ReactNode]> }) {
  return (
    <dl className="key-value">
      {rows.map(([key, value]) => (
        <div key={key}>
          <dt>{key}</dt>
          <dd>{value ?? '—'}</dd>
        </div>
      ))}
    </dl>
  );
}

function JsonBlock({ value, maxHeight }: { value: unknown; maxHeight?: number }) {
  return (
    <pre className="json-block" style={maxHeight ? { maxHeight } : undefined}>
      {typeof value === 'string' ? value : compactJson(value)}
    </pre>
  );
}

function StatusPill({ value }: { value: string }) {
  const tone = value.includes('failed') || value === 'Blocked' || value === 'Cancelled' || value === 'denied'
    ? 'danger'
    : value.includes('pending') || value === 'Working' || value === 'Human Review'
      ? 'warning'
      : value === 'Done' || value === 'completed' || value === 'Ready'
        ? 'good'
        : 'neutral';
  return <Pill tone={tone}>{value}</Pill>;
}

function AppShell({ route, data, loading, mutating, error, authError, daemonUnavailable, sseState, children, reload }: {
  route: RouteState;
  data: DashboardData;
  loading: boolean;
  mutating: boolean;
  error: string | null;
  authError: boolean;
  daemonUnavailable: boolean;
  sseState: string;
  children: ReactNode;
  reload: () => Promise<void>;
}) {
  const nav: Array<[PageKey, string]> = [
    ['overview', 'Overview'],
    ['board', 'Board'],
    ['approvals', 'Approval Inbox'],
    ['workflow', 'Workflow'],
    ['diagnostics', 'Diagnostics']
  ];
  return (
    <div className="app-shell">
      <header className="topbar">
        <div>
          <h1>Local Symphony</h1>
          <p className="subtitle">Local control plane over the REST/SSE API.</p>
        </div>
        <div className="topbar-meta">
          <Pill tone={data.health?.ok ? 'good' : 'muted'}>{data.health?.project_id || 'project unknown'}</Pill>
          <Pill tone={sseState === 'connected' ? 'good' : sseState === 'reconnecting' ? 'warning' : 'muted'}>SSE {sseState}</Pill>
          <button type="button" onClick={() => void reload()} disabled={loading || mutating}>Refresh</button>
        </div>
      </header>
      <nav className="nav-tabs" aria-label="Dashboard pages">
        {nav.map(([page, label]) => (
          <button key={page} type="button" className={route.page === page ? 'active' : ''} onClick={() => navigate({ page })}>{label}</button>
        ))}
      </nav>
      {loading ? <Banner kind="info">Loading dashboard state from the local daemon…</Banner> : null}
      {mutating ? <Banner kind="info">Command submitted. Waiting for API confirmation before updating the UI.</Banner> : null}
      {daemonUnavailable ? <Banner kind="warning">Daemon unavailable. Start the local API with <code>symphony serve</code>, then reopen the dashboard.</Banner> : null}
      {authError ? <Banner kind="warning">Session expired or CSRF validation failed. Reopen the local dashboard through the daemon open URL or refresh the browser session.</Banner> : null}
      {error ? <Banner kind="error">{error}</Banner> : null}
      <main>{children}</main>
    </div>
  );
}

function OverviewPage({ data }: { data: DashboardData }) {
  const running = data.runs.filter((run) => activeRunStatuses.has(run.status));
  const failed = data.runs.filter((run) => run.status === 'failed' || run.status === 'completed_without_handoff');
  const pendingApprovals = data.approvals.filter((approval) => approval.status === 'pending');
  const humanReview = data.issues.filter((issue) => issue.state === 'Human Review');
  const paused = data.issues.filter((issue) => issue.dispatch_paused);
  const codexAvailable = Boolean(data.diagnostics?.codex?.available);

  return (
    <>
      <section className="metric-grid" aria-label="Overview metrics">
        <MetricCard label="Workflow" value={data.workflow?.validation?.valid ? 'Valid' : 'Invalid'} tone={data.workflow?.validation?.valid ? 'good' : 'danger'} helper={data.workflow?.workflow_path || 'WORKFLOW.md not loaded'} />
        <MetricCard label="Running runs" value={running.length} tone={running.length ? 'warning' : 'neutral'} helper="Active run attempts" />
        <MetricCard label="Pending approvals" value={pendingApprovals.length} tone={pendingApprovals.length ? 'warning' : 'neutral'} helper="Command/file/network requests" />
        <MetricCard label="Failed runs" value={failed.length} tone={failed.length ? 'danger' : 'neutral'} helper="Recent run failures" />
        <MetricCard label="Human Review" value={humanReview.length} tone={humanReview.length ? 'warning' : 'neutral'} helper="Issues awaiting operator review" />
        <MetricCard label="Paused issues" value={paused.length} tone={paused.length ? 'warning' : 'neutral'} helper="Dispatch paused by failure or operator" />
        <MetricCard label="Codex" value={codexAvailable ? 'Available' : 'Unavailable'} tone={codexAvailable ? 'good' : 'muted'} helper="Reported by diagnostics" />
      </section>
      <Section title="Recent events">
        {data.events.length === 0 ? (
          <EmptyState title="No events yet" body="Create an issue, dispatch a run, or call a tool to populate the normalized event timeline." />
        ) : (
          <EventList events={data.events.slice(-10).reverse()} />
        )}
      </Section>
      <Section title="Active work">
        {running.length === 0 ? (
          <EmptyState title="No active runs" body="Ready or Rework issues can be dispatched from the Board." action={<button type="button" onClick={() => navigate({ page: 'board' })}>Open Board</button>} />
        ) : (
          <RunTable runs={running} />
        )}
      </Section>
    </>
  );
}

function MetricCard({ label, value, helper, tone }: { label: string; value: ReactNode; helper: string; tone: 'neutral' | 'good' | 'warning' | 'danger' | 'muted' }) {
  return (
    <article className={`metric metric-${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{helper}</small>
    </article>
  );
}

function EventList({ events }: { events: RunEvent[] }) {
  return (
    <ol className="event-list">
      {events.map((event) => (
        <li key={event.id}>
          <span className="event-seq">#{event.seq}</span>
          <div>
            <strong>{event.event_type}</strong>
            <p>{event.actor_type} · {formatDate(event.created_at)} {event.redacted ? '· redacted' : ''}</p>
            {Object.keys(event.data || {}).length ? <JsonBlock value={event.data} maxHeight={120} /> : null}
          </div>
        </li>
      ))}
    </ol>
  );
}

function BoardPage({ issues, runMutation }: { issues: Issue[]; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const byState = useMemo(() => {
    const grouped = new Map<IssueState, Issue[]>();
    BOARD_STATES.forEach((state) => grouped.set(state, []));
    issues.forEach((issue) => grouped.get(issue.state)?.push(issue));
    return grouped;
  }, [issues]);

  return (
    <>
      <CreateIssueForm runMutation={runMutation} />
      {issues.length === 0 ? (
        <EmptyState title="No issues" body="Create the first local issue to start the Symphony workflow." />
      ) : null}
      <div className="board" role="list" aria-label="Issue board columns">
        {BOARD_STATES.map((state) => (
          <section className="board-column" key={state}>
            <h2>{state} <span>{byState.get(state)?.length || 0}</span></h2>
            {(byState.get(state) || []).map((issue) => (
              <IssueCard key={issue.id} issue={issue} runMutation={runMutation} />
            ))}
          </section>
        ))}
      </div>
    </>
  );
}

function CreateIssueForm({ runMutation }: { runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [acceptance, setAcceptance] = useState('');
  const [priority, setPriority] = useState(3);
  const [labels, setLabels] = useState('');

  async function submit(event: FormEvent) {
    event.preventDefault();
    const created = await runMutation(() => api.createIssue({
      title,
      description,
      acceptance_criteria: acceptance.split('\n').map((line) => line.trim()).filter(Boolean),
      priority,
      labels: labels.split(',').map((label) => label.trim()).filter(Boolean)
    }));
    if (created) {
      setTitle('');
      setDescription('');
      setAcceptance('');
      setPriority(3);
      setLabels('');
      navigate({ page: 'issue', issueRef: created.identifier });
    }
  }

  return (
    <Section title="Create issue">
      <form className="form-grid" onSubmit={(event) => void submit(event)}>
        <label>
          Title
          <input value={title} onChange={(event) => setTitle(event.target.value)} required placeholder="Implement local workflow" />
        </label>
        <label>
          Priority
          <select value={priority} onChange={(event) => setPriority(Number(event.target.value))}>
            {[1, 2, 3, 4, 5].map((value) => <option key={value} value={value}>{value}</option>)}
          </select>
        </label>
        <label className="span-2">
          Description
          <textarea value={description} onChange={(event) => setDescription(event.target.value)} required rows={3} />
        </label>
        <label className="span-2">
          Acceptance criteria, one per line
          <textarea value={acceptance} onChange={(event) => setAcceptance(event.target.value)} required rows={3} />
        </label>
        <label className="span-2">
          Labels, comma-separated
          <input value={labels} onChange={(event) => setLabels(event.target.value)} placeholder="backend, workflow" />
        </label>
        <div className="span-2 form-actions">
          <button type="submit">Create issue</button>
        </div>
      </form>
    </Section>
  );
}

function IssueCard({ issue, runMutation }: { issue: Issue; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const [target, setTarget] = useState<IssueState>('Ready');
  const [reason, setReason] = useState('');
  const [duplicateOf, setDuplicateOf] = useState('');

  async function transition() {
    const changed = await runMutation(() => api.transitionIssue(issue.identifier, { state: target, reason, duplicate_of: duplicateOf || undefined }));
    if (changed) {
      setReason('');
      setDuplicateOf('');
    }
  }

  return (
    <article className="issue-card">
      <div className="issue-card-header">
        <button type="button" className="link-button" onClick={() => navigate({ page: 'issue', issueRef: issue.identifier })}>{issue.identifier}</button>
        <StatusPill value={issue.state} />
      </div>
      <h3>{issue.title}</h3>
      <p>{issue.description}</p>
      <div className="tag-row">
        <Pill>p{issue.priority}</Pill>
        {issue.labels.map((label) => <Pill key={label} tone="muted">{label}</Pill>)}
        {issue.dispatch_paused ? <Pill tone="warning">dispatch paused</Pill> : null}
      </div>
      <div className="card-actions">
        {isDispatchable(issue) ? <button type="button" onClick={() => void runMutation(() => api.dispatchIssue(issue.identifier))}>Dispatch</button> : null}
        {issue.latest_run_id ? <button type="button" onClick={() => navigate({ page: 'run', runId: issue.latest_run_id || undefined })}>Run</button> : null}
        {issue.latest_review_packet_id || issue.state === 'Human Review' ? <button type="button" onClick={() => navigate({ page: 'review', issueRef: issue.identifier })}>Review</button> : null}
      </div>
      <details className="inline-command">
        <summary>Transition issue</summary>
        <label>
          Target state
          <select value={target} onChange={(event) => setTarget(event.target.value as IssueState)}>
            {transitionTargets.map((state) => <option key={state} value={state}>{state}</option>)}
          </select>
        </label>
        {(target === 'Blocked' || target === 'Cancelled' || target === 'Duplicate') ? (
          <label>
            Reason
            <input value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Required for this transition" />
          </label>
        ) : null}
        {target === 'Duplicate' ? (
          <label>
            Duplicate of
            <input value={duplicateOf} onChange={(event) => setDuplicateOf(event.target.value)} placeholder="Canonical issue ref, e.g. LOC-1" />
          </label>
        ) : null}
        <button type="button" onClick={() => void transition()}>Apply transition</button>
      </details>
    </article>
  );
}

function IssueDetailPage({ route, issues, runs, events, runMutation }: {
  route: RouteState;
  issues: Issue[];
  runs: RunAttempt[];
  events: RunEvent[];
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
}) {
  const [comment, setComment] = useState('');
  const [blocker, setBlocker] = useState('');
  const [pauseReason, setPauseReason] = useState('');
  const [fetchedIssue, setFetchedIssue] = useState<Issue | null>(null);
  const [missingIssueRef, setMissingIssueRef] = useState<string | null>(null);
  const [issueLoadError, setIssueLoadError] = useState<string | null>(null);
  const listedIssue = issueByIdOrRef(issues, route.issueRef);
  useEffect(() => {
    setFetchedIssue(null);
    setMissingIssueRef(null);
    setIssueLoadError(null);
    if (!route.issueRef || listedIssue) return undefined;
    let cancelled = false;
    api.issue(route.issueRef)
      .then((loaded) => {
        if (!cancelled) setFetchedIssue(loaded);
      })
      .catch((error) => {
        if (cancelled) return;
        if (error instanceof ApiError && error.status === 404) {
          setMissingIssueRef(route.issueRef || null);
          return;
        }
        setIssueLoadError(errorLabel(error));
      });
    return () => { cancelled = true; };
  }, [route.issueRef, listedIssue]);
  const issue = listedIssue || (fetchedIssue && (fetchedIssue.id === route.issueRef || fetchedIssue.identifier === route.issueRef) ? fetchedIssue : undefined);
  const issueEvents = issue ? events.filter((event) => event.issue_id === issue.id) : [];
  const runHistory = issue ? runs.filter((run) => run.issue_id === issue.id) : [];

  if (!issue) {
    if (issueLoadError) {
      return <EmptyState title="Issue failed to load" body={issueLoadError} action={<button type="button" onClick={() => navigate({ page: 'board' })}>Open Board</button>} />;
    }
    if (route.issueRef && missingIssueRef !== route.issueRef) {
      return <EmptyState title="Loading issue" body={`Fetching ${route.issueRef} from the local API.`} />;
    }
    return <EmptyState title="Issue not found" body={route.issueRef ? `No local issue matches ${route.issueRef}.` : 'Open an issue from the Board to see its facts, relations, workspace, run history, and review packets.'} action={<button type="button" onClick={() => navigate({ page: 'board' })}>Open Board</button>} />;
  }
  const selectedIssue = issue;

  async function postComment(event: FormEvent) {
    event.preventDefault();
    const result = await runMutation(() => api.commentIssue(selectedIssue.identifier, comment));
    if (result) setComment('');
  }

  async function addBlocker(event: FormEvent) {
    event.preventDefault();
    const result = await runMutation(() => api.addBlocker(selectedIssue.identifier, blocker));
    if (result) setBlocker('');
  }

  async function pauseResume(paused: boolean) {
    const reason = pauseReason || (paused ? 'operator resumed dispatch' : 'operator paused dispatch');
    const result = await runMutation(() => paused ? api.resumeDispatch(selectedIssue.identifier, reason) : api.pauseDispatch(selectedIssue.identifier, reason));
    if (result) setPauseReason('');
  }

  return (
    <>
      <Section title={`${issue.identifier} · ${issue.title}`} actions={<button type="button" onClick={() => navigate({ page: 'board' })}>Back to Board</button>}>
        <KeyValue rows={[
          ['State', <StatusPill value={issue.state} />],
          ['Priority', issue.priority],
          ['Labels', issue.labels.length ? issue.labels.join(', ') : '—'],
          ['Dispatch paused', issue.dispatch_paused ? `yes · ${issue.dispatch_pause_reason || ''}` : 'no'],
          ['Created', formatDate(issue.created_at)],
          ['Updated', formatDate(issue.updated_at)]
        ]} />
        <div className="description-block">
          <h3>Description</h3>
          <p>{issue.description || '—'}</p>
        </div>
        <div className="description-block">
          <h3>Acceptance criteria</h3>
          {issue.acceptance_criteria.length ? <ul>{issue.acceptance_criteria.map((item) => <li key={item}>{item}</li>)}</ul> : <p>—</p>}
        </div>
        <div className="card-actions">
          {isDispatchable(issue) ? <button type="button" onClick={() => void runMutation(() => api.dispatchIssue(issue.identifier))}>Dispatch eligible issue</button> : null}
          {issue.latest_run_id ? <button type="button" onClick={() => navigate({ page: 'run', runId: issue.latest_run_id || undefined })}>Open latest run</button> : null}
          {issue.latest_review_packet_id || issue.state === 'Human Review' ? <button type="button" onClick={() => navigate({ page: 'review', issueRef: issue.identifier })}>Open review packet</button> : null}
        </div>
      </Section>

      <IssueEditForm issue={issue} runMutation={runMutation} />

      <Section title="Dispatch controls">
        <div className="form-grid compact">
          <label className="span-2">
            Reason
            <input value={pauseReason} onChange={(event) => setPauseReason(event.target.value)} placeholder="Required by API for pause/resume; default text will be used if omitted" />
          </label>
          <div className="span-2 form-actions">
            <button type="button" onClick={() => void pauseResume(issue.dispatch_paused)} disabled={Boolean(issue.active_run_id)}>
              {issue.dispatch_paused ? 'Dispatch resume issue' : 'Dispatch pause issue'}
            </button>
          </div>
        </div>
      </Section>

      <Section title="Relations">
        <div className="relation-grid">
          <RelationList title="Blocked by" refs={issue.blocked_by} remove={(ref) => runMutation(() => api.removeBlocker(issue.identifier, ref.identifier))} />
          <RelationList title="Blocks" refs={issue.blocks} />
          <RelationList title="Duplicates" refs={issue.duplicates} />
          <RelationList title="Follow-ups" refs={issue.followups} />
        </div>
        <KeyValue rows={[
          ['Duplicate of', issue.duplicate_of ? <span>{refLabel(issue.duplicate_of)} <button type="button" onClick={() => void runMutation(() => api.removeDuplicate(issue.identifier, issue.duplicate_of?.identifier || issue.duplicate_of?.id || ''))}>Remove duplicate relation</button></span> : '—'],
          ['Follow-up of', refLabel(issue.followup_of)]
        ]} />
        <form className="inline-form" onSubmit={(event) => void addBlocker(event)}>
          <label>
            Add blocker
            <input value={blocker} onChange={(event) => setBlocker(event.target.value)} placeholder="Blocking issue ref" />
          </label>
          <button type="submit">Add blocker</button>
        </form>
      </Section>

      <Section title="Workspace and Git">
        <KeyValue rows={[
          ['Workspace path', issue.workspace_path || issue.workspace?.path || '—'],
          ['Branch', issue.branch_name || issue.git?.branch_name || '—'],
          ['Base ref', issue.base_ref || issue.git?.base_ref || '—'],
          ['Base SHA', issue.base_sha || issue.git?.base_sha || '—']
        ]} />
      </Section>

      <Section title="Run history">
        {runHistory.length ? <RunTable runs={runHistory} /> : <EmptyState title="No run history" body="Dispatch a Ready or Rework issue to create the first run attempt." />}
      </Section>

      <Section title="Review packets">
        {issue.latest_review_packet ? (
          <div className="summary-card">
            <KeyValue rows={[
              ['Packet', `#${issue.latest_review_packet.packet_no}`],
              ['Status', <StatusPill value={issue.latest_review_packet.status} />],
              ['Run', issue.latest_review_packet.run_id],
              ['Created', formatDate(issue.latest_review_packet.created_at)],
              ['Failure', issue.latest_review_packet.failure_code || '—']
            ]} />
            <button type="button" onClick={() => navigate({ page: 'review', issueRef: issue.identifier })}>Open Review Packet</button>
          </div>
        ) : <EmptyState title="No review packet" body="A review packet appears after a completed handoff run reaches Human Review." />}
      </Section>

      <Section title="Comments and issue events">
        <form className="inline-form" onSubmit={(event) => void postComment(event)}>
          <label>
            Comment
            <input value={comment} onChange={(event) => setComment(event.target.value)} placeholder="Leave an operator comment" />
          </label>
          <button type="submit">Add comment</button>
        </form>
        {issueEvents.length ? <EventList events={issueEvents.slice().reverse()} /> : <EmptyState title="No issue events" body="Comments and state changes are displayed from normalized issue events." />}
      </Section>
    </>
  );
}

function IssueEditForm({ issue, runMutation }: { issue: Issue; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const [title, setTitle] = useState(issue.title);
  const [description, setDescription] = useState(issue.description);
  const [acceptance, setAcceptance] = useState(issue.acceptance_criteria.join('\n'));
  const [priority, setPriority] = useState(issue.priority);
  const [labels, setLabels] = useState(issue.labels.join(', '));

  useEffect(() => {
    setTitle(issue.title);
    setDescription(issue.description);
    setAcceptance(issue.acceptance_criteria.join('\n'));
    setPriority(issue.priority);
    setLabels(issue.labels.join(', '));
  }, [issue.id, issue.title, issue.description, issue.acceptance_criteria, issue.priority, issue.labels]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    await runMutation(() => api.updateIssue(issue.identifier, {
      title,
      description,
      acceptance_criteria: acceptance.split('\n').map((line) => line.trim()).filter(Boolean),
      priority,
      labels: labels.split(',').map((label) => label.trim()).filter(Boolean)
    }));
  }

  return (
    <Section title="Update issue">
      <form className="form-grid" onSubmit={(event) => void submit(event)}>
        <label>
          Title
          <input value={title} onChange={(event) => setTitle(event.target.value)} required />
        </label>
        <label>
          Priority
          <select value={priority} onChange={(event) => setPriority(Number(event.target.value))}>
            {[1, 2, 3, 4, 5].map((value) => <option key={value} value={value}>{value}</option>)}
          </select>
        </label>
        <label className="span-2">
          Description
          <textarea value={description} onChange={(event) => setDescription(event.target.value)} required rows={4} />
        </label>
        <label className="span-2">
          Acceptance criteria, one per line
          <textarea value={acceptance} onChange={(event) => setAcceptance(event.target.value)} required rows={4} />
        </label>
        <label className="span-2">
          Labels, comma-separated
          <input value={labels} onChange={(event) => setLabels(event.target.value)} />
        </label>
        <div className="span-2 form-actions">
          <button type="submit">Update issue</button>
        </div>
      </form>
    </Section>
  );
}

function RelationList({ title, refs, remove }: { title: string; refs: IssueRefSummary[]; remove?: (ref: IssueRefSummary) => Promise<unknown> }) {
  return (
    <div className="relation-list">
      <h3>{title}</h3>
      {refs.length === 0 ? <p>—</p> : refs.map((ref) => (
        <div key={ref.id}>
          <button type="button" className="link-button" onClick={() => navigate({ page: 'issue', issueRef: ref.identifier })}>{ref.identifier}</button>
          <span>{ref.title}</span>
          <StatusPill value={ref.state} />
          {remove ? <button type="button" onClick={() => void remove(ref)}>Remove</button> : null}
        </div>
      ))}
    </div>
  );
}

function RunTable({ runs }: { runs: RunAttempt[] }) {
  return (
    <table className="data-table">
      <thead>
        <tr>
          <th>Run</th>
          <th>Issue</th>
          <th>Status</th>
          <th>Attempt</th>
          <th>Failure</th>
          <th>Updated</th>
        </tr>
      </thead>
      <tbody>
        {runs.map((run) => (
          <tr key={run.id}>
            <td><button type="button" className="link-button" onClick={() => navigate({ page: 'run', runId: run.id })}>{run.id}</button></td>
            <td>{run.issue_identifier || run.issue_id}</td>
            <td><StatusPill value={run.status} /></td>
            <td>{run.attempt_no}</td>
            <td>{run.failure_code || '—'}</td>
            <td>{formatDate(run.updated_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function RunDetailPage({ route, runs, issues, events, runMutation }: {
  route: RouteState;
  runs: RunAttempt[];
  issues: Issue[];
  events: RunEvent[];
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
}) {
  const [reason, setReason] = useState('operator cancelled run');
  const [fetchedRun, setFetchedRun] = useState<RunAttempt | null>(null);
  const [missingRunId, setMissingRunId] = useState<string | null>(null);
  const [runLoadError, setRunLoadError] = useState<string | null>(null);
  const listedRun = runs.find((item) => item.id === route.runId);
  useEffect(() => {
    setFetchedRun(null);
    setMissingRunId(null);
    setRunLoadError(null);
    if (!route.runId || listedRun) return undefined;
    let cancelled = false;
    api.run(route.runId)
      .then((loaded) => {
        if (!cancelled) setFetchedRun(loaded);
      })
      .catch((error) => {
        if (cancelled) return;
        if (error instanceof ApiError && error.status === 404) {
          setMissingRunId(route.runId || null);
          return;
        }
        setRunLoadError(errorLabel(error));
      });
    return () => { cancelled = true; };
  }, [route.runId, listedRun]);
  const run = listedRun || (fetchedRun && fetchedRun.id === route.runId ? fetchedRun : undefined);
  const issue = issueByIdOrRef(issues, run?.issue_id);
  const runEvents = events.filter((event) => event.run_id === run?.id);

  if (!run) {
    if (runLoadError) {
      return <EmptyState title="Run failed to load" body={runLoadError} action={<button type="button" onClick={() => navigate({ page: 'board' })}>Open Board</button>} />;
    }
    if (route.runId && missingRunId !== route.runId) {
      return <EmptyState title="Loading run" body={`Fetching ${route.runId} from the local API.`} />;
    }
    return <EmptyState title="Run not found" body={route.runId ? `No local run matches ${route.runId}.` : 'Dispatch an issue to create a run, then open it from the Board or Issue Detail.'} action={<button type="button" onClick={() => navigate({ page: 'board' })}>Open Board</button>} />;
  }

  const canCancel = activeRunStatuses.has(run.status);
  return (
    <>
      <Section title={`Run ${run.id}`} actions={issue ? <button type="button" onClick={() => navigate({ page: 'issue', issueRef: issue.identifier })}>Open issue</button> : null}>
        <KeyValue rows={[
          ['Issue', issue ? `${issue.identifier} · ${issue.title}` : run.issue_identifier || run.issue_id],
          ['Status', <StatusPill value={run.status} />],
          ['Attempt', run.attempt_no],
          ['Runner', run.runner_kind],
          ['Dispatch reason', run.dispatch_reason],
          ['Source state', run.source_issue_state],
          ['Branch', run.branch_name || '—'],
          ['Base ref', run.base_ref || '—'],
          ['Started', formatDate(run.started_at)],
          ['Ended', formatDate(run.ended_at)],
          ['Failure code', run.failure_code || '—'],
          ['Failure message', run.failure_message || '—']
        ]} />
        {canCancel ? (
          <div className="inline-form">
            <label>
              Cancel reason
              <input value={reason} onChange={(event) => setReason(event.target.value)} />
            </label>
            <button type="button" onClick={() => void runMutation(() => api.cancelRun(run.id, reason))}>Cancel run</button>
          </div>
        ) : null}
      </Section>
      <Section title="Normalized timeline">
        {runEvents.length === 0 ? (
          <EmptyState title="No run events" body="Timeline events are replayed from the REST events API and SSE stream. Raw Codex protocol logs are not shown." />
        ) : <EventList events={runEvents} />}
      </Section>
    </>
  );
}

function ApprovalInboxPage({ approvals, runMutation }: { approvals: Approval[]; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const pending = approvals.filter((approval) => approval.status === 'pending');
  const resolved = approvals.filter((approval) => approval.status !== 'pending');

  return (
    <>
      <Section title="Pending approvals">
        {pending.length === 0 ? (
          <EmptyState title="No pending approvals" body="Command, file, and network approvals requested by active runs will appear here." />
        ) : pending.map((approval) => <ApprovalCard key={approval.id} approval={approval} runMutation={runMutation} />)}
      </Section>
      <Section title="Resolved / expired approvals">
        {resolved.length === 0 ? (
          <EmptyState title="No resolved approvals" body="Expired, denied, approved, and cancelled approvals are listed after they leave pending state." />
        ) : (
          <table className="data-table">
            <thead><tr><th>ID</th><th>Kind</th><th>Status</th><th>Run</th><th>Created</th></tr></thead>
            <tbody>{resolved.map((approval) => (
              <tr key={approval.id}>
                <td>{approval.id}</td>
                <td>{approval.kind}</td>
                <td><StatusPill value={approval.status} /></td>
                <td><button type="button" className="link-button" onClick={() => navigate({ page: 'run', runId: approval.run_id })}>{approval.run_id}</button></td>
                <td>{formatDate(approval.created_at)}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </Section>
    </>
  );
}

function ApprovalCard({ approval, runMutation }: { approval: Approval; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const [reason, setReason] = useState('operator decision');
  const actionSummary = approval.action_summary || `${approval.kind} approval ${approval.id}`;

  return (
    <article className="approval-card">
      <div className="issue-card-header">
        <strong>{actionSummary}</strong>
        <StatusPill value={approval.kind} />
      </div>
      <KeyValue rows={[
        ['ID', approval.id],
        ['Run', <button type="button" className="link-button" onClick={() => navigate({ page: 'run', runId: approval.run_id })}>{approval.run_id}</button>],
        ['Risk level', approval.risk_level || '—'],
        ['Policy match', approval.policy_match || '—'],
        ['Created', formatDate(approval.created_at)]
      ]} />
      <label>
        Decision reason
        <input value={reason} onChange={(event) => setReason(event.target.value)} />
      </label>
      <div className="decision-grid">
        {approvalDecisions.map((decision) => (
          <button key={decision.value} type="button" onClick={() => void runMutation(() => api.decideApproval(approval.id, decision.value, reason))}>
            {decision.label}
            <small>{decision.helper}</small>
          </button>
        ))}
      </div>
    </article>
  );
}

function ReviewPacketPage({ route, issues, runMutation }: { route: RouteState; issues: Issue[]; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const defaultIssue = route.issueRef || issues.find((issue) => issue.state === 'Human Review')?.identifier || issues[0]?.identifier || '';
  const [issueRef, setIssueRef] = useState(defaultIssue);
  const [review, setReview] = useState<ReviewPacketSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reason, setReason] = useState('operator review decision');
  const [artifactContent, setArtifactContent] = useState<Record<string, { title: string; text: string; refused?: boolean }>>({});

  useEffect(() => {
    if (route.issueRef) setIssueRef(route.issueRef);
  }, [route.issueRef]);

  const loadReview = useCallback(async (ref: string) => {
    if (!ref) return;
    setLoading(true);
    setError(null);
    setArtifactContent({});
    try {
      const result = await api.review(ref);
      setReview(result);
    } catch (err) {
      setReview(null);
      setError(errorLabel(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (issueRef) void loadReview(issueRef);
  }, [issueRef, loadReview]);

  const artifacts: ReviewPacketArtifact[] = review?.artifacts || review?.files || [];

  async function loadArtifact(artifact: ReviewPacketArtifact) {
    if (!artifact.content_url) {
      setArtifactContent((current) => ({ ...current, [artifact.artifact_id]: { title: artifact.kind, text: 'Content is not exposed by the Review/Artifact API. Metadata only.', refused: true } }));
      return;
    }
    setArtifactContent((current) => ({ ...current, [artifact.artifact_id]: { title: artifact.kind, text: 'Loading…' } }));
    try {
      const text = await fetchArtifactContent(artifact.content_url);
      setArtifactContent((current) => ({ ...current, [artifact.artifact_id]: { title: artifact.kind, text } }));
    } catch (err) {
      const refused = err instanceof ApiError && (err.code === 'raw_log_access_not_supported' || err.status === 403);
      setArtifactContent((current) => ({ ...current, [artifact.artifact_id]: { title: artifact.kind, text: errorLabel(err), refused } }));
    }
  }

  async function reviewAction(kind: 'rework' | 'done') {
    const result = await runMutation(() => kind === 'rework' ? api.sendToRework(issueRef, reason) : api.markDone(issueRef, reason));
    if (result) navigate({ page: 'issue', issueRef: result.identifier });
  }

  return (
    <>
      <Section title="Review Packet" actions={<button type="button" onClick={() => issueRef && void loadReview(issueRef)} disabled={!issueRef || loading}>Reload review</button>}>
        <div className="inline-form">
          <label>
            Issue ref
            <input value={issueRef} onChange={(event) => setIssueRef(event.target.value)} placeholder="LOC-1" list="review-issues" />
            <datalist id="review-issues">{issues.map((issue) => <option key={issue.id} value={issue.identifier}>{issue.title}</option>)}</datalist>
          </label>
          <button type="button" onClick={() => void loadReview(issueRef)} disabled={!issueRef || loading}>Load latest packet</button>
        </div>
        {loading ? <Banner kind="info">Loading review packet summary through the Review API…</Banner> : null}
        {error ? <Banner kind="error">{error}</Banner> : null}
        {!review && !loading ? <EmptyState title="No review packet loaded" body="Load an issue in Human Review to inspect summary metadata and allowed artifacts." /> : null}
        {review ? (
          <div className="summary-card">
            <KeyValue rows={[
              ['Packet ID', review.id],
              ['Run', <button type="button" className="link-button" onClick={() => navigate({ page: 'run', runId: review.run_id })}>{review.run_id}</button>],
              ['Packet number', review.packet_no],
              ['Status', <StatusPill value={review.status} />],
              ['Root path', review.root_path],
              ['Failure', review.failure_code || review.failure_message || '—'],
              ['Created', formatDate(review.created_at)]
            ]} />
          </div>
        ) : null}
      </Section>

      {review ? (
        <Section title="Artifacts and redaction boundary">
          {artifacts.length === 0 ? <EmptyState title="No artifact metadata" body="The Review API returned a packet summary with no exposed artifact entries." /> : null}
          <div className="artifact-list">
            {artifacts.map((artifact) => (
              <article key={artifact.artifact_id} className="artifact-card">
                <div className="issue-card-header">
                  <strong>{artifact.kind}</strong>
                  {artifact.content_url ? <Pill tone="good">content API</Pill> : <Pill tone="warning">metadata only</Pill>}
                </div>
                <KeyValue rows={[
                  ['Artifact ID', artifact.artifact_id],
                  ['Path', artifact.path],
                  ['Redacted', artifact.redacted ? 'yes' : 'no']
                ]} />
                <button type="button" onClick={() => void loadArtifact(artifact)}>{artifact.content_url ? 'Fetch allowed content' : 'Show refusal state'}</button>
                {artifactContent[artifact.artifact_id] ? (
                  <div className={artifactContent[artifact.artifact_id].refused ? 'refusal-box' : ''}>
                    <h4>{artifactContent[artifact.artifact_id].title}</h4>
                    <JsonBlock value={artifactContent[artifact.artifact_id].text} maxHeight={360} />
                  </div>
                ) : null}
              </article>
            ))}
          </div>
        </Section>
      ) : null}

      {review ? (
        <Section title="Human Review actions">
          <label>
            Reason
            <textarea value={reason} onChange={(event) => setReason(event.target.value)} rows={3} />
          </label>
          <div className="card-actions">
            <button type="button" onClick={() => void reviewAction('rework')}>Send to Rework</button>
            <button type="button" onClick={() => void reviewAction('done')}>Mark Done</button>
          </div>
        </Section>
      ) : null}
    </>
  );
}

function WorkflowPage({ workflow, runMutation }: { workflow: WorkflowResponse | null; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const [validation, setValidation] = useState<WorkflowValidateResponse | null>(null);
  const [preview, setPreview] = useState<WorkflowRenderPreviewResponse | null>(null);
  const [localError, setLocalError] = useState<string | null>(null);

  async function validate() {
    setLocalError(null);
    try {
      setValidation(await api.validateWorkflow());
    } catch (err) {
      setLocalError(errorLabel(err));
    }
  }

  async function renderPreview() {
    setLocalError(null);
    try {
      setPreview(await api.renderWorkflowPreview());
    } catch (err) {
      setLocalError(errorLabel(err));
    }
  }

  return (
    <>
      <Section title="Workflow status">
        {workflow ? (
          <>
            <KeyValue rows={[
              ['Path', workflow.workflow_path],
              ['Valid', workflow.validation?.valid ? <Pill tone="good">valid</Pill> : <Pill tone="danger">invalid</Pill>],
              ['Warnings', workflow.validation?.warnings?.length || 0],
              ['Errors', workflow.validation?.errors?.length || 0]
            ]} />
            {workflow.validation?.warnings?.length ? <Banner kind="warning">{workflow.validation.warnings.join('; ')}</Banner> : null}
            {workflow.validation?.errors?.length ? <Banner kind="error">{workflow.validation.errors.join('; ')}</Banner> : null}
          </>
        ) : <EmptyState title="Workflow not loaded" body="The workflow page reads only through the REST API. Check daemon status if this remains empty." />}
      </Section>
      <Section title="Workflow actions">
        <div className="card-actions">
          <button type="button" onClick={() => void validate()}>Workflow validate</button>
          <button type="button" onClick={() => void renderPreview()}>Render preview</button>
          <button type="button" onClick={() => void runMutation(() => api.reloadWorkflow())}>Workflow reload</button>
        </div>
        {localError ? <Banner kind="error">{localError}</Banner> : null}
        {validation ? (
          <div className="summary-card">
            <h3>Current filesystem validation</h3>
            <p>This dry-run validation does not replace effective config or dispatch work.</p>
            <JsonBlock value={validation} maxHeight={320} />
          </div>
        ) : null}
        {preview ? (
          <div className="summary-card">
            <h3>Redacted render preview</h3>
            <JsonBlock value={{ rendered_prompt_preview: preview.rendered_prompt_preview, prompt_metadata: preview.prompt_metadata || {} }} maxHeight={360} />
            <KeyValue rows={[
              ['Source', preview.source],
              ['Redactions', preview.redactions_applied.join(', ') || '—'],
              ['Preview valid', preview.validation.valid ? 'yes' : 'no']
            ]} />
          </div>
        ) : null}
      </Section>
      <Section title="Last loaded config">
        {workflow?.config ? <JsonBlock value={workflow.config} maxHeight={420} /> : <EmptyState title="No config object" body="The workflow API returned validation without an effective config payload." />}
      </Section>
    </>
  );
}

function DiagnosticsPage({ diagnostics, runMutation }: { diagnostics: Diagnostics | null; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const [exportResult, setExportResult] = useState<{ artifact_id: string; path: string } | null>(null);

  async function exportDiagnostics() {
    const result = await runMutation(() => api.exportDiagnostics());
    if (result) setExportResult(result);
  }

  if (!diagnostics) {
    return <EmptyState title="No diagnostics" body="Diagnostics are loaded from /api/v1/diagnostics. If the daemon is unavailable, start symphony serve." />;
  }

  return (
    <>
      <Section title="Diagnostics" actions={<button type="button" onClick={() => void exportDiagnostics()}>Diagnostics export</button>}>
        <KeyValue rows={[
          ['Project', diagnostics.project_id],
          ['Generated', formatDate(diagnostics.generated_at)],
          ['Repo root', diagnostics.repo_root],
          ['Redacted export only', diagnostics.redacted ? 'yes' : 'no'],
          ['Warnings', diagnostics.warnings.length]
        ]} />
        {exportResult ? <Banner kind="success">Redacted diagnostics exported as artifact {exportResult.artifact_id} at {exportResult.path}</Banner> : null}
      </Section>
      <section className="diagnostics-grid">
        <DiagnosticCard title="Daemon" value={diagnostics.daemon} />
        <DiagnosticCard title="Project paths / DB" value={diagnostics.database} />
        <DiagnosticCard title="Codex" value={diagnostics.codex} />
        <DiagnosticCard title="Git" value={diagnostics.git} />
        <DiagnosticCard title="Workflow" value={diagnostics.workflow} />
        <DiagnosticCard title="Redaction" value={diagnostics.redaction} />
        <DiagnosticCard title="Failure summary" value={diagnostics.failure_summary} />
        <DiagnosticCard title="Pause summary" value={diagnostics.pause_summary} />
      </section>
      <Section title="Checks and remediation">
        <JsonBlock value={{ checks: diagnostics.checks, inconsistent_issues: diagnostics.inconsistent_issues, remediation: diagnostics.remediation }} maxHeight={420} />
      </Section>
    </>
  );
}

function DiagnosticCard({ title, value }: { title: string; value: unknown }) {
  return (
    <article className="diagnostic-card">
      <h3>{title}</h3>
      <JsonBlock value={value} maxHeight={260} />
    </article>
  );
}

export function App() {
  const route = useRoute();
  const { data, loading, mutating, error, authError, daemonUnavailable, sseState, reload, runMutation } = useDashboardData();

  let content: ReactNode;
  switch (route.page) {
    case 'board':
      content = <BoardPage issues={data.issues} runMutation={runMutation} />;
      break;
    case 'issue':
      content = <IssueDetailPage route={route} issues={data.issues} runs={data.runs} events={data.events} runMutation={runMutation} />;
      break;
    case 'run':
      content = <RunDetailPage route={route} runs={data.runs} issues={data.issues} events={data.events} runMutation={runMutation} />;
      break;
    case 'approvals':
      content = <ApprovalInboxPage approvals={data.approvals} runMutation={runMutation} />;
      break;
    case 'review':
      content = <ReviewPacketPage route={route} issues={data.issues} runMutation={runMutation} />;
      break;
    case 'workflow':
      content = <WorkflowPage workflow={data.workflow} runMutation={runMutation} />;
      break;
    case 'diagnostics':
      content = <DiagnosticsPage diagnostics={data.diagnostics} runMutation={runMutation} />;
      break;
    default:
      content = <OverviewPage data={data} />;
  }

  return (
    <AppShell
      route={route}
      data={data}
      loading={loading}
      mutating={mutating}
      error={error}
      authError={authError}
      daemonUnavailable={daemonUnavailable}
      sseState={sseState}
      reload={reload}
    >
      {content}
    </AppShell>
  );
}
