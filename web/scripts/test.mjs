import fs from 'node:fs';

const root = new URL('..', import.meta.url);
const inv = JSON.parse(fs.readFileSync(new URL('action-inventory.json', root), 'utf8'));
const app = fs.readFileSync(new URL('src/App.tsx', root), 'utf8');
const api = fs.readFileSync(new URL('src/api.ts', root), 'utf8');
const styles = fs.readFileSync(new URL('src/styles.css', root), 'utf8');

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

for (const action of ['create issue','update issue','transition issue','dispatch eligible issue','send to rework','mark done','diagnostics export']) {
  if (!inv.required_actions.includes(action)) throw new Error(`missing ${action}`);
}
for (const action of ['git push','create pr','workspace delete','secret read']) {
  if (!inv.forbidden_actions.includes(action)) throw new Error(`missing forbidden ${action}`);
  if (app.toLowerCase().includes(action) || api.toLowerCase().includes(action)) throw new Error(`forbidden action leaked into frontend: ${action}`);
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

console.log('web tests passed');
