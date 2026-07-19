# Symphony Local Tracker 扩展规范

状态：Draft v1，语言无关的可选 extension profile

目的：定义 `tracker.kind: local`，使团队可以在不使用 Linear 服务的前提下采用 Symphony 的调度模型。

## 1. 规范性语言与文档关系

本文的 `MUST`、`MUST NOT`、`REQUIRED`、`SHOULD`、`SHOULD NOT`、`RECOMMENDED`、`MAY` 和 `OPTIONAL`
按 RFC 2119 解释。

本文是 [SPEC.md](SPEC.md) 的独立可选扩展，而不是替代品。`SPEC.md` 是上位规范；除非本文明确覆盖，
Local 实现 MUST 满足 `SPEC.md`。选择独立文档而非在 `SPEC.md` 主线散布 `if local` 条件，理由是本地持久化、
fencing lease、恢复和受限 dynamic tool 有不同的一致性与授权模型。独立 profile 能保持通用 tracker 与
Linear conformance 稳定、让覆盖范围可审计，并以最小 diff 保持实现与规范边界清晰。

当 `tracker.kind: local` 时，本文覆盖 `SPEC.md` 中与 Linear transport、credential、in-memory retry、
`linear_graphql` 和 Linear-only test 相关的规定，特别是 Sections 3.2、3.3、5.3.1、6.3、6.4、7.4、
8.4--8.6、10.5、11、14.3、15.5、17.1、17.3--17.5 和 18。`issue` prompt variable、workspace path
safety、agent app-server protocol 及未被明确覆盖的调度规则继续生效。

## 2. 目标、非目标与 Linear 兼容边界

### 2.1 目标

- 提供 durable local issue discovery、reconciliation、comment、state update、PR link 与 follow-up。
- 允许同一 host 上多个 Symphony process 使用同一 database，并保证同一 issue 同时至多一个有效 claim。
- 在 service、worker 或 host crash 后，安全恢复 issue/retry 状态并保留 non-terminal workspace。
- 保持 prompt 可见的 normalized `issue` 模型与 Linear adapter 可移植。
- 只向当前 session 暴露完成其 assigned issue 所需的最小 tracker authority。

### 2.2 非目标

- 不重做 Linear GraphQL、team/project、完整 UI、任意 external tracker sync 或 remote multi-host
  coordination。
- 不把 network filesystem、cloud-sync folder 或共享目录当作 distributed database protocol。
- `SPEC.md` Appendix A 的 SSH Worker Extension 不属于 Local V1；`tracker.kind: local` 下
  `worker.ssh_hosts` 非空时，startup 或 reload MUST 以 `invalid_local_tracker_config` fail-closed。省略或空列表
  表示 local execution；remote worker 需要 Future profile 明确定义 coordinator、workspace locality、tracker tool
  transport 与 agent-storage isolation，MUST NOT 静默退回本机执行。
- 不恢复被杀死的 Codex app-server session；恢复只能在新 claim 下启动新 session。host-derived receipt 只对同一
  tool call identity 的重复投递提供 exactly-once mutation，不把新的 session/run 自动视为旧 call 的重放。
- 不让 orchestrator 决定业务状态、comment 或 PR policy；这些仍由 `WORKFLOW.md` 和 agent tool 负责。

### 2.3 Linear-only 向后兼容

- `tracker.kind: linear` MUST 保持 `SPEC.md` 的 query、credential、retry 与 `linear_graphql` 语义。
- 一个有效 `WORKFLOW.md` MUST 为一个 process 选择恰好一个 `tracker.kind`；不得把 local 与 Linear
  candidate 混合调度。
- Local database 不是 Linear cache。import/export MUST 是显式 operator action，MUST NOT 在 daemon startup
  自动发生。

## 3. Generic `Issue` 与现有 adapter 迁移

### 3.1 领域模型

`SPEC.md` Section 4.1.1 的 `Issue` 是 tracker-neutral 模型。实现 MUST 在 adapter、orchestrator、prompt、
workspace、logging 与 persistence boundary 使用 `Issue` 或 `TrackerIssue`；`Linear.Issue` MUST NOT 是这些
边界的 canonical type。

既有字段 `id`、`native_ref`、`identifier`、`title`、`description`、`priority`、`state`、`branch_name`、`url`、
`assignee_id`、`labels`、`blocked_by`、`dispatchable`、`created_at` 和 `updated_at` MUST 全部保留。Local V1 的
`native_ref` MUST 是非秘密 JSON-safe object，至少为 `{"local_version": positive_integer}`；`assignee_id` MUST 为
`null`。`dispatchable` 仅表达 Local provider 专有的 blocker 资格，不能编码 state 或 label 资格：`Todo` 有任一
非终态 `blocks` predecessor 时为 `false`，其他 Local V1 issue 为 `true`。`id` 是不可变 UUID；`identifier` 是由
database allocator 生成且不可变的 `LOCAL-N`；不得从 title、path 或 agent input 派生。

Local 的 issue row `version` 仅在 `state`、`title`、`description`、`priority`、`labels`、`branch_name` 或 `url`
变更时递增。它不是 `SPEC.md` 通用 `Tracker.Issue` 的字段，MUST NOT 为 Local 擅自扩展该 core struct；adapter
通过 `native_ref.local_version` 暴露它，Local tool 的 read/write result 另以 `data.issue_version` 返回它。comment、
workpad、link、relation 和 lease 各自具有版本或 audit record，MUST NOT 隐式递增 issue row `version`。

### 3.2 Adapter callback 契约

Local adapter MUST 实现当前通用 tracker contract 的以下 callback；不得保留或重新引入旧的 candidate-only、
state-only refresh 或 generic write callback 作为 core contract：

| callback | 成功返回值 | Local 规则 |
| --- | --- | --- |
| `fetch_issues_by_states/1` | `{:ok, [Tracker.Issue]}` | 返回请求 state 的完整 normalized snapshot；空列表返回 `{:ok, []}` 且不得访问 SQLite。不得预过滤 `required_labels` 或 `dispatchable=false`。 |
| `fetch_issues_by_ids/1` | `{:ok, [Tracker.Issue]}` | 返回 supplied opaque ID 的完整 normalized snapshot；空列表不访问 SQLite。不可见/不存在 ID 省略；requested malformed row MUST 使整个调用失败。 |
| `agent_tool_specs/0` | `[map()]` | Local profile 只声明 `local_tracker`；启动或 session binding 未通过 Local isolation preflight 时 MUST 不创建该 Local session。 |
| `execute_agent_tool/3` | provider-native result map | 仅执行 `local_tracker`；Local tool write 直接调用 versioned Local store capability，不经 generic write wrapper。 |
| `secret_environment_names/1` | `[String.t()]` | 返回 Local adapter 声明的 secret environment name；Local V1 返回 `[]`，不得读取 Linear credential。 |
| `validate_config/1` | `:ok | {:error, term()}` | 校验 `tracker.provider` 的 Local fields、路径、状态、deployment fingerprint 与 isolation 前置条件。 |

state query 的结果顺序不构成调度排序保证；scheduler MUST 在 normalization 后统一应用 configured state、
`required_labels`、`dispatchable`、claim、retry 与 concurrency，并按 `SPEC.md` 排序。ID refresh 的成功结果是该
调用的完整 snapshot，不能只返回 state string；caller 通过输入集合与返回 ID 比较处理 omission。Local profile
不得发明 `{issues, missing_ids}` 或以旧 callback 替代上述接口。

Local 业务写入不是 generic adapter CRUD。`execute_agent_tool/3` 和 operator CLI MUST 直接调用共享的 versioned
Local store capability，并执行 claim/fence、CAS、审计和幂等规则；agent 或外部管理接口 MUST NOT 获得 legacy
ambient write context。

迁移顺序 MUST 为：先引入唯一的 generic runtime `Issue` struct，并迁移所有 construction、serialization 和
pattern match；再让 Linear、Memory adapter 适配该 struct；再增加 Local read capability；最后增加 Local V1
claim/write capability 与 dynamic tool。`Linear.Issue` 在兼容 release 中只能提供 conversion function 或
`@type` alias，MUST NOT 保持第二个可在 runtime 构造/pattern-match 的 struct identity。
`memory` MAY 留作 test-only kind，且 MUST NOT 被伪装为 local persistence。config parser MUST 穷尽匹配
supported kind；unknown kind MUST 返回 `unsupported_tracker_kind`，不得 fallback 到 Linear。

`linear_graphql` advertisement MUST 随 selected adapter/session binding 变化。Local profile 不得约束 Linear 的
provider-native tool schema 或 multi-operation 语义；它们由 `SPEC.md` 与 Linear adapter profile 定义，Local 改动
不得静默改变该行为。

### 3.3 Local read/config error 形态与映射

`fetch_issues_by_states/1`、`fetch_issues_by_ids/1`、`validate_config/1` 和 startup lock lifecycle 的失败 MUST 对外
保留以下 logical error shape：`{:error, %{code: stable_local_code, category: portable_category, message: safe_summary,
retryable: boolean}}`。语言原生 tagged error MAY 替代此 literal shape，但 MUST 无损保留这四个字段；scheduler
只依赖 success/failure，operator、log 和 test 可依赖 stable `code`/`category`。`message` MUST 不含 path、SQL、
credential 或 database 内容。

| Local stable code | portable `category` | `retryable` | `message` | 适用场景 |
| --- | --- | --- | --- | --- |
| `invalid_local_tracker_config`、`local_storage_missing`、`local_storage_path_invalid`、`local_storage_permission_denied`、`local_schema_unsupported`、`local_schema_migration_required`、`local_deployment_mismatch` | `invalid_tracker_config` | `false` | `Local tracker configuration is invalid.` | effective Local config、path、schema 或 deployment identity 不可接受 |
| `local_agent_isolation_unavailable` | `invalid_tracker_config` | `false` | `Local tracker agent isolation is unavailable.` | launch/session preflight 无法证明 agent storage isolation |
| `local_storage_busy` | `tracker_request` | `true` | `Local tracker storage is busy.` | SQLite busy timeout；不把它伪装成 remote HTTP request |
| `local_migration_in_progress` | `tracker_request` | `true` | `Local tracker migration is in progress.` | startup 或 ordinary CLI 未取得 required coordination lock；等待后重试，不得自行绕过 lock |
| `local_storage_corrupt`、`local_integrity_check_failed`、requested malformed issue row | `tracker_response` | `false` | `Local tracker storage cannot produce a valid response.` | storage 或无法生成 required normalized `Issue` 的数据失效 |
| `local_schema_migration_failed` | `invalid_tracker_config` | `false` | `Local tracker migration must be repaired.` | operator migration 未完成或失败，runtime 必须 fail-closed |

Local 没有 tracker secret、remote status、pagination 或 provider rate limit；因此 `missing_tracker_secret`、
`tracker_status`、`tracker_pagination` 和 `tracker_rate_limited` 对 Local read/config adapter 不适用，MUST NOT 用它们
包装本地故障；`unsupported_tracker_kind` 由 core adapter selection 在进入 Local adapter 前处理。`local_tracker`
dynamic tool 的 structured result/envelope、claim/CAS 和 input failures 仅按 Section 7 处理，不是本节 adapter
callback error。

## 4. `tracker.kind: local` 配置

### 4.1 字段、默认值和状态词汇

`tracker.provider.database_path`、`tracker.provider.admin_root` 的 `~` 和显式 `$VAR` expansion 及 relative
path resolution MUST 遵循 `SPEC.md` Section 6.1：relative path 相对于选中的 `WORKFLOW.md` 所在目录。

| 字段 | 默认值 | 规则 |
| --- | --- | --- |
| `tracker.kind` | 无 | REQUIRED，必须为 `local` |
| `tracker.provider.store_mode` | `sqlite` | REQUIRED effective value；本版本只支持 `sqlite` |
| `tracker.provider.database_path` | `<WORKFLOW.md-dir>/.symphony/local-tracker.sqlite3` | SQLite database path |
| `tracker.provider.admin_root` | `<database-parent>/admin` | operator import/export/repair 文件的独立 containment root |
| `tracker.provider.create_if_missing` | `false` | daemon 默认不得意外创建空 tracker |
| `tracker.provider.busy_timeout_ms` | `5000` | 正整数 SQLite lock wait 上限，且必须小于 `lease_heartbeat_ms` |
| `tracker.provider.lease_duration_ms` | `120000` | 正整数 durable lease 时间 |
| `tracker.provider.lease_heartbeat_ms` | `30000` | 正整数且严格小于 lease duration |
| `tracker.provider.states` | `[Backlog, Todo, In Progress, Human Review, Merging, Rework, Done, Closed, Cancelled, Canceled, Duplicate]` | 在 Local comparison key 下唯一的完整词汇 |
| `tracker.provider.default_state` | `Backlog` | CLI issue create 的默认值，必须属于 `states` |
| `tracker.provider.followup_state` | `Backlog` | follow-up 默认状态，必须属于 `states` |
| `tracker.provider.close_state` | `Done` | CLI close 默认目标，必须属于 terminal states |
| `tracker.active_states` | `[Todo, In Progress, Rework, Merging]` | core scheduler 的可调度状态，必须为 provider `states` 子集 |
| `tracker.terminal_states` | `[Done, Closed, Cancelled, Canceled, Duplicate]` | core scheduler 的终态，必须为 provider `states` 子集 |
| `tracker.required_labels` | `[]` | core scheduler 字段，使用本节的 Local label comparison/eligibility 规则 |

`instance_id` MUST 由 runtime 在每次 process start 生成并注入，不是 `WORKFLOW.md` 可配置字段；同时运行的
instance ID 必须唯一。`tracker.active_states` 与 `tracker.terminal_states` 的交集 MUST 在 startup 和 hot reload 时报
validation failure，MUST NOT 仅记录 warning。`tracker.provider.states` 内的重复、空白、超过长度限制的名字、任一
`tracker.provider.default_state`/`tracker.provider.followup_state`/`tracker.provider.close_state` 不属于
`tracker.provider.states`、或 `tracker.provider.close_state` 不属于 `tracker.terminal_states`，均 MUST fail validation。

