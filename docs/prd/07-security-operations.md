# 安全、权限、Secrets、Sandbox 与运维策略

## 1. 默认威胁模型

v1 采用：

> 本地但不完全可信。

不把以下对象视为天然可信：

```text
repo
issue content
prompt
hook
agent output
test output
dependency install script
```

## 2. 默认安全模式

v1 默认：

```text
balanced-secure
```

默认基线：

```text
API 只绑定 loopback
UI / CLI 需要 session token
command API 需要 CSRF
agent tool 使用 run-scoped token
agent 不继承完整 host env
agent 默认只能写 workspace
网络默认关闭
sensitive path 默认保护
git push / PR / force push 禁止
日志 / prompt / review packet 默认 redacted
```

## 3. API 安全

默认：

```yaml
server:
  host: 127.0.0.1
  port: 0
  require_auth: true
  allow_remote: false
```

禁止：

```text
0.0.0.0 bind
远程 dashboard
无 token API
CORS wildcard
把 agent tool token 复用给 UI
把 UI session token 注入 agent
```

## 4. Capability-based 权限

v1 不做复杂 RBAC。

Principal：

| Principal | 说明 |
|---|---|
| operator | 当前本机用户 |
| ui_session | 浏览器或桌面 UI 会话 |
| cli_session | 本地 CLI |
| agent_run | 某个 issue 的某次 run |
| tool_token | 注入给 agent 的短期 token |
| daemon | Go 后端 |

核心原则：

```text
长期权限给 operator
短期权限给 run
agent 不继承 operator 全部本机权限
所有有副作用动作都通过 command/tool gateway
```

## 5. Secrets 策略

v1 不做通用 secret manager。

| Secret 类型 | v1 策略 |
|---|---|
| OpenAI / Codex auth | 由 Codex 自己管理，Symphony 只读取 auth status |
| GitHub/GitLab token | 使用 gh、Git credential helper、SSH agent，不存入 Symphony DB |
| Local tracker token | 不需要外部 token |
| Agent tool token | 每 run 短期生成，只存 hash |
| Desktop session token | 短期生成，只存 hash |
| Workflow config secret | 支持 `$VAR` indirection，但不打印值 |
| Project-specific secrets | 不进入 agent env，除非用户显式 allowlist |

禁止：

```text
在 WORKFLOW.md 写明文 token
在 SQLite 存第三方明文 token
把整个 host env 传给 agent
把 ~/.ssh、~/.aws、~/.kube 暴露给 agent
把 secret value 写进 issue comment、review packet、logs
```

## 6. Agent 环境变量

不继承完整 host env。

默认注入：

```bash
PATH
HOME
TMPDIR
SYMPHONY_TOOL_ENDPOINT
SYMPHONY_TOOL_TOKEN
SYMPHONY_PROJECT_ID
SYMPHONY_ISSUE_ID
SYMPHONY_ISSUE_IDENTIFIER
SYMPHONY_RUN_ID
SYMPHONY_WORKSPACE_PATH
```

默认不注入：

```bash
OPENAI_API_KEY
GITHUB_TOKEN
AWS_ACCESS_KEY_ID
AWS_SECRET_ACCESS_KEY
GOOGLE_APPLICATION_CREDENTIALS
KUBECONFIG
SSH_AUTH_SOCK
NPM_TOKEN
DATABASE_URL
```

## 7. 文件系统边界

workspace 是唯一默认可写边界。

默认：

```text
agent cwd = issue workspace
workspace path 必须在 workspace.root 下
workspace key 必须 sanitize
workspace 外写入默认拒绝
主 repo working tree 默认不可写
project DB 不可由 agent 直接写
```

## 8. Protected paths

默认保护：

```text
.env
.env.*
**/*.pem
**/*.key
**/*_rsa
**/*_ed25519
.ssh/
.aws/
.gcp/
.azure/
.kube/
.npmrc
.pypirc
.netrc
```

默认策略：

| 动作 | 默认 |
|---|---|
| 读取普通 workspace 文件 | 允许 |
| 修改普通 workspace 文件 | 允许 |
| 读取 protected path | 拒绝或 UI 审批 |
| 修改 protected path | 拒绝 |
| attach protected file | 拒绝 |
| 把 protected 内容写入 comment | redaction |

## 9. 命令策略

v1 分三类：

```text
allow
review
deny
```

默认 allow：

```text
git status
git diff
git log
git show
ls
cat
sed
grep
rg
find
go test ./...
cargo test
pytest
npm test
pnpm test
yarn test
```

默认 review：

```text
npm install
pnpm install
yarn install
pip install
go mod download
cargo fetch
docker build
docker compose up
make
scripts/*
任何需要网络的命令
```

默认 deny：

```text
sudo
su
rm -rf /
rm -rf ~
chmod -R
chown -R
curl | sh
wget | sh
ssh
scp
rsync to remote
git push
git push --force
git clean -xfd
gh pr create
gh pr merge
访问 workspace 外路径
```

## 10. 网络策略

默认：

```yaml
network:
  default: deny
  allowlist: []
  approval_required: true
```

## 11. v1 后移项

根据最终决策，以下能力不进入 v1：

| 编号 | 能力 | 后续版本 |
|---|---|---|
| D92 | migration / destructive action 前自动 SQLite backup | v1.1 |
| D93 | migration / upgrade 生产级流程 | v1.1 |
| D94 | 崩溃恢复：恢复调度事实 | v1.1 |
| D95 | 供应链 / install / remote script 深度审批策略 | v1.2 |
| D97 | 完整 audit log 模型 | v1.1 |

v1 只做轻量事件记录，不承诺 compliance-grade audit trail。

## 12. v1 必须诚实展示的失败

```text
workflow invalid
codex unavailable
approval timeout
tool token invalid
missing handoff
workspace conflict
blocker active
unsupported DB version
```
