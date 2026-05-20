# Desktop Bottom Bar Sync Recording Fix Design

**Date:** 2026-05-20
**Status:** Approved

## Problem

The desktop bottom bar has two incomplete behaviors:

- Panel services appear unchecked by default, so the bulk service actions are hidden until the user manually selects each visible service.
- Sync recording can toggle bookmark state, but it does not complete the user workflow. Ending the sync recording must freeze each participating panel's captured logs and expose a way to consume the merged result.

## Design

Keep the change scoped to the existing Vue/Pinia desktop implementation:

- `BottomBar.vue` owns bottom-bar service selection and sync recording controls.
- `bookmarkStore` remains the source of truth for per-panel bookmark state and sync group state.
- `LogPanel.vue` already captures logs while a bookmark is recording, so sync recording should reuse that path instead of duplicating log-filtering logic in the bottom bar.

## Behavior

- Every service currently visible in the panel area is selected by default when it first appears in the bottom bar.
- User changes to the bottom-bar service checkboxes are respected after the default selection has been applied.
- Enabling sync recording includes all panels that currently bind a `serviceId`.
- Starting sync recording starts bookmark recording for every panel in the sync group.
- Stopping sync recording first closes active folded rows for each scoped service, then ends every sync bookmark. Each `LogPanel` finalizes its own visible filtered snapshot through the existing bookmark flow.
- After sync recording stops, the bottom bar exposes copy and export actions for the merged sync result.
- Empty sync output is handled explicitly with a small user alert.

## Files

- `desktop/src/components/BottomBar.vue`: default service selection, sync recording controls, copy/export merged sync output.
- `desktop/src/stores/bookmark.ts`: sync bookmark start should preserve each panel's `serviceId`; end should route through the same snapshot-capable logic as single-panel bookmarks.
- `desktop/src/components/__tests__/BottomBar.test.ts`: component tests for default selection and sync recording workflow.
- `desktop/src/stores/__tests__/bookmark.test.ts`: store tests for sync bookmark metadata and snapshot capture.

## Non-Goals

- Do not redesign the bottom bar UI.
- Do not change single-panel bookmark behavior.
- Do not persist sync group state across app restarts.
- Do not start the desktop dev server as part of verification.
