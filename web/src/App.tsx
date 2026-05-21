import { FormEvent, ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError, api, errorLabel, fetchArtifactContent, isAuthError } from './api';
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
  RunSummary,
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
const dispatchPauseDefaultReason = 'operator paused dispatch';
const dispatchResumeDefaultReason = 'operator resumed dispatch';

type PageKey = 'overview' | 'board' | 'issue' | 'run' | 'approvals' | 'review' | 'workflow' | 'diagnostics';

interface RouteState {
  page: PageKey;
  issueRef?: string;
  runId?: string;
}

type QueueGroupKey = 'needs_action' | 'ready_to_run' | 'watching' | 'all';

interface QueueGroup {
  key: QueueGroupKey;
  label: string;
  helper: string;
  issues: Issue[];
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

function decodeRouteSegment(segment: string): string | null {
  try {
    return decodeURIComponent(segment);
  } catch {
    return null;
  }
}

function parseRoute(): RouteState {
  const raw = window.location.hash.replace(/^#\/?/, '');
  const [path] = raw.split('?');
  const segments: string[] = [];
  for (const segment of path.split('/')) {
    const decoded = decodeRouteSegment(segment);
    if (decoded === null) return { page: 'overview' };
    segments.push(decoded);
  }
  const [page = 'overview', id] = segments;
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

const openTokenParamNames = ['open_token', 'openToken'];

function openTokenFromParams(params: URLSearchParams): string | null {
  for (const name of openTokenParamNames) {
    const token = params.get(name);
    if (token) return token;
  }
  return null;
}

function getOpenTokenFromUrl(): string | null {
  const queryToken = openTokenFromParams(new URLSearchParams(window.location.search));
  if (queryToken) return queryToken;

  const hash = window.location.hash.replace(/^#/, '');
  const queryStart = hash.indexOf('?');
  const hashQuery = queryStart >= 0 ? hash.slice(queryStart + 1) : hash;
  return openTokenFromParams(new URLSearchParams(hashQuery));
}

function cleanOpenTokenFromUrl(): void {
  const url = new URL(window.location.href);
  for (const name of openTokenParamNames) url.searchParams.delete(name);

  const hash = url.hash.replace(/^#/, '');
  if (hash && (hash.includes('?') || !hash.startsWith('/'))) {
    const queryStart = hash.indexOf('?');
    const hashPath = queryStart >= 0 ? hash.slice(0, queryStart) : '';
    const hashQuery = queryStart >= 0 ? hash.slice(queryStart + 1) : hash;
    const hashParams = new URLSearchParams(hashQuery);
    for (const name of openTokenParamNames) hashParams.delete(name);
    const nextHashQuery = hashParams.toString();
    if (queryStart >= 0) {
      url.hash = nextHashQuery ? `${hashPath}?${nextHashQuery}` : hashPath;
    } else {
      url.hash = nextHashQuery ? nextHashQuery : '';
    }
  }

  window.history.replaceState(window.history.state, '', url);
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

function canOpenReviewPacket(issue: Issue): boolean {
  return Boolean(issue.latest_review_packet_id) || Boolean(issue.latest_review_packet?.id);
}

function canPerformReviewAction(issue: Issue): boolean {
  return issue.state === 'Human Review' && issue.latest_review_packet?.status === 'generated';
}

function requiresTransitionReason(state: IssueState): boolean {
  return state === 'Blocked' || state === 'Cancelled' || state === 'Duplicate';
}

function transitionValidationError(target: IssueState, reason: string): string | null {
  if (requiresTransitionReason(target) && !reason.trim()) {
    return 'Reason is required for Blocked, Cancelled, and Duplicate transitions.';
  }
  return null;
}

function dispatchPauseResumeReason(input: string, isResuming: boolean): string {
  const manualReason = input.trim();
  if (manualReason) return manualReason;
  return isResuming ? dispatchResumeDefaultReason : dispatchPauseDefaultReason;
}

function compareIssuePriority(a: Issue, b: Issue): number {
  return a.priority - b.priority || a.sequence_no - b.sequence_no || a.identifier.localeCompare(b.identifier);
}

function eventKey(event: RunEvent): string {
  return event.seq > 0 ? `seq:${event.seq}` : `id:${event.id}`;
}

function eventMaxSeq(events: RunEvent[]): number {
  return events.reduce((max, event) => Math.max(max, event.seq || 0), 0);
}

function mergeEvents(existing: RunEvent[], incoming: RunEvent[]): RunEvent[] {
  if (incoming.length === 0) return existing;
  const byKey = new Map(existing.map((event) => [eventKey(event), event]));
  for (const event of incoming) byKey.set(eventKey(event), event);
  return Array.from(byKey.values()).sort((a, b) => a.seq - b.seq || a.created_at.localeCompare(b.created_at));
}

const runEventPageSize = 200;

async function loadRunEvents(runId: string): Promise<RunEvent[]> {
  const events: RunEvent[] = [];
  let afterSeq = 0;
  for (;;) {
    const loaded = await api.runEvents(runId, afterSeq);
    events.push(...loaded);
    const nextAfterSeq = Math.max(afterSeq, eventMaxSeq(loaded));
    if (loaded.length < runEventPageSize || afterSeq === nextAfterSeq) break;
    afterSeq = nextAfterSeq;
  }
  return events;
}

function eventFromSseMessage(message: MessageEvent): RunEvent | null {
  if (!message.data) return null;
  try {
    const payload = JSON.parse(message.data) as unknown;
    if (payload && typeof payload === 'object' && 'seq' in payload && 'event_type' in payload) {
      return payload as RunEvent;
    }
  } catch {
    return null;
  }
  return null;
}

const summaryRefreshEvents = new Set([
  'issue.created',
  'issue.updated',
  'issue.transitioned',
  'issue.state_changed',
  'issue.dispatch_paused',
  'issue.dispatch_resumed',
  'issue.completed',
  'run.claimed',
  'run.failed',
  'run.cancelled',
  'review.packet_generated',
  'review.sent_to_rework',
  'review.marked_done',
  'approval.requested',
  'approval.decided'
]);

function shouldRefreshSummary(event: RunEvent): boolean {
  return summaryRefreshEvents.has(event.event_type);
}

function runsForIssue(issue: Issue, runs: RunAttempt[]): RunAttempt[] {
  return runs
    .filter((run) => run.issue_id === issue.id)
    .sort((a, b) => new Date(b.updated_at || b.created_at).getTime() - new Date(a.updated_at || a.created_at).getTime());
}

function latestRunForIssue(issue: Issue, runs: RunAttempt[]): RunAttempt | RunSummary | null {
  return runsForIssue(issue, runs)[0] || issue.latest_run || null;
}

function hasActiveRun(issue: Issue, runs: RunAttempt[]): boolean {
  return Boolean(issue.active_run_id || runsForIssue(issue, runs).some((run) => activeRunStatuses.has(run.status)));
}

function pendingApprovalsForIssue(issue: Issue, approvals: Approval[]): Approval[] {
  return approvals.filter((approval) => approval.issue_id === issue.id && approval.status === 'pending');
}

function isNeedsAction(issue: Issue, runs: RunAttempt[], approvals: Approval[]): boolean {
  const latestRun = latestRunForIssue(issue, runs);
  return issue.state === 'Human Review'
    || issue.state === 'Blocked'
    || issue.dispatch_paused
    || latestRun?.status === 'failed'
    || latestRun?.status === 'completed_without_handoff'
    || pendingApprovalsForIssue(issue, approvals).length > 0;
}

function issueSignals(issue: Issue, runs: RunAttempt[], approvals: Approval[]): string[] {
  const latestRun = latestRunForIssue(issue, runs);
  const signals: string[] = [];
  if (issue.state === 'Human Review') signals.push('review');
  if (issue.state === 'Blocked') signals.push('blocked');
  if (issue.dispatch_paused) signals.push('paused');
  if (latestRun?.status === 'failed') signals.push('failed run');
  if (latestRun?.status === 'completed_without_handoff') signals.push('no handoff');
  if (pendingApprovalsForIssue(issue, approvals).length) signals.push('approval');
  if (hasActiveRun(issue, runs)) signals.push('active run');
  return signals;
}

function buildQueueGroups(data: DashboardData): QueueGroup[] {
  const sorted = data.issues.slice().sort(compareIssuePriority);
  const needsAction = sorted.filter((issue) => isNeedsAction(issue, data.runs, data.approvals));
  const needsActionIds = new Set(needsAction.map((issue) => issue.id));
  const readyToRun = sorted.filter((issue) => !needsActionIds.has(issue.id) && isDispatchable(issue));
  const readyIds = new Set(readyToRun.map((issue) => issue.id));
  const watching = sorted.filter((issue) => !needsActionIds.has(issue.id) && !readyIds.has(issue.id) && (issue.state === 'Working' || hasActiveRun(issue, data.runs)));

  return [
    { key: 'needs_action', label: 'Needs action', helper: 'Human review, blocked, paused, failed, or approval pending.', issues: needsAction },
    { key: 'ready_to_run', label: 'Ready to run', helper: 'Ready or Rework issues eligible for dispatch.', issues: readyToRun },
    { key: 'watching', label: 'Watching', helper: 'Active work and run attempts to monitor.', issues: watching },
    { key: 'all', label: 'All issues', helper: 'Complete issue index for quick selection.', issues: sorted }
  ];
}

function pickDefaultIssue(data: DashboardData): Issue | undefined {
  for (const group of buildQueueGroups(data)) {
    if (group.issues.length) return group.issues[0];
  }
  return undefined;
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
  const [authenticated, setAuthenticated] = useState(false);
  const [authError, setAuthError] = useState(false);
  const [authMessage, setAuthMessage] = useState<string | null>(null);
  const [authResetKey, setAuthResetKey] = useState(0);
  const [daemonUnavailable, setDaemonUnavailable] = useState(false);
  const [sseState, setSseState] = useState<'connecting' | 'connected' | 'reconnecting' | 'disabled'>('connecting');
  const maxSeqRef = useRef(0);
  const authEpochRef = useRef(0);
  const authenticatedRef = useRef(false);
  const exchangePromiseRef = useRef<Promise<void> | null>(null);
  const refreshTimerRef = useRef<number | null>(null);

  const markUnauthenticated = useCallback(() => {
    authEpochRef.current += 1;
    authenticatedRef.current = false;
    setAuthenticated(false);
    setAuthError(true);
    setAuthMessage('Dashboard is not authenticated. Reopen the local dashboard through the daemon open URL.');
    setAuthResetKey((value) => value + 1);
    setData(emptyData);
    maxSeqRef.current = 0;
  }, []);

  const mergeEventBatch = useCallback((incoming: RunEvent[]) => {
    if (incoming.length === 0) return;
    setData((current) => {
      const events = mergeEvents(current.events, incoming);
      maxSeqRef.current = eventMaxSeq(events);
      return { ...current, events };
    });
  }, []);

  const loadIncrementalEvents = useCallback(async () => {
    const requestAuthEpoch = authEpochRef.current;
    try {
      const events = await api.events(maxSeqRef.current);
      if (authEpochRef.current !== requestAuthEpoch || !authenticatedRef.current) return;
      mergeEventBatch(events);
    } catch (err) {
      if (isAuthError(err)) {
        markUnauthenticated();
      }
    }
  }, [markUnauthenticated, mergeEventBatch]);

  const bootstrapSession = useCallback(async (): Promise<boolean> => {
    const openToken = getOpenTokenFromUrl();
    if (openToken && !exchangePromiseRef.current) {
      cleanOpenTokenFromUrl();
      exchangePromiseRef.current = api.exchangeOpenToken(openToken)
        .then(() => undefined)
        .finally(() => { exchangePromiseRef.current = null; });
    }
    if (exchangePromiseRef.current) {
      try {
        await exchangePromiseRef.current;
      } catch (error) {
        const session = await api.session().catch(() => null);
        if (!session?.authenticated) throw error;
      }
    }

    const session = await api.session();
    if (!session.authenticated) {
      markUnauthenticated();
      return false;
    }
    authenticatedRef.current = true;
    setAuthMessage(null);
    return true;
  }, [markUnauthenticated]);

  const loadAll = useCallback(async () => {
    const requestAuthEpoch = authEpochRef.current;
    setError(null);
    setAuthError(false);
    setAuthMessage(null);
    try {
      const authenticated = await bootstrapSession();
      if (!authenticated) return;
      setDaemonUnavailable(false);
      const loadIssues = async () => {
        const issues: Issue[] = [];
        let cursor: string | null | undefined;
        for (;;) {
          const query = new URLSearchParams();
          query.set('limit', '200');
          if (cursor) query.set('cursor', cursor);
          const page = await api.issues(`?${query.toString()}`);
          issues.push(...(page.items || []));
          if (authEpochRef.current !== requestAuthEpoch || !authenticatedRef.current) return null;
          cursor = page.page?.next_cursor;
          if (!page.page?.has_more || !cursor) return issues;
        }
      };
      const [health, issues, runs, approvals, workflow, diagnostics, events] = await Promise.all([
        api.health(),
        loadIssues(),
        api.runs(),
        api.approvals(),
        api.workflow(),
        api.diagnostics(),
        api.events(maxSeqRef.current)
      ]);
      if (issues === null) return;
      if (authEpochRef.current !== requestAuthEpoch || !authenticatedRef.current) return;
      setData((current) => {
        const mergedEvents = mergeEvents(current.events, events);
        maxSeqRef.current = eventMaxSeq(mergedEvents);
        return {
          health,
          issues,
          runs,
          approvals,
          workflow,
          diagnostics,
          events: mergedEvents
        };
      });
      setAuthenticated(true);
    } catch (err) {
      if (isAuthError(err)) {
        markUnauthenticated();
      } else if (err instanceof TypeError) {
        setDaemonUnavailable(true);
      } else {
        setError(errorLabel(err));
      }
    } finally {
      setLoading(false);
    }
  }, [bootstrapSession, markUnauthenticated]);

  const scheduleSummaryRefresh = useCallback(() => {
    if (refreshTimerRef.current !== null) return;
    refreshTimerRef.current = window.setTimeout(() => {
      refreshTimerRef.current = null;
      void loadAll();
    }, 250);
  }, [loadAll]);

  useEffect(() => {
    void loadAll();
    const interval = window.setInterval(() => void loadAll(), 12_000);
    return () => {
      window.clearInterval(interval);
      if (refreshTimerRef.current !== null) {
        window.clearTimeout(refreshTimerRef.current);
        refreshTimerRef.current = null;
      }
    };
  }, [loadAll]);

  useEffect(() => {
    if (!authenticated) {
      setSseState('disabled');
      return undefined;
    }
    if (!('EventSource' in window)) {
      setSseState('disabled');
      return undefined;
    }
    setSseState('connecting');
    const source = new EventSource(`/api/v1/events/stream?after_seq=${maxSeqRef.current}`);
    source.onopen = () => setSseState('connected');
    const handleMessage = (message: MessageEvent) => {
      setSseState('connected');
      const event = eventFromSseMessage(message);
      if (event) {
        mergeEventBatch([event]);
        if (shouldRefreshSummary(event)) scheduleSummaryRefresh();
      } else {
        void loadIncrementalEvents();
        scheduleSummaryRefresh();
      }
    };
    source.onmessage = handleMessage;
    source.onerror = () => {
      setSseState('reconnecting');
    };
    return () => source.close();
  }, [authenticated, loadAll, loadIncrementalEvents, mergeEventBatch, scheduleSummaryRefresh]);

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
        markUnauthenticated();
      }
      setError(errorLabel(err));
      return null;
    } finally {
      setMutating(false);
    }
  }, [loadAll, markUnauthenticated]);

  return { data, loading, mutating, error, authenticated, authError, authMessage, authResetKey, daemonUnavailable, sseState, reload: loadAll, runMutation, markUnauthenticated };
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

function PageHeader({ eyebrow, title, description, meta, actions }: {
  eyebrow: string;
  title: string;
  description: string;
  meta?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <section className="page-header">
      <div>
        <p className="page-eyebrow">{eyebrow}</p>
        <h2>{title}</h2>
        <p>{description}</p>
        {meta ? <div className="page-header-meta">{meta}</div> : null}
      </div>
      {actions ? <div className="page-header-actions">{actions}</div> : null}
    </section>
  );
}

function MetricStrip({ items }: { items: Array<{ label: string; value: ReactNode; tone?: 'neutral' | 'good' | 'warning' | 'danger' | 'muted' }> }) {
  return (
    <div className="metric-strip">
      {items.map((item) => (
        <div key={item.label} className={item.tone ? `metric-strip-item-${item.tone}` : undefined}>
          <span>{item.label}</span>
          <strong>{item.value}</strong>
        </div>
      ))}
    </div>
  );
}

function AppShell({ route, data, loading, mutating, error, authError, authMessage, daemonUnavailable, sseState, children, reload }: {
  route: RouteState;
  data: DashboardData;
  loading: boolean;
  mutating: boolean;
  error: string | null;
  authError: boolean;
  authMessage: string | null;
  daemonUnavailable: boolean;
  sseState: string;
  children: ReactNode;
  reload: () => Promise<void>;
}) {
  const nav: Array<[PageKey, string]> = [
    ['overview', 'Workbench'],
    ['board', 'Board'],
    ['approvals', 'Approval Inbox'],
    ['workflow', 'Workflow'],
    ['diagnostics', 'Diagnostics']
  ];
  const running = data.runs.filter((run) => activeRunStatuses.has(run.status));
  const failed = data.runs.filter((run) => run.status === 'failed' || run.status === 'completed_without_handoff');
  const pendingApprovals = data.approvals.filter((approval) => approval.status === 'pending');
  const humanReview = data.issues.filter((issue) => issue.state === 'Human Review');
  const paused = data.issues.filter((issue) => issue.dispatch_paused);
  const unauthenticated = authError && !loading;
  return (
    <div className="app-shell">
      <header className="topbar">
        <div>
          <h1>Local Symphony</h1>
          <p className="subtitle">Operator workbench for local agent workflows.</p>
        </div>
        <div className="topbar-meta">
          <Pill tone={data.health?.ok ? 'good' : 'muted'}>{data.health?.project_id || 'project unknown'}</Pill>
          <Pill tone={data.workflow?.validation?.valid ? 'good' : 'danger'}>{data.workflow?.validation?.valid ? 'workflow valid' : 'workflow invalid'}</Pill>
          <Pill tone={sseState === 'connected' ? 'good' : sseState === 'reconnecting' ? 'warning' : 'muted'}>SSE {sseState}</Pill>
          <Pill tone={running.length ? 'warning' : 'muted'}>{running.length} running</Pill>
          <Pill tone={pendingApprovals.length ? 'warning' : 'muted'}>{pendingApprovals.length} approvals</Pill>
          <Pill tone={humanReview.length ? 'warning' : 'muted'}>{humanReview.length} review</Pill>
          <Pill tone={paused.length || failed.length ? 'danger' : 'muted'}>{paused.length} paused · {failed.length} failed</Pill>
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
      {authError ? <Banner kind="warning">{authMessage || 'Session expired or CSRF validation failed. Reopen the local dashboard through the daemon open URL or refresh the browser session.'}</Banner> : null}
      {error ? <Banner kind="error">{error}</Banner> : null}
      <main>
        {unauthenticated ? (
          <EmptyState
            title="Dashboard is not authenticated"
            body={authMessage || 'Session expired or CSRF validation failed. Reopen the local dashboard through the daemon open URL or refresh the browser session.'}
          />
        ) : children}
      </main>
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

function WorkbenchPage({ data, runMutation }: {
  data: DashboardData;
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
}) {
  const groups = useMemo(() => buildQueueGroups(data), [data]);
  const [selectedIssueRef, setSelectedIssueRef] = useState<string | null>(null);
  const selectedIssue = issueByIdOrRef(data.issues, selectedIssueRef) || pickDefaultIssue(data);

  useEffect(() => {
    if (selectedIssueRef && !issueByIdOrRef(data.issues, selectedIssueRef)) setSelectedIssueRef(null);
  }, [data.issues, selectedIssueRef]);

  return (
    <section className="workbench" aria-label="Operator workbench">
      <WorkQueue groups={groups} data={data} selectedIssueId={selectedIssue?.id || null} onSelect={(issue) => setSelectedIssueRef(issue.identifier)} />
      <IssueContextPanel issue={selectedIssue || null} data={data} />
      <ActionRail issue={selectedIssue || null} data={data} runMutation={runMutation} />
    </section>
  );
}

function WorkQueue({ groups, data, selectedIssueId, onSelect }: {
  groups: QueueGroup[];
  data: DashboardData;
  selectedIssueId: string | null;
  onSelect: (issue: Issue) => void;
}) {
  return (
    <aside className="work-queue" aria-label="Work queue">
      <div className="panel-heading">
        <div>
          <h2>Work Queue</h2>
          <p>{data.issues.length} local issues</p>
        </div>
        <button type="button" onClick={() => navigate({ page: 'board' })}>Board</button>
      </div>
      {groups.map((group) => (
        <section className="queue-group" key={group.key}>
          <div className="queue-group-heading">
            <h3>{group.label}</h3>
            <span>{group.issues.length}</span>
          </div>
          <p>{group.helper}</p>
          {group.issues.length === 0 ? (
            <div className="queue-empty">None</div>
          ) : group.issues.map((issue) => {
            const signals = issueSignals(issue, data.runs, data.approvals);
            return (
              <button
                key={`${group.key}-${issue.id}`}
                type="button"
                className={`queue-item ${selectedIssueId === issue.id ? 'active' : ''}`}
                aria-pressed={selectedIssueId === issue.id}
                onClick={() => onSelect(issue)}
              >
                <span className="queue-item-top">
                  <strong>{issue.identifier}</strong>
                  <StatusPill value={issue.state} />
                </span>
                <span className="queue-title">{issue.title}</span>
                <span className="queue-meta">
                  <Pill tone="muted">p{issue.priority}</Pill>
                  {signals.slice(0, 3).map((signal) => <Pill key={signal} tone={signal === 'active run' ? 'warning' : 'danger'}>{signal}</Pill>)}
                </span>
              </button>
            );
          })}
        </section>
      ))}
    </aside>
  );
}

function IssueContextPanel({ issue, data }: { issue: Issue | null; data: DashboardData }) {
  if (!issue) {
    return (
      <section className="context-panel">
        <div className="panel-heading">
          <div>
            <h2>No issues</h2>
            <p>Start with a local issue or inspect system state.</p>
          </div>
        </div>
        <div className="next-actions">
          <button type="button" onClick={() => navigate({ page: 'board' })}>Create issue</button>
          <button type="button" onClick={() => navigate({ page: 'board' })}>Open Board</button>
          <button type="button" onClick={() => navigate({ page: 'diagnostics' })}>Diagnostics</button>
        </div>
      </section>
    );
  }

  const issueEvents = data.events
    .filter((event) => event.issue_id === issue.id || event.run_id === issue.active_run_id || event.run_id === issue.latest_run_id)
    .sort((a, b) => b.seq - a.seq)
    .slice(0, 8);
  const runHistory = runsForIssue(issue, data.runs);
  const latestRun = latestRunForIssue(issue, data.runs);

  return (
    <section className="context-panel">
      <div className="context-header">
        <div>
          <div className="context-kicker">{issue.identifier}</div>
          <h2>{issue.title}</h2>
          <div className="tag-row">
            <StatusPill value={issue.state} />
            <Pill tone="muted">p{issue.priority}</Pill>
            {issue.labels.map((label) => <Pill key={label} tone="muted">{label}</Pill>)}
            {issue.dispatch_paused ? <Pill tone="warning">dispatch paused</Pill> : null}
          </div>
        </div>
        <button type="button" onClick={() => navigate({ page: 'issue', issueRef: issue.identifier })}>Open detail</button>
      </div>

      <div className="context-grid">
        <section className="context-block">
          <h3>Acceptance</h3>
          <p>{issue.description || '—'}</p>
          {issue.acceptance_criteria.length ? (
            <ul>{issue.acceptance_criteria.map((item) => <li key={item}>{item}</li>)}</ul>
          ) : <p>—</p>}
        </section>
        <section className="context-block">
          <h3>Relations</h3>
          <CompactRelationList title="Blocked by" refs={issue.blocked_by} />
          <CompactRelationList title="Blocks" refs={issue.blocks} />
          <CompactRelationList title="Duplicates" refs={issue.duplicates} />
          <CompactRelationList title="Follow-ups" refs={issue.followups} />
          <KeyValue rows={[
            ['Duplicate of', issue.duplicate_of ? refLabel(issue.duplicate_of) : '—'],
            ['Follow-up of', refLabel(issue.followup_of)]
          ]} />
        </section>
      </div>

      <section className="context-block">
        <div className="context-block-header">
          <h3>Run and review</h3>
          <div className="card-actions">
            {issue.latest_run_id ? <button type="button" onClick={() => navigate({ page: 'run', runId: issue.latest_run_id || undefined })}>Open run</button> : null}
            {canOpenReviewPacket(issue) ? <button type="button" onClick={() => navigate({ page: 'review', issueRef: issue.identifier })}>Open review</button> : null}
          </div>
        </div>
        <KeyValue rows={[
          ['Latest run', latestRun ? `${latestRun.id} · ${latestRun.status}` : '—'],
          ['Run attempts', runHistory.length],
          ['Latest packet', issue.latest_review_packet ? `#${issue.latest_review_packet.packet_no} · ${issue.latest_review_packet.status}` : '—'],
          ['Failure', latestRun?.failure_code || issue.latest_review_packet?.failure_code || '—']
        ]} />
      </section>

      <section className="context-block">
        <h3>Workspace and Git</h3>
        <KeyValue rows={[
          ['Workspace', issue.workspace_path || issue.workspace?.path || '—'],
          ['Branch', issue.branch_name || issue.git?.branch_name || '—'],
          ['Base ref', issue.base_ref || issue.git?.base_ref || '—'],
          ['Base SHA', issue.base_sha || issue.git?.base_sha || '—']
        ]} />
      </section>

      <section className="context-block">
        <div className="context-block-header">
          <h3>Recent timeline</h3>
          <span>{issueEvents.length} shown</span>
        </div>
        {issueEvents.length ? <EventList events={issueEvents} /> : <EmptyState title="No issue events" body="Run and issue events appear here after work starts." />}
      </section>
    </section>
  );
}

function CompactRelationList({ title, refs }: { title: string; refs: IssueRefSummary[] }) {
  return (
    <div className="compact-relations">
      <span>{title}</span>
      {refs.length === 0 ? <strong>—</strong> : (
        <div>
          {refs.map((ref) => (
            <button key={ref.id} type="button" className="link-button" onClick={() => navigate({ page: 'issue', issueRef: ref.identifier })}>{ref.identifier}</button>
          ))}
        </div>
      )}
    </div>
  );
}

function CommandFeedback({ message }: { message: string | null }) {
  return message ? <Banner kind="success">{message}</Banner> : null;
}

function ActionRail({ issue, data, runMutation }: {
  issue: Issue | null;
  data: DashboardData;
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
}) {
  const [pauseReason, setPauseReason] = useState('');
  const [comment, setComment] = useState('');
  const [blocker, setBlocker] = useState('');
  const [target, setTarget] = useState<IssueState>('Ready');
  const [transitionReason, setTransitionReason] = useState('');
  const [duplicateOf, setDuplicateOf] = useState('');
  const [reviewReason, setReviewReason] = useState('operator review decision');
  const [localMessage, setLocalMessage] = useState<string | null>(null);

  useEffect(() => {
    setPauseReason('');
    setComment('');
    setBlocker('');
    setTarget('Ready');
    setTransitionReason('');
    setDuplicateOf('');
    setLocalMessage(null);
  }, [issue?.id]);

  if (!issue) {
    return (
      <aside className="action-rail" aria-label="Action rail">
        <h2>Action rail</h2>
        <p>Select an issue to see contextual commands.</p>
        <button type="button" onClick={() => navigate({ page: 'board' })}>Create issue</button>
        <button type="button" onClick={() => navigate({ page: 'workflow' })}>Workflow</button>
        <button type="button" onClick={() => navigate({ page: 'diagnostics' })}>Diagnostics</button>
      </aside>
    );
  }

  const selectedIssue = issue;
  const pendingApprovals = pendingApprovalsForIssue(selectedIssue, data.approvals);
  const active = hasActiveRun(selectedIssue, data.runs);
  const latestRun = latestRunForIssue(selectedIssue, data.runs);
  const workflowInvalid = data.workflow && !data.workflow.validation.valid;
  const transitionValidation = transitionValidationError(target, transitionReason);

  async function perform<T>(label: string, operation: () => Promise<T>) {
    const result = await runMutation(operation);
    if (result) setLocalMessage(label);
    return result;
  }

  async function pauseResume() {
    const reason = dispatchPauseResumeReason(pauseReason, selectedIssue.dispatch_paused);
    const label = selectedIssue.dispatch_paused ? 'Dispatch resumed.' : 'Dispatch paused.';
    const result = await perform(label, () => selectedIssue.dispatch_paused ? api.resumeDispatch(selectedIssue.identifier, reason) : api.pauseDispatch(selectedIssue.identifier, reason));
    if (result) setPauseReason('');
  }

  async function addComment() {
    if (!comment.trim()) return;
    const result = await perform('Comment added.', () => api.commentIssue(selectedIssue.identifier, comment.trim()));
    if (result) setComment('');
  }

  async function addIssueBlocker() {
    if (!blocker.trim()) return;
    const result = await perform('Blocker added.', () => api.addBlocker(selectedIssue.identifier, blocker.trim()));
    if (result) setBlocker('');
  }

  async function transitionIssue() {
    if (transitionValidation) return;
    const result = await perform('Issue transitioned.', () => api.transitionIssue(selectedIssue.identifier, {
      state: target,
      reason: transitionReason.trim() || undefined,
      duplicate_of: target === 'Duplicate' ? duplicateOf.trim() || undefined : undefined
    }));
    if (result) {
      setTransitionReason('');
      setDuplicateOf('');
    }
  }

  async function reviewAction(kind: 'rework' | 'done') {
    if (!canPerformReviewAction(selectedIssue) || !reviewReason.trim()) return;
    const result = await perform(kind === 'rework' ? 'Sent to Rework.' : 'Marked Done.', () => kind === 'rework' ? api.sendToRework(selectedIssue.identifier, reviewReason.trim()) : api.markDone(selectedIssue.identifier, reviewReason.trim()));
    if (result) navigate({ page: 'issue', issueRef: result.identifier });
  }

  return (
    <aside className="action-rail" aria-label="Action rail">
      <div className="panel-heading">
        <div>
          <h2>Action rail</h2>
          <p>{selectedIssue.identifier}</p>
        </div>
        <StatusPill value={selectedIssue.state} />
      </div>
      <CommandFeedback message={localMessage} />

      <section className="rail-group">
        <h3>Primary</h3>
        {isDispatchable(selectedIssue) ? (
          <button type="button" className="primary-action" onClick={() => void perform('Dispatch submitted.', () => api.dispatchIssue(selectedIssue.identifier))}>Dispatch eligible issue</button>
        ) : null}
        <label>
          Pause/resume reason
          <input value={pauseReason} onChange={(event) => setPauseReason(event.target.value)} placeholder={selectedIssue.dispatch_paused ? dispatchResumeDefaultReason : dispatchPauseDefaultReason} />
        </label>
        <button type="button" onClick={() => void pauseResume()} disabled={active && !selectedIssue.dispatch_paused}>
          {selectedIssue.dispatch_paused ? 'Dispatch resume issue' : 'Dispatch pause issue'}
        </button>
        {active && !selectedIssue.dispatch_paused ? <p className="rail-hint">An active run is attached; pause is disabled until it settles.</p> : null}
      </section>

      {pendingApprovals.length ? (
        <section className="rail-group rail-warning">
          <h3>Pending approvals</h3>
          <p>{pendingApprovals.length} command/file/network request needs a decision.</p>
          <button type="button" onClick={() => navigate({ page: 'approvals' })}>Open Approval Inbox</button>
        </section>
      ) : null}

      {canPerformReviewAction(selectedIssue) ? (
        <section className="rail-group">
          <h3>Review</h3>
          <button type="button" onClick={() => navigate({ page: 'review', issueRef: selectedIssue.identifier })}>Open review packet</button>
          <label>
            Review reason
            <textarea value={reviewReason} onChange={(event) => setReviewReason(event.target.value)} rows={3} />
          </label>
          <div className="rail-split">
            <button type="button" onClick={() => void reviewAction('rework')} disabled={!reviewReason.trim()}>Send to Rework</button>
            <button type="button" className="primary-action" onClick={() => void reviewAction('done')} disabled={!reviewReason.trim()}>Mark Done</button>
          </div>
        </section>
      ) : null}

      <section className="rail-group">
        <h3>Issue edits</h3>
        <button type="button" onClick={() => navigate({ page: 'issue', issueRef: selectedIssue.identifier })}>Update issue</button>
        <label>
          Comment
          <textarea value={comment} onChange={(event) => setComment(event.target.value)} rows={3} placeholder="Leave an operator comment" />
        </label>
        <button type="button" onClick={() => void addComment()} disabled={!comment.trim()}>Add comment</button>
        <label>
          Add blocker
          <input value={blocker} onChange={(event) => setBlocker(event.target.value)} placeholder="LOC-1" />
        </label>
        <button type="button" onClick={() => void addIssueBlocker()} disabled={!blocker.trim()}>Add blocker</button>
      </section>

      <section className="rail-group">
        <h3>Transition</h3>
        <label>
          Target state
          <select value={target} onChange={(event) => setTarget(event.target.value as IssueState)}>
            {transitionTargets.map((state) => <option key={state} value={state}>{state}</option>)}
          </select>
        </label>
        <label>
          Reason
          <input value={transitionReason} onChange={(event) => setTransitionReason(event.target.value)} placeholder="Reason for state change" />
        </label>
        {target === 'Duplicate' ? (
          <label>
            Duplicate of
            <input value={duplicateOf} onChange={(event) => setDuplicateOf(event.target.value)} placeholder="Canonical issue ref" />
          </label>
        ) : null}
        {transitionValidation ? <p className="rail-hint">{transitionValidation}</p> : null}
        <button type="button" onClick={() => void transitionIssue()} disabled={Boolean(transitionValidation)}>Apply transition</button>
      </section>

      <section className="rail-group">
        <h3>Related views</h3>
        {latestRun ? <button type="button" onClick={() => navigate({ page: 'run', runId: latestRun.id })}>Open latest run</button> : null}
        {workflowInvalid ? <button type="button" onClick={() => navigate({ page: 'workflow' })}>Workflow invalid</button> : null}
        <button type="button" onClick={() => navigate({ page: 'diagnostics' })}>Diagnostics</button>
      </section>
    </aside>
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
  const ready = issues.filter((issue) => isDispatchable(issue)).length;
  const blocked = issues.filter((issue) => issue.state === 'Blocked').length;
  const review = issues.filter((issue) => issue.state === 'Human Review').length;

  return (
    <>
      <PageHeader
        eyebrow="Board View"
        title="Issue board"
        description="Scan every issue state, then open the workbench or a detail page for focused actions."
        meta={<MetricStrip items={[
          { label: 'Total', value: issues.length, tone: 'muted' },
          { label: 'Ready', value: ready, tone: ready ? 'good' : 'muted' },
          { label: 'Blocked', value: blocked, tone: blocked ? 'danger' : 'muted' },
          { label: 'Review', value: review, tone: review ? 'warning' : 'muted' }
        ]} />}
        actions={<button type="button" onClick={() => navigate({ page: 'overview' })}>Open Workbench</button>}
      />
      <details className="create-drawer">
        <summary>Create issue</summary>
        <CreateIssueForm runMutation={runMutation} />
      </details>
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
  const transitionValidation = transitionValidationError(target, reason);

  async function transition() {
    if (transitionValidation) return;
    const changed = await runMutation(() => api.transitionIssue(issue.identifier, {
      state: target,
      reason: reason.trim() || undefined,
      duplicate_of: target === 'Duplicate' ? duplicateOf.trim() || undefined : undefined
    }));
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
        {canOpenReviewPacket(issue) ? <button type="button" onClick={() => navigate({ page: 'review', issueRef: issue.identifier })}>Review</button> : null}
      </div>
      <details className="inline-command">
        <summary>Transition issue</summary>
        <label>
          Target state
          <select value={target} onChange={(event) => setTarget(event.target.value as IssueState)}>
            {transitionTargets.map((state) => <option key={state} value={state}>{state}</option>)}
          </select>
        </label>
        {requiresTransitionReason(target) ? (
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
        {transitionValidation ? <p className="rail-hint">{transitionValidation}</p> : null}
        <button type="button" onClick={() => void transition()} disabled={Boolean(transitionValidation)}>Apply transition</button>
      </details>
    </article>
  );
}

function IssueDetailPage({ route, issues, runs, events, runMutation, markUnauthenticated, authenticated, authResetKey }: {
  route: RouteState;
  issues: Issue[];
  runs: RunAttempt[];
  events: RunEvent[];
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
  markUnauthenticated: () => void;
  authenticated: boolean;
  authResetKey: number;
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
  }, [authResetKey]);
  useEffect(() => {
    setFetchedIssue(null);
    setMissingIssueRef(null);
    setIssueLoadError(null);
    if (!authenticated || !route.issueRef || listedIssue) return undefined;
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
        if (isAuthError(error)) {
          markUnauthenticated();
          return;
        }
        setIssueLoadError(errorLabel(error));
      });
    return () => { cancelled = true; };
  }, [authenticated, markUnauthenticated, route.issueRef, listedIssue]);
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
  const latestRun = latestRunForIssue(issue, runs);

  async function postComment(event: FormEvent) {
    event.preventDefault();
    if (!comment.trim()) return;
    const result = await runMutation(() => api.commentIssue(selectedIssue.identifier, comment.trim()));
    if (result) setComment('');
  }

  async function addBlocker(event: FormEvent) {
    event.preventDefault();
    if (!blocker.trim()) return;
    const result = await runMutation(() => api.addBlocker(selectedIssue.identifier, blocker.trim()));
    if (result) setBlocker('');
  }

  async function pauseResume(paused: boolean) {
    const reason = dispatchPauseResumeReason(pauseReason, paused);
    const result = await runMutation(() => paused ? api.resumeDispatch(selectedIssue.identifier, reason) : api.pauseDispatch(selectedIssue.identifier, reason));
    if (result) setPauseReason('');
  }

  return (
    <>
      <PageHeader
        eyebrow="Issue Detail"
        title={`${issue.identifier} · ${issue.title}`}
        description="Review issue facts, edit scope, inspect relations, and operate dispatch from one detail view."
        meta={<MetricStrip items={[
          { label: 'State', value: <StatusPill value={issue.state} />, tone: issue.state === 'Ready' ? 'good' : issue.state === 'Blocked' ? 'danger' : 'warning' },
          { label: 'Priority', value: `p${issue.priority}`, tone: 'muted' },
          { label: 'Runs', value: runHistory.length, tone: runHistory.length ? 'warning' : 'muted' },
          { label: 'Latest', value: latestRun?.status || 'none', tone: latestRun?.status === 'failed' ? 'danger' : 'muted' }
        ]} />}
        actions={<button type="button" onClick={() => navigate({ page: 'board' })}>Back to Board</button>}
      />

      <div className="page-split">
        <div className="page-main">
          <Section title="Issue facts">
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
              {canOpenReviewPacket(issue) ? <button type="button" onClick={() => navigate({ page: 'review', issueRef: issue.identifier })}>Open review packet</button> : null}
            </div>
          </Section>

          <IssueEditForm issue={issue} runMutation={runMutation} />

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
              <button type="submit" disabled={!blocker.trim()}>Add blocker</button>
            </form>
          </Section>

          <Section title="Run history">
            {runHistory.length ? <RunTable runs={runHistory} /> : <EmptyState title="No run history" body="Dispatch a Ready or Rework issue to create the first run attempt." />}
          </Section>

          <Section title="Comments and issue events">
            <form className="inline-form" onSubmit={(event) => void postComment(event)}>
              <label>
                Comment
                <input value={comment} onChange={(event) => setComment(event.target.value)} placeholder="Leave an operator comment" />
              </label>
              <button type="submit" disabled={!comment.trim()}>Add comment</button>
            </form>
            {issueEvents.length ? <EventList events={issueEvents.slice().reverse()} /> : <EmptyState title="No issue events" body="Comments and state changes are displayed from normalized issue events." />}
          </Section>
        </div>

        <aside className="page-aside">
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

          <Section title="Workspace and Git">
            <KeyValue rows={[
              ['Workspace path', issue.workspace_path || issue.workspace?.path || '—'],
              ['Branch', issue.branch_name || issue.git?.branch_name || '—'],
              ['Base ref', issue.base_ref || issue.git?.base_ref || '—'],
              ['Base SHA', issue.base_sha || issue.git?.base_sha || '—']
            ]} />
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
        </aside>
      </div>
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

function RunDetailPage({ route, runs, issues, events, runMutation, markUnauthenticated, authenticated, authResetKey }: {
  route: RouteState;
  runs: RunAttempt[];
  issues: Issue[];
  events: RunEvent[];
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
  markUnauthenticated: () => void;
  authenticated: boolean;
  authResetKey: number;
}) {
  const [reason, setReason] = useState('operator cancelled run');
  const [fetchedRun, setFetchedRun] = useState<RunAttempt | null>(null);
  const [fetchedRunEvents, setFetchedRunEvents] = useState<RunEvent[]>([]);
  const [missingRunId, setMissingRunId] = useState<string | null>(null);
  const [runLoadError, setRunLoadError] = useState<string | null>(null);
  const listedRun = runs.find((item) => item.id === route.runId);
  useEffect(() => {
    setFetchedRun(null);
    setFetchedRunEvents([]);
    setMissingRunId(null);
    setRunLoadError(null);
  }, [authResetKey]);
  useEffect(() => {
    setFetchedRun(null);
    setMissingRunId(null);
    setRunLoadError(null);
    if (!authenticated || !route.runId || listedRun) return undefined;
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
        if (isAuthError(error)) {
          markUnauthenticated();
          return;
        }
        setRunLoadError(errorLabel(error));
      });
    return () => { cancelled = true; };
  }, [authenticated, markUnauthenticated, route.runId, listedRun]);
  useEffect(() => {
    setFetchedRunEvents([]);
    if (!authenticated || !route.runId) return undefined;
    let cancelled = false;
    loadRunEvents(route.runId)
      .then((loaded) => {
        if (!cancelled) setFetchedRunEvents(loaded);
      })
      .catch((error) => {
        if (cancelled) return;
        if (isAuthError(error)) {
          markUnauthenticated();
          return;
        }
        setRunLoadError(errorLabel(error));
      });
    return () => { cancelled = true; };
  }, [authenticated, markUnauthenticated, route.runId]);
  const run = listedRun || (fetchedRun && fetchedRun.id === route.runId ? fetchedRun : undefined);
  const issue = issueByIdOrRef(issues, run?.issue_id);
  const runEvents = run ? mergeEvents(
    events.filter((event) => event.run_id === run.id),
    fetchedRunEvents.filter((event) => !event.run_id || event.run_id === run.id)
  ) : [];

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
      <PageHeader
        eyebrow="Run Detail"
        title={`Run ${run.id}`}
        description="Inspect normalized run metadata, cancellation state, and event timeline without exposing raw agent logs."
        meta={<MetricStrip items={[
          { label: 'Status', value: <StatusPill value={run.status} />, tone: run.status === 'failed' ? 'danger' : activeRunStatuses.has(run.status) ? 'warning' : 'muted' },
          { label: 'Attempt', value: run.attempt_no, tone: 'muted' },
          { label: 'Events', value: runEvents.length, tone: runEvents.length ? 'warning' : 'muted' },
          { label: 'Failure', value: run.failure_code || 'none', tone: run.failure_code ? 'danger' : 'muted' }
        ]} />}
        actions={issue ? <button type="button" onClick={() => navigate({ page: 'issue', issueRef: issue.identifier })}>Open issue</button> : null}
      />
      <div className="page-split">
        <div className="page-main">
          <Section title="Run facts">
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
          </Section>
          <Section title="Normalized timeline">
            {runEvents.length === 0 ? (
              <EmptyState title="No run events" body="Timeline events are replayed from the REST events API and SSE stream. Raw Codex protocol logs are not shown." />
            ) : <EventList events={runEvents} />}
          </Section>
        </div>
        <aside className="page-aside">
          <Section title="Run controls">
            {canCancel ? (
              <div className="form-grid compact">
                <label className="span-2">
                  Cancel reason
                  <input value={reason} onChange={(event) => setReason(event.target.value)} />
                </label>
                <div className="span-2 form-actions">
                  <button type="button" onClick={() => void runMutation(() => api.cancelRun(run.id, reason))}>Cancel run</button>
                </div>
              </div>
            ) : <EmptyState title="No active command" body="Completed, failed, and cancelled runs cannot be cancelled." />}
          </Section>
          <Section title="Dispatch context">
            <KeyValue rows={[
              ['Workflow snapshot', run.workflow_snapshot_id || '—'],
              ['Workspace ID', run.workspace_id || '—'],
              ['Base config', run.base_ref_config || '—'],
              ['Updated', formatDate(run.updated_at)]
            ]} />
          </Section>
        </aside>
      </div>
    </>
  );
}

function ApprovalInboxPage({ approvals, runMutation }: { approvals: Approval[]; runMutation: <T>(operation: () => Promise<T>) => Promise<T | null> }) {
  const pending = approvals.filter((approval) => approval.status === 'pending');
  const resolved = approvals.filter((approval) => approval.status !== 'pending');

  return (
    <>
      <PageHeader
        eyebrow="Approval Inbox"
        title="Approval decisions"
        description="Review command, file, and network approval requests generated by active runs."
        meta={<MetricStrip items={[
          { label: 'Pending', value: pending.length, tone: pending.length ? 'warning' : 'muted' },
          { label: 'Resolved', value: resolved.length, tone: 'muted' },
          { label: 'Total', value: approvals.length, tone: 'muted' }
        ]} />}
        actions={<button type="button" onClick={() => navigate({ page: 'overview' })}>Open Workbench</button>}
      />
      <div className="page-split">
        <div className="page-main">
          <Section title="Pending approvals">
            {pending.length === 0 ? (
              <EmptyState title="No pending approvals" body="Command, file, and network approvals requested by active runs will appear here." />
            ) : pending.map((approval) => <ApprovalCard key={approval.id} approval={approval} runMutation={runMutation} />)}
          </Section>
        </div>
        <aside className="page-aside">
          <Section title="Resolved / expired approvals">
            {resolved.length === 0 ? (
              <EmptyState title="No resolved approvals" body="Expired, denied, approved, and cancelled approvals are listed after they leave pending state." />
            ) : (
              <table className="data-table compact-table">
                <thead><tr><th>ID</th><th>Status</th><th>Run</th></tr></thead>
                <tbody>{resolved.map((approval) => (
                  <tr key={approval.id}>
                    <td>{approval.id}</td>
                    <td><StatusPill value={approval.status} /></td>
                    <td><button type="button" className="link-button" onClick={() => navigate({ page: 'run', runId: approval.run_id })}>{approval.run_id}</button></td>
                  </tr>
                ))}</tbody>
              </table>
            )}
          </Section>
        </aside>
      </div>
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

function ReviewPacketPage({ route, issues, runMutation, markUnauthenticated, authenticated, authResetKey }: {
  route: RouteState;
  issues: Issue[];
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
  markUnauthenticated: () => void;
  authenticated: boolean;
  authResetKey: number;
}) {
  const defaultIssue = route.issueRef || issues.find((issue) => canOpenReviewPacket(issue))?.identifier || '';
  const [issueRef, setIssueRef] = useState(defaultIssue);
  const [review, setReview] = useState<ReviewPacketSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reason, setReason] = useState('operator review decision');
  const [artifactContent, setArtifactContent] = useState<Record<string, { title: string; text: string; refused?: boolean }>>({});

  useEffect(() => {
    if (route.issueRef) setIssueRef(route.issueRef);
  }, [route.issueRef]);

  useEffect(() => {
    setReview(null);
    setArtifactContent({});
    setError(null);
    setLoading(false);
  }, [authResetKey]);

  const loadReview = useCallback(async (ref: string) => {
    if (!authenticated || !ref) return;
    setLoading(true);
    setError(null);
    setArtifactContent({});
    try {
      const result = await api.review(ref);
      setReview(result);
    } catch (err) {
      if (isAuthError(err)) {
        setReview(null);
        setArtifactContent({});
        markUnauthenticated();
        return;
      }
      setReview(null);
      setError(errorLabel(err));
    } finally {
      setLoading(false);
    }
  }, [authenticated, markUnauthenticated]);

  useEffect(() => {
    if (authenticated && issueRef) void loadReview(issueRef);
  }, [authenticated, issueRef, loadReview]);

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
      if (isAuthError(err)) {
        setArtifactContent({});
        markUnauthenticated();
        return;
      }
      const refused = err instanceof ApiError && (err.code === 'raw_log_access_not_supported' || err.status === 403);
      setArtifactContent((current) => ({ ...current, [artifact.artifact_id]: { title: artifact.kind, text: errorLabel(err), refused } }));
    }
  }

  async function reviewAction(kind: 'rework' | 'done') {
    if (!reviewActionsAvailable || !reason.trim()) return;
    const result = await runMutation(() => kind === 'rework' ? api.sendToRework(issueRef, reason.trim()) : api.markDone(issueRef, reason.trim()));
    if (result) navigate({ page: 'issue', issueRef: result.identifier });
  }
  const reviewIssue = issueByIdOrRef(issues, issueRef);
  const reviewActionsAvailable = Boolean(review && review.status === 'generated' && reviewIssue && canPerformReviewAction(reviewIssue));

  return (
    <>
      <PageHeader
        eyebrow="Review Packet"
        title="Human review packet"
        description="Inspect redacted packet metadata and allowed artifacts, then decide whether to rework or mark done."
        meta={<MetricStrip items={[
          { label: 'Issue', value: issueRef || 'none', tone: issueRef ? 'muted' : 'warning' },
          { label: 'Packet', value: review ? `#${review.packet_no}` : 'not loaded', tone: review ? 'good' : 'muted' },
          { label: 'Artifacts', value: artifacts.length, tone: artifacts.length ? 'warning' : 'muted' },
          { label: 'Status', value: review?.status || '—', tone: review?.status === 'failed' ? 'danger' : 'muted' }
        ]} />}
        actions={<button type="button" onClick={() => issueRef && void loadReview(issueRef)} disabled={!issueRef || loading}>Reload review</button>}
      />

      <div className="page-split">
        <div className="page-main">
          <Section title="Packet lookup">
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
        </div>

        <aside className="page-aside">
          {reviewActionsAvailable ? (
            <Section title="Human Review actions">
              <label>
                Reason
                <textarea value={reason} onChange={(event) => setReason(event.target.value)} rows={3} />
              </label>
              <div className="card-actions">
                <button type="button" onClick={() => void reviewAction('rework')} disabled={!reason.trim()}>Send to Rework</button>
                <button type="button" onClick={() => void reviewAction('done')} disabled={!reason.trim()}>Mark Done</button>
              </div>
            </Section>
          ) : (
            <Section title="Human Review actions">
              <EmptyState title="No packet selected" body="Load a Human Review issue with a latest review packet before sending it to Rework or marking it Done." />
            </Section>
          )}
          <Section title="Boundary">
            <p className="rail-hint">This view uses Review and Artifact APIs only. Raw Codex logs and unsupported artifact content remain unavailable.</p>
          </Section>
        </aside>
      </div>
    </>
  );
}

function WorkflowPage({ workflow, runMutation, markUnauthenticated, authResetKey }: {
  workflow: WorkflowResponse | null;
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
  markUnauthenticated: () => void;
  authResetKey: number;
}) {
  const [validation, setValidation] = useState<WorkflowValidateResponse | null>(null);
  const [preview, setPreview] = useState<WorkflowRenderPreviewResponse | null>(null);
  const [localError, setLocalError] = useState<string | null>(null);

  useEffect(() => {
    setValidation(null);
    setPreview(null);
    setLocalError(null);
  }, [authResetKey]);

  async function validate() {
    setLocalError(null);
    try {
      setValidation(await api.validateWorkflow());
    } catch (err) {
      if (isAuthError(err)) {
        setValidation(null);
        setPreview(null);
        markUnauthenticated();
        return;
      }
      setLocalError(errorLabel(err));
    }
  }

  async function renderPreview() {
    setLocalError(null);
    try {
      setPreview(await api.renderWorkflowPreview());
    } catch (err) {
      if (isAuthError(err)) {
        setValidation(null);
        setPreview(null);
        markUnauthenticated();
        return;
      }
      setLocalError(errorLabel(err));
    }
  }

  return (
    <>
      <PageHeader
        eyebrow="Workflow"
        title="Workflow configuration"
        description="Validate, preview, and reload the local workflow through dry-run oriented API actions."
        meta={<MetricStrip items={[
          { label: 'Valid', value: workflow?.validation?.valid ? 'yes' : 'no', tone: workflow?.validation?.valid ? 'good' : 'danger' },
          { label: 'Warnings', value: workflow?.validation?.warnings?.length || 0, tone: workflow?.validation?.warnings?.length ? 'warning' : 'muted' },
          { label: 'Errors', value: workflow?.validation?.errors?.length || 0, tone: workflow?.validation?.errors?.length ? 'danger' : 'muted' }
        ]} />}
        actions={<button type="button" onClick={() => navigate({ page: 'overview' })}>Open Workbench</button>}
      />
      <div className="page-split">
        <div className="page-main">
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
        </div>
        <aside className="page-aside">
          <Section title="Last loaded config">
            {workflow?.config ? <JsonBlock value={workflow.config} maxHeight={420} /> : <EmptyState title="No config object" body="The workflow API returned validation without an effective config payload." />}
          </Section>
        </aside>
      </div>
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
      <PageHeader
        eyebrow="Diagnostics"
        title="Redacted system diagnostics"
        description="Inspect daemon, workflow, Codex, git, and redaction state without exposing raw secrets or logs."
        meta={<MetricStrip items={[
          { label: 'Warnings', value: diagnostics.warnings.length, tone: diagnostics.warnings.length ? 'warning' : 'muted' },
          { label: 'Checks', value: diagnostics.checks.length, tone: 'muted' },
          { label: 'Redacted', value: diagnostics.redacted ? 'yes' : 'no', tone: diagnostics.redacted ? 'good' : 'danger' }
        ]} />}
        actions={<button type="button" onClick={() => void exportDiagnostics()}>Diagnostics export</button>}
      />
      <div className="page-split">
        <div className="page-main">
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
        </div>
        <aside className="page-aside">
          <Section title="Export summary">
            <KeyValue rows={[
              ['Project', diagnostics.project_id],
              ['Generated', formatDate(diagnostics.generated_at)],
              ['Repo root', diagnostics.repo_root],
              ['Redacted export only', diagnostics.redacted ? 'yes' : 'no'],
              ['Warnings', diagnostics.warnings.length]
            ]} />
            {exportResult ? <Banner kind="success">Redacted diagnostics exported as artifact {exportResult.artifact_id} at {exportResult.path}</Banner> : null}
          </Section>
        </aside>
      </div>
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
  const { data, loading, mutating, error, authenticated, authError, authMessage, authResetKey, daemonUnavailable, sseState, reload, runMutation, markUnauthenticated } = useDashboardData();

  let content: ReactNode;
  switch (route.page) {
    case 'board':
      content = <BoardPage issues={data.issues} runMutation={runMutation} />;
      break;
    case 'issue':
      content = <IssueDetailPage key={authResetKey} route={route} issues={data.issues} runs={data.runs} events={data.events} runMutation={runMutation} markUnauthenticated={markUnauthenticated} authenticated={authenticated} authResetKey={authResetKey} />;
      break;
    case 'run':
      content = <RunDetailPage key={authResetKey} route={route} runs={data.runs} issues={data.issues} events={data.events} runMutation={runMutation} markUnauthenticated={markUnauthenticated} authenticated={authenticated} authResetKey={authResetKey} />;
      break;
    case 'approvals':
      content = <ApprovalInboxPage approvals={data.approvals} runMutation={runMutation} />;
      break;
    case 'review':
      content = <ReviewPacketPage key={authResetKey} route={route} issues={data.issues} runMutation={runMutation} markUnauthenticated={markUnauthenticated} authenticated={authenticated} authResetKey={authResetKey} />;
      break;
    case 'workflow':
      content = <WorkflowPage key={authResetKey} workflow={data.workflow} runMutation={runMutation} markUnauthenticated={markUnauthenticated} authResetKey={authResetKey} />;
      break;
    case 'diagnostics':
      content = <DiagnosticsPage diagnostics={data.diagnostics} runMutation={runMutation} />;
      break;
    default:
      content = <WorkbenchPage data={data} runMutation={runMutation} />;
  }

  return (
    <AppShell
      route={route}
      data={data}
      loading={loading}
      mutating={mutating}
      error={error}
      authError={authError}
      authMessage={authMessage}
      daemonUnavailable={daemonUnavailable}
      sseState={sseState}
      reload={reload}
    >
      {content}
    </AppShell>
  );
}
