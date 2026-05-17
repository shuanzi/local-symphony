# WebUI Workbench Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 dashboard 默认入口改为以 work queue、issue context、action rail 为核心的 operator workbench。

**Architecture:** 保留现有 REST/SSE、route、API client 和二级页面能力，在 `web/src/App.tsx` 内先提取纯计算 helper 与工作台组件，避免本轮引入新依赖或后端改动。样式集中在 `web/src/styles.css`，用固定三栏布局和响应式纵向布局支持桌面与窄屏。

**Tech Stack:** React, TypeScript, Vite, existing REST/SSE client, repository static test scripts.

---

## File Structure

- Modify: `web/src/App.tsx`
  - Add queue classification helpers.
  - Change `overview` route to render `WorkbenchPage`.
  - Add `WorkQueue`, `IssueContextPanel`, `ActionRail`, and contextual feedback blocks.
  - Keep `BoardPage`, `IssueDetailPage`, `RunDetailPage`, `ReviewPacketPage`, `WorkflowPage`, and `DiagnosticsPage` available as secondary views.
- Modify: `web/src/styles.css`
  - Add compact operator shell, status bar, queue, context, and action rail styles.
  - Reduce large-card emphasis for the workbench only.
  - Add responsive collapse for the three-column layout.
- Modify: `web/scripts/test.mjs`
  - Extend static coverage so the default dashboard includes `WorkbenchPage`, queue, selected issue context, and action rail.
- Create or keep: `docs/superpowers/specs/2026-05-17-webui-workbench-redesign.md`
  - Approved product/design spec.

## Task 1: Static Coverage First

- [ ] **Step 1: Add workbench expectations to static test**

Modify `web/scripts/test.mjs` so the component token list includes the new default view and the shared phrase list includes operator-facing states:

```js
for (const token of ['WorkbenchPage', 'WorkQueue', 'IssueContextPanel', 'ActionRail', 'BoardPage', 'IssueDetailPage', 'RunDetailPage', 'ApprovalInboxPage', 'ReviewPacketPage', 'WorkflowPage', 'DiagnosticsPage']) {
  if (!app.includes(token)) throw new Error(`missing component ${token}`);
}

const sharedStates = [
  'Loading dashboard state', 'No issues', 'Session expired', 'Daemon unavailable',
  'Content is not exposed by the Review/Artifact API', 'Command submitted',
  'Needs action', 'Ready to run', 'Action rail'
];
```

- [ ] **Step 2: Run failing test**

Run: `npx -y pnpm@9 --dir web test`

Expected: FAIL before implementation with `missing component WorkbenchPage`.

## Task 2: Workbench Data Model

- [ ] **Step 1: Add helper types and classification**

Modify `web/src/App.tsx` near existing helper functions:

```ts
type QueueGroupKey = 'needs_action' | 'ready_to_run' | 'watching' | 'all';

interface QueueGroup {
  key: QueueGroupKey;
  label: string;
  issues: Issue[];
}
```

Add helpers that use existing `Issue`, `RunAttempt`, `Approval`, and `RunEvent` data only:

```ts
function hasActiveRun(issue: Issue, runs: RunAttempt[]): boolean {
  return Boolean(issue.active_run_id || runs.some((run) => run.issue_id === issue.id && activeRunStatuses.has(run.status)));
}

function isNeedsAction(issue: Issue, runs: RunAttempt[], approvals: Approval[]): boolean {
  const latestRun = issue.latest_run || runs.find((run) => run.id === issue.latest_run_id);
  return issue.state === 'Human Review'
    || issue.state === 'Blocked'
    || issue.dispatch_paused
    || latestRun?.status === 'failed'
    || latestRun?.status === 'completed_without_handoff'
    || approvals.some((approval) => approval.issue_id === issue.id && approval.status === 'pending');
}
```

- [ ] **Step 2: Verify typecheck catches no helper errors**

Run: `npx -y pnpm@9 --dir web typecheck`

