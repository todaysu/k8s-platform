# Unified Inspection Findings Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use Code and frontend-design to implement this plan task-by-task.

**Goal:** Turn the existing “needs attention” signals into one ACM-style inspection findings experience that is reused by the home page and a dedicated inspection page.

**Architecture:** Add a pure frontend rule adapter that converts the already-fetched cluster health and fleet-capacity snapshots into normalized findings. The first slice is explicitly a current-state preview: it does not claim persistence, acknowledgement, recovery history, or a second collection pipeline. The home page consumes the same finding set as the inspection page, preventing duplicate health logic.

**Tech Stack:** React, TypeScript, PatternFly 6, Vitest, Testing Library.

---

### Task 1: Define normalized findings and rule adapter

**Files:**
- Create: `web/src/inspections/findings.ts`
- Test: `web/src/inspections/findings.test.ts`

**Steps:**
1. Write tests for unreachable clusters, unready nodes/instances, missing metrics, low capacity, capacity drift, missing requests, and deduplication keys.
2. Run the focused test and confirm it fails before the module exists.
3. Implement the typed finding model and pure rule adapter.
4. Run the focused test and confirm it passes.

### Task 2: Build the inspection center

**Files:**
- Create: `web/src/pages/FleetInspectionsPage.tsx`
- Create: `web/src/pages/FleetInspectionsPage.css`
- Modify: `web/src/pages/ClusterManagementPage.tsx`
- Test: `web/src/App.test.tsx`

**Steps:**
1. Add a failing navigation and content test for `/inspections`.
2. Replace the “后续” navigation item with an active “巡检分析” route.
3. Add summary cards, filters, an operational findings table, and a selected-finding evidence panel.
4. Add direct paths to the affected cluster and the relevant capacity view.
5. Run the focused UI test.

### Task 3: Reuse findings on the overview

**Files:**
- Modify: `web/src/pages/ClusterManagementPage.tsx`
- Modify: `web/src/pages/ClusterManagementPage.css`
- Test: `web/src/App.test.tsx`

**Steps:**
1. Add a failing test proving the overview “需要关注” card opens the inspection center.
2. Replace the cluster-only attention list with the highest-priority normalized findings.
3. Keep fleet health and raw resource totals as factual summary cards.
4. Run the UI tests.

### Task 4: Verify the complete slice

**Files:**
- Verify only.

**Steps:**
1. Run `npm test -- --run`.
2. Run `npm run lint`.
3. Run `npm run build`.
4. Open `/inspections` and verify layout, filtering, selection, empty states, and drill-down at desktop width.
5. Verify `/` uses the same finding set and no longer routes “需要关注” directly to a duplicate cluster filter.

