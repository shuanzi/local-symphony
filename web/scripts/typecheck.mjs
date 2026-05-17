import { existsSync, readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const requiredFiles = [
  'src/App.tsx',
  'src/api.ts',
  'src/types.ts',
  'src/main.tsx',
  'src/styles.css',
  'index.html',
  'vite.config.ts',
  'tsconfig.json'
];
for (const file of requiredFiles) {
  if (!existsSync(new URL(`../${file}`, import.meta.url))) throw new Error(`missing ${file}`);
}

const app = readFileSync(new URL('../src/App.tsx', import.meta.url), 'utf8');
for (const token of ['OverviewPage', 'BoardPage', 'IssueDetailPage', 'RunDetailPage', 'ApprovalInboxPage', 'ReviewPacketPage', 'WorkflowPage', 'DiagnosticsPage']) {
  if (!app.includes(token)) throw new Error(`missing component ${token}`);
}

const hasReactDeps = existsSync(new URL('../node_modules/react', import.meta.url));
if (!hasReactDeps) {
  console.warn('web dependencies not installed; static frontend checks passed. Run pnpm --dir web install for full tsc/vite checks.');
  process.exit(0);
}

const tsc = spawnSync(process.platform === 'win32' ? 'npx.cmd' : 'npx', ['tsc', '--noEmit'], {
  cwd: new URL('..', import.meta.url),
  stdio: 'inherit'
});
if (tsc.status !== 0) process.exit(tsc.status ?? 1);
console.log('web typecheck passed');