**Local state/label comparison override.** 对 `tracker.kind: local`，定义 `local_compare_key(value)` 为依次执行
Unicode 15.1 NFC、trim、Unicode 15.1 Default Case Folding、再 NFC 的结果。此规则明确覆盖 `SPEC.md` 的通用
state/label lowercase comparison：配置 membership/duplicate 校验、storage uniqueness/lookup、scheduler
lease-eligibility、claim/renew/release/CAS transaction 内的所有 state/label 比较，以及 deployment fingerprint 都
MUST 只比较该 key，不得使用 ASCII lowercase、SQLite `NOCASE` 或 host locale。`tracker.provider.states` 的原始
configured spelling 是唯一的 canonical display spelling；`issues.state` MUST 存储并返回该 spelling，即使输入使用
等价 key。`issue_labels` MUST 存储 `local_compare_key(label)`，并以该值返回 Local normalized label。此 Local
override 不适用于 `tracker.kind: linear`，Linear 继续遵循 `SPEC.md`。
Local provider 的 allowed key 仅为本表所有 `tracker.provider.*` field；未知 provider key MUST 返回
`invalid_local_tracker_config`，不得忽略或透传。Local mode MUST NOT 读取 `endpoint`、`api_key`、`project_slug` 或
`assignee`，也不得建立 tracker network connection：它们在 `tracker.provider` 或 legacy flat `tracker` 路径任一非空
时，唯一结果都是 `invalid_local_tracker_config`。这条白名单不改变 `SPEC.md` 对其他 adapter 的 provider extensibility。

一个 database 只属于一个 Local deployment。`tracker init` MUST 生成不可变 UUID `deployment_id`，并把
`deployment_id`、canonical `workspace.root` 与 `deployment_fingerprint_sha256` 写入 metadata。
fingerprint MUST 是下列固定 JSON object 按 RFC 8785 JSON Canonicalization Scheme (JCS) 编码后的 UTF-8 bytes
的 SHA-256。object 的 key、value type 与 literal `profile_version: 1` 都是协议的一部分，MUST NOT 改为 positional
array、不得省略空 array，也不得使用显示 state spelling：

```json
{"active_states":["in progress","merging","rework","todo"],"admin_root":"/srv/symphony/admin","close_state":"done","default_state":"backlog","followup_state":"backlog","lease_duration_ms":120000,"lease_heartbeat_ms":30000,"profile_version":1,"required_labels":[],"states":["backlog","canceled","cancelled","closed","done","duplicate","human review","in progress","merging","rework","todo"],"terminal_states":["canceled","cancelled","closed","done","duplicate"],"workspace_root":"/srv/symphony/workspaces"}
```

该 exact UTF-8 test vector 的 SHA-256 MUST 为
`305bc71c0932a5d696e57971a1a3612999a5a0cdc4f5e09a4d715db334c394dd`。实现 MUST 用 effective config 替换
示例 path/list/numeric value。state/label string MUST 使用 `local_compare_key`，set-like array 再按 normalized UTF-8
byte lexicographic order 排序；canonical filesystem path MUST 使用 path
normalization 得到的 absolute string 原值，不得 trim、case fold 或做 Unicode normalization。被编码的
database-bound 值为 profile version、canonical
`workspace.root`、canonical `tracker.provider.admin_root`、`tracker.provider.lease_duration_ms`、
`tracker.provider.lease_heartbeat_ms`、按本文规则 Unicode-normalize/trim/case-fold 并按 normalized value 排序的
`tracker.provider.states`/`tracker.active_states`/`tracker.terminal_states`/`tracker.required_labels`，以及 normalized
`tracker.provider.default_state`/`tracker.provider.followup_state`/`tracker.provider.close_state`。同一 database 的所有
daemon/CLI 必须计算完全相同的 fingerprint；
不匹配返回 `local_deployment_mismatch` 并在 workspace scan、claim、cleanup 或 tool advertisement 前 fail-closed。
Local V1 不支持原地 rebind deployment identity；需要这些值变化时，operator 必须停止所有实例并初始化新 database，
或使用未来单独规范的 export/import/rebind 流程。

```yaml
tracker:
  kind: local
  provider:
    store_mode: sqlite
    database_path: .symphony/local-tracker.sqlite3
    admin_root: .symphony/admin
    default_state: Backlog
    followup_state: Backlog
    close_state: Done
  active_states: [Todo, In Progress, Rework, Merging]
  terminal_states: [Done, Closed, Cancelled, Canceled, Duplicate]
```

### 4.2 Path、reload 和启动校验

runtime MUST 将 database/admin path 规范化为 absolute path，并拒绝现有 ancestor/path 为 symlink、database
为 directory/device/FIFO/socket 的情况。database parent 必须可写、非不安全的 world-writable directory，并
支持 SQLite locking。若 database 不存在且 `tracker.provider.create_if_missing: false`，启动 MUST 失败为
`local_storage_missing`；允许 create 时只能初始化新文件，MUST NOT 覆盖无效已有文件。

`tracker.provider.database_path`、其 WAL/SHM/coordination lock 文件和 `tracker.provider.admin_root` MUST NOT 位于
有效 `workspace.root` 内，也不得是其 ancestor；该检查在 path normalization 后执行，失败为
`local_storage_path_invalid`。这避免 agent workspace cleanup、hook 或 repository 内容触及 tracker data。
workspace-operation lock root MUST 固定为 canonical `tracker.provider.admin_root/.workspace-operations`；它不是
workflow 可配置路径，且同样不得位于或成为 `workspace.root` 的 ancestor。runtime 必须 no-follow 地逐级解析并以
restrictive owner/mode/ACL、exclusive-create 创建该目录和其 lock file；已有对象必须重新验证为受限 regular
directory/file，任一 symlink、非预期 type、owner/mode/ACL 或 containment 失败均 fail-closed。该 root 与其中
lock file 属于 tracker-only storage，必须纳入 agent isolation preflight。

`tracker.kind`、任一 `tracker.provider` database-bound field、canonical `workspace.root`，以及上述所有 fingerprint
字段改变 MUST require restart；reload 必须拒绝并保留 last-known-good config。仅
`tracker.provider.busy_timeout_ms` 可在成功 reload 后应用于未来 connection；其他 Local tracker 字段在 V1 不 hot
reload。
所有启动与 reload 校验 MUST 检查
`tracker.provider.lease_heartbeat_ms < tracker.provider.lease_duration_ms`、
`tracker.provider.lease_duration_ms >= 3 * tracker.provider.lease_heartbeat_ms`、lease 至少为 poll interval 的两倍、
`tracker.provider.busy_timeout_ms < tracker.provider.lease_heartbeat_ms`、schema 支持性、`PRAGMA quick_check` 和
`foreign_key_check`。lease renew MUST 由独立 monotonic watchdog 管理；即使 SQLite call、tool turn 或 scheduler tick
阻塞，也必须在无法于 current expiry 前确认 renew 时停止 worker，而不是等阻塞调用返回。

## 5. SQLite 持久化、schema 与 migration

### 5.1 选型和连接要求

Local store REQUIRED 为同一 host local filesystem 上的 SQLite 3 database，使用 WAL。SQLite 提供可检查的
embedded file、ACID transaction 与 cross-process lock，不引入独立 service。本文不规定具体语言 dependency；
各语言实现 MAY 选用适当 SQLite driver；未来的 Elixir implementation 可评估 `exqlite`，但本文不锁定依赖。
NFS、SMB、FUSE、cloud-sync directory 或其他不保证 SQLite lock
语义的 filesystem MUST 在可检测时拒绝，否则 MUST 作为 unsupported deployment 明确记录。

每个 connection MUST 设置 `journal_mode=WAL`、`foreign_keys=ON`、配置的 `busy_timeout` 与
`synchronous=FULL` 或已文档化的等效 durability mode。Local adapter 不需要也不得使用 Linear transport。

### 5.2 Schema version 1

SQLite `PRAGMA user_version` 是 authoritative schema version。所有 timestamp 以 UTC Unix epoch
milliseconds 存储在 `INTEGER` `*_at_ms` column；adapter 输出时转换为 `SPEC.md` 的 timestamp 表示。除非另有
说明，下列 issue foreign key 都是 `ON DELETE RESTRICT`：正常 API 不提供 issue delete，operator purge 必须先
显式处理 dependent record，避免默默丢失审计数据。

| 表 | 必需字段、约束和关系 |
| --- | --- |
| `metadata` | `key` primary key、`value`；至少含初始值为 `1` 的 `next_local_number`、`deployment_id`、canonical `workspace_root`、`deployment_fingerprint_sha256`，MUST NOT 存 `schema_version` |
| `issues` | UUID `id` primary key；唯一正整数 `local_number`；唯一 `identifier`；title、nullable description/priority/branch/url、state、`version >= 1`、created/updated timestamp；Local V1 不存 assignee/project/专用 routing column；`dispatchable` 从 relation/blocker 派生，state/label 资格由 scheduler 处理 |
| `issue_labels` | `issue_id` + `local_compare_key(label)` composite primary key，`issue_id` FK `ON DELETE RESTRICT`；不依赖 JSON1 |
| `issue_relations` | 唯一权威关系表：`from_issue_id`、`to_issue_id`、`kind` in `blocks`/`related`、created timestamp、composite primary key；两个 FK 均 `ON DELETE RESTRICT` |
| `issue_comments` | UUID ID、issue ID、body、`author` display snapshot、不可变 `creator_kind` (`agent`/`operator`/`system`)、不可变 host-derived `creator_subject`、nullable `creator_instance_id`/`creator_run_id`/`creator_session_id`、`version >= 1`、nullable resolved/archived timestamp、created/updated timestamp；FK `ON DELETE RESTRICT`。`author` 不是授权字段；`creator_kind=agent` 时后三个 creator ID MUST 全部非空，其他 kind 的 agent-only ID MUST 为 `NULL` |
| `issue_workpads` | 每 issue 一行：body、`version >= 1`、nullable archived timestamp、updated_by、created/updated timestamp；issue 创建 transaction MUST 同时创建 `body=""`、`version=1`、`updated_by=system` 的 row；archive 后同一 row 由 CAS unarchive/reset，version 继续单调；FK `ON DELETE RESTRICT` |
| `workpad_revisions` | UUID ID、issue ID、workpad version、body、archived flag、actor、timestamp；唯一 `(issue_id, version)`，保留 audit。issue 创建 transaction MUST 同时写入对应的空 body/version 1/system revision |
| `issue_links` | UUID ID、issue ID、kind in `pr`/`related`、URL、`version >= 1`、created/updated timestamp；唯一 `(issue_id, kind, URL)`；FK `ON DELETE RESTRICT` |
| `issue_leases` | 每 issue 一行：永不回退的 `fence_version >= 0`，nullable owner instance/run ID、nullable expiry、更新时间；FK `ON DELETE RESTRICT` |
| `run_attempts` | UUID `run_id` primary key、issue ID、attempt、fence version（唯一 `(issue_id, fence_version)`）、instance ID、deployment fingerprint、canonical workspace path、write-once `workspace_prepare_origin`（`expected_absent`/`preexisting_reusable`）、nullable `workspace_disposition`（`pending` 时仅 `expected_absent` 可为 `NULL`；`ready` 后不可变为 `created`/`reused`）、write-once host-generated opaque `workspace_prepare_intent`（含 nonce 与本 attempt exact intent marker 名）、`workspace_prepare_state`（`expected_absent` 为 `pending -> ready -> delete_pending -> deleted`，仅 missing `pending` 可直接进入 `delete_pending`；`preexisting_reusable` 只可 `ready` 且永不 `delete_pending`）、nullable write-once `workspace_identity`（仅 directory-FD/no-follow 的跨 rename stable directory identity，不含 path inventory 或内容 digest；`preexisting_reusable` 必须在 prepare intent transaction 内写入）、nullable write-once `prelaunch_boundary`（仅 Section 6.4.3 无 child 的 `not_started` recovery 使用的 canonical inventory/digest；`preexisting_reusable` 必须同一 transaction 写入）、nullable write-once canonical `quarantine_path`、nullable `quarantine_state` (`pending`/`completed`；只能由 `NULL -> pending -> completed`)、nullable write-once canonical `deleting_path`、nullable `after_run_state` (`pending`/`completed`，仅 child-start 后的 `after_run` 生命周期使用)、host-generated opaque UUID `launch_idempotency_key`、immutable initial `claim_expires_at_ms`、`launch_phase` (`claimed`/`manifest_writing`/`manifest_written`/`child_starting`/`child_started`/`finished`)、status、immutable expected workspace manifest digest、nullable write-once durable hook-start marker、per-run containment identity/diagnostic 与 child-handle diagnostic、nullable process-group/PID diagnostic、started/ended timestamp、nullable `termination_verified_at_ms`/`termination_method`/`termination_proof_subject`、error；唯一 `(issue_id, launch_idempotency_key)`。prepare intent MUST 在任何 `mkdir` 前持久化 observed origin：仅 probe 到 exact leaf 缺失时写 `expected_absent`/`pending`；仅 probe 到既有 exact reusable identity/boundary 时写 `preexisting_reusable`/`reused`/`ready`，且不得再尝试 `mkdir`。`ready` 后 disposition 不可变。除 `termination_method=not_started` 必须证明 hook marker、containment identity 与 child handle 均不存在外，termination proof 的 subject 必须是 Section 6.4.3 的 containment identity，证明该 run 的 containment 已空；process group/PID 只可诊断，不能构成 proof。recovery 只可按 Section 6.4 的 immutable run/fence + expired lease + safe phase/host evidence CAS 写 proof |
| `retry_queue` | issue ID primary key、attempt、due time、error code、state (`pending`/`consumed`)、idempotency key、updated timestamp；FK `ON DELETE RESTRICT` |
| `idempotency_keys` | issue ID、host-derived key、operation、canonical-argument hash、immutable `outcome`/nullable stable error code、`response_snapshot_json`、created/completed timestamp；唯一 `(issue_id, key)` |
| `audit_events` | monotonic event ID、nullable issue ID、action、actor kind、safe metadata、timestamp；issue FK `ON DELETE RESTRICT` |

`metadata.next_local_number` MUST 在创建 issue 的同一 write transaction 内以 conditional update 递增，并把得到的
旧值用于 `local_number` 和 `LOCAL-N` identifier。两个 concurrent create 不得得到相同 N。`issue_blockers`
表 MUST NOT 存在；`issue_relations(kind=blocks)` 是唯一 blocker authority，adapter 据此生成 normalized
`blocked_by`。

