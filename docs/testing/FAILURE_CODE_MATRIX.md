# Failure Code Matrix

| failure_code | Trigger | run.status | issue.state after outcome | dispatch_paused | Notes |
|---|---|---|---|---|---|
| workflow_invalid | workflow cannot parse/load | failed | source_issue_state | true | Dispatch should fail before runner start. |
| workflow_validation_failed | strict validation fails | failed | source_issue_state | true | Include validation errors. |
| prompt_render_failed | prompt body render fails | failed | source_issue_state | true | Prompt body cannot be empty. |
| workspace_prepare_failed | worktree creation fails | failed | source_issue_state | true | Keep diagnostics. |
| workspace_conflict | existing workspace/path conflict | failed | source_issue_state | true | No cleanup. |
| after_create_failed | after_create hook fails | failed | source_issue_state | true | If configured. |
| before_run_failed | before_run hook fails | failed | source_issue_state | true | Runner not started. |
| codex_startup_failed | codex process cannot start | failed | source_issue_state | true | Real adapter only. |
| unsupported_codex_version | no committed fixture | failed | source_issue_state | true | Must fail before launching real Codex process. |
| codex_protocol_error | JSON-RPC/protocol mismatch | failed | source_issue_state | true | Include version info. |
| turn_timeout | turn exceeds timeout | failed | source_issue_state | true | Terminate process group. |
| stall_timeout | no events/progress | failed | source_issue_state | true | Terminate process group. |
| approval_timeout | approval unanswered | failed | source_issue_state | true | Approval row timeout. |
| command_denied | command policy deny | failed | source_issue_state | true | Security auto-deny terminal in v1. |
| network_denied | network policy deny | failed | source_issue_state | true | Do not claim OS isolation. |
| protected_path_denied | protected path access | failed | source_issue_state | true | Path override wins. |
| tool_gateway_failed | tool IPC/schema/scope error | failed | source_issue_state | true | Persist tool_call failed. |
| missing_handoff | no handoff after continuation | completed_without_handoff | source_issue_state | true | One continuation max. |
| review_packet_failed | packet generation failed | failed | source_issue_state | true | No Human Review transition. |
| operator_cancelled | operator/approval cancel_run | cancelled | source_issue_state unless separately transitioned | true | Token revoked. |
| agent_blocked | issue.block tool | cancelled | Blocked | true | No blocker relation created. |
| issue_state_changed | operator moved issue while active | cancelled | operator target state | true | Reconciliation. |
| canceled_by_reconciliation | active run invalid/stale | cancelled | current valid issue state | true | Startup/tick. |
| daemon_restarted_run_interrupted | stale active DB row | failed | source_issue_state | true | Startup stale-run guard. |
