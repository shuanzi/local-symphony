import type {
  Approval,
  ApprovalDecision,
  Artifact,
  Diagnostics,
  DiagnosticsExportResult,
  DispatchResult,
  Health,
  Issue,
  IssueState,
  Page,
  ReviewPacketSummary,
  RunAttempt,
  RunEvent,
  SessionInfo,
  WorkflowRenderPreviewResponse,
  WorkflowResponse,
  WorkflowValidateResponse
} from './types';

export const API_PREFIX = '/api/v1';

export interface ApiErrorBody {
  code: string;
  message: string;
  details?: Record<string, unknown>;
  request_id?: string;
}

export class ApiError extends Error {
  status: number;
  code: string;
  details?: Record<string, unknown>;
  requestId?: string;

  constructor(status: number, body: ApiErrorBody) {
    super(body.message || body.code || `HTTP ${status}`);
    this.name = 'ApiError';
    this.status = status;
    this.code = body.code || `http_${status}`;
    this.details = body.details;
    this.requestId = body.request_id;
  }
}

interface SuccessEnvelope<T> {
  data: T;
  meta?: Record<string, unknown>;
}

interface ErrorEnvelope {
  error: ApiErrorBody;
}

let csrfToken: string | null = window.localStorage.getItem('symphony.csrf');

export function setCsrfToken(token: string | null): void {
  csrfToken = token;
  if (token) {
    window.localStorage.setItem('symphony.csrf', token);
  } else {
    window.localStorage.removeItem('symphony.csrf');
  }
}

function commandHeaders(init?: RequestInit): Headers {
  const headers = new Headers(init?.headers || {});
  if (!headers.has('Content-Type') && init?.body !== undefined) {
    headers.set('Content-Type', 'application/json');
  }
  if (csrfToken && init?.method && init.method.toUpperCase() !== 'GET') {
    headers.set('X-Symphony-CSRF', csrfToken);
  }
  return headers;
}

async function readJsonOrNull(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

export async function apiRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`${API_PREFIX}${path}`, {
    credentials: 'same-origin',
    ...init,
    headers: commandHeaders(init)
  });
  const payload = await readJsonOrNull(response);

  if (!response.ok) {
    const envelope = payload as Partial<ErrorEnvelope> | null;
    const body = envelope && typeof envelope === 'object' && 'error' in envelope && envelope.error
      ? envelope.error
      : { code: `http_${response.status}`, message: response.statusText || `HTTP ${response.status}` };
    throw new ApiError(response.status, body);
  }

  if (payload && typeof payload === 'object' && 'data' in payload) {
    return (payload as SuccessEnvelope<T>).data;
  }
  return payload as T;
}

export async function fetchArtifactContent(contentUrl: string): Promise<string> {
  const response = await fetch(contentUrl, { credentials: 'same-origin' });
  const contentType = response.headers.get('Content-Type') || '';
  const text = await response.text();
  if (!response.ok) {
    if (contentType.includes('application/json')) {
      try {
        const envelope = JSON.parse(text) as ErrorEnvelope;
        throw new ApiError(response.status, envelope.error);
      } catch (error) {
        if (error instanceof ApiError) throw error;
      }
    }
    throw new ApiError(response.status, { code: `http_${response.status}`, message: text || response.statusText });
  }
  return text;
}