每种 issue create（CLI create 与 `create_followup`）MUST 在同一 transaction 分配 `LOCAL-N`、创建 issue/version 1
与 labels、创建 `fence_version=0` 且 owner/run/expiry 均为 `NULL` 的 lease row、创建 workpad row 和空的 version 1
revision，并写入 audit；`create_followup` 还必须在该 transaction 写 relation 和 receipt。因此 `get_issue` 对任何
V1 issue 都返回 workpad `body=""`、`version=1`，不存在 nullable/missing workpad 状态。首次 `write_workpad` 必须
提交 `expected_version=1`，成功后 row/revision 变为 version 2；CAS 冲突语义与后续更新相同。

`priority` MUST 是 `NULL` 或 `1..4` 整数；数值越小优先级越高，`NULL` 及其他未知值按 `SPEC.md` 排在该 bucket
之后。Local V1 MUST NOT 接受或生成 `0`，以免改变通用 scheduler 的 priority 语义。
state 长度为 `1..64` UTF-8 code point；label 为 `1..64`，每 issue 最多 64 labels。本文的 unsafe control character 指 Unicode `Cc`，但 description/comment/workpad 中经 CRLF
归一化后的 LF 与 TAB 除外；title/state/label/URL 不允许任何 `Cc`。所有约束必须由 database 和 adapter 双重校验。

### 5.3 原子性、版本和 migration

state/title/description/priority/labels/branch/url 的一次 issue row mutation、对应 issue row `version` 增量、
idempotency receipt 和 audit event MUST 在一个 SQLite transaction 内提交。comment、workpad、link、relation
分别只更新自身 version/revision 和 audit，MUST NOT bump issue version。tool receipt 的 key 仅由固定 host
namespace 加 `session/thread/turn/callId` 派生；`operation` 和 canonical argument hash 是同一 receipt 的独立
属性。

canonical argument hash MUST 是 operation 的 `arguments` object 在完成 UTF-8/字段校验、拒绝未知字段、应用
Section 7.2 的 optional default，以及执行该 operation 已规定的 CRLF、state 和 label normalization 后，按
RFC 8785 JCS 编码所得 UTF-8 bytes 的 SHA-256 lowercase hex。未规定 normalization 的 string 必须保留原 byte
sequence；storage 生成的 ID/timestamp 不属于 arguments，`operation` 也不进入 hash，因为它是 receipt 的独立字段。
例如 `create_comment` 输入 `{"body":"line1\r\nline2"}` 经 CRLF normalization 和 default materialization 后的
exact JCS bytes 为 `{"body":"line1\nline2","unresolved":true}`，其 hash MUST 为
`d0ad740e85094523f08945d8070483eb9a9c9f8e8619d5da6212d4a2e80a2864`。

每个成功的 idempotent write MUST 在该 transaction 内保存 immutable receipt：`outcome=success`、完整 canonical
`{"success":true,"data":...}` response snapshot、所有返回 ID/version/timestamp 和完成时间。
`response_snapshot_json` MUST 是 RFC 8785 JCS 编码的 UTF-8 JSON，最大 131072 bytes；实现必须在 mutation 前证明
response 可在该上限内编码，超限返回 `local_idempotency_snapshot_too_large` 且不得提交 mutation。snapshot 是该次写入
提交时的结果，不得在 replay 时重新查询当前 resource；它按 Section 9.2 的 sensitive data 处理，且 MUST NOT 含
raw claim token、host credential 或未请求的 database 内容。输入校验或 claim/CAS conflict 等未产生 mutation 的失败
MAY 不创建 receipt，并按当次当前状态返回。相同 key、operation 和 hash 重放 MUST 仅返回保存的完整 snapshot，
不得产生 mutation 或 audit event；相同 key 但 operation 或 hash 不同 MUST 返回 `local_idempotency_conflict`。因此
crash 后同一 transaction 中 receipt 与 mutation 要么同时可见，要么都不可见；同一 RPC identity 重投时调用方
不必用“当前资源”伪造旧结果。该保证不跨越新的 host call identity：若 app-server/session 已丢失，新的 run 无法知道
未送达 call 的 identity，`create_comment`、`create_followup` 等操作 MAY 在业务层重复。实现 MUST 在 audit/log 中保留
receipt identity 以供 operator 对账，workflow SHOULD 在创建非天然幂等资源前重新读取 current issue；未来若提供跨
run exactly-once，必须持久化由 host 在首次 intent 前分配、可由新 run 安全恢复的 request identity，并另行规范，
不能接受 model 自报 key。Local V1 不声称消除这个 external RPC uncertainty window。

migration MUST forward-only、ordered 和 idempotent。新 database 的 `tracker init` 直接初始化到 version 1；
runtime startup 发现旧的 non-empty supported schema 时 MUST 返回 `local_schema_migration_required`，不得自动
apply pending migration。唯一 migration 路径是 MVP `tracker migrate`。

每个 canonical database path 的 coordination file MUST 固定为同目录下的
`<database-filename>.symphony.lock`；lock path 只由 canonical `database_path` 派生，MUST NOT 受 `admin_root`、cwd
或 instance config 的其他字段影响。runtime 必须以不跟随 symlink 的方式创建/打开该 regular file，并按 Section 9.1
限制 owner/mode/ACL。所有 Local scheduler 在打开 database 前 MUST 取得 non-blocking OS advisory **shared** lock，
并把 lock descriptor 保留到停止 Local mode、所有 worker 退出且不再接受 Local tool write 为止。standalone
`tracker check` 和 `tracker issue *` 命令也必须在打开 database 前取得同一 shared lock，并持有到连接关闭；
`tracker init`、`tracker migrate`、restore 和 repair MUST 取得同一 file 的 non-blocking OS advisory
**exclusive** lock。不能取得即返回 `local_migration_in_progress`，不得等待、不得仅凭 lock file 是否存在判断
scheduler 已停止。exclusive lock 在取得后到 init 或 backup/migration/integrity check 完成且数据库连接关闭前均
不得释放。此协议以 kernel 在 crash/exit 后自动释放的 lock 为准；仅 conforming process 才会参与，直接 SQLite
访问不受支持，必须由 OS 文件权限隔离。

上述 database coordination shared/exclusive lock descriptor 也必须以 `O_CLOEXEC` 或不可继承 handle 打开，并在取得
advisory lock 前复核 noninherit；不得 `dup`、transfer 或传给 child。每条 fork path 必须在执行 hook、app-server、agent、
background/timeout shell 或其他代码前显式 close；每条 exec/spawn path 必须设置并复核 close action。daemon crash 后只能由
daemon 自身 close/exit 使 kernel 释放该 lock，不能由仍存活的 child、background process、PID 或 lock-file presence 推断或
延迟释放。

daemon 的 `tracker.provider.create_if_missing: true` 遇到 missing database 时也必须执行同一 bootstrap authorization，并先取得
exclusive lock 完成 schema/deployment metadata 初始化和关闭连接；随后释放 exclusive lock、重新取得 shared lock
并 reopen/verify，MUST NOT 依赖 non-portable lock downgrade。两个并发 initializer 因此只有一个能创建，另一个在
重新取得 lock 后必须打开并验证已存在 database，不能覆盖或删除它。

持有 exclusive coordination lock 后，命令 MUST 再以 `BEGIN IMMEDIATE` 检查 `user_version`，先以 SQLite backup
API（或等价 consistent snapshot）创建 backup，再逐个应用 pending migration、运行 integrity check、更新
`user_version` 并 commit。该 migration backup 是 REQUIRED，不等同于 optional `tracker backup` command：其 canonical
target MUST 位于 `tracker.provider.admin_root`，不得位于 workspace、system temporary directory 或任意 caller path。
实现 MUST no-follow 地解析每个 parent component，以 exclusive-create 和 restrictive mode/ACL 创建新 regular file，
写完后重新验证 canonical containment、owner、type、mode/ACL 与 snapshot integrity；任一检查失败 MUST 在 migration
前停止。operator 输出可报告 stable backup ID 或相对 `admin_root` 的 safe name，但 MUST NOT 泄露 canonical absolute
path。任何 migration error MUST 整体 rollback、产生 `local_schema_migration_failed`、并阻止 dispatch；不得仅复制 live
main database 而遗漏 WAL。

`quick_check`、`foreign_key_check`、打不开的 file、unsupported schema 或 migration failure 都必须
fail-closed：停止 Local worker、new dispatch 与 Local tool write，保留 database/WAL/SHM，记录
`local_storage_corrupt`、`local_integrity_check_failed` 或对应 error。实现 MUST NOT 用空 database 覆盖它，
也不得从 workspace name 推断 issue state。

## 6. Local adapter、claim、retry 与恢复

### 6.1 Read/write capability 语义

Local V1 没有 assignee、project 或 hidden `assigned_to_worker` routing 条件。`dispatchable` 仅按 Section 3.1 的
blocker 规则计算；state、required label、claim、retry 与 concurrency 仍由 generic scheduler 判断。创建为 active
state 的 issue 同样在下一 poll 被路由；`Backlog`、`Human Review` 和其他 non-active state 不会因“曾经被处理过”
而路由。state routing 只决定候选资格，claim 才是 ownership 决策点。

`fetch_issues_by_states/1` 用于 candidate polling 与 terminal cleanup。它 MUST 返回每个 requested state 的
normalized issue，且不得因 `required_labels` 不匹配、`dispatchable=false`、已有 lease、retry 或其他 instance 而
隐藏 issue。`fetch_issues_by_ids/1` 用于 reconciliation 与 stale-dispatch revalidation，并返回完整 snapshot；caller
自行比较请求/返回 ID，将 omission 作为 no-longer-visible 处理。adapter 不承诺排序；scheduler 按 `SPEC.md` 的
`1..4`、created_at、identifier 规则排序。

blocker 规则保持 `SPEC.md`：默认只在 issue state 为 `Todo` 时，若任一 `blocks` predecessor 非 terminal 则
不得 dispatch。Local profile 不把该规则扩大到 `In Progress`、`Rework` 或 `Merging`；若未来扩展需要扩大，
MUST 新增显式配置和 versioned profile，而不是隐式改变 core 行为。

predecessor 的 state 改变或任一 `blocks` relation add/remove 都必须原子重算受影响 `Todo` target 的 dispatchability，
并 revoke 因此新变为 ineligible 的 matching lease。受影响 closure 在 V1 是该 predecessor 的 direct `blocks` targets
（规则不递归依赖 target 的 dispatchability）：host 先无锁预读 predecessor 与所有 target UUID，去重并按 canonical UUID
bytewise ascending 取得其全部 issue locks；随后才开始 transaction，重读同一 relation/target 集合与相关 state。若集合或
state 相比预读不一致，MUST rollback、释放全部 lock 并重试，不能在持有较高 UUID lock 时临时等待新 target lock。transaction
必须在同一提交中应用 state 或 relation mutation、重新计算每个受影响 target、更新其 derived dispatchability，并清除新
ineligible target 的 matching lease（保留 fence）和写 audit。`blocks` relation add/remove 同样适用；不能只锁 mutation
endpoint、等下一 poll 才发现 target 失去资格。

Local V1 write capability 的 `update_issue` 对 issue row 使用 `expected_issue_version` CAS。`create_comment`、
`update_comment`、`write_workpad`、`archive_workpad`、`add_pr_link`、`create_followup` 使用各自 record version
或要求 expected version。写入前必须验证 host-injected claim context，且返回相关 resource 的当前 version。

对 lease 而言，issue 仅当 state 属于 `tracker.active_states`、不属于 `tracker.terminal_states`、满足
`tracker.required_labels` 且 `dispatchable=true` 时为 **lease-eligible**。每次 agent lease renew 和每次 agent
write MUST 在同一 transaction 中重新读取此 eligibility 与 matching claim 五元组；不得依赖 worker memory 的旧
snapshot。若 issue 已 non-active、terminal 或 unroutable，transaction MUST 不写入 agent mutation，并原子清除仍
matching lease 的 owner/run/expiry、保留 `fence_version`，返回 `local_issue_not_routable`。若五元组已经不匹配，
返回 `local_claim_lost`，不得改动新 owner。

`set_state` 可以从当前 lease-eligible issue 转入任何 mutation 后不再 lease-eligible 的状态，包括 non-active、
terminal，或仍 active 但变为 unroutable（例如带 non-terminal blocker 的 `Todo`）。该次 issue mutation、version CAS、
idempotency receipt、audit event 与 matching lease release MUST 在同一 transaction 提交，且保留 fence；成功后旧 agent
的 renew/write 必须按前段拒绝。operator `tracker issue update` 或 `tracker issue close` 若使 issue non-active、terminal
或 unroutable，也 MUST 在同一 transaction 更新 issue、audit 并 revoke 任何现有 lease（保留 fence）。runtime 观察到
该 revoke 后 MUST 停止对应 worker；lease release 不代表可继续执行外部副作用，也不删除该 run 的受限 finalization
authority。worker 已通过 Section 6.4.3 的 host-enforced containment 确认 app-server、agent 及其 descendant 已退出后，
原 owning runtime 或该节授权的 restart recoverer 只能按 durable `after_run_state` 有限 authority 继续；`after_run` 清空 containment 后，才可在
owner/run/expiry 均为 `NULL`、lease 的 `fence_version` 仍精确等于该 run fence、`run_attempts` 仍为同一未结束 run、且没有
更高 fence attempt 时，以 immutable issue/instance/run/fence CAS 写 `ended_at_ms`、termination proof、attempt status 和
audit。该 post-revoke 路径 MUST NOT 恢复 lease、执行 tracker mutation、创建 retry 或取得新 launch authority；fence 已推进
时必须返回 `local_claim_lost`，不得改动新 owner/attempt。

### 6.2 多实例并发作用域

`agent.max_concurrent_agents` 与 `agent.max_concurrent_agents_by_state` 继承 `SPEC.md` 的 per-instance
orchestrator 语义；每个 Symphony instance 独立计算 slots。相同 deployment 的总并发 MAY 达到各 instance 上限之和。
SQLite lease 只保证同一 issue 在任一时刻最多一个有效 owner，不提供 deployment-wide global concurrency cap。
需要 deployment-wide cap 的实现属于 Future profile，MUST 使用 durable slot/permit authority 并单独规定 acquire、
release、crash recovery 与 observability；不得把 in-memory counter 或 lease 数量误称为该 cap。

