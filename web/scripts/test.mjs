import fs from 'node:fs';

const root = new URL('..', import.meta.url);
const inv = JSON.parse(fs.readFileSync(new URL('action-inventory.json', root), 'utf8'));
const app = fs.readFileSync(new URL('src/App.tsx', root), 'utf8');
const api = fs.readFileSync(new URL('src/api.ts', root), 'utf8');
const styles = fs.readFileSync(new URL('src/styles.css', root), 'utf8');
const validateContracts = fs.readFileSync(new URL('../scripts/validate_contracts.py', root), 'utf8');

function extractIssuePaginationBlock(source) {
  const helperMatch = /const\s+loadIssues\s*=\s*async\s*\([^)]*\)\s*=>\s*\{/.exec(source);
  if (helperMatch) {
    let depth = 1;
    let i = helperMatch.index + helperMatch[0].length;
    for (; i < source.length && depth > 0; i += 1) {
      if (source[i] === '{') depth += 1;
      if (source[i] === '}') depth -= 1;
    }
    return source.slice(helperMatch.index, i);
  }
  const loopMatch = /(for\s*\([^)]*\)|while\s*\([^)]*\))\s*\{[\s\S]{0,2000}api\.issues\s*\([\s\S]{0,2000}page\.page\?\.has_more[\s\S]{0,2000}\}/.exec(source);
  return loopMatch?.[0] || '';
}

