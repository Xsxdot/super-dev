# Desktop Bottom Bar Sync Recording Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make desktop bottom-bar panel services selected by default and make sync recording produce usable merged bookmark output.

**Architecture:** Keep bottom-bar UI state in `BottomBar.vue`, bookmark state in `bookmarkStore`, and log capture in `LogPanel.vue`. Reuse the existing per-panel bookmark capture path by making sync end call the snapshot-aware bookmark ending logic per panel.

**Tech Stack:** Vue 3, Pinia, Vitest, Vue Test Utils, Tauri dialog/fs plugins loaded dynamically by export handlers

---

## File Map

| File | Responsibility |
|------|----------------|
| `desktop/src/components/BottomBar.vue` | Bottom-bar service selection, sync group control, merged copy/export actions |
| `desktop/src/components/__tests__/BottomBar.test.ts` | Component-level tests for bottom-bar default selection and sync workflow |
| `desktop/src/stores/bookmark.ts` | Per-panel and sync bookmark lifecycle |
| `desktop/src/stores/__tests__/bookmark.test.ts` | Store-level tests for sync lifecycle and capture snapshots |

## Tasks

### Task 1: Bookmark Store Sync Lifecycle

- [ ] Add a failing test that `startSyncBookmark` accepts panel metadata and stores each panel's `serviceId`.
- [ ] Add a failing test that `endSyncBookmark` can freeze per-panel logs through the existing `endBookmark` capture path.
- [ ] Update `startSyncBookmark` and `endSyncBookmark` with the smallest API change needed for those tests.
- [ ] Run `pnpm exec vitest run src/stores/__tests__/bookmark.test.ts`.

### Task 2: Bottom Bar Default Selection

- [ ] Add a failing `BottomBar` test that services visible in panels are checked on first render.
- [ ] Implement default selection for newly visible panel services without overwriting user toggles.
- [ ] Run `pnpm exec vitest run src/components/__tests__/BottomBar.test.ts`.

### Task 3: Bottom Bar Sync Recording Workflow

- [ ] Add a failing `BottomBar` test that enabling sync and starting recording registers service-bound panel ids with metadata.
- [ ] Add a failing `BottomBar` test that stopping sync recording calls the store end path and then reveals copy/export actions when merged output exists.
- [ ] Implement sync group setup, start/stop, copy, and export controls in `BottomBar.vue`.
- [ ] Run the bottom-bar component test.

### Task 4: Verification

- [ ] Run focused store and component tests.
- [ ] Run `pnpm build` from `desktop/`.
- [ ] Review `git diff` to ensure no unrelated dirty files were changed.