### 6.3 Claim 与 fencing lease

`issue_leases` 是跨 process ownership 的唯一 authority；in-memory `claimed` set 只是 cache。有效 claim 由
`issue_id`、`owner_instance_id`、`run_id`、`fence_version`、`lease_expires_at_ms` 五元组表示。`worker_pid`、
process group ID 与 containment 的实现诊断 MAY 记录在 `run_attempts` 中；只有 Section 6.4.3 的 containment-empty
verification 可构成 termination proof，三者均不得作为 ownership proof 或 takeover 条件。

host MUST 在任何 claim intent 开始前生成一个新的 opaque UUID `launch_idempotency_key`。model arguments、prompt、
`WORKFLOW.md` 或其他 config 均不得提供、选择或覆盖该 key。因 response 丢失而无法确认 claim 是否已提交时，runtime
MUST 使用同一 key 重试；不得以新 key 重新 claim。一次新的 scheduler dispatch intent（包括消费下一次 durable retry）
MUST 生成新 key。key 的生命周期从该 intent 开始，到对应 `run_attempts` 被保留的审计期结束；它不是 lease token，也
不得从 model-visible identity 派生。

claim（包括同 key replay）MUST 先取得 Section 6.4 的 per-issue workspace-operation exclusive lock，并在持锁时
查询 `(issue_id, launch_idempotency_key)`。若 row 已存在，runtime 只有在该 row 的
`(owner_instance_id, run_id, fence_version)` 精确等于 current `issue_leases`，lease 尚未过期，且 issue 仍
lease-eligible 时，才可返回该 row 不可变的 `(run_id, fence_version, claim_expires_at_ms)` **可执行** claim result。
任何不匹配、expiry 或不 eligible 都 MUST 返回 `local_claim_lost`；不得推进 fence、创建 attempt、进入 takeover/
launch preflight，或执行任何 filesystem side effect。此 replay check 不是重新取得已过期 claim 的机制。

不存在该 key 的新 claim MUST 在同一 lock 内的一个 write transaction 完成以下 CAS：重新读取 issue eligibility、检查
`retry_queue` gate，锁定其 `issue_leases` row；仅当 owner 为 `NULL` 或 expiry `<= now` 时，将 `fence_version`
增加一，写入新 instance/run/expiry，并以 database-bound workspace root 派生 canonical workspace path，创建包含当前
deployment fingerprint、该 path、`launch_idempotency_key` 与 immutable initial expiry 的
`run_attempts(status=claimed, launch_phase=claimed)`。成功仅返回新的 `(run_id, fence_version, expiry)`；同一时刻
至多一个 transaction 成功。该锁覆盖 eligibility/lease 验证和 CAS，防止验证后由新 fence 接管。
`fence_version` MUST 在 release、retry、terminal cleanup 或 crash 后保留，绝不归零或回退。

上述普通 claim CAS 有一个 REQUIRED 例外：expired non-null owner 对应的旧 attempt 仍为 unfinished
`launch_phase=claimed`，或为 Section 6.4 定义的 safe `manifest_writing`，且该节的 not-started filesystem 条件成立时，
caller MUST 在同一 per-issue lock 中使用该节的原子 recover-claimed CAS；不得先用普通 claim 覆盖旧 owner/fence，再
尝试结束旧 attempt。若 not-started 条件不成立，普通 claim MAY 覆盖 expired tuple，但新 owner 只能先执行完整 takeover
preflight；在 preflight 成功前不得运行 hook、创建 child 或启动 app-server。

renew 与 release CAS 都必须按 Section 6.4.1.1 先取得该 issue lock、再开启 transaction。renew CAS MUST 同时匹配
issue、instance、run、fence、未过期 lease 和当前 lease-eligible state，才可延长 expiry。
不再 lease-eligible 时必须按 Section 6.1 revoke matching lease 并返回 `local_issue_not_routable`。release CAS 必须
匹配相同五元组；成功时只把 owner/run/expiry 置空，保留 `fence_version`。任何 stale CAS 返回
`local_claim_lost`，不得改动新 owner 的 row。所有 session tracker write 同样要求当前五元组与 eligibility，从而以
fencing 拒绝 stale writer。

### 6.4 外部副作用、takeover 和 workspace

database 内的 claim 满足严格的 at-most-one active claim；但 crash 后 workspace、Git remote、Codex 或已启动
child process 的外部副作用只能是 at-least-once replay。lease expiry 不会自动停止旧 process，也不能阻止其
继续访问外部系统；实现 MUST 不把 expiry 声称为外部副作用的排他保证。

#### 6.4.1 Per-issue workspace-operation lock

每个 immutable issue UUID 的 canonical UTF-8 representation 经 SHA-256 lowercase hex 派生一个固定 lock filename，路径为
`<admin_root>/.workspace-operations/<derived>.lock`。host 必须以 directory-FD、`O_NOFOLLOW`（或平台等价机制）
逐级打开该固定 root；首次创建目录和 lock file 时必须 exclusive-create，并验证 restrictive owner/mode/ACL、regular
type、canonical containment 与 stable file identity。已有 lock file 只能在同样验证后打开，随后取得 kernel-enforced
exclusive OS advisory lock；lock file 的存在本身不是 lock ownership，crash/exit 由 kernel 释放 advisory lock。
不得把 lock root 或 lock descriptor 暴露给 agent。任何不支持这些 no-follow、exclusive-create、advisory-lock 或
identity-check 语义的平台 MUST fail-closed，而不是用 process-local mutex 或 lock-file presence 代替。

host 在 open/create 时 MUST 使用 `O_CLOEXEC`（Windows 或其他平台为不可继承 handle 的等价机制），并在取得 advisory
lock 前确认该 descriptor 保持 close-on-exec。lock descriptor MUST NOT `dup`、duplicate、transfer 或作为任意 child 的
inherited descriptor。每条 fork path 必须在 child 执行任何 hook、app-server、agent 或其他代码前显式 close 此 descriptor；
每条 exec/spawn path 必须显式设置 close action 并在 child start 前复核，shell hook 的 background/timeout process 亦然。
因此 hook、app-server、agent、其 descendant 与 daemonized background process 都不得持有该 descriptor。daemon crash 时，
advisory lock 只能由 kernel 基于 conforming host 自己持有的 descriptor 的 close/exit 释放；不得从 child、descendant、PID、
lock pathname 或 lock-file existence 推断 release。

#### 6.4.1.1 Lock order 与 transaction scope

凡可能改变同一 issue 的 lease eligibility、terminal-proof gate、owner/fence/expiry、retry 或 run termination proof、
launch phase，或与之绑定的 cleanup audit 的 transaction，MUST 先取得本节同一个 per-issue exclusive lock，随后才可
开始 SQLite transaction，并持锁至该 transaction commit/rollback 和所授权 filesystem mutation 完成。至少包括 agent/
operator 的 state 与 label mutation、`blocks` relation mutation（两个 endpoint 都适用）、lease revoke/release/renew、
claim/replay/recover-claimed、finalize/retry/run proof/phase，以及 terminal cleanup gate、rename/delete 与 cleanup audit。
普通 read 不得为此无谓取得 per-issue lock；不改变上述值的普通 read-only transaction 也不得借此获得 filesystem authority。

一个 transaction 若会触及多个 issue，host MUST 先按 immutable canonical issue UUID 的 bytewise ascending order 取得所有
对应 lock，再开始该 SQLite transaction，且绝不反序取得或在已持有较高 UUID lock 时等待较低 UUID lock。`create_followup`
必须先生成 immutable child UUID，再把 current parent 与 child UUID 排序后取得两把 lock，随后才在同一 transaction 创建
child、lease/workpad/relation/receipt；这样不会与另一条 relation 或 state transaction 死锁。该顺序使 operator 把 terminal
issue 改回 active 时不能与 cleanup rename/delete 竞态，也使 agent `set_state` 的 matching-lease revoke 不能与 manifest 或
quarantine mutation 竞态。

同一 issue 的下列 host operation MUST 持有这个 lock：claim/replay 和 recover-claimed 的 CAS；takeover preflight 与
quarantine；workspace mkdir/reuse、manifest、`after_create`、`before_run`、`after_run`、`child_starting` 和
`child_started` launch phase；以及 terminal cleanup、`.deleting` rename、`before_remove` 和删除。除
recover-claimed 的 expired-old-attempt gate、cleanup 的 terminal-proof gate 与 Section 6.4.3.1 明确限定的 post-revoke
`after_run` gate 外，持锁 operation MUST 在每个 filesystem mutation 前的同一 SQLite transaction 重新验证 current issue
的 lease-eligibility 与未过期精确 `(owner_instance_id, run_id, fence_version)`；对应的 phase/audit/record CAS 必须在仍持锁时
完成。cleanup 则必须在
持锁时重新验证下文的 terminal proof gate 和没有未过期 lease。验证失败只可返回 `local_claim_lost` 或保留/block，
不得触碰 workspace。lock 只覆盖有限 host operation：一旦 app-server/agent child 已成功启动，runtime MUST 释放 lock，
不得在 agent run、turn、session 或其他长时 child 执行期间持有它。

所有上述 filesystem operation（包括 `.quarantine`、`.deleting` 和 workspace-operation lock root）必须用
directory-FD/no-follow 逐级解析每个 parent component，并在创建、rename 前后重新验证 canonical containment、owner/
mode/ACL、expected type 和 `fstat` identity。目标必须 exclusive-create 或使用不覆盖的 atomic primitive；普通会覆盖
existing target 的 rename 不 conform。任一 symlink swap、identity/type 变化、parent fsync 失败或无法重验都 MUST
fail closed，并保留/block 而不得沿 path string 继续、枚举替代目标或递归删除。

#### 6.4.2 Takeover 与 quarantine

新 owner 的 takeover preflight MUST 在 claim 成功后、任何 workspace hook、Codex process 或 app-server session 启动前，
并在同一 per-issue lock 中完成。它 MUST 从 database 枚举同一 issue 的每个较早 `run_attempts` row，只要该 row 缺少
`termination_verified_at_ms`；不得只检查 latest attempt 或当前 expired lease owner，也不得用 workspace directory
enumeration。对每个 row，runtime MUST 只从其持久化的 `canonical_workspace_path`、per-run manifest path 和已记录的
`quarantine_path` 定位对象；不得从 cwd、新 config 或目录名重推导。stored deployment fingerprint、metadata
fingerprint、canonical workspace root/path、persisted workspace identity，或已有 manifest 的 deployment/run/fence 任一
不匹配时 MUST 返回 `local_deployment_mismatch` 并阻止 launch/cleanup。若仍由当前 runtime 管理旧 containment，可按已知
run/fence best-effort terminate；仅凭复用 PID/process group 不得终止 process。

对每个可能仍在运行、或 manifest run/fence 不匹配的未验证 attempt，当前 matching claimant MUST 使用下列持锁
quarantine protocol。canonical target 固定为同 filesystem 的
`<workspace.root>/.quarantine/<issue-identifier>-<old-fence>`，并且 `quarantine_path` 一旦写入不得改变：

1. 在 rename 前，transaction MUST 以该 old attempt 的 immutable run/fence 和 current claimant 的 matching lease/fence
   CAS 持久化 target 与 `quarantine_state=pending`。已有 `pending`/`completed` row 只能使用其存储 target，不能改名或
   选择新 target；CAS 失败不得进行 filesystem action。
2. 使用 source/target parent 的 directory-FD/no-follow 检查 source containment/type/identity，检查 target 不存在，
   并以同 filesystem 的 atomic no-replace rename 执行移动。实现必须使用具备 no-replace 语义的 platform primitive
   或等价操作；不得先删除、覆盖或以普通 replace rename 竞态模拟。
3. rename 后 MUST fsync source 与 target parent directory，或使用明确文档化且等价的 directory-entry durability
   primitive；随后重新验证 target containment、directory type、identity 及其 manifest/run/fence。最后以 CAS 将同一
   target 的 `pending` 标为 `completed`。任何 DB CAS 或 durability failure 都必须保留 `pending`，阻止 launch。

恢复 `pending` quarantine 只能在 per-issue lock 内检查记录的 source 和 target 两个精确路径，绝不枚举目录或覆盖
target：source 存在且 target 不存在时重试步骤 2；source 不存在且 target 存在且 identity/manifest 匹配 old attempt 时
fsync/revalidate 后 CAS `completed`；source 与 target 都存在、两者都不存在、或任一 identity/manifest 不匹配时必须
block 并返回 `workspace_takeover_required`。`completed` 同样要求 source 不存在且 target 的 identity/manifest 仍匹配，
否则 block。该规则覆盖 pending CAS 前、CAS 后 rename 前、rename 后 parent fsync 前/后及 completed CAS 前/后的 crash；
恢复只能按持久化 state 和上述四种组合进行。quarantine 只提供 namespace 分离，不是权限或撤销边界：旧 process
仍可能通过 cwd、open FD、同一 OS user 或重建 path 访问内容。只有对每个该等 attempt 都已验证 termination，或以
sandbox/path allowlist 验证其与新 run 隔离，新 launch 才可继续；否则 MUST block。retry/continuation 的 B 到 C 仍必须
重检更早且未验证的 A，不能因 B 是 latest attempt 而绕过 A。

#### 6.4.3 Workspace、manifest 和 launch phase

manifest 只含 deployment ID/fingerprint、issue ID、identifier、run ID、fence、instance ID、timestamp 和
`PRAGMA user_version`。每个 attempt 的 manifest path 必须固定为其 stored canonical workspace path 下的
`.symphony-run-manifests/<run_id>.json`，以 directory-FD/no-follow 创建；runtime 不得通过枚举发现它。

