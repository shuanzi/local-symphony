import fs from 'node:fs';

const root = new URL('..', import.meta.url);
const inv = JSON.parse(fs.readFileSync(new URL('action-inventory.json', root), 'utf8'));
const app = fs.readFileSync(new URL('src/App.tsx', root), 'utf8');
const api = fs.readFileSync(new URL('src/api.ts', root), 'utf8');
const styles = fs.readFileSync(new URL('src/styles.css', root), 'utf8');

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
  'Content is not exposed by the Review/Artifact API', 'Command submitted'
];
for (const phrase of sharedStates) {
  if (!app.includes(phrase)) throw new Error(`missing shared-state phrase: ${phrase}`);
}

if (!api.includes('csrf_token')) throw new Error('API client must read csrf_token');
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

for (const selector of ['.board', '.metric-grid', '.approval-card', '.artifact-card', '.diagnostics-grid']) {
  if (!styles.includes(selector)) throw new Error(`missing style selector ${selector}`);
}

console.log('web tests passed');