Expected: PASS or static fallback pass.

## Task 3: Workbench Page

- [ ] **Step 1: Implement default workbench route**

Modify `App()` so `overview` renders:

```tsx
content = <WorkbenchPage data={data} route={route} runMutation={runMutation} />;
```

Add `WorkbenchPage` before `MetricCard`:

```tsx
function WorkbenchPage({ data, route, runMutation }: {
  data: DashboardData;
  route: RouteState;
  runMutation: <T>(operation: () => Promise<T>) => Promise<T | null>;
}) {
  const selected = useMemo(() => chooseSelectedIssue(data, route.issueRef), [data, route.issueRef]);
  const groups = useMemo(() => buildQueueGroups(data), [data]);

  return (
    <section className="workbench" aria-label="Operator workbench">
      <WorkQueue groups={groups} selectedIssueId={selected?.id || null} />
      <IssueContextPanel issue={selected} data={data} />
      <ActionRail issue={selected} data={data} runMutation={runMutation} />
    </section>
  );
}
```

- [ ] **Step 2: Keep no-issue guidance actionable**

Inside `IssueContextPanel`, when `issue` is null, render buttons for create issue, Board, and Diagnostics using existing `navigate()` calls.

## Task 4: Work Queue and Context Panel

- [ ] **Step 1: Implement grouped queue**

`WorkQueue` renders `Needs action`, `Ready to run`, `Watching`, and `All issues`, with issue buttons using `navigate({ page: 'issue', issueRef: issue.identifier })`. Use `StatusPill`, `Pill`, and stable button layout.

- [ ] **Step 2: Implement issue context**

`IssueContextPanel` renders header, acceptance criteria, relations, latest run/review summary, workspace/git facts, and the latest 8 related events using `EventList`. It should link to existing run and review routes rather than duplicating deep pages.

## Task 5: Action Rail

- [ ] **Step 1: Implement contextual actions**

`ActionRail` renders only available actions:

```tsx
{isDispatchable(issue) ? <button type="button" onClick={() => void runMutation(() => api.dispatchIssue(issue.identifier))}>Dispatch eligible issue</button> : null}
{issue.dispatch_paused ? <button type="button" onClick={() => void resumeDispatch()}>Dispatch resume issue</button> : <button type="button" onClick={() => void pauseDispatch()}>Dispatch pause issue</button>}
```

It also links to Review, Run Detail, Workflow, and Diagnostics when those are contextually useful.

- [ ] **Step 2: Add command feedback**

Keep local action state in `ActionRail`:

```ts
const [localMessage, setLocalMessage] = useState<string | null>(null);
```

Set success messages after non-null mutation results and render them near the relevant buttons.

## Task 6: Styling

- [ ] **Step 1: Add workbench layout CSS**

Add selectors to `web/src/styles.css`:

```css
.workbench {
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr) 320px;
  gap: 14px;
  align-items: start;
}
```

Add `.work-queue`, `.context-panel`, `.action-rail`, `.queue-item`, `.timeline-summary`, and responsive rules under the existing media query.

- [ ] **Step 2: Preserve existing pages**

Do not remove `.board`, `.metric-grid`, `.approval-card`, `.artifact-card`, or `.diagnostics-grid`; existing static tests rely on them and the pages remain available.

## Task 7: Verification

- [ ] **Step 1: Run web checks**

Run:

```bash
npx -y pnpm@9 --dir web typecheck
npx -y pnpm@9 --dir web test
npx -y pnpm@9 --dir web build
```

Expected: all commands pass.

- [ ] **Step 2: Browser smoke**

Start or reuse the local dashboard service at `http://127.0.0.1:3777`, then verify:

- Default route opens the workbench.
- Left queue has `Needs action`, `Ready to run`, `Watching`, and `All issues`.
- Selecting an issue exposes context and action rail.
- Board, Review, Workflow, and Diagnostics remain reachable.
- No raw prompt, raw Codex log, or secret-like data appears in the new UI.