workspace prepare 是独立的 durable intent，且在任何 `mkdir` 前必须完成。runtime MUST 在仍为 `claimed`、持有 issue
lock 时，以 directory-FD/no-follow 对 deterministic exact leaf 做无副作用 probe：只有 leaf 缺失才是
`expected_absent`；leaf 已存在时，必须先完成 Section 6.4.3 reuse rule 的 exact identity/boundary 验证，才是
`preexisting_reusable`，其他结果 MUST 在写 intent 或 filesystem mutation 前 block。随后在重新匹配 current unexpired
claim/fence 的同一 SQLite write transaction 中持久化 exact canonical workspace path、host 生成的
nonce/`workspace_prepare_intent`、expected manifest digest 和 observed origin，并 commit：`expected_absent` MUST 写
`workspace_prepare_state=pending`、`workspace_disposition=NULL`、没有 identity/boundary；
`preexisting_reusable` MUST 在同一 transaction 写入 probe 到的 `workspace_identity`/`prelaunch_boundary`，并直接写
`workspace_disposition=reused`/`workspace_prepare_state=ready`。因此既有目录的 reusable identity 是 intent 的初始
durable witness，而非 `mkdir` 返回 `EEXIST` 后的推断；后者不得把 attempt 改为 reuse。`preexisting_reusable` 不得尝试
`mkdir`、不得写 intent marker、不得进入 `delete_pending`，也不得被本 attempt 删除。

对已提交的 `expected_absent` intent，host 在每次 `mkdir` 前 MUST 在 issue lock 内重新匹配 row 的 intent/claim/fence，
并 no-follow 复查 exact leaf 仍缺失；随后只能以 directory-FD/no-follow 的 exclusive `mkdir` 创建该 exact directory。
`mkdir` 返回 existing、或复查后 path 出现，均是 ambiguous ownership，MUST block，绝不能改为 reuse。成功后，以
exclusive/no-follow 写入该 attempt exact intent marker；marker 是该 host intent 的唯一 filesystem witness，不得接受
相近名称或其他 attempt 的 marker。随后捕获 stable identity 与 `prelaunch_boundary`，并以匹配 intent、claim/fence 和
`expected_absent`/`pending` 的 CAS 一并写 `workspace_disposition=created`、identity/boundary 与 `ready`；只有该 CAS
commit 后才可推进 `manifest_writing`。

`expected_absent` 的 `pending` recovery 只能检查 row 中 exact canonical path，绝不枚举或寻找替代目录。path missing 是
安全的未创建结果，可在新的 matching recovery authority 下重试 exclusive `mkdir`，或按本节后文进入 missing
`delete_pending`；path existing 时必须 directory-FD/no-follow 验证 containment、directory type、owner/mode/ACL 与
stable identity。仅当存在本 attempt exact intent marker，且目录只含该 marker 和/或本 attempt exact manifest artifact，
才可捕获 identity/boundary 并 CAS 为 `created`/`ready`。marker 缺失时，即使目录为空，也 MUST 保留并 block；它不能被
归属、删除或作为已有 workspace 自动 reuse。任何其他 entry、marker、manifest、type/identity/authorization 不匹配同样
MUST block。`preexisting_reusable` 不得处于 `pending`；若数据库中出现该组合，MUST fail-closed。

`workspace_identity`
只保存 stable directory identity：POSIX 至少为同一 filesystem 上 no-follow `st_dev`/`st_ino`；其他平台必须使用 OS
提供、存活期间跨 rename 稳定且绑定 volume/filesystem 的 file identity。pathname、PID、process group、ctime、应用自造
随机值和任何内容 digest 都不是它的组成部分。`prelaunch_boundary` 另行保存 manifest 写入前的 no-follow canonical
inventory/digest；它只可用于下文没有 hook marker、containment identity 或 child handle 的 `not_started` recovery，
不得作为正常运行或 terminal cleanup 的内容完整性 gate。无法取得所需 stable identity 或 boundary 的平台 MUST fail-closed。

因此 crash 在 prepare intent commit 前没有获授权的路径；`expected_absent` commit 后、`mkdir` 前由 `pending`+missing
安全恢复；`mkdir` 后、marker 前的 existing path 一律 preserve/block；marker 后、`ready` 前只能归属该 marker/本 attempt
exact manifest artifact；`ready` 后按持久化 identity/boundary 恢复。`preexisting_reusable` commit 后从未具有 created
authority。每一步都重新验证 current claim/fence。只有 runtime 以 `expected_absent`、exact marker 与 stable identity
共同证明的 `created` incomplete path 可由 recover-claimed 删除。`after_create` 与 `before_run` 是 host-initiated workspace side
effect，必须在锁内、在执行前后重验相应 current claim/fence；claim 已失去时不得运行。`after_run` 仅按
Section 6.4.3.1 的 limited post-revoke authority 执行。`after_create` 与 `before_run` 的顺序由下段 launch phase 规定，
`before_remove` 适用下节 terminal-proof gate。

runtime MUST NOT 先写 manifest 再推进 phase。`workspace_prepare_state=ready` 后，持有 lock 且匹配 current unexpired
claim/fence 时，它才可 CAS `claimed -> manifest_writing`。随后仅以 restrictive permission 的 exclusive/no-follow atomic
write 创建该 attempt manifest，fsync file 与 parent，并读回验证 exact bytes/digest/identity；只有成功验证后才可 CAS
`manifest_writing -> manifest_written`。若 target manifest 已存在，runtime 只能 no-follow 读回并验证它精确匹配该 attempt
的 expected digest/identity；任何不匹配、malformed 或 type change MUST block 并返回 `workspace_takeover_required`，不得
覆盖。每次 `manifest_written -> child_starting` CAS 必须先于任何可创建 child 的 hook、containment 或 syscall。
`after_create` 和 `before_run` 都是任意 shell hook，runtime MUST 视为 child-capable：只能在该 CAS 后、仍持同一 lock 且
重新匹配 current unexpired claim/fence 时运行；它们不得在 `child_starting` 前启动、间接启动或请求启动 agent/app-server/
其他 child。紧随其后的 containment 建立、launch syscall 与 `child_started` CAS 都必须持同一 lock 并重新匹配 current
claim/fence；取得 app-server handle 后立即释放 lock。expired 或 stale owner 不得运行 hook、建立 containment、写 manifest、
创建 child 或启动 app-server。

##### 6.4.3.1 Per-run process containment 与 termination proof

在执行任何 child-capable hook 或启动 app-server 前，host MUST 为该 `(run_id, fence_version)` 建立并持久化 immutable
per-run containment identity；Linux 可用独立 cgroup、Windows 可用禁止 breakaway 的 Job Object，或使用具备相同 descendant
accounting、kill 与 empty-query 语义的 container/platform primitive。`after_create`、`before_run`、`after_run`、app-server、
agent 及所有 descendant 都必须被加入该 containment；daemonize、`setsid`、重新 parent、breakaway 或其他逃逸不得使 process
离开它。process group/PID 只作诊断，不能替代 containment identity、termination 或 takeover evidence。无法在 launch 前
建立/验证这一 host-enforced containment，或无法阻止其 escape，MUST fail-closed 为 `local_agent_isolation_unavailable`，
不得启动 hook/app-server 或 advertise Local session。

host 必须在第一个 hook 启动前以持锁 CAS 写 durable hook-start marker，并在 app-server handle 可得时写 child-handle
diagnostic；这两个记录只可增写，不能以 crash recovery 清空。`manifest_writing`、`manifest_written`，以及尚未建立
containment 或启动 hook 的早期 `child_starting`，都仅在 hook marker、containment identity 与 child handle 三者均不存在
时可适用 not-started recovery；任何一个记录存在都使其成为不确定 launch evidence。

每个 hook 的 primary process return 或 timeout 后，host MUST terminate 并 wait 该 hook 留在 containment 内的全部 residual
background descendant，直到 containment 对该 hook 已空；hook failure/timeout 不得留下可继续运行的 child。正常 app-server/
agent descendant 退出后，host 必须先在 issue lock 内把 `after_run_state` durable CAS 为 `pending`。`after_run` 在同一
containment 内运行并按相同规则清空；成功或失败/timeout 后都只能由 host 完成 `pending -> completed`。若 agent `set_state`
或 operator mutation 已 revoke matching lease，原 owning runtime 仍只有有限 post-revoke authority：在 issue lock 内精确证明
same run/fence、lease owner/run/expiry 都为 `NULL`、没有更高 fence attempt，且 app-server/agent descendant 已空后，才可
at-least-once 执行 trusted `after_run`。它不得恢复 claim、启动 app-server/agent、取得 tracker write authority 或创建 retry；
若存在更高 fence attempt，MUST 不运行 hook，也不得触碰新 attempt workspace。after_run 的 residual 清空后，host 才可在
同一 lock 的 CAS 中写 `after_run_state=completed`、`termination_verified_at_ms`、termination proof、attempt finished 和 audit；
post-revoke 路径不得写 retry。仅当 host 可查询并证明本 run containment 中所有 hook/app-server/agent descendant 均为空时，
才可写 `termination_verified_at_ms`；`termination_proof_subject` 必须精确引用 stored containment identity。`before_remove`
是 terminal cleanup scope 的 child-capable hook：它必须在独立、同样 host-enforced cleanup containment 中运行，return/
timeout 后清空该 containment，随后才可 delete；它不得重新开启或替代已完成 run 的 termination proof。无法
terminate/wait/empty-query 时不得写 run proof、不得 retry/release/cleanup，并按 `local_agent_isolation_unavailable` fail-closed。

daemon crash/restart 不得使 `after_run_state=pending` 永久依赖已消失的 runtime。scheduler MUST 在同一 issue 的任何新
claim/fence CAS 前，在 per-issue lock 内优先发现并恢复该 pending row；它不得在 pending `after_run` 未按本段完成或
fail-closed block 前推进 fence。restart recoverer 只能针对唯一 `(issue_id, fence_version)` 的 exact `run_id`，并在持锁
transaction 中同时验证：row 仍为 unfinished 且 `after_run_state=pending`；其 stored containment identity、workspace path/
identity 与 manifest 都精确匹配；`issue_leases.fence_version` 精确等于该 run fence，且 lease 要么 owner/run/expiry 全为
`NULL`（已 revoke），要么仍为该 exact run 但已过期；不存在更高 fence attempt，且不存在 target 以外缺少
termination proof 的危险 attempt。未过期的旧 lease、partial lease tuple、任一 mismatch 或更高 fence 都 MUST 拒绝
replay/complete，绝不写该 stale row 或触碰后续 attempt workspace。

符合上述 predicate 时，recoverer MUST 先以 stored exact containment identity terminate/wait 任何 residual，并重新查询
确认旧 containment 已空；随后必须能够重新打开同一 durable containment，才可在其中 at-least-once 重放 trusted
`after_run`。若 containment 不能重新查询、重开或清空，MUST fail-closed/block，不得以新 containment、PID 或 process
group 代替。recoverer 在 hook 执行至返回/timeout、清空该 containment 和最终 transaction 期间必须保持 issue lock；最终
transaction 再次匹配前段 exact predicate 与 `pending`，才可 CAS `pending -> completed` 并写 termination proof、attempt
finished 与 audit。若 crash 发生在 hook 副作用后、该 CAS 前，下一 recoverer 必须按同一规则重放，故语义为 at-least-once。
已 revoke 路径不得创建 retry；仅 exact expired、未 revoke lease 的正常退出恢复可按 Section 6.5 在该 final transaction
写 continuation retry 并 release 该 exact expired tuple。这样，健康 restart 最多等待旧 lease expiry 后即可恢复；不安全
证据显式返回 `workspace_takeover_required` 或 `local_agent_isolation_unavailable`，而不是静默挂起 pending state。

只有旧 attempt 为 unfinished `claimed`，或为没有 hook marker、containment identity 或 child handle 的
`manifest_writing`、`manifest_written` 或早期 `child_starting`，recovery 才可评估 `termination_method=not_started`。
这是 `prelaunch_boundary` 的唯一比较时机。对 `created`，`ready` path 必须同时精确匹配 canonical path、stable
`workspace_identity`、`prelaunch_boundary`，且仅允许本 attempt exact manifest artifact 相对 boundary 的已知变化；`pending`
则只能按上段的 exact-path ownership 规则处理。对 `reused`，directory identity/path、`prelaunch_boundary` 与本 attempt
per-run manifest missing/exact-digest 必须匹配，才可标记 `not_started`，但 MUST 保留 workspace，绝不 delete、
`delete_pending` 或 quarantine。任何 identity/boundary/path/manifest mismatch、malformed/type change 或
hook/containment/child evidence 都必须 block 或按 takeover quarantine，而不得推断未启动。先前 run 的 manifest 或 workspace
artifact 是 reused boundary 的既有内容，不是当前 attempt 的 side effect，不能据此删除或 quarantine reused workspace。

仅 `workspace_prepare_origin=expected_absent` 的 not-started deletion 可以是两阶段操作，且 filesystem delete 绝不能发生
在未提交的 SQLite transaction 内。首先，runtime 在 per-issue lock 内以独立 write transaction 精确匹配旧
issue/owner instance/run/fence、`expiry <= now`、safe phase 与没有更高 fence attempt；`ready` 仅在
`workspace_disposition=created` 且 no-follow 验证 exact canonical path、stable identity 与 exact marker 后，才可 CAS 为
`delete_pending` 并绑定待删 directory 的 exact identity。`pending` 只在 exact path 仍 missing 时才可进入
`delete_pending`，transaction 必须绑定该 exact canonical missing subject 而非伪造 directory identity；任何 existing
`pending` path（包括空目录）均 MUST preserve/block，不能删除。随后 commit。其次，持 lock 以 directory-FD/no-follow bounded
deletion walker 删除该 exact identity/path，并 fsync parent directory。最后才开启新 transaction，重新匹配
`delete_pending` subject，将 state CAS 为 `deleted`，写 attempt `finished`、`not_started` proof 与 audit，并在同一 final
transaction 中按 eligibility/retry gate 创建新 claim 或 release old lease。path existing 时 `delete_pending` recovery 只能在
identity 仍精确匹配时重试 delete；path missing 时只能完成 `deleted`/finalize；其他任何 state/path/identity 组合都 MUST
block。因而 crash 或 SQLite rollback 发生在 first CAS、delete、parent fsync 或 final CAS 的任一边界时，重启都只能沿
`delete_pending` 的精确 subject 继续，绝不能删除替代目录。`reused` not-started recovery 只完成 attempt/proof/audit/new
claim-or-release transaction 并保留 workspace。并发 recoverer 必须在取得 lock 与 write ownership 后重新匹配，失败者不得
再触碰 workspace；该流程不要求 owner 为 `NULL` 的中间状态，也不得声称 recovery instance 是旧 matching owner。
只有 hook marker、containment identity 或 child handle 任一存在的 `manifest_written`、`child_starting` 或 `child_started`，
以及没有 exact expected manifest 的上述 phase，才必须 quarantine/block takeover，不得推断 child 未启动。missing manifest
对 `claimed` 或符合上段条件的 `manifest_writing` 是 not-started evidence；对 `manifest_written`/`child_starting` 则必须有
exact manifest 才是 not-started evidence。若返回 `workspace_takeover_required`，runtime MUST 在持锁且 matching claim/fence
的 transaction 中把本次尚无 hook marker、containment identity 或 child handle 的 preflight `run_attempts` 标记
`blocked_prelaunch`、写已知的 `not_started` proof、durable failure retry、audit 并 release claim；旧 attempt 的不确定 launch
evidence 不得被该 proof 覆盖。失去 claim 时只返回 `local_claim_lost`，不得改动新 owner。retry 到期后的新 claim必须重新执行
完整 preflight；issue 变为 non-active/terminal 时不得继续创建 retry。

