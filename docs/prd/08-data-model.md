# v1 数据模型草案

> 本文档是实现阶段的 schema 起点，不是最终 SQL migration。v1 不做完整 migration / rollback，但仍需要最小 `schema_version` 判断兼容性。

## 1. 必须表

```text
projects
issues
issue_comments
issue_labels
issue_relations
issue_state_history

workspaces
run_attempts
run_events
approval_requests
tool_calls
handoffs
review_packets

workflow_snapshots
prompt_snapshots
settings
schema_version
```

## 2. 暂不做完整表

```text
audit_log
backup_manifest
migration_history
crash_recovery_leases
supply_chain_findings
```

## 3. projects

字段建议：

```text
id TEXT PRIMARY KEY
name TEXT NOT NULL
repo_root TEXT NOT NULL
project_db_path TEXT NOT NULL
workflow_path TEXT NOT NULL
created_at TEXT NOT NULL
updated_at TEXT NOT NULL
```

## 4. issues

字段建议：

```text
id TEXT PRIMARY KEY
identifier TEXT UNIQUE NOT NULL
title TEXT NOT NULL
description TEXT
acceptance_criteria_json TEXT
priority INTEGER
state TEXT NOT NULL
branch_name TEXT
workspace_path TEXT
base_ref TEXT
base_sha TEXT
created_at TEXT NOT NULL
updated_at TEXT NOT NULL
```

## 5. issue_comments

```text
id TEXT PRIMARY KEY
issue_id TEXT NOT NULL
author_type TEXT NOT NULL
body TEXT NOT NULL
created_at TEXT NOT NULL
```

## 6. issue_labels

```text
issue_id TEXT NOT NULL
label TEXT NOT NULL
PRIMARY KEY(issue_id, label)
```

## 7. issue_relations

用于 blocker / blocks：

```text
id TEXT PRIMARY KEY
source_issue_id TEXT NOT NULL
target_issue_id TEXT NOT NULL
relation_type TEXT NOT NULL
created_at TEXT NOT NULL
```

`relation_type`：

```text
blocks
blocked_by
```

实现时可以只存一个方向，然后查询时反向推导。

## 8. issue_state_history

```text
id TEXT PRIMARY KEY
issue_id TEXT NOT NULL
from_state TEXT
to_state TEXT NOT NULL
actor_type TEXT NOT NULL
reason TEXT
created_at TEXT NOT NULL
```

## 9. workspaces

```text
id TEXT PRIMARY KEY
issue_id TEXT NOT NULL
workspace_path TEXT NOT NULL
branch_name TEXT NOT NULL
base_ref TEXT
base_sha TEXT
status TEXT NOT NULL
created_at TEXT NOT NULL
updated_at TEXT NOT NULL
```

`status`：

```text
created
ready
in_use
conflict
archived
```

v1 不实现 delete / cleanup 主路径。

## 10. run_attempts

```text
id TEXT PRIMARY KEY
issue_id TEXT NOT NULL
workspace_id TEXT
attempt_number INTEGER NOT NULL
status TEXT NOT NULL
codex_thread_id TEXT
codex_turn_id TEXT
session_id TEXT
started_at TEXT
ended_at TEXT
failure_category TEXT
failure_message TEXT
created_at TEXT NOT NULL
updated_at TEXT NOT NULL
```

`status`：

```text
created
running
completed
completed_without_handoff
failed
cancelled
```

## 11. run_events

```text
id INTEGER PRIMARY KEY AUTOINCREMENT
event_uuid TEXT UNIQUE NOT NULL
project_id TEXT NOT NULL
issue_id TEXT
run_id TEXT
type TEXT NOT NULL
severity TEXT NOT NULL
summary TEXT NOT NULL
data_json TEXT
raw_ref TEXT
created_at TEXT NOT NULL
```

事件分类：

```text
system.*
workflow.*
issue.*
scheduler.*
workspace.*
agent.*
codex.*
approval.*
tool.*
handoff.*
review.*
git.*
```

## 12. approval_requests

```text
id TEXT PRIMARY KEY
run_id TEXT NOT NULL
issue_id TEXT NOT NULL
type TEXT NOT NULL
status TEXT NOT NULL
command TEXT
cwd TEXT
file_path TEXT
network_host TEXT
network_protocol TEXT
network_port INTEGER
requested_at TEXT NOT NULL
resolved_at TEXT
decision TEXT
reason TEXT
```

`type`：

```text
command
file_change
network
```

## 13. tool_calls

```text
id TEXT PRIMARY KEY
run_id TEXT NOT NULL
issue_id TEXT NOT NULL
tool_name TEXT NOT NULL
input_hash TEXT
redacted_input_json TEXT
output_hash TEXT
redacted_output_json TEXT
success INTEGER NOT NULL
error_code TEXT
started_at TEXT NOT NULL
ended_at TEXT
```

## 14. handoffs

```text
id TEXT PRIMARY KEY
run_id TEXT NOT NULL
issue_id TEXT NOT NULL
summary TEXT NOT NULL
tests_json TEXT
risks_json TEXT
verification_json TEXT
followups_json TEXT
state_after TEXT NOT NULL
created_at TEXT NOT NULL
```

## 15. review_packets

```text
id TEXT PRIMARY KEY
run_id TEXT NOT NULL
issue_id TEXT NOT NULL
artifact_dir TEXT NOT NULL
review_md_path TEXT NOT NULL
review_json_path TEXT NOT NULL
patch_path TEXT
status TEXT NOT NULL
created_at TEXT NOT NULL
```

`status`：

```text
complete
partial
failed
```

## 16. workflow_snapshots

```text
id TEXT PRIMARY KEY
project_id TEXT NOT NULL
workflow_path TEXT NOT NULL
workflow_sha TEXT NOT NULL
parsed_config_json TEXT
validation_status TEXT NOT NULL
validation_error TEXT
created_at TEXT NOT NULL
```

## 17. prompt_snapshots

```text
id TEXT PRIMARY KEY
run_id TEXT NOT NULL
workflow_sha TEXT NOT NULL
runtime_envelope_version TEXT NOT NULL
context_hash TEXT NOT NULL
rendered_prompt_hash TEXT NOT NULL
redacted_prompt_path TEXT
created_at TEXT NOT NULL
```

## 18. settings

```text
key TEXT PRIMARY KEY
value_json TEXT NOT NULL
updated_at TEXT NOT NULL
```

## 19. schema_version

```text
version INTEGER NOT NULL
created_at TEXT NOT NULL
```

v1 用途：

```text
判断 DB 是否兼容
如果 unsupported，明确提示，不自动迁移
```