function assertIssuePaginationLoadsAllPages(source) {
  const block = extractIssuePaginationBlock(source);
  if (!block) throw new Error('loadAll must use a loadIssues helper or issue pagination loop');
  if (!/api\.issues\s*\(/s.test(block)) throw new Error('issue pagination must call api.issues');
  if (!/query\.set\(\s*['"]cursor['"]\s*,\s*cursor\s*\)|api\.issues\s*\([^)]*cursor/s.test(block)) throw new Error('issue pagination must pass cursor to api.issues requests');
  if (!/\bcursor\s*=\s*page\.page\?\.next_cursor/s.test(block)) throw new Error('issue pagination must advance cursor from page.page?.next_cursor');
  if (!/page\.page\?\.has_more/s.test(block)) throw new Error('issue pagination must read page.page?.has_more');
  const apiIndex = block.search(/api\.issues\s*\(/s);
  const itemsIndex = block.search(/page\.items/s);
  const hasMoreIndex = block.search(/page\.page\?\.has_more/s);
  if (itemsIndex < 0 || itemsIndex < apiIndex || itemsIndex > hasMoreIndex) throw new Error('issue pagination must accumulate page.items before deciding pagination is complete');
}

function exactSet(name, actual, expected) {
  const missing = expected.filter((item) => !actual.includes(item));
  const extra = actual.filter((item) => !expected.includes(item));
  if (missing.length || extra.length) {
    throw new Error(`${name} drift: missing=${missing.join(', ') || 'none'} extra=${extra.join(', ') || 'none'}`);
  }
}

const requiredActionEvidence = {
  'create issue': { ui: /Create issue/s, api: /createIssue:[\s\S]*\/issues/s },
  'update issue': { ui: /Update issue/s, api: /updateIssue:[\s\S]*method:\s*'PATCH'/s },
  'transition issue': { ui: /Transition issue|Apply transition/s, api: /transitionIssue:[\s\S]*\/transition/s },
  'dispatch eligible issue': { ui: /Dispatch eligible issue/s, api: /dispatchIssue:[\s\S]*\/dispatch/s },
  'dispatch pause issue': { ui: /Dispatch pause issue/s, api: /pauseDispatch:[\s\S]*\/dispatch-pause/s },
  'dispatch resume issue': { ui: /Dispatch resume issue/s, api: /resumeDispatch:[\s\S]*\/dispatch-resume/s },
  'approve once': { ui: /Approve once/s, api: /approve_once/s },
  'approve for run': { ui: /Approve for run/s, api: /approve_for_run/s },
  'approve for session': { ui: /Approve for session/s, api: /approve_for_session/s },
  'deny approval': { ui: /Deny current action/s, api: /decision:\s*ApprovalDecision/s },
  'cancel run': { ui: /Cancel run/s, api: /cancelRun:[\s\S]*\/cancel/s },
  'send to rework': { ui: /Send to Rework/s, api: /sendToRework:[\s\S]*\/send-to-rework/s },
  'mark done': { ui: /Mark Done/s, api: /markDone:[\s\S]*\/mark-done/s },
  'workflow validate': { ui: /Workflow validate/s, api: /validateWorkflow:[\s\S]*\/workflow\/validate/s },
  'workflow reload': { ui: /Workflow reload/s, api: /reloadWorkflow:[\s\S]*\/workflow\/reload/s },
  'diagnostics export': { ui: /Diagnostics export/s, api: /exportDiagnostics:[\s\S]*\/diagnostics\/export/s }
};
exactSet('required action inventory', inv.required_actions, Object.keys(requiredActionEvidence));
for (const action of inv.required_actions) {
  const evidence = requiredActionEvidence[action];
  if (!evidence.ui.test(app)) throw new Error(`required action missing UI evidence: ${action}`);
  if (!evidence.api.test(`${app}\n${api}`)) throw new Error(`required action missing API evidence: ${action}`);
}

const forbiddenActionEvidence = {
  'git push': /git\s+push/i,
  publish: /\bpublish\b/i,
  'create pr': /create\s+pr|pull request/i,
  'backup database': /backup\s+database/i,
  'restore database': /restore\s+database/i,
  'migrate database': /migrate\s+database/i,
  'workspace delete': /workspace\s+delete|deleteWorkspace/i,
  'secret read': /secret\s+read|readSecret/i,
  'project settings mutation': /project\s+settings\s+mutation|updateProjectSettings/i,
  'issue delete': /issue\s+delete|deleteIssue/i,
  'arbitrary state mutation': /arbitrary\s+state\s+mutation|\/state\b/i
};
exactSet('forbidden action inventory', inv.forbidden_actions, Object.keys(forbiddenActionEvidence));
for (const action of inv.forbidden_actions) {
  if (forbiddenActionEvidence[action].test(app) || forbiddenActionEvidence[action].test(api)) {
    throw new Error(`forbidden action leaked into frontend: ${action}`);
  }
}

const requiredApiPaths = [
  '/health', '/issues', '/runs', '/approvals', '/workflow', '/diagnostics',
  '/transition', '/dispatch', '/dispatch-pause', '/dispatch-resume', '/decide',
  '/reviews/', '/send-to-rework', '/mark-done', '/workflow/validate', '/workflow/render-preview', '/diagnostics/export'
];
for (const path of requiredApiPaths) {
  if (!api.includes(path)) throw new Error(`missing API client path ${path}`);
}

const sharedStates = [
  'Loading dashboard state', 'No issues', 'Session expired', 'Daemon unavailable',
  'Content is not exposed by the Review/Artifact API', 'Command submitted',
  'Needs action', 'Ready to run', 'Action rail'
];
for (const phrase of sharedStates) {
  if (!app.includes(phrase)) throw new Error(`missing shared-state phrase: ${phrase}`);
}

if (!api.includes('csrf_token')) throw new Error('API client must read csrf_token');
if (!api.includes('/auth/exchange')) throw new Error('API client must exchange open_token');
if (!/exchangeOpenToken:\s*async\s*\([^)]*openToken[^)]*\)\s*=>\s*\{[\s\S]*method:\s*'POST'[\s\S]*JSON\.stringify\(\{\s*open_token:\s*openToken\s*\}\)/s.test(api)) throw new Error('API client must POST open_token exchange payload');
if (!app.includes('open_token') || !app.includes('openToken')) throw new Error('dashboard must read open-token aliases from URL');
if (!app.includes('api.exchangeOpenToken')) throw new Error('dashboard must bootstrap auth with open-token exchange');
if (!app.includes('history.replaceState')) throw new Error('dashboard must clean exchanged open token from URL');
if (/\.split\(\s*['"]\/['"]\s*\)\.map\(\s*decodeURIComponent\s*\)/s.test(app)) throw new Error('route parsing must not directly map decodeURIComponent over hash segments');
if (!/function\s+\w*decode\w*Route\w*\([^)]*\)[\s\S]*try\s*\{[\s\S]*decodeURIComponent[\s\S]*\}\s*catch/s.test(app)) throw new Error('route parsing must use a safe decode helper with malformed hash fallback');
if (!app.includes('exchangePromiseRef')) throw new Error('dashboard must guard one-time open-token exchange against duplicate effects');
if (!/cleanOpenTokenFromUrl\(\);\s*exchangePromiseRef\.current = api\.exchangeOpenToken/s.test(app)) throw new Error('dashboard must clean open token before awaiting exchange');
if (!app.includes('Dashboard is not authenticated')) throw new Error('dashboard must render explicit unauthenticated state');
if (!/setData\(emptyData\)/s.test(app)) throw new Error('dashboard must clear protected data after auth loss');
if (!/authEpochRef\s*=\s*useRef\(0\)/s.test(app)) throw new Error('dashboard must track auth epoch for protected data requests');
if (!/authEpochRef\.current\s*\+=\s*1[\s\S]*setData\(emptyData\)/s.test(app)) throw new Error('markUnauthenticated must advance auth epoch before clearing protected data');
if (!/const requestAuthEpoch\s*=\s*authEpochRef\.current[\s\S]*authEpochRef\.current\s*!==\s*requestAuthEpoch[\s\S]*setData/s.test(app)) throw new Error('loadAll must skip stale protected data commits after auth epoch changes');
if (/api\.issues\('\?limit=200'\)/s.test(app)) throw new Error('loadAll must not fetch only the first issue page');
assertIssuePaginationLoadsAllPages(app);
if (!app.includes('Update issue')) throw new Error('missing issue update UI');
if (/const issue = issueByIdOrRef\([^;]+?\)\s*\|\|\s*issues\[0\]/s.test(app)) throw new Error('issue deep link falls back to first issue');
if (/const run = runs\.find\([^;]+?\)\s*\|\|\s*runs\[0\]/s.test(app)) throw new Error('run deep link falls back to first run');
if (!/api\.issue\([^)]*route\.issueRef/s.test(app)) throw new Error('issue deep link must fetch missing issue details');
if (!/api\.run\([^)]*route\.runId/s.test(app)) throw new Error('run deep link must fetch missing run details');
if (!app.includes('Issue not found')) throw new Error('missing issue not found state');
if (!app.includes('Run not found')) throw new Error('missing run not found state');
if (!app.includes('Issue failed to load')) throw new Error('missing issue deep link failed state');
if (!app.includes('Run failed to load')) throw new Error('missing run deep link failed state');
if (!/setIssueLoadError\(errorLabel\(error\)\)/s.test(app)) throw new Error('issue deep link non-404 errors must stop loading');
if (!/setRunLoadError\(errorLabel\(error\)\)/s.test(app)) throw new Error('run deep link non-404 errors must stop loading');
if (!app.includes('prompt_metadata')) throw new Error('workflow preview must display prompt metadata instead of raw prompt text');
if (!/function mergeEvents\(/s.test(app)) throw new Error('events must merge/dedupe incrementally');
if (!/maxSeqRef/s.test(app)) throw new Error('events must track max seq cursor');
if (!/api\.events\(maxSeqRef\.current\)/s.test(app)) throw new Error('events must fetch after max seq cursor');
if (!/new EventSource\(`\/api\/v1\/events\/stream\?after_seq=\$\{maxSeqRef\.current\}`\)/s.test(app)) throw new Error('EventSource URL must include after_seq cursor');
if (/source\.onmessage\s*=\s*\(\)\s*=>\s*\{?\s*setSseState\('connected'\);\s*void loadAll\(\)/s.test(app)) throw new Error('SSE messages must not reload first events page');
if (!/loadRunEvents\(route\.runId\)/s.test(app)) throw new Error('run detail must fetch run-specific timeline events');
if (!/runEvents:\s*\([^)]*afterSeq[^)]*\)\s*=>[\s\S]*after_seq=\$\{afterSeq\}/s.test(api)) throw new Error('API client runEvents must support after_seq');
if (!/async function loadRunEvents\([^)]*runId[^)]*\)/s.test(app)) throw new Error('run detail must load run events through a paginated helper');
if (!/api\.runEvents\(runId,\s*afterSeq\)/s.test(app)) throw new Error('run detail pagination must pass after_seq');
if (!/loaded\.length\s*<\s*runEventPageSize/s.test(app)) throw new Error('run detail pagination must stop after a short page');
if (!/afterSeq\s*===\s*nextAfterSeq/s.test(app)) throw new Error('run detail pagination must stop when no new seq is returned');
if (!/RunDetailPage\(\{[\s\S]*markUnauthenticated[\s\S]*\}\s*:/s.test(app)) throw new Error('run detail must receive the global unauthenticated handler');
if (!/loadRunEvents\(route\.runId\)[\s\S]*catch\(\(error\)[\s\S]*isAuthError\(error\)[\s\S]*markUnauthenticated\(\)/s.test(app)) throw new Error('run detail runEvents auth errors must clear global protected data');
if (!/ReviewPacketPage\(\{[\s\S]*markUnauthenticated[\s\S]*\}\s*:/s.test(app)) throw new Error('review packet page must receive the global unauthenticated handler');
if (!/api\.review\(ref\)[\s\S]*catch \(err\)[\s\S]*isAuthError\(err\)[\s\S]*markUnauthenticated\(\)/s.test(app)) throw new Error('review packet auth errors must clear global protected data');
if (!/fetchArtifactContent\(artifact\.content_url\)[\s\S]*catch \(err\)[\s\S]*isAuthError\(err\)[\s\S]*markUnauthenticated\(\)/s.test(app)) throw new Error('artifact content auth errors must clear global protected data');
if (!/WorkflowPage\(\{[\s\S]*markUnauthenticated[\s\S]*\}\s*:/s.test(app)) throw new Error('workflow page must receive the global unauthenticated handler');
if (!/api\.validateWorkflow\(\)[\s\S]*catch \(err\)[\s\S]*isAuthError\(err\)[\s\S]*markUnauthenticated\(\)/s.test(app)) throw new Error('workflow validate auth errors must clear global protected data');
if (!/api\.renderWorkflowPreview\(\)[\s\S]*catch \(err\)[\s\S]*isAuthError\(err\)[\s\S]*markUnauthenticated\(\)/s.test(app)) throw new Error('workflow render preview auth errors must clear global protected data');
if (!/requiresTransitionReason\(/s.test(app) || !/transitionValidationError\(/s.test(app)) throw new Error('transition forms must use shared local validation');
if (/Duplicate canonical ref is required|target === 'Duplicate'[\s\S]*!duplicateOf\.trim\(\)/s.test(app)) throw new Error('duplicate transition must not require canonical ref locally');
if (!/duplicate_of:\s*target === 'Duplicate' \? duplicateOf\.trim\(\) \|\| undefined : undefined/s.test(app)) throw new Error('duplicate transition must submit duplicate_of only when a trimmed value is present');
if (!/disabled=\{Boolean\(transitionValidation\)\}/s.test(app)) throw new Error('transition submit buttons must disable invalid local transitions');
if (!/disabled=\{!comment\.trim\(\)\}/s.test(app)) throw new Error('blank issue comments must be disabled locally');
if (!/disabled=\{!blocker\.trim\(\)\}/s.test(app)) throw new Error('blank blocker submissions must be disabled locally');
if (!/function canOpenReviewPacket\(issue: Issue\)/s.test(app)) throw new Error('review packet open gating must be split from action gating');
if (!/function canPerformReviewAction\(issue: Issue\)/s.test(app)) throw new Error('review actions must use dedicated availability gating');
if (!/function canOpenReviewPacket\(issue: Issue\)[\s\S]*Boolean\(issue\.latest_review_packet_id\)[\s\S]*Boolean\(issue\.latest_review_packet\?\.id\)/s.test(app)) throw new Error('review packet open gating must allow any issue with a latest packet');
if (!/function canPerformReviewAction\(issue: Issue\)[\s\S]*issue\.state === 'Human Review'[\s\S]*issue\.latest_review_packet\?\.status === 'generated'/s.test(app)) throw new Error('review actions must require Human Review state and generated latest packet');
if (!/async function reviewAction\(kind: 'rework' \| 'done'\) \{[\s\S]*!canPerformReviewAction\(selectedIssue\)[\s\S]*!reviewReason\.trim\(\)[\s\S]*return/s.test(app)) throw new Error('action rail review decision handler must re-check generated packet gating');
if (!/\{canPerformReviewAction\(selectedIssue\) \? \([\s\S]*<h3>Review<\/h3>[\s\S]*Send to Rework[\s\S]*Mark Done[\s\S]*\) : null\}/s.test(app)) throw new Error('action rail review controls must render only behind generated packet gating');
if (!/const reviewActionsAvailable = Boolean\(review && review\.status === 'generated' && reviewIssue && canPerformReviewAction\(reviewIssue\)\)/s.test(app)) throw new Error('review page actions must require loaded generated packet status');
if (!/async function reviewAction\(kind: 'rework' \| 'done'\) \{[\s\S]*!reviewActionsAvailable[\s\S]*!reason\.trim\(\)[\s\S]*return/s.test(app)) throw new Error('review page decision handler must re-check generated packet gating');
if (!/\{reviewActionsAvailable \? \([\s\S]*<Section title="Human Review actions">[\s\S]*Send to Rework[\s\S]*Mark Done[\s\S]*\) : \(/s.test(app)) throw new Error('review page controls must render only behind generated packet gating');
if (!/api\.sendToRework\([^,]+,\s*reviewReason\.trim\(\)\)/s.test(app) || !/api\.markDone\([^,]+,\s*reviewReason\.trim\(\)\)/s.test(app)) throw new Error('action rail review decisions must submit trimmed reasons');
if (!/api\.sendToRework\([^,]+,\s*reason\.trim\(\)\)/s.test(app) || !/api\.markDone\([^,]+,\s*reason\.trim\(\)\)/s.test(app)) throw new Error('review page decisions must submit trimmed reasons');
if (!/disabled=\{!reviewReason\.trim\(\)\}/s.test(app) || !/disabled=\{!reason\.trim\(\)\}/s.test(app)) throw new Error('blank review decision reasons must be disabled locally');
if (!/authResetKey/s.test(app)) throw new Error('protected local page state must reset on auth/data reset');
if (!/authResetKey[\s\S]*setFetchedIssue\(null\)/s.test(app)) throw new Error('issue detail must clear fetchedIssue on auth reset');
if (!/authResetKey[\s\S]*setFetchedRun\(null\)[\s\S]*setFetchedRunEvents\(\[\]\)/s.test(app)) throw new Error('run detail must clear fetchedRun and fetchedRunEvents on auth reset');
if (!/authResetKey[\s\S]*setReview\(null\)[\s\S]*setArtifactContent\(\{\}\)/s.test(app)) throw new Error('review page must clear review and artifactContent on auth reset');
if (!/authResetKey[\s\S]*setValidation\(null\)[\s\S]*setPreview\(null\)/s.test(app)) throw new Error('workflow page must clear validation and preview on auth reset');
if (!app.includes('summaryRefreshEvents') || !app.includes('scheduleSummaryRefresh')) throw new Error('SSE event handling must refresh dashboard summaries for state-changing events');
const summaryRefreshBlock = app.match(/summaryRefreshEvents\s*=\s*new Set\(\[([\s\S]*?)\]\)/);
if (!summaryRefreshBlock) throw new Error('summaryRefreshEvents must be an explicit event set');
const summaryRefreshEventSet = new Set(Array.from(summaryRefreshBlock[1].matchAll(/'([^']+)'/g), (match) => match[1]));
const requiredSummaryRefreshEvents = [
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
];
const missingSummaryRefreshEvents = requiredSummaryRefreshEvents.filter((event) => !summaryRefreshEventSet.has(event));
if (missingSummaryRefreshEvents.length > 0) throw new Error(`summary refresh missing events: ${missingSummaryRefreshEvents.join(', ')}`);

for (const selector of ['.page-header', '.page-split', '.metric-strip', '.workbench', '.work-queue', '.context-panel', '.action-rail', '.board', '.metric-grid', '.approval-card', '.artifact-card', '.diagnostics-grid']) {
  if (!styles.includes(selector)) throw new Error(`missing style selector ${selector}`);
}

if (!/jsonschema is required/s.test(validateContracts) || !/python3 -m pip install -r requirements-dev\.txt/s.test(validateContracts)) {
  throw new Error('contract validation must fail fast with jsonschema install guidance');
}

console.log('web tests passed');
