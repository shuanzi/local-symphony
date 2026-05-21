# Local Symphony Dashboard

This directory contains the React/TypeScript dashboard for the Local Symphony v1 local control plane. The dashboard uses only REST/SSE endpoints under `/api/v1`; it does not read SQLite, Git, workspaces, artifacts, Codex logs, or Tool Gateway state directly.

## Development

```bash
pnpm --dir web install
pnpm --dir web dev
```

The Vite dev server proxies `/api` and `/tool` to `http://127.0.0.1:3777`. Start the local daemon separately:

```bash
symphony serve --addr 127.0.0.1:3777
```

## Production build

```bash
pnpm --dir web build
symphony serve --addr 127.0.0.1:3777
```

After `web/dist` exists, point `SYMPHONY_DASHBOARD_DIST` at that built directory when running from a checkout:

```bash
SYMPHONY_DASHBOARD_DIST="$PWD/web/dist" symphony serve --addr 127.0.0.1:3777
```

Without the environment variable, `symphony serve` looks for dashboard assets in trusted install locations derived from the executable directory, such as `web/dist`, `../web/dist`, and `../share/local-symphony/web/dist`. It does not serve dashboard assets from the managed project root by default. API routes under `/api/v1` and Tool Gateway at `/tool/v1/call` keep priority over dashboard fallback routing.

## Checks

```bash
pnpm --dir web typecheck
pnpm --dir web test
```

When dependencies are installed, `typecheck` runs TypeScript. Without dependencies it still verifies that the required dashboard files, pages, and action inventory exist, which keeps the repository acceptance harness usable in minimal environments.