#### 6.4.4 Terminal cleanup

startup MUST 验证 storage 后扫描 normalized `workspace.root`，不 follow symlink，reconcile local issue state 和
lease 后再 launch。non-terminal workspace 不因 crash 删除。terminal cleanup MUST 在 per-issue lock 内重新验证下列
terminal-proof gate；任一条件不满足时 MUST NOT 删除，只能保留或按本节 quarantine：

1. storage read 已成功直接按 manifest immutable `issue_id` 查到同一 issue，且该 issue 目前在 configured
   `terminal_states`；不得只按目录名或 identifier 推断。
2. workspace、per-run manifest 与所有待检查 parent 都在 canonical `workspace.root` 内且经 directory-FD/no-follow
   验证，并精确匹配该 attempt persisted stable `workspace_identity`、canonical path 与 manifest evidence；canonical path
   只可经已持久化的 `quarantine_path` 或 `deleting_path` 所记录的受控 no-replace rename 改为该 target。任一 symlink、
   缺失、malformed、type/identity 变化，或 manifest issue ID/identifier 不匹配时，MUST 保留或 quarantine。terminal cleanup
   MUST NOT 比较 `prelaunch_boundary`：agent 正常修改 workspace 内容不构成 cleanup mismatch。
3. `issue_leases` 没有未过期 owner，manifest 的 run/fence 精确匹配 `run_attempts`，该 attempt 已有 `ended_at_ms` 和
   matching owner 或本节 authorized recovery 写入的 termination proof。runtime 管理中的 worker 必须先按 run ID/fence
   终止并验证其 stored containment 已空，再原子写 proof；缺失 proof 时即使 lease 已过期、PID/process group 看似不存在
   或 issue 已终态也必须保留或 quarantine。

通过 gate 后，runtime 必须先在持锁 transaction 中以 write-once CAS 把同 filesystem 的唯一 `.deleting` target 持久化为
该 attempt 的 `deleting_path`，再以 directory-FD/no-follow、atomic no-replace rename 将 workspace 移至该 target，fsync
source/target parent，并在 renamed directory 内重新验证
containment、type、identity、manifest 与同一 terminal-proof gate；不得对原路径作递归 fallback 删除。只有该重验成功后
才可在 renamed directory 中运行 `before_remove`，随后以 no-follow deletion walker 尝试删除。hook failure/timeout 必须
记录并忽略，但 symlink/type/identity/lease/proof 重验失败必须停止并保留 `.deleting` entry。crash、删除失败或 gate
失败时保留 entry；startup 只能从该 row 的 persisted `deleting_path` 取得 exact target、再次取得该 issue lock 并重做上述
验证后恢复 hook/delete，不能仅凭 entry 位于 `.deleting` 删除。`.quarantine` entry 在 Local V1 永不自动删除，只能由授权 operator 检查后处理；unknown 或 malformed
workspace 必须保留或 quarantine，不能删除。cleanup 完成后可在锁内写 audit 并移除 terminal issue retry entry，但不得
删除 database issue、lease fence、run attempt 或 workpad/comment 审计记录。

### 6.5 独立 `retry_queue`

retry MUST 不存储在 lease row。worker abnormal failure 时，一个 transaction MUST：在 Section 6.4.3 containment
empty verification 后写 termination proof、结束对应 `run_attempts`、以
失败 attempt 与 error 写入/更新 `retry_queue(state=pending, due_at_ms)`、写 audit、再按 matching fence release
claim。failure backoff 复用 `SPEC.md` 的 `min(10000 * 2^(attempt - 1), max_retry_backoff_ms)`。正常 worker exit
也 MUST 先确认 containment termination，再在一个 matching claim/fence transaction 中标记 attempt finished、
写 termination proof/audit、按 issue current eligibility 决定是否写 continuation retry，并 release claim；issue 仍
lease-eligible 时 `due_at_ms = now + 1000`，terminal/non-active/unroutable issue 不创建 retry。事务失去 claim 时不得
修改新 owner。若 lease 已因 Section 6.1 的 state/operator mutation 被 matching-fence revoke，worker MUST 改用该节
限定的 post-revoke finalize；它只结束 attempt/写 proof/audit，不创建 retry。由此 continuation 的 new claim 可在一秒后
取得 owner 为 `NULL` 的 lease，不必等待旧 expiry，终态 cleanup 也可取得 termination proof。

`retry_queue` 是 durable retry source of truth；in-memory `claimed` set、timer 和 wakeup 仅是 cache。claim transaction
MUST 拒绝 `retry_queue` 中 `pending` 且 `due_at_ms > now` 的 issue；当 due 到达且新 claim 成功，在同一 transaction
将其标为 `consumed` 或删除并建立新 `run_attempts`。每个 tick MUST 从 database 的 due gate 恢复候选 retry，不能只等
遗失的 local timer；写入 failure/release transaction 成功后 MUST 移除对应 instance 的 claimed/timer cache，避免 durable
retry 被 stale cache 永久阻塞。因此重启后 retry 仍有效，多个 instance 只能有一个消费该 gate。claim 前 storage
unavailable 时不得 launch；`local_storage_busy` 超过 timeout 时本 tick 跳过 dispatch。renew 无法确认时 worker MUST
在自身 expiry 前终止。

## 7. Codex app-server dynamic tools

### 7.1 advertisement、executor context 与幂等 key

`agent_tool_specs/0`、app-server session binding 与 `execute_agent_tool/3` MUST 按 selected tracker kind 改造。
每个 app-server session MUST 绑定启动时验证过的一个 adapter、effective provider config 和其 tool set，reload 不得
改变 in-flight session 的 adapter 或 credentials。`tracker.kind: linear` 的 provider-native tools 继续由
`SPEC.md` 与 Linear adapter profile 定义；`tracker.kind: local` MUST NOT advertise `linear_graphql`，也不得读取
Linear credential 或发起 network request。通过 isolation preflight 的 Local session 必须且只能 advertise
`local_tracker`。如果 agent 在 local session 请求 `linear_graphql`，executor MUST 返回
`{"success": false, "error": {"code": "unsupported_tool_for_tracker_kind", "message": "This tool is unavailable for the local tracker."}}`
并继续 session。

host MUST 将 current `issue_id`、`owner_instance_id`、`run_id`、`fence_version`、lease expiry 和 targeted
app-server `session/thread/turn/callId` 注入 `local_tracker` executor；model-provided arguments 不得携带或覆盖
这些字段。host context 还 MUST 绑定到 runtime 当前注册且未退出的 session，而不是仅信任历史 `session_id` string。
对 mutation，executor MUST 先验证不可伪造的 host `issue_id` 与 session/thread/turn/call identity，并由固定 host
namespace 加 `session/thread/turn/callId` 派生 receipt key；agent 不得提交 raw `idempotency_key`。这一 receipt identity
只覆盖 app-server 对同一 call 的重复投递；新的 turn/session/run 会产生新 key，Section 5.3 的跨 run uncertain-outcome
限制适用。

对 mutation，executor MUST 先以 key、operation 和 canonical arguments hash 查询 completed receipt：exact match MUST 直接
replay immutable snapshot，即使原 mutation 已使 issue release lease；同 key 但 operation/hash 不同 MUST 返回
`local_idempotency_conflict`。这是唯一不要求 current claim 的 tool result，且不得重新读取资源或产生 mutation/audit。
所有其他 operation（包括 `get_issue` 与 `list_comments`）MUST 在同一 storage transaction 验证 current unexpired
matching claim 五元组、current lease-eligibility，以及仍绑定该 targeted session/thread/turn/call context。revoke、claim
loss、expiry 或 session exit/unregister MUST 立即使 reads 和新的 writes 返回 `local_claim_lost` 或
`local_tool_context_required`；不得使用 worker cache、历史 session identity 或任何 grace period。只有没有 matching
completed receipt 的新 mutation，才可在上述验证后执行。缺少、失效或不匹配的 host context MUST 返回
`local_tool_context_required` 或 `local_claim_lost`，并且不得执行新 write。受信任的 non-model caller MAY 经独立管理 API 提供 explicit request key；
host 必须加固定 namespace、校验长度，并同样持久化 operation/hash；同 key 不同 payload 必须 conflict。

Local session 只暴露一个 tool name：`local_tracker`。下表中的名称都是其 `operation` 值，不是
`local_tracker.get_issue` 一类的独立 tool。tool input MUST 是
`{"operation": "<name>", "arguments": {...}}`，unknown top-level/argument field MUST reject。adapter 的 canonical
Local result 为结构化 `{"success": true, "data": {...}}` 或
`{"success": false, "error": {"code": "<stable-code>", "message": "<safe-summary>"}}`；failure MAY 额外携带
`data`。AppServer protocol envelope MAY 额外投影为其要求的 `output`、`contentItems` 或等价字段，但不得替换、
扁平化或丢失 Local 的 `data`/`error` 语义。CAS conflict MUST 提供
`data.current_version`，并在适用时提供 `data.current_resource`。
所有 operation 的 authority 必须以 current issue 为根；`create_followup` 是唯一可创建其他 issue 的 operation，且每次
成功调用只能创建一个 child 和本节规定的 current-to-child relations，不得取得任意 issue mutation authority。

### 7.2 固定输入 profile 和操作

以下默认上限是 conformance profile：所有字段 MUST 为 valid UTF-8。title `1..256` code point，state/label
`1..64`，URL `1..2048` bytes，且这四类字段 MUST 拒绝 NUL 与全部 unsafe control character。description
`0..16384` bytes、comment body `1..16384` bytes、workpad body `0..65536` bytes 可以包含 LF/TAB；CRLF MUST
归一为 LF，并拒绝 NUL、非法 UTF-8 与其余 unsafe control character。label 至多 64 个，priority 仅 `1..4` 或
`null`，page limit 为 `1..100`，默认 50。PR URL v1 MUST 为 absolute `https` URL；不提供 loopback exception。

| `operation` | `arguments` | `data` 与写入语义 |
| --- | --- | --- |
| `get_issue` | 无 | 完整 normalized issue（含 `native_ref.local_version`）、`data.issue_version`、完整 active/archived workpad body/version、links 和 relations |
| `list_comments` | optional `cursor`、`limit`、`include_archived` | 按创建顺序分页的 comment：ID、body、author、version、`unresolved`、archive/resolution timestamp、`next_cursor` |
| `create_comment` | `body`、optional `unresolved` | 创建 agent-author comment，返回 ID/version/timestamp |
| `update_comment` | `comment_id`、`expected_version`，以及 `body`、`unresolved` 或 `archive: true` 之一 | creator run 可改 body/resolution/archive；同 issue 的 later run 只可 resolve 或 archive；CAS 返回当前 version |
| `write_workpad` | `expected_version`、`body` | 更新或 CAS unarchive 唯一 `## Codex Workpad` row，写 revision，返回 body/version/archive 状态 |
| `archive_workpad` | `expected_version` | CAS archive 唯一 workpad row，保留 revision/audit；rework 以 `write_workpad` 在同 row unarchive/reset，version 继续单调 |
| `set_state` | `expected_issue_version`、`state` | issue row CAS，返回完整新 issue 与 `data.issue_version` |
| `link_pr` | `url` | 添加或以 URL 去重的 `pr` link，返回 link ID/version/URL |
| `create_followup` | `title`、optional `description`、`priority`、`labels`、`blocked_by_current` | 事务创建 child、返回完整 child 和 relation 列表 |

`get_issue` 与 `list_comments` 虽没有 mutation arguments，仍 MUST 使用 Section 7.1 的 current unexpired matching
claim、lease-eligibility 和 live bound session context；它们不是 release 后可用的 read-only capability。
`get_issue.arguments` MUST 是空 object。`list_comments` 的 `limit` 默认 50，`include_archived` 默认 `false`；结果按
`(created_at_ms, id)` 升序，opaque cursor 表示上一页最后一组 tuple，并绑定 current issue 与
`include_archived`。malformed、跨 issue 或参数不匹配的 cursor MUST 返回 `local_tool_invalid_arguments`。当没有下一页时
`next_cursor` MUST 为 `null`。`create_comment.unresolved` 默认 `true`；`update_comment` 必须恰好提供 `body`、
`unresolved` 或 `archive: true` 之一，creator run 的 `unresolved=false` 设置 resolution timestamp、`true` 清除它；
later run 仅可使用 `unresolved=false` 或 `archive: true`，archive 后不得再更新。`create_followup` 的 optional default
为 `description=null`、`priority=null`、`labels=[]`、
`blocked_by_current=false`，state 必须取 `tracker.provider.followup_state`。`link_pr` 命中相同 validated URL byte string 时 MUST
返回既有 link 且不新增 row/audit。state input 必须以 `local_compare_key` 匹配 configured state，并按
`tracker.provider.states` 中的 canonical spelling 存储。

