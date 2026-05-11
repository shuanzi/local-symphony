# Agent Execution Protocol

本文件定义 M0-M8 implementation agent 的通用执行纪律。每个 milestone 的 `M*.md` 只描述该阶段范围；具体实现时必须同时遵守本文件、`README.md`、`PRD.md`、`TECH_SPEC.md` 和可执行合同。

## 1. 执行前检查

每个 agent 在开始 milestone 前必须完成：

```bash
scripts/validate-contracts.sh
git status --short
```

若合同校验失败，先修合同或文档，不得绕过失败继续实现。若 worktree 有无关改动，保留并避免覆盖；若改动影响当前 milestone，先读懂后再继续。

## 2. 权威顺序

发生冲突时按以下规则处理：

```text
产品范围、用户故事、非目标：PRD.md
实现合同、状态机、API、DB、CLI、安全、测试：TECH_SPEC.md
机器可读 API/DB/JSON shape：api/openapi.yaml、db/schema/*.sql、schemas/*.schema.json、schemas/tools/*.input.schema.json
阶段拆解与验收：docs/agent_work_orders/*.md、docs/testing/*.md
入口说明：README.md
```

若 `TECH_SPEC.md` 与机器合同冲突，先提交文档/合同对齐，再写实现。不得在代码中发明第三套 API、DB 或 JSON shape。

## 3. 必须保持的 v1 不变量

```text
tracker.kind = local only
Done 只能由 operator 触发
handoff.submit 只记录 handoff，不直接状态流转
Human Review 必须由 review packet finalizer 成功后进入
失败、取消、missing handoff、agent block、review packet failure 都必须 pause dispatch
Ready/Rework 是普通 scheduler 唯一候选状态
Working 只用于 active run reconciliation
每个 issue 复用同一 workspace/branch/base_sha，Rework packet 是累计 diff
不实现 Linear、PR/push/merge/publish、auto retry、workspace cleanup、dynamic tools、MCP
raw prompt/raw Codex log 不通过 v1 API、dashboard 或 diagnostics export 暴露
```

## 4. Tool CLI 映射

Tool Gateway registry 使用点分工具名；CLI 使用分组子命令。实现必须做一一映射，不得新增动态工具。

| Registry tool | CLI command | Input schema |
|---|---|---|
| `issue.get` | `symphony tool issue get` | `schemas/tools/issue_get.input.schema.json` |
| `issue.comment` | `symphony tool issue comment --json <file>` | `schemas/tools/issue_comment.input.schema.json` |
| `issue.block` | `symphony tool issue block --json <file>` | `schemas/tools/issue_block.input.schema.json` |
| `artifact.attach` | `symphony tool artifact attach --json <file>` | `schemas/tools/artifact_attach.input.schema.json` |
| `followup.create` | `symphony tool followup create --json <file>` | `schemas/tools/followup_create.input.schema.json` |
| `handoff.submit` | `symphony tool handoff submit --json <file>` | `schemas/tools/handoff_submit.input.schema.json` |

`symphony tool ...` 必须只向 stdout 输出 JSON。诊断信息只写 stderr。

## 5. Milestone 完成条件

每个 milestone 完成前必须：

```text
1. 运行该 milestone 的验收命令。
2. 运行 scripts/validate-contracts.sh。
3. 更新或新增与改动匹配的测试。
4. 确认 OpenAPI、SQL、JSON Schema、CLI help、README/Tech SPEC 没有漂移。
5. 在 handoff 中列出已运行命令、失败命令、剩余风险和下一 milestone 注意事项。
```

若某个验收命令尚未存在，agent 必须创建最小可运行的测试入口或在 handoff 中明确阻塞原因；不能把缺失命令当作通过。

## 6. 需要人工介入的情况

出现以下情况必须暂停并请求人工确认：

```text
需要改变 v1 产品范围或非目标
需要改变 issue 状态机、handoff target、Done/Rework 规则
需要放宽安全边界或暴露 raw prompt/raw Codex log
需要引入 Linear/GitHub Issues/PR/push/merge/publish/auto retry/workspace cleanup
机器合同与 PRD/TECH_SPEC 冲突但无法用局部文档修正解决
```