export const api = {
  health: () => apiRequest<Health>('/health'),
  session: async () => {
    const session = await apiRequest<SessionInfo>('/auth/session');
    setCsrfToken(session.csrf_token || session.csrf || null);
    return session;
  },
  issues: (query = '') => apiRequest<Page<Issue>>(`/issues${query}`),
  issue: (issueRef: string) => apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}`),
  createIssue: (input: { title: string; description: string; acceptance_criteria: string[]; priority: number; labels: string[] }) =>
    apiRequest<Issue>('/issues', { method: 'POST', body: JSON.stringify(input) }),
  updateIssue: (issueRef: string, input: Partial<Pick<Issue, 'title' | 'description' | 'acceptance_criteria' | 'priority' | 'labels'>>) =>
    apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}`, { method: 'PATCH', body: JSON.stringify(input) }),
  transitionIssue: (issueRef: string, input: { state: IssueState; reason?: string; duplicate_of?: string }) =>
    apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}/transition`, { method: 'POST', body: JSON.stringify(input) }),
  commentIssue: (issueRef: string, body: string) =>
    apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}/comments`, { method: 'POST', body: JSON.stringify({ body }) }),
  addBlocker: (issueRef: string, blockedBy: string) =>
    apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}/blockers`, { method: 'POST', body: JSON.stringify({ blocked_by: blockedBy }) }),
  removeBlocker: (issueRef: string, blockerRef: string) =>
    apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}/blockers/${encodeURIComponent(blockerRef)}`, { method: 'DELETE' }),
  removeDuplicate: (issueRef: string, canonicalRef: string) =>
    apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}/duplicates/${encodeURIComponent(canonicalRef)}`, { method: 'DELETE' }),
  dispatchIssue: (issueRef: string) =>
    apiRequest<DispatchResult>(`/issues/${encodeURIComponent(issueRef)}/dispatch`, { method: 'POST', body: JSON.stringify({}) }),
  pauseDispatch: (issueRef: string, reason: string) =>
    apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}/dispatch-pause`, { method: 'POST', body: JSON.stringify({ reason }) }),
  resumeDispatch: (issueRef: string, reason: string) =>
    apiRequest<Issue>(`/issues/${encodeURIComponent(issueRef)}/dispatch-resume`, { method: 'POST', body: JSON.stringify({ reason }) }),
  runs: () => apiRequest<RunAttempt[]>('/runs'),
  run: (runId: string) => apiRequest<RunAttempt>(`/runs/${encodeURIComponent(runId)}`),
  runEvents: (runId: string, afterSeq = 0) => apiRequest<RunEvent[]>(`/runs/${encodeURIComponent(runId)}/events?after_seq=${afterSeq}`),
  events: (afterSeq = 0) => apiRequest<RunEvent[]>(`/events?after_seq=${afterSeq}`),
  cancelRun: (runId: string, reason: string) =>
    apiRequest<RunAttempt>(`/runs/${encodeURIComponent(runId)}/cancel`, { method: 'POST', body: JSON.stringify({ reason }) }),
  approvals: () => apiRequest<Approval[]>('/approvals'),
  decideApproval: (approvalId: string, decision: ApprovalDecision, reason: string) =>
    apiRequest<{ id: string; status: string }>(`/approvals/${encodeURIComponent(approvalId)}/decide`, {
      method: 'POST',
      body: JSON.stringify({ decision, reason })
    }),
  review: (issueRef: string) => apiRequest<ReviewPacketSummary>(`/reviews/${encodeURIComponent(issueRef)}`),
  sendToRework: (issueRef: string, reason: string) =>
    apiRequest<Issue>(`/reviews/${encodeURIComponent(issueRef)}/send-to-rework`, { method: 'POST', body: JSON.stringify({ reason }) }),
  markDone: (issueRef: string, reason: string) =>
    apiRequest<Issue>(`/reviews/${encodeURIComponent(issueRef)}/mark-done`, { method: 'POST', body: JSON.stringify({ reason }) }),
  artifact: (artifactId: string) => apiRequest<Artifact>(`/artifacts/${encodeURIComponent(artifactId)}`),
  workflow: () => apiRequest<WorkflowResponse>('/workflow'),
  validateWorkflow: () => apiRequest<WorkflowValidateResponse>('/workflow/validate', { method: 'POST', body: JSON.stringify({ dry_run: true }) }),
  renderWorkflowPreview: () => apiRequest<WorkflowRenderPreviewResponse>('/workflow/render-preview', { method: 'POST', body: JSON.stringify({}) }),
  reloadWorkflow: () => apiRequest<{ reloaded: boolean; validation: WorkflowResponse['validation'] }>('/workflow/reload', { method: 'POST', body: JSON.stringify({}) }),
  diagnostics: () => apiRequest<Diagnostics>('/diagnostics'),
  exportDiagnostics: () => apiRequest<DiagnosticsExportResult>('/diagnostics/export', { method: 'POST', body: JSON.stringify({}) })
};

export function isAuthError(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 401 || error.status === 403 || error.code === 'csrf_required' || error.code === 'unauthorized');
}

export function errorLabel(error: unknown): string {
  if (error instanceof ApiError) {
    return `${error.code}: ${error.message}`;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}