`list_comments` MUST 返回 body，而不是仅 summary；`get_issue` MUST 返回 workpad body，使 rework agent 能读取
reviewer feedback。`create_comment` 必须把 host 注入的 `creator_kind=agent`、`creator_subject`、instance/run/session
identity 写入不可变 comment creator 字段，model 不得提供或覆盖它。这里的 `creator_session_id` 是创建 call 的
`SPEC.md` live session snapshot (`<thread_id>-<turn_id>`)，用于 provenance，不是后续授权 gate。`update_comment` 除了
claim/CAS，还 MUST 对 body update 精确匹配不可变 `creator_subject`、`creator_instance_id` 与 `creator_run_id`；同一
run 的 later turn MAY 更新该 comment。新的 valid claim/run 对 current issue 的旧 agent comment MAY 只提交
`unresolved=false` 或 `archive: true`，不得改 body、不得重新设为 unresolved；mutation audit 必须记录原 creator 与
resolving run。operator/system comment、其他 issue 的 comment 永远不是 agent 可更新的对象。operator 可通过
`tracker issue show --include-content` 定位残留 comment；MVP CLI 不提供 comment mutation，不能由 new agent 冒充旧
creator 改写正文。
除 exact completed mutation receipt replay 外，comment/workpad/link/follow-up、`set_state` 以及两个 read operation
都需要 valid host claim/session context；claim loss、expiry、session exit、revoke 或 storage fail-closed 时必须立即拒绝。
`set_state` 同样需要 issue CAS。工具 argument 违反 fixed profile 必须返回 `local_tool_invalid_arguments`，不得截断后
悄悄执行。

## 8. 默认 `WORKFLOW.md`、状态流和 follow-up

当前 `elixir/WORKFLOW.md` MUST 继续是 Linear profile；Local implementation MUST NOT 改变其运行配置。Local MVP
SHOULD 提供单独的 `WORKFLOW.local.md` 或等价 template，避免把 Local tracker 字段混入现有 Linear workflow。
该 template 的 `SOURCE_REPO_URL` 是 trusted host environment prerequisite：operator MUST 在启动 daemon 前设置它；它不是
tracker provider field、tracker secret，也没有 implicit fallback。template 的 `after_create` 必须以 POSIX
`: "${SOURCE_REPO_URL:?...}"` assertion 开始；unset 或 empty 必须在任何 Git mutation 前失败。保留 `git init`、set/add
remote、fetch 与 checkout 的 at-least-once 重跑语义。
空 prompt fallback MUST 使用 tracker-neutral 文字，例如 `You are working on issue {{ issue.identifier }}.`，
不得使用 `You are working on an issue from Linear.`。Local workflow 和其验收 MUST NOT 依赖 Linear MCP、
Linear API key 或 Linear network；Local MVP 的 normalized `issue.url` MUST 为 `null`，除非实现提供经过验证的
local management URL。Local V1 不存在 project/assignee routing；只有处于 `active_states` 的 issue 是
local-routable，进入和离开这些状态的行为遵循 Section 6.1。实现不得新增隐式 `assignee_id` 或
`assigned_to_worker` gate 来改变该默认路由。

默认 Local workflow 的状态流为：

```text
Backlog -> Todo -> In Progress -> Human Review -> Merging -> Done
                         ^          |
                         |          v
                       Rework <-----+

Todo/In Progress/Rework/Merging -> Cancelled | Canceled | Duplicate
```

`Backlog` 和 `Human Review` 不 active，`Todo`、`In Progress`、`Rework`、`Merging` active，terminal state 来自
Section 4。agent MUST 仅调用一个 `local_tracker` tool，并以 `operation` 选择动作，例如
`{"operation":"get_issue","arguments":{}}`。每次 dispatch 后 agent MUST 先重新读取 issue 并按 Local result 的
`data.issue_version` 路由：`Todo`/`Rework` 用该 version CAS 转入 `In Progress`；`In Progress` 原状态继续；`Merging` 只执行已批准的
merge flow，成功后 CAS 转入 `Done`，不得回退到 `In Progress`；若 state 已变为 non-active 或 terminal，必须停止
业务写入并让 runtime release claim。执行期间持续更新唯一
`## Codex Workpad`，将 plan、progress、reviewer feedback、blocker 与 validation evidence 记录其中。进入
`Human Review` 前 MUST 将 final handoff 与 validation evidence 更新到该 workpad，再以 state CAS 转入；不得
强制额外创建 completion comment。只有 blocker、状态不一致或需要人工通知的明确例外才 SHOULD 新建 comment。
存在 PR 时 MUST 先用 `link_pr` 加 `https` PR URL。`Merging` 同样必须通过 state CAS 进入。

`create_followup` 默认以 `tracker.provider.followup_state`（默认 `Backlog`）创建 child，并总是创建 `related` relation。
仅当 `blocked_by_current: true` 时，才额外创建 current parent 到 child 的 `blocks` relation。它不得固定为
`Todo` 或固定 blocks。agent 应在 parent workpad 中说明 follow-up scope；不得用它绕过 state/claim conflict。

最小 template 示例：

```md
---
tracker:
  kind: local
  provider:
    store_mode: sqlite
    database_path: .symphony/local-tracker.sqlite3
    admin_root: .symphony/admin
    default_state: Backlog
    followup_state: Backlog
    close_state: Done
  active_states: [Todo, In Progress, Rework, Merging]
  terminal_states: [Done, Closed, Cancelled, Canceled, Duplicate]
---

仅调用 `local_tracker`，并传入 `{"operation":"get_issue","arguments":{}}` 读取 {{ issue.identifier }} 与
Codex Workpad。仅在当前 state 为 Todo 或 Rework 时，以 `get_issue` 返回的 `data.issue_version` 通过 `set_state`
operation 转为 In Progress；
In Progress 直接继续，Merging 只执行批准后的 merge flow。持续更新 workpad；在 Human Review
前写入 final handoff 和验证结果，存在 PR 时以 `link_pr` operation 添加 https 链接。独立后续工作以
`create_followup` operation 创建，并仅在确有依赖时设置 blocked_by_current。
```

## 9. 最低限度 CLI、operator action 与安全

### 9.1 MVP CLI 契约

MVP REQUIRED 提供 non-interactive、local-only、由 OS identity/permissions 认证授权的 operator CLI；不要求完整
UI。公开完整命令形式 MUST 为 `symphony tracker <subcommand>`；下表为省略 `symphony` 前缀的 `<subcommand>`。
纯管理命令 MUST NOT 要求 daemon 正在运行，也 MUST NOT 要求 Codex/daemon guardrails acknowledgement。CLI 必须从
kernel 提供的有效 OS principal（Unix uid/gid、Windows SID 或等价 stable subject）取得身份，
不得接受 `--user`、环境变量、workflow 字段或 comment 文本作为授权身份。mutation 命令仅可由 database 与
`admin_root` 的 service-account owner，或 host 明确授予等效 filesystem ACL 的 principal 执行；实现 MUST 在
canonical path 上实际验证该 principal 对 database、WAL/SHM、coordination lock、workspace-operation lock root/file 与
`admin_root` 的访问权限。
read-only 命令至少需要该 principal 的 read/traverse 权限；mutation 命令还需要 write 权限。POSIX 下 file 的
world permission bits MUST 全为 0，group bits 只有在该 gid 是明确授权 subject 时才可非零；directory 同理，且
不得是未受 sticky/owner 约束的 world-writable directory。Windows 或其他平台 MUST 用等价 ACL 检查拒绝 broad
write principal。无法验证时返回 `local_storage_permission_denied`。`--force` 仅绕过 version precondition，
MUST NOT 绕过 OS authorization；每个
operator mutation audit MUST 记录安全的 OS principal snapshot，而不是可伪造的用户名。所有命令接受可选
`--workflow <path>`，其 workflow selection 与 `SPEC.md` 一致：显式 path 优先，否则使用 process cwd 的
`WORKFLOW.md`。database/admin path 从该 effective workflow 的 Local config 解析；CLI 不得把任意 path 当作
workspace path。`tracker init` 允许 `--database` bootstrap override，但该 path 必须经过与
`database_path` 相同的 normalization/security check，且不得在 workspace root 内。

`tracker init` 的目标文件可能尚不存在。此时 authorization MUST 先验证每个 canonical target 的 nearest existing
parent 由调用 principal 拥有或通过精确 ACL 授权、没有 symlink component，且满足上述 directory mode 规则；随后以
exclusive-create 和 restrictive umask/ACL 创建 admin directory、coordination lock 与 database。创建后必须重新
`lstat`/查询 ACL 验证 owner、type 和 mode。WAL/SHM 尚未 materialize 时必须验证 database parent 的创建权限，并在
SQLite 首次创建它们后、允许 daemon/agent dispatch 前再次验证实际文件。失败时只能清理由本次 invocation 以唯一
identity 确认创建的 path，MUST NOT 删除或覆盖任何 pre-existing file。

MVP REQUIRED 命令如下：

| 命令 | 行为 |
| --- | --- |
| `tracker init` | missing path 时以 effective Local config 创建 version 1；已有且 schema/integrity/fingerprint 全部匹配时返回 `already_initialized` success 且不写入，其他已有 file 一律 fail 且不得覆盖 |
| `tracker migrate` | 以 Section 5.3 的 exclusive coordination lock 证明没有 conforming scheduler，再 backup/migrate；报告 safe backup ID 或相对 `tracker.provider.admin_root` 的名称与 schema version |
| `tracker check` | 只读运行 schema、`quick_check`、foreign key、lease/retry consistency check |
| `tracker issue create` | 创建 issue；准确 flags 见下文，缺省 state 为 `tracker.provider.default_state` |
| `tracker issue show <id-or-identifier>` | 输出脱敏 normalized issue、comments/workpad、links、relations、lease/retry status；正文仅在 explicit `--include-content` 时输出 |
| `tracker issue update <id-or-identifier>` | 按下文 mutation flags 更新 title/description/priority/labels/state；默认要求 issue version CAS |
| `tracker issue close <id-or-identifier>` | 转到 `tracker.provider.close_state`；默认要求 issue version CAS |

MVP command synopsis MUST 为下列形式；`--label` 可重复，其余重复的 singleton flag MUST 作为 usage error：

```text
symphony tracker [--workflow <path>] init [--database <path>]
symphony tracker [--workflow <path>] migrate
symphony tracker [--workflow <path>] check
symphony tracker [--workflow <path>] issue create --title <text>
  [--description <text>] [--priority <1..4>] [--label <label>]... [--state <state>]
symphony tracker [--workflow <path>] issue show <id-or-identifier> [--include-content]
symphony tracker [--workflow <path>] issue update <id-or-identifier>
  (--expected-version <positive-int> | --force)
  [--title <text>] [--description <text> | --clear-description]
  [--priority <1..4> | --clear-priority] [--state <state>]
  [--label <label>]... [--clear-labels]
symphony tracker [--workflow <path>] issue close <id-or-identifier>
  (--expected-version <positive-int> | --force)
```

`issue update` MUST 提供至少一个 mutation flag。`--description`/`--clear-description`、
`--priority`/`--clear-priority`、任意 `--label`/`--clear-labels` 分别互斥；一个或多个 `--label` 表示用去重后的完整
label set 替换当前 labels，`--clear-labels` 表示替换为空集合。`--expected-version` 与 `--force` 互斥；`--force`
仅绕过 issue version precondition，MUST 产生 operator audit，且不得绕过 state/input validation、lease revoke、
filesystem authorization 或其他安全检查。Local V1 CLI 不允许 mutation 未列出的 `branch_name`、`url` 或
provider-native field。

exit code MUST 为：`0` success；`2` usage/workflow/config error；`3` issue/record not found；`4` version/claim
conflict；`5` storage busy/permission/I-O error；`6` integrity/schema/migration failure（包括
`local_schema_migration_required`）。CLI MUST 不默认在共享 terminal 打印 comment/workpad body，且 MUST NEVER
接受 raw SQL。

`tracker backup`、`tracker restore`、import、export、repair 属于 Hardening，可选而非 MVP REQUIRED。若实现，
它们必须是 operator-only：输入/输出 file path 必须 canonicalize 并包含在 `tracker.provider.admin_root`（不是
`workspace.root`）中；restore/repair MUST 停止 scheduler、验证 integrity 后才允许 dispatch。不得要求所有
import path 伪装为 workspace-contained path。

### 9.2 秘密、路径和不可信数据

tracker 不主动接受或存储 credential；`api_key`/`endpoint` 在 Local mode 的唯一语义见 Section 4。实现不能保证
任意 issue/comment/workpad 文本永远不含 secret，因此 database、WAL、backup 和 export MUST 按 sensitive data
处理：service account ownership、文件 SHOULD `0600`、目录 SHOULD `0700`。encryption at rest、access control、
retention/secure deletion 是 RECOMMENDED host policy。

Codex app-server、agent shell 与其所有 descendant process 属于相对 tracker storage 的不可信 boundary。conforming
Local runtime MUST 在 claim/launch 前证明这些 process 无法 read、write、open、rename 或创建
database/WAL/SHM/coordination lock、workspace-operation lock root/file、`admin_root` 及其 tracker-only parent path。可接受机制只有由 host 强制执行的
独立 OS principal + DAC/ACL、container/mount namespace 中完全不暴露这些 path，或等价的 platform sandbox deny
policy；仅把文件放在 `workspace.root` 外、仅使用 `workspace-write` sandbox、隐藏 path/environment，或让 agent 与
daemon 共享可访问这些文件的 uid/SID，均不 conform。无法证明隔离时必须返回
`local_agent_isolation_unavailable`、不得 dispatch 或 advertise `local_tracker`。因此 claim/fence/scoped tool 的最小
authority 保证以该 filesystem isolation 为前提，而不是对可信 agent 的行为假设。

agent 输入、issue title/description、comment、workpad、PR metadata 和 import 数据都是不可信文本。实现 MUST
parameterize SQL，escape HTML/log output，且不得把它们拼入 shell command。manifest、log、audit safe metadata、
tool error MUST NOT 输出 credential、raw claim token、完整 secret-like environment value 或不必要的全文内容。
workspace hook 保持 `SPEC.md` 的 trusted configuration 边界，不因 Local profile 而放宽；即使 hook 来自 trusted config，
它和 app-server/agent 仍必须受 Section 6.4.3 containment，不能以 trusted hook、`setsid` 或 daemonize 绕过 host enforcement。

## 10. 日志、错误与降级

