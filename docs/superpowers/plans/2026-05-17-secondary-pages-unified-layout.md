# Secondary Pages Unified Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 统一 Local Symphony WebUI 二级页面的信息结构和操作布局。

**Architecture:** 在 `web/src/App.tsx` 内新增轻量复用组件，不改变 API client、route 或数据模型。样式集中追加到 `web/src/styles.css`，现有页面选择性套用 `PageHeader`、`page-split` 和 `page-aside`。

**Tech Stack:** React, TypeScript, Vite, existing static frontend checks.

---

## Files

- Modify: `web/src/App.tsx`
  - Add `PageHeader`, `MetricStrip`, and split-layout wrappers.
  - Apply them to Board, Issue Detail, Run Detail, Approval Inbox, Review Packet, Workflow, and Diagnostics.
- Modify: `web/src/styles.css`
  - Add `.page-header`, `.page-split`, `.page-main`, `.page-aside`, `.metric-strip`, `.create-drawer`.
- Modify: `web/scripts/typecheck.mjs`
  - Require the new shared components.
- Modify: `web/scripts/test.mjs`
  - Require the new shared selectors.

## Checklist

- [ ] Add static test expectations for `PageHeader`, `.page-header`, and `.page-split`.
- [ ] Add reusable page header and metric strip components.
- [ ] Wrap Board with page header and collapsible create issue area.
- [ ] Convert Issue Detail into main content plus right-side operational summary.
- [ ] Convert Run Detail into run summary plus timeline split.
- [ ] Add page headers and tighter grouping for Approval, Review, Workflow, and Diagnostics.
- [ ] Add responsive CSS so split pages collapse to one column below 1180px.
- [ ] Run web typecheck, test, and build.
- [ ] Browser smoke all primary secondary pages.
