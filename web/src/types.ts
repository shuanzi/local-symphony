export type IssueState =
  | 'Inbox'
  | 'Ready'
  | 'Working'
  | 'Rework'
  | 'Blocked'
  | 'Human Review'
  | 'Done'
  | 'Cancelled'
  | 'Duplicate';

export type RunStatus =
  | 'pending'
  | 'preparing_workspace'
  | 'rendering_prompt'
  | 'starting_agent'
  | 'running'
  | 'completed'
  | 'completed_without_handoff'
  | 'failed'
  | 'cancelled';

export type ApprovalDecision = 'approve_once' | 'approve_for_run' | 'approve_for_session' | 'deny' | 'cancel_run';

export interface IssueRefSummary {
  id: string;
  identifier: string;
  title: string;
  state: IssueState;
}

export interface WorkspaceSummary {
  id: string;
  path: string;
  branch_name: string;
  base_ref: string;
  base_ref_config: string;
  base_sha: string;
  status: string;
}

export interface GitSummary {
  branch_name: string | null;
  base_ref: string | null;
  base_ref_config: string | null;
  base_sha: string | null;
}

export interface RunSummary {
  id: string;
  status: RunStatus;
  attempt_no: number;
  failure_code?: string | null;
  created_at: string;
}

export interface IssueReviewPacketSummary {
  id: string;
  run_id: string;
  packet_no: number;
  status: string;
  created_at: string;
  failure_code?: string | null;
  failure_message?: string | null;
}

export interface Issue {
  id: string;
  identifier: string;
  sequence_no: number;
  title: string;
  description: string;
  acceptance_criteria: string[];
  priority: number;
  state: IssueState;
  url: string | null;
  labels: string[];
  blocked_by: IssueRefSummary[];
  blocks: IssueRefSummary[];
  duplicate_of: IssueRefSummary | null;
  duplicates: IssueRefSummary[];
  followup_of: IssueRefSummary | null;
  followups: IssueRefSummary[];
  dispatch_paused: boolean;
  dispatch_pause_reason: string | null;
  dispatch_paused_at: string | null;
  branch_name: string | null;
  workspace_path: string | null;
  base_ref: string | null;
  base_ref_config: string | null;
  base_sha: string | null;
  workspace: WorkspaceSummary | null;
  git: GitSummary | null;
  latest_run: RunSummary | null;
  active_run_id: string | null;
  latest_run_id: string | null;
  latest_review_packet: IssueReviewPacketSummary | null;
  latest_review_packet_id: string | null;
  created_at: string;
  updated_at: string;
  completed_at: string | null;
  archived_at: string | null;
}

export interface RunAttempt {
  id: string;
  issue_id: string;
  issue_identifier?: string;
  attempt_no: number;
  workspace_id: string | null;
  workflow_snapshot_id: string | null;
  status: RunStatus;
  dispatch_reason: string;
  source_issue_state: IssueState;
  runner_kind: string;
  base_ref_config: string | null;
  base_ref: string | null;
  base_sha: string | null;
  branch_name: string | null;
  failure_code: string | null;
  failure_message: string | null;
  started_at: string | null;
  ended_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface RunEvent {
  seq: number;
  id: string;
  project_id: string;
  issue_id?: string | null;
  run_id?: string | null;
  event_type: string;
  actor_type: 'system' | 'operator' | 'agent' | 'codex' | 'hook' | string;
  data: Record<string, unknown>;
  redacted: boolean;
  created_at: string;
}

export interface Approval {
  id: string;
  run_id: string;
  issue_id: string;
  kind: 'command' | 'file_change' | 'network';
  status: 'pending' | 'approved_once' | 'approved_for_run' | 'approved_for_session' | 'denied' | 'auto_denied' | 'cancelled' | 'timeout';
  action_summary: string;
  risk_level: string;
  policy_match: string;
  requested_at: string;
  created_at: string;
  timeout_ms: number | null;
  expires_at: string | null;
  resolved_at: string | null;
  reason: string | null;
}

export interface Artifact {
  id: string;
  issue_id?: string | null;
  run_id?: string | null;
  review_packet_id?: string | null;
  kind: string;
  path: string;
  mime_type?: string | null;
  size_bytes?: number | null;
  sha256?: string | null;
  redacted: boolean;
  description?: string | null;
  created_at: string;
}

export interface ReviewPacketArtifact {
  kind: string;
  artifact_id: string;
  path: string;
  redacted: boolean;
  content_url: string | null;
  description?: string | null;
}

export interface ReviewPacketSummary {
  id: string;
  issue_id?: string;
  run_id: string;
  packet_no: number;
  status: 'generated' | 'partial' | 'failed' | string;
  root_path: string;
  review_md_path?: string | null;
  review_json_path?: string | null;
  patch_path?: string | null;
  changed_files_path?: string | null;
  untracked_files_path?: string | null;
  diffstat_path?: string | null;
  artifacts?: ReviewPacketArtifact[];
  files?: ReviewPacketArtifact[];
  failure_code?: string | null;
  failure_message?: string | null;
  created_at?: string;
}

export interface WorkflowValidationSummary {
  valid: boolean;
  errors: string[];
  warnings: string[];
  effective_config?: unknown;
  [key: string]: unknown;
}

export interface WorkflowResponse {
  workflow_path: string;
  validation: WorkflowValidationSummary;
  config?: unknown;
}

export interface WorkflowValidateResponse {
  source: 'current_filesystem' | string;
  workflow_path: string;
  validation: WorkflowValidationSummary;
  side_effects: Record<string, boolean>;
}

export interface WorkflowRenderPreviewResponse {
  source: 'effective' | 'candidate' | string;
  rendered_prompt_preview: string | null;
  prompt_metadata?: Record<string, unknown>;
  validation: WorkflowValidationSummary;
  redactions_applied: string[];
}

export interface Diagnostics {
  project_id: string;
  generated_at: string;
  redacted: true;
  repo_root: string;
  database: Record<string, unknown>;
  workflow: Record<string, unknown>;
  daemon: Record<string, unknown>;
  codex: Record<string, unknown>;
  git: Record<string, unknown>;
  redaction: Record<string, unknown>;
  warnings: string[];
  inconsistent_issues: unknown[];
  remediation: unknown[];
  failure_summary: Record<string, unknown>;
  pause_summary: Record<string, unknown>;
  checks: Array<Record<string, unknown>>;
}

export interface Health {
  ok: boolean;
  project_id: string;
}

export interface SessionInfo {
  authenticated: boolean;
  project_id?: string;
  csrf_token?: string;
  csrf?: string;
}

export interface Page<T> {
  items: T[];
  page?: { limit?: number; next_cursor?: string | null; has_more?: boolean };
}

export interface DispatchResult {
  run: RunAttempt;
  issue: Issue;
  workspace?: WorkspaceSummary;
}

export interface DiagnosticsExportResult {
  artifact_id: string;
  path: string;
}