Local tracker 相关 log MUST 包含 `tracker_kind=local` 和 stable outcome/error code。`deployment_id`、fingerprint
prefix、`issue_id`/`issue_identifier`、`instance_id`、`run_id`、`fence_version`、`lease_state` 与 `session_id` 在已知且
适用时 MUST 一并记录；startup 的 missing/corrupt metadata 或 pre-open path failure 只记录可安全取得的字段和主错误，
不得伪造不存在的 identity。MUST 不记录 raw SQL value、claim token、database contents 或完整 tool payload。optional status snapshot SHOULD
包含从 `PRAGMA user_version` 读取的 schema version、last integrity check、active/expired lease、retry due、claim
conflict、renew failure 与 storage latency，并明确标注 stale/unavailable。

至少必须区分：`local_storage_missing`、`local_storage_path_invalid`、`local_storage_permission_denied`、
`local_storage_busy`、`local_storage_corrupt`、`local_schema_unsupported`、
`local_schema_migration_required`、`local_schema_migration_failed`、`local_integrity_check_failed`、
`local_issue_not_found`、
`local_issue_version_conflict`、`local_invalid_state`、`local_claim_lost`、`local_claim_conflict`、
`local_issue_not_routable`、
`local_lease_renewal_failed`、`local_idempotency_conflict`、`local_idempotency_snapshot_too_large`、
`local_tool_context_required`、
`local_migration_in_progress`、`local_tool_invalid_arguments`、
`local_deployment_mismatch`、`local_agent_isolation_unavailable`、`workspace_takeover_required` 和
`unsupported_tool_for_tracker_kind`。

超过 `busy_timeout_ms` 的 `local_storage_busy` 是 transient：本 tick 跳过 dispatch；worker 仅在已确认未过期 lease
下可继续。renew 无法确认时必须在 expiry 前停止 worker。corruption、permission denied、unsupported schema、
`local_schema_migration_required`、migration failure、integrity failure、`local_deployment_mismatch` 和
`local_agent_isolation_unavailable` 都是 fatal fail-closed：runtime MUST 停止所有 Local worker，且不得 dispatch
或接受 Local tracker write，直至 operator 修复。`workspace_takeover_required` 仅阻止对应 issue，并按 Section 6.5
durable retry/release；它不得停止其他健康 issue。log sink 或 optional status failure 本身 MUST NOT 停止健康 tracker。

## 11. 验收测试矩阵

### 11.1 验收标准

Local profile 的最低验收标准为：

- 不提供 Linear API key、Linear MCP 或 Linear network 时，Local runtime 仍可启动、claim 并 dispatch。
- 两个 instance 对同一 issue 竞争时仅一个 active claim；过期/旧 fence 的 tracker write 必须被拒绝。
- `local_tracker` 可完成 workpad、state CAS、`https` PR link 与 follow-up，且不 advertisement `linear_graphql`。
- crash/restart 后 retry、takeover/quarantine 和 terminal workspace cleanup 满足本文语义。
- 同 key replay 仅在 current unexpired exact lease 与 eligibility 下可执行；stale replay 不得改变后来 owner 的
  workspace、manifest、inode、run row 或 quarantine state。
- corrupt database 必须 fail-closed 并停止 Local worker，不能以空 database 自动替代。
- Linear profile 的 callback、`linear_graphql` 和现有 Linear workflow 行为保持不变。

### 11.2 测试矩阵

conforming implementation MUST 覆盖下列 deterministic test。并发/crash case 必须用独立 process 与独立 SQLite
connection，不得使用 shared in-memory mock。

| 层级 | 范围 | 必需测试 |
| --- | --- | --- |
| Unit | Config/reload | `local` exhaustive kind、provider allowed-key whitelist/unknown-key reject、无 Linear key/MCP/network、`worker.ssh_hosts` 非空 fail、provider path/env/home resolution、database 不在 workspace、state subset/disjoint、lease/heartbeat/poll/busy-timeout inequalities 与 renew watchdog、Unicode 15.1 `local_compare_key` 的 `Straße`/`STRASSE` state/label 等价、duplicate 拒绝与 canonical display spelling、database-bound deployment fingerprint canonical object/test vector/mismatch、restart-only/hot-reload、provider/legacy flat `endpoint`/`api_key`/`project_slug`/`assignee` 非空 fail、`SOURCE_REPO_URL` unset/empty assertion 在任何 Git mutation 前失败且 present 时 `git init`/remote/fetch/checkout 可 at-least-once 重跑 |
| Unit | Generic adapter | 两个 REQUIRED read callback 的空列表、state query 不过滤 label/dispatchable、ID query 的完整 snapshot/omission/malformed-row failure、`agent_tool_specs/0`、`execute_agent_tool/3`、`secret_environment_names/1`、`validate_config/1`、Local public `code`/portable category/message mapping（含 isolation unavailable 与 migration-in-progress lifecycle error）、shared versioned Local capability，以及唯一 generic runtime struct 与 Linear conversion/type alias |
| Unit | Schema/transaction | `next_local_number=1`、counter 并发分配不碰撞、无 `issue_blockers`、relation blocker authority、labels 无 JSON1、FK/ON DELETE、issue create 原子 bootstrap issue/labels/lease fence 0/workpad/revision/audit、agent comment creator provenance、same-run/later-turn body authority、later-run same-issue resolve/archive-only、issue/subresource version、receipt JCS/default-normalization/hash test vector、receipt key/payload conflict、lease release 后 exact key/operation/hash replay immutable snapshot、later mutation 后 snapshot replay、new session key 不误认 replay 并记录 uncertain outcome、host launch UUID response lost 后同 key 仅在 exact current unexpired eligible lease 时返回既有 run/fence/initial expiry，其他 replay 为 `local_claim_lost` 且不创建第二 row/fence 或 filesystem side effect、prepare 前 no-follow origin probe 的 `expected_absent/pending` 与 `preexisting_reusable/reused/ready` 原子写入、前者 `NULL -> created/ready -> delete_pending -> deleted` CAS、后者 identity/boundary 同 transaction 写入且永不 delete、marker 缺失的 existing empty `pending` preserve/block、stable `workspace_identity` 不含 content digest、`prelaunch_boundary` 只用于 no-child recovery、`after_run_state` pending/completed CAS 及 exact `(issue_id, fence_version)`、hook marker/containment/child-handle 只增写、`quarantine_path`/`deleting_path` write-once 与 `NULL -> pending -> completed` quarantine CAS、131072-byte 超限原子拒绝、new DB init、matching existing DB 的 `already_initialized` no-op、其他 existing file 不覆盖、old schema migration-required |
| Integration | Migration/CLI | scheduler/ordinary CLI shared lock 与 init/migrate exclusive lock 互斥、crash 自动释放 lock、bootstrap parent authorization/secure create、migration backup 的 `admin_root` containment/no-follow/exclusive restrictive create/owner-mode-ACL/safe reporting、consistent migration rollback、MVP CLI 完整 `symphony tracker ...` syntax 与 exit code、无 daemon/guardrails acknowledgement、OS principal/ACL 拒绝、not-found/conflict/storage；optional backup/restore 仅在实现该 Hardening command 时测试其 operator-only 语义 |
| Concurrency | Claim/retry | 两个 instance race 同一 issue 仅一个 claim、不同 issue 可分别占用各 instance 的 per-instance concurrency slot、per-issue workspace-operation 与 database coordination shared/exclusive advisory lock 的 exclusive/create/crash-release、`O_CLOEXEC`/fork/exec/spawn 显式 close、hook/background child 不继承并在 daemon crash 后 deterministic release、与 claim/recover-claimed CAS 不存在验证后 fence takeover、fence 永不回退、renew/release CAS、`child_starting` CAS 严格先于 `after_create`/`before_run`、predecessor state 与 `blocks` add/remove 的 affected closure 预读/UUID lock order/transaction reread/retry、同事务重算 `Todo` target dispatchability 并 revoke 新 ineligible lease、state/label/dispatchability transition（含 active-but-unroutable `Todo`）后 stale renew/write 拒绝并 revoke matching lease、`set_state -> Human Review/Done` 与 operator close/revoke 后旧 agent write 被拒绝、post-revoke `after_run` pending/completed 的 at-least-once finalize、daemon crash 后 recoverer 与新 claim 的 race：pending 优先、exact run/fence、old containment empty、无高 fence/其他危险 attempt 才可 replay，fence 已推进时 hook/finalize 被拒绝、operator terminal->active 与 terminal cleanup rename/delete 的确定性竞争、agent `set_state` revoke 与 manifest/quarantine filesystem mutation 的确定性竞争、multi-issue UUID canonical lock order 与 `create_followup` 无死锁、`set_state`/operator transition 的 mutation-receipt-audit-release 原子性、retry_queue due/consume、failure 后 cache 清理、tick 从 DB due gate 恢复、normal exit 原子 termination/finalize/release + 一秒 continuation、failure backoff、restart durable retry、同 issue 的危险未验证 A、safe retry B 与 retry C：C 仍枚举 A、持久化 pending/completed quarantine target 并在 proof/isolation 前 block、duplicate launch record、f1 response lost 后 expiry、f2 takeover 并写 manifest，再 replay f1 key 必须 `local_claim_lost`，且 f2 workspace/manifest/inode/run rows/quarantine state 均不变 |
| Crash-recovery | Workspace | `claimed`/`manifest_writing`/`manifest_written`/early `child_starting`/`child_started`/write/retry/migration 前后 crash 及每 phase 的 expected recovery、prepare origin probe/intent commit、existing reusable identity/boundary 同 transaction、exclusive `mkdir`、exact intent marker、`mkdir` 后 marker 前 crash 的 existing empty path preserve/block、`ready` CAS 前后各 crash，及 pending exact path missing/marker/attempt-manifest/other-entry 归属结果、expired unfinished safe no-child phase 的 recover-claimed CAS（无 owner-null window、concurrent recoverer）、created 的 `delete_pending` first CAS、no-follow delete、parent fsync、final CAS 前后 crash 与 SQLite rollback、`delete_pending`+exact existing identity 重试、`delete_pending`+missing finalize、其他组合 block、reused identity/path/prelaunch-boundary 与 per-run manifest missing/exact-digest 匹配时标记 not-started 但保留 workspace、任一 identity mismatch block/quarantine、daemon crash 在 `after_run` pending、hook 副作用后和 completed CAS 前的 exact containment reopen/empty/replay、expired/revoked lease、higher-fence/危险 attempt 拒绝、manifest intent/write/fsync/verify/CAS 边界 crash 与 existing mismatched manifest 不覆盖、stored canonical workspace path/deployment mismatch、expired lease takeover、旧 process 存活、containment-empty termination/sandbox isolation、quarantine pending-CAS/rename/source-parent-fsync/target-parent-fsync/completed-CAS 每个边界 crash、pending recovery 的 source/target 四种组合、identity/manifest mismatch、symlink swap 与无覆盖 fail-closed、`run_attempts.quarantine_path`/state recovery、`workspace_takeover_required` 的 blocked-prelaunch/retry/release transaction、terminal cleanup 的 terminal/manifest/lease/containment termination proof/rename/revalidation/`before_remove` cleanup containment 条件、agent normal content mutation 不阻止 terminal cleanup、`.deleting` persisted target/parent fsync/identity/symlink swap、缺失 proof 时保留、不得凭 PID/process-group ownership |
| Integration | Dynamic tools | kind conditional `agent_tool_specs/0`、Local 只 advertise `local_tracker` 且不 advertise `linear_graphql`、Linear 继续可用、trusted claim/session/thread/turn/call context、host-derived key、receipt-first executor ordering、lease release 后 exact receipt replay、`get_issue`/`list_comments` 与 revoke、claim loss 或 session exit 的 deterministic race 立即拒绝且无 grace、Local structured result 到 AppServer envelope projection、failure optional `data`、CAS `data.current_version` 与 safe unsupported-tool message、current issue scope、input limit/CAS/pagination、workpad body/revision/archive、comment update/archive、PR link、follow-up default relation/block flag |
| Integration | Corruption/security/observability | quick/foreign key check、WAL corruption、permission/busy、workspace-operation lock root 与 `.quarantine`/`.deleting` 的 no-follow/restrictive owner-mode-ACL/exclusive-create 验证、same-principal/direct-SQL preflight 拒绝与 container/ACL isolation success、per-run cgroup/Job Object/container containment、daemonize/`setsid`/breakaway 逃逸拒绝、hook return/timeout 终止并 wait background descendant、run proof 只在 containment empty 时写入、containment unavailable fail-closed、fatal error 停 Local worker、stale snapshot、required log context、log sink failure 不影响 healthy dispatch、secret-like text 按 sensitive data 处理 |

production 前 SHOULD 在 local filesystem temporary database 上运行两个真实 Symphony process 的 smoke test；测试不得使用
network-mounted temporary directory。

## 12. 分阶段实现与取舍

1. **MVP**：generic `Issue` boundary、当前 adapter callback、SQLite schema/migration/check、Local read/V1
   write capability、fenced claim、`retry_queue`、deployment identity、agent filesystem isolation、workspace
   reconciliation/quarantine、MVP CLI、`local_tracker`
   tool 和上述 deterministic test。单 host multi-process safety 是 MVP；完整 UI、remote worker、attachment、
   import/export/backup/restore 不是。
2. **Hardening**：operator backup/restore/import/export/repair、encryption/retention、role control、丰富 audit/
   status 与 fault-injection corruption/crash drill。
3. **Future**：受控 Linear import/export、attachment、真正 network coordinator 的 remote worker、可配置
   transition graph；每项改变 trust/consistency boundary，MUST 有单独规范。

SQLite WAL 被选中而非 JSON/YAML，因为 cross-process claim、counter allocator 和 atomic CAS 需要 durable lock/
transaction；选择 SQLite 而非 PostgreSQL，因为 Local profile 不引入服务，且不假装共享 filesystem 安全。fencing
lease 取代 in-memory claim set 以支持 crash/multi-instance recovery，但外部副作用仍只有 at-least-once；
quarantine 限制 workspace 冲突而不能撤销旧进程的外部行为。scoped `local_tracker` 取代 raw SQL/GraphQL，以限制
model-controlled input authority。orchestrator 不直接转换业务 state，因为 policy 属于 `WORKFLOW.md`。

满足本文全部 MUST 以及未覆盖的 `SPEC.md` 要求，才是 conforming Local Tracker。仅把 issue 保存到本地、但缺少
generic compatibility、fence/retry/recovery 或 scoped write tool 的实现，不 conform。
