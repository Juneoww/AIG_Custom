# AI Infrastructure Workbench and Task Remark Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a polished, AI-infrastructure-only task workbench and creation page while adding a bounded, task-level `remark` that is persisted and visible only in authorized task details.

**Architecture:** Keep the existing generic task routes, shared `PageHeader`, `DataTable`, and `TaskOperationsSummary` unchanged by rendering dedicated AI-infrastructure components only when `fixedTaskType === 'ai_infra_scan'`. Carry `remark` through the protected platform task create/detail boundary and database, but deliberately stop it before the engine adapter, audit metadata, list DTO, and report inputs. Persist the server-calculated, expanded-and-deduplicated AI target count alongside the task so attachment-only and mixed-source task details remain accurate. The specialized form keeps manual targets and uploaded target-list files as parallel sources in one scan-object section; the backend remains the source of truth for merging, validating, and deduplicating those sources.

**Tech Stack:** Go, GORM/PostgreSQL migrations, Gin, React 19, TypeScript, Fluent UI v9, TanStack Query, Vitest/Testing Library, OpenAPI Swagger artifacts, pnpm.

---

## Guardrails and confirmed behavior

- Work only on the dedicated AI routes: `/tasks/ai-infra`, `/tasks/ai-infra/new`, and the already-dedicated AI task detail's new remark display. Do not redesign the global sidebar, global theme, generic task list, or generic create page.
- Do not change task query semantics. Status remains an exact server-side `status` filter, `page_size` remains the real value returned by the API, and no client-side pseudo-filter such as a multi-status “需关注” filter is introduced.
- The four AI workbench metrics are explicitly page/query scoped:
  - 当前查询匹配任务: API `total` for the active exact query;
  - 本页正在执行: only `running` rows;
  - 本页等待 / 调度中: `pending` plus `dispatching` rows;
  - 本页需关注: `failed`, `dispatch_failed`, plus `dispatch_unknown` rows.
- `remark` is optional, trimmed at the create boundary, valid UTF-8, and at most 2,000 Unicode code points. It is neither a target expression nor a scan parameter.
- The dedicated AI form permits manual targets, uploaded ready target-list attachments, or both. It rejects only the case where both sources are absent. The generic create form keeps its current attachment step and content requirement.
- The safe `input_summary.target_count` for a newly created AI task is the persisted count of `runner.ParseTargets` after both sources have been merged and deduplicated. Legacy rows with a zero persisted count retain the existing manual-content fallback solely for backwards-compatible display.
- Follow `@test-driven-development` before implementation, use `@frontend-design` for the page-local visual work, apply `@chinese-script-comments` when adding or materially rewriting TypeScript/TSX modules, and use `@verification-before-completion` before declaring the work complete.

## File structure and responsibility map

### Backend persistence and protected browser contract

- Modify: `internal/platform/tasks/entity.go`
  - Add `Remark` and server-derived `TargetCount` to the persisted `Task` and internal `View`; add only `Remark` to browser `CreateInput`, without adding either field to `EngineTask`.
- Modify: `internal/platform/tasks/service.go`
  - Normalize/validate the bounded Unicode remark, persist it, calculate the final AI target count only for a new task, and leave target validation/engine dispatch input untouched.
- Modify: `internal/platform/tasks/dto.go`
  - Add an optional `remark` only to the browser-safe `TaskDetail` projection after the existing authorization path; source `target_count` from the persisted safe field; keep `TaskSummary` unchanged.
- Modify: `internal/platform/tasks/service_test.go`
  - Cover remark validation, persistence, idempotency mismatch, exact merged target counts for manual/attachment sources, and the no-engine/no-audit propagation boundary.
- Modify: `internal/platform/tasks/report_snapshot_test.go`
  - Prove a task remark is not copied into the persisted report snapshot when a task succeeds.
- Modify: `internal/platform/tasks/browser_contract_test.go`
  - Lock down detail-only remark projection, list omission, and safe task/view equivalence.
- Modify: `internal/platform/tasks/handler_test.go`
  - Prove `POST /tasks` and `GET /tasks/{id}` accept/project a safe remark without exposing raw task inputs.

### Database migration and schema readiness

- Modify: `pkg/database/migrate.go`
  - Add schema migration 10: `platform_tasks.remark text NOT NULL DEFAULT ''` and `platform_tasks.target_count integer NOT NULL DEFAULT 0` using idempotent SQL.
- Modify: `pkg/database/runtime_schema.go`
  - Raise `LatestSchemaVersion` to 10 and require `platform_tasks.remark` at runtime.
- Modify: `pkg/database/migrate_test.go`
  - Update version-count assertions and test migration from a frozen released-v9 task table shape, not the current migration model.
- Modify: `pkg/database/runtime_schema_test.go`
  - Verify a database missing the new column fails read-only runtime validation instead of being altered.

### Frontend API and task detail

- Modify: `web/console/src/shared/api/types.ts`
  - Add `remark?: string` to `TaskCreateRequest` and `TaskDetail`, never to `TaskSummary` or `TaskInputSummary`.
- Modify: `web/console/src/features/tasks/api.ts`
  - Parse a present detail remark only when it is a nonempty bounded string of at most 2,000 Unicode code points; reject malformed/oversized responses.
- Modify: `web/console/src/features/tasks/TaskWorkflow.test.tsx`
  - Test safe parser acceptance, rejection, and that list decoding cannot receive a detail remark.
- Modify: `web/console/src/features/tasks/TaskDetailPage.tsx`
  - Render “任务说明” only when a dedicated AI task has a nonempty safe detail remark; retain the existing safe summary and model-name behavior.
- Modify: `web/console/src/features/tasks/TaskPages.test.tsx`
  - Test conditional detail rendering and keep existing role/type-mismatch behavior intact.

### Dedicated workbench visual components

- Create: `web/console/src/features/tasks/components/AIInfraWorkbenchHeader.tsx`
  - Provide the AI-only large title/subtitle/action layout and optional return link without changing shared `PageHeader`.
- Create: `web/console/src/features/tasks/components/AIInfraTaskOperationsSummary.tsx`
  - Derive and render the four semantically exact signal cards from safe `TaskSummary` rows only.
- Create: `web/console/src/features/tasks/components/AIInfraTaskOperationsSummary.test.tsx`
  - Test the exact status matrix, scope labels, four cards, and accessible named region.
- Create: `web/console/src/features/tasks/components/AIInfraTaskTable.tsx`
  - Render a dedicated semantic Fluent table, precise status pills, and the real pagination footer without changing shared `DataTable` defaults.
- Create: `web/console/src/features/tasks/components/AIInfraWorkbench.styles.ts`
  - Centralize the page-local cold-gray canvas, surface, cobalt, and four semantic status palettes/shared responsive primitives.
- Modify: `web/console/package.json`
- Modify: `web/console/pnpm-lock.yaml`
  - Add the official `@fluentui/react-icons` dependency used by the AI-only header, signal cards, and step indicators; do not use emoji as business icons.

### Dedicated list and create-page integration

- Modify: `web/console/src/features/tasks/TaskListPage.tsx`
  - Preserve the generic branch; use the AI-only header, filter toolbar, four-card summary, and table card when `fixedTaskType` is `ai_infra_scan`.
- Modify: `web/console/src/features/tasks/TaskCreatePage.tsx`
  - Preserve generic markup and behavior; render the dedicated three-section AI workbench with parallel target sources, independent note, governed configuration, and confirmation card.
- Modify: `web/console/src/features/tasks/TaskPages.test.tsx`
  - Update the prior four-step expectation and add focused interaction/accessible-name/API-body assertions for both dedicated pages.

### Public API documentation

- Modify: `docs/api/reference.md`
- Modify: `docs/api/reference.en.md`
  - Document the optional detail-only `remark`, 2,000-character bound, task-level semantics, and explicit non-propagation to engine/audit/report/list data planes.
- Modify: `internal/apidocs/swagger.yaml`
- Modify: `internal/apidocs/swagger.json`
- Modify: `internal/apidocs/docs.go`
  - Keep the checked-in Swagger trio synchronized with the Go handler/request/response contract; update only the affected task schemas and descriptions, not with a blanket `swag init` overwrite.
- Modify: `internal/apidocs/swagger_sync_test.go`
  - Update exact property, request-bound, and schema-version assertions for the deliberate task contract change.

## Implementation tasks

### Task 1: Lock the database migration, target-count, and runtime schema contract

**Files:**
- Modify: `pkg/database/migrate_test.go:72-119, 332-430`
- Modify: `pkg/database/runtime_schema_test.go:57-95`
- Modify: `pkg/database/migrate.go:40-50, 264-283, 366-403`
- Modify: `pkg/database/runtime_schema.go:20-60, 96-127`

- [ ] **Step 1: Write failing migration tests for version 10 and the remark column.**

  Extend the existing “through version nine” test into a version-ten test. Assert all of the following after `Migrate(db)`:

  ```go
  assert.True(t, db.Migrator().HasColumn("platform_tasks", "remark"))
  assert.True(t, db.Migrator().HasColumn("platform_tasks", "target_count"))
  require.Len(t, versions, 10)
  assert.Equal(t, int64(10), versions[9].Version)
  ```

  Create a test-only `releasedV9PlatformTaskMigration` struct in `migrate_test.go` with the exact released fields and `TableName() string { return "platform_tasks" }`; it must deliberately omit both `Remark` and `TargetCount`. Use it (or explicit frozen old-table DDL) to construct the v9 fixture rather than calling the now-evolved `migratePlatformTaskSchema` helper. Run `Migrate` twice and assert that both new columns are added once with `remark == ""` and `target_count == 0` for preexisting rows. Add runtime-schema tests that separately omit `remark` and `target_count`, call `ValidateRuntimeSchema`, and assert a migration-required error without creating either column.

- [ ] **Step 2: Run only the new database tests and confirm they fail for the missing migration.**

  Run: `go test ./pkg/database -run 'TestMigration.*Remark|TestRuntimeSchema.*Remark' -count=1`

  Expected: FAIL because migration 10, `LatestSchemaVersion`, and the required runtime columns do not yet exist. If this workstation lacks `go`, record that limitation and run the command in the Go-enabled validation environment before merging.

- [ ] **Step 3: Implement a minimal, forward-only migration.**

  Add a tenth entry and a migration function; do not alter versions 1–9:

  ```go
  {version: 10, apply: migratePlatformTaskRemarkAndTargetCount},

  func migratePlatformTaskRemarkAndTargetCount(db *gorm.DB) error {
      for _, statement := range []string{
          `ALTER TABLE platform_tasks ADD COLUMN IF NOT EXISTS remark text NOT NULL DEFAULT ''`,
          `ALTER TABLE platform_tasks ADD COLUMN IF NOT EXISTS target_count integer NOT NULL DEFAULT 0`,
      } {
          if err := db.Exec(statement).Error; err != nil { return err }
      }
      return nil
  }
  ```

  Raise `LatestSchemaVersion` to `10`, add `"remark"` and `"target_count"` to `requiredRuntimeColumns["platform_tasks"]`, and add matching `Remark string`/`TargetCount int` fields to the migration-local `platformTaskMigration` model so future schema inspection stays accurate. Do not add an index.

- [ ] **Step 4: Re-run migration and schema tests.**

  Run: `go test ./pkg/database -run 'TestMigration|TestRuntimeSchema' -count=1`

  Expected: PASS; migration is idempotent, v9 data reads an empty remark/zero derived count, and runtime validation fails safely if either new column is absent.

- [ ] **Step 5: Commit the isolated schema change.**

  ```bash
  git add pkg/database/migrate.go pkg/database/runtime_schema.go pkg/database/migrate_test.go pkg/database/runtime_schema_test.go
  git commit -m "feat: persist platform task remarks"
  ```

### Task 2: Make the remark a bounded platform field and persist final target counts without engine/audit/report propagation

**Files:**
- Modify: `internal/platform/tasks/entity.go:16-96`
- Modify: `internal/platform/tasks/service.go:125-310`
- Modify: `internal/platform/tasks/service_test.go:286-440, 769-845, 914-1168`
- Modify: `internal/platform/tasks/handler_test.go:86-170`
- Modify: `internal/platform/tasks/report_snapshot_test.go:23-208`

- [ ] **Step 1: Add failing service tests for the platform-only remark boundary.**

  Add tests that create an AI infrastructure task with a useful note such as `本次仅扫描已授权的预发布集群` and assert:

  ```go
  assert.Equal(t, "本次仅扫描已授权的预发布集群", stored.Remark)
  assert.Equal(t, "192.0.2.10", capturedEngineTask.Content)
  assert.NotContains(t, string(capturedEngineTask.Params), "预发布集群")
  assert.NotContains(t, string(auditMetadata), "预发布集群")
  ```

  Use the existing recording/fake engine to capture the submitted `EngineTask`; do not add a `Remark` field to `EngineTask` just to test this. Add table-driven rejection tests for more than 2,000 runes and invalid UTF-8, and idempotency tests proving: same key plus same normalized remark returns the persisted task; same key plus a different remark returns `ErrInvalid` before reference validation/audit/dispatch side effects.

  Add server-count regressions to the existing target-expression tests:

  ```go
  // Manual range expands to nine real targets.
  assert.Equal(t, 9, stored.TargetCount)
  // Attachment-only list is accepted and counted.
  assert.Equal(t, 2, stored.TargetCount)
  // Overlap across manual and attachment input is deduplicated once.
  assert.Equal(t, 3, stored.TargetCount)
  ```

  The attachment-only success case must use `Content: ""`, one ready UTF-8 target-list attachment, and a remark. Keep the existing manual+attachment merge test as a separate proof that both sources combine, and add the overlap case so a raw line count cannot accidentally stand in for the merged result. In `report_snapshot_test.go`, complete a task carrying the sentinel note and assert none of `RawResult`, `Risk`, `RenderData`, or the serialized snapshot contains it.

  Add server-final zero-target regressions: a direct service create with empty `Content`/no attachments and a service create with only an all-blank ready target-list attachment must return `ErrInvalid`, persist nothing, and submit nothing. In `handler_test.go`, send the protected `POST /tasks` JSON for an AI task with `content: ""` and no attachment IDs; assert the fixed `400 {"error":"invalid task request"}` response. This proves browser-side checks are not the only barrier.

- [ ] **Step 2: Run the focused service tests and confirm they fail.**

  Run: `go test ./internal/platform/tasks -run 'TestCreate.*Remark|TestIdempotent.*Remark|TestCreateAIInfra.*Attachment|TestTrustedSuccess.*Remark|TestTaskCreate.*Empty' -count=1`

  Expected: FAIL because `CreateInput`, `Task`, and candidate comparison currently have no remark semantics, and safe details derive only manual nonempty-line counts rather than the expanded, merged target count.

- [ ] **Step 3: Add one canonical remark normalizer at the service boundary.**

  In `service.go`, define a single bounded helper and reuse it before any lock, audit, attachment read, or engine action:

  ```go
  const MaxTaskRemarkRunes = 2_000

  func normalizeTaskRemark(value string) (string, bool) {
      value = strings.TrimSpace(value)
      return value, utf8.ValidString(value) && utf8.RuneCountInString(value) <= MaxTaskRemarkRunes
  }
  ```

  Set `input.Remark` to the normalized value in `Service.Create`; reject it when the helper returns false. Add `Remark string` with `gorm:"not null;column:remark"` and `TargetCount int` with `gorm:"not null;column:target_count"` to `Task`, plus matching `Remark`/`TargetCount` fields in `View` and `viewOf`. `CreateInput` contains `Remark` but must not accept a client-supplied target count. Copy the normalized remark and a zero initial count into the creation candidate.

  Include `persisted.Remark == candidate.Remark` in `sameCreateRequest`, but do **not** compare `TargetCount`: it is a service-derived result not part of the browser's idempotent request, and a duplicate retry must still return before attachment reads/reference validation. Leave `taskCreatedAuditMetadata`, `EngineTask`, WebSocket task structures, and adapter construction free of `Remark` and `TargetCount`. The existing engine call should continue to receive only ID, owner, task type, content, normalized params, attachment IDs, and country.

- [ ] **Step 4: Preserve the correct target-source rule.**

  Change `validateInfrastructureTargets` to return `(int, error)`: it must call `AppendTargetExpressionLines(nil, content)`, then `ReadReadyTargetExpressions` for attachment IDs, then `runner.ParseTargets(expressions)`. Explicitly reject an empty expanded result before returning the count:

  ```go
  expanded, err := runner.ParseTargets(expressions)
  if err != nil || len(expanded) == 0 {
      return 0, ErrInvalid
  }
  return len(expanded), nil
  ```

  It must never receive or read the remark. Preserve the existing idempotency ordering: build/check the zero-count candidate first, return an existing matching task without live reads, then for a truly new candidate validate references/attachments and assign the returned count before persistence. Ensure attachment-only input succeeds only when a ready target-list attachment contributes expressions; an empty manual body with no attachments or an all-blank list remains invalid at the service boundary.

- [ ] **Step 5: Re-run focused service tests.**

  Run: `go test ./internal/platform/tasks -run 'TestCreate.*Remark|TestIdempotent.*Remark|TestCreateAIInfra.*Attachment|TestTrustedSuccess.*Remark|TestTaskCreate.*Empty' -count=1`

  Expected: PASS, with exact expanded/deduplicated counts stored for manual, attachment-only, and overlapping sources; no captured engine params/content, audit metadata, target parser input, or report snapshot contains the note.

- [ ] **Step 6: Commit the service boundary.**

  ```bash
  git add internal/platform/tasks/entity.go internal/platform/tasks/service.go internal/platform/tasks/service_test.go internal/platform/tasks/handler_test.go internal/platform/tasks/report_snapshot_test.go
  git commit -m "feat: persist AI task metadata safely"
  ```

### Task 3: Project remarks and server-computed target counts only through the authorized task-detail contract

**Files:**
- Modify: `internal/platform/tasks/dto.go:17-90`
- Modify: `internal/platform/tasks/browser_contract_test.go:230-388`
- Modify: `internal/platform/tasks/handler_test.go:86-121`

- [ ] **Step 1: Write failing browser-contract tests.**

  Add a task with a nonempty remark and persisted `TargetCount`, then assert the authorized detail JSON has a top-level `remark` and `input_summary.target_count` equal to that exact persisted value, while the corresponding list JSON does not include either field. Cover both `taskDetailOf(task)` and `taskDetailOfView(viewOf(task))` so the accepted-create response and later GET response remain equivalent. Add a task with no remark and assert `remark` is omitted rather than serialized as an empty string. Keep a legacy zero-count fixture and assert it uses the old manual-content display fallback only for compatibility.

  Include sentinel checks showing `content`, `params`, `attachment_ids`, engine session, and `dispatch_error` are still absent. The remark is the only newly approved free-text field.

- [ ] **Step 2: Run the targeted browser/handler tests and confirm they fail.**

  Run: `go test ./internal/platform/tasks -run 'TestTaskBrowser.*Remark|TestTaskCreate.*Remark|TestTaskAndViewDetailProjection' -count=1`

  Expected: FAIL because `TaskDetail` and `taskDetailFields` currently cannot carry a remark.

- [ ] **Step 3: Implement the narrow safe projection.**

  Add this field only to `TaskDetail`:

  ```go
  Remark string `json:"remark,omitempty"`
  ```

  Thread it through `taskDetailFields`, `taskDetailOf`, `taskDetailOfView`, and `taskDetailFromFields`. Thread `TargetCount` through the same internal field flow, then have `safeInputSummary` prefer a positive persisted count bounded by `runner.MaxTargetExpressions`; use `nonEmptyLineCount(content)` only when the persisted value is zero for an old row. Do not add the remark to `TaskSummary`, `TaskInputSummary`, audit metadata, or report models. Keep the authorization gate in `BrowserGet`/handler unchanged; the DTO is constructed only after that gate.

- [ ] **Step 4: Re-run targeted protected-contract tests.**

  Run: `go test ./internal/platform/tasks -run 'TestTaskBrowser.*Remark|TestTaskCreate.*Remark|TestTaskAndViewDetailProjection' -count=1`

  Expected: PASS; lists remain narrow, new AI details report their final server-derived count, old rows retain a safe fallback, empty remarks are absent, and authorized detail/accepted-create responses match.

- [ ] **Step 5: Commit the safe projection.**

  ```bash
  git add internal/platform/tasks/dto.go internal/platform/tasks/browser_contract_test.go internal/platform/tasks/handler_test.go
  git commit -m "feat: expose task remarks in safe details"
  ```

### Task 4: Add the typed frontend detail/create contract before changing UI

**Files:**
- Modify: `web/console/src/shared/api/types.ts:74-113`
- Modify: `web/console/src/features/tasks/api.ts:26-121`
- Modify: `web/console/src/features/tasks/TaskWorkflow.test.tsx:42-150`
- Modify: `web/console/src/features/tasks/TaskDetailPage.tsx:22-180`
- Modify: `web/console/src/features/tasks/TaskPages.test.tsx:423-603`

- [ ] **Step 1: Write failing frontend API/detail tests.**

  Add parser cases for:

  ```ts
  expect(parseTaskDetail({ ...base, remark: '仅扫描授权范围' }).remark).toBe('仅扫描授权范围')
  expect(() => parseTaskDetail({ ...base, remark: 7 })).toThrow(ApiError)
  expect(() => parseTaskDetail({ ...base, remark: 'a'.repeat(2001) })).toThrow(ApiError)
  ```

  Use an astral Unicode case to prove the 2,000-character check counts Unicode code points rather than UTF-16 units. In `TaskPages.test.tsx`, assert the AI detail shows the “任务说明” fact for a present remark and omits the entire fact when it is absent. Also assert the generic task list has no remark column/text.

- [ ] **Step 2: Run the focused frontend tests and confirm they fail.**

  Run: `pnpm --dir web/console test:run -- src/features/tasks/TaskWorkflow.test.tsx src/features/tasks/TaskPages.test.tsx`

  Expected: FAIL because detail types and decoder have no recognized `remark` field and the detail page does not render it.

- [ ] **Step 3: Implement safe types and parsing.**

  Add `remark?: string` to `TaskCreateRequest` and `TaskDetail`, but not to list/summary types. Add a `parseOptionalRemark` helper in `api.ts` that accepts an absent field, rejects non-strings/empty strings/over-2,000-code-point values, and uses `Array.from(value).length` for the Unicode bound. Merge it into the result only when present.

  In `TaskDetailPage`, render a plain-text “任务说明” fact only for a matching dedicated AI task with `query.data.remark`; do not use `dangerouslySetInnerHTML`, do not put the note in `input_summary`, and do not make it part of model-catalog fetching.

- [ ] **Step 4: Re-run the focused frontend tests.**

  Run: `pnpm --dir web/console test:run -- src/features/tasks/TaskWorkflow.test.tsx src/features/tasks/TaskPages.test.tsx`

  Expected: PASS, with malformed detail responses failing closed and ordinary text safely rendered by React.

- [ ] **Step 5: Commit the browser contract slice.**

  ```bash
  git add web/console/src/shared/api/types.ts web/console/src/features/tasks/api.ts web/console/src/features/tasks/TaskWorkflow.test.tsx web/console/src/features/tasks/TaskDetailPage.tsx web/console/src/features/tasks/TaskPages.test.tsx
  git commit -m "feat: show safe AI task remarks in console"
  ```

### Task 5: Build isolated AI workbench primitives and metric semantics

**Files:**
- Create: `web/console/src/features/tasks/components/AIInfraWorkbench.styles.ts`
- Create: `web/console/src/features/tasks/components/AIInfraWorkbenchHeader.tsx`
- Create: `web/console/src/features/tasks/components/AIInfraTaskOperationsSummary.tsx`
- Create: `web/console/src/features/tasks/components/AIInfraTaskOperationsSummary.test.tsx`
- Create: `web/console/src/features/tasks/components/AIInfraTaskTable.tsx`
- Modify: `web/console/package.json`
- Modify: `web/console/pnpm-lock.yaml`

- [ ] **Step 1: Write failing, standalone metric-component tests.**

  Use an eight-status `TaskSummary` matrix and require this exact derivation:

  ```ts
  expect(deriveAIInfraTaskPageActivity(statusMatrix)).toEqual({
    running: 1,
    waitingOrDispatching: 2,
    attention: 3,
  })
  ```

  Render the component and assert an accessible region named “AI 基础设施扫描运行态势”, a card labeled “当前查询匹配任务”, visible “当前页”/“当前查询” scope text, and all four named values. Assert `dispatching` does not increase “本页正在执行”.

- [ ] **Step 2: Run the new component test and confirm it fails.**

  Run: `pnpm --dir web/console test:run -- src/features/tasks/components/AIInfraTaskOperationsSummary.test.tsx`

  Expected: FAIL because the dedicated component does not yet exist.

- [ ] **Step 3: Install the official Fluent icon package.**

  Run: `pnpm --dir web/console add @fluentui/react-icons`

  Expected: `web/console/package.json` and `web/console/pnpm-lock.yaml` update. Review the lockfile diff; do not replace unrelated dependency versions.

- [ ] **Step 4: Implement page-local visual primitives.**

  In `AIInfraWorkbench.styles.ts`, keep the palette and reusable visual primitives local to this task feature: a light cool-gray canvas/light surface treatment, cobalt primary action, and blue/green/orange/red signal colors. Express every surface, text, border, focus, and status color through Fluent `tokens`/theme CSS variables (not fixed white or fixed dark hex values) so `ledgerDarkTheme` automatically supplies dark canvas/surface/contrast values. Include responsive grid styles for four, two, and one columns; all text must retain readable contrast in both themes.

  Implement `AIInfraWorkbenchHeader` with a semantic `<header>`, one `<h1>`, optional subtitle/return link, and an accessible Fluent primary action. Use icons such as `AddRegular`, `ArrowLeftRegular`, `ClipboardTaskRegular`, `PlayCircleRegular`, `ClockRegular`, and `WarningRegular` as decorative `aria-hidden` elements; retain text labels for every action/state.

  Implement `AIInfraTaskOperationsSummary` against only safe `TaskSummary[]` and `total`; it must make no API calls. Its core branching must remain equivalent to:

  ```ts
  if (task.status === 'running') activity.running += 1
  else if (task.status === 'pending' || task.status === 'dispatching') activity.waitingOrDispatching += 1
  else if (task.status === 'failed' || task.status === 'dispatch_failed' || task.status === 'dispatch_unknown') activity.attention += 1
  ```

  Implement `AIInfraTaskTable` with Fluent `Table`/caption/column scopes rather than changing `shared/components/DataTable.tsx`. Give its status pills named semantic styles for every exact status, stable columns, horizontal overflow containment, and a footer that only displays `total`, `page`, `pageSize`, previous, and next from the real API response.

- [ ] **Step 5: Re-run primitive tests and type checking.**

  Run: `pnpm --dir web/console test:run -- src/features/tasks/components/AIInfraTaskOperationsSummary.test.tsx`

  Run: `pnpm --dir web/console typecheck`

  Add a dark-theme render assertion in the dedicated component test by wrapping it in `FluentProvider theme={ledgerDarkTheme}`. Verify it still exposes all named text/actions and that its surface/status styles resolve through Fluent CSS variables rather than hard-coded light-only colors.

  Expected: PASS. Generic `TaskOperationsSummary`, `DataTable`, and `PageHeader` are untouched, and the dedicated component remains usable in `ledgerDarkTheme`.

- [ ] **Step 6: Commit isolated workbench primitives.**

  ```bash
  git add web/console/package.json web/console/pnpm-lock.yaml web/console/src/features/tasks/components/AIInfraWorkbench.styles.ts web/console/src/features/tasks/components/AIInfraWorkbenchHeader.tsx web/console/src/features/tasks/components/AIInfraTaskOperationsSummary.tsx web/console/src/features/tasks/components/AIInfraTaskOperationsSummary.test.tsx web/console/src/features/tasks/components/AIInfraTaskTable.tsx
  git commit -m "feat: add AI infrastructure workbench components"
  ```

### Task 6: Integrate the dedicated visual task workbench without affecting generic lists

**Files:**
- Modify: `web/console/src/features/tasks/TaskListPage.tsx:1-344`
- Modify: `web/console/src/features/tasks/TaskPages.test.tsx:120-395`

- [ ] **Step 1: Write failing dedicated-list interaction and DOM tests.**

  Extend the existing fixed-type list tests to assert the dedicated page has:

  ```tsx
  screen.getByRole('heading', { name: 'AI 基础设施扫描' })
  screen.getByRole('link', { name: '新建 AI 基础设施扫描任务' })
  screen.getByRole('group', { name: 'AI 基础设施扫描状态筛选' })
  screen.getByRole('region', { name: 'AI 基础设施扫描运行态势' })
  screen.getByRole('table', { name: /AI 基础设施扫描任务台账/i })
  ```

  Verify a status change and pagination still send `task_type=ai_infra_scan`, only one exact `status`, `page_size=20`, and no invented attention-filter parameter. Keep assertions for auditors not seeing a creation action.

- [ ] **Step 2: Run the targeted page tests and confirm they fail.**

  Run: `pnpm --dir web/console test:run -- src/features/tasks/TaskPages.test.tsx`

  Expected: FAIL because the current AI route still uses the neutral shared header, summary, and table.

- [ ] **Step 3: Render a dedicated AI branch inside `TaskListPage`.**

  Keep parsing, URL normalization, `useQuery`, role checks, and the generic task-list rendering as-is. When `fixedTaskType === 'ai_infra_scan'`, compose `AIInfraWorkbenchHeader`, a white status toolbar with the existing controlled `<Select>`, `AIInfraTaskOperationsSummary`, and `AIInfraTaskTable`.

  The new primary action must navigate to the existing `/tasks/ai-infra/new` route and use the same role gate. Keep the exact table rows/links and status labels, but supply the dedicated table/status visual treatment. Keep loading, empty, forbidden, and error states explicit and accessible; wrap them in the local canvas only, not a global layout.

  Do not modify default styling or props of `PageHeader`, `DataTable`, or `TaskOperationsSummary`. Do not change query keys, `normalizedTaskSearch`, pagination math, `taskStatusLabels`, `taskTypeLabels`, or API filters.

- [ ] **Step 4: Run focused regression tests.**

  Run: `pnpm --dir web/console test:run -- src/features/tasks/TaskPages.test.tsx src/features/tasks/components/TaskOperationsSummary.test.tsx src/features/tasks/components/AIInfraTaskOperationsSummary.test.tsx`

  Expected: PASS; old generic summary tests prove the shared component retained its original `dispatching + running` semantics, while the dedicated AI component uses the agreed stricter semantics.

- [ ] **Step 5: Commit the dedicated list integration.**

  ```bash
  git add web/console/src/features/tasks/TaskListPage.tsx web/console/src/features/tasks/TaskPages.test.tsx
  git commit -m "feat: redesign AI infrastructure task workbench"
  ```

### Task 7: Rework the dedicated AI creation page into three polished sections

**Files:**
- Modify: `web/console/src/features/tasks/TaskCreatePage.tsx:1-388`
- Modify: `web/console/src/features/tasks/TaskPages.test.tsx:603-1039`

- [ ] **Step 1: Write failing behavior and accessible-layout tests.**

  Replace the old “固定四步” test with a dedicated-page test for exactly these visible guide labels: “1 扫描对象”, “2 扫描配置”, and “3 确认并提交”. Assert the generic `/tasks/new` page still shows its independent attachment step.

  Add dedicated AI tests for all four source/note states:

  1. manual target only submits;
  2. ready uploaded target-list attachment only submits with `content: ''`;
  3. manual target plus ready attachment submits both without merging them in the browser;
  4. neither source blocks POST with “请填写扫描目标或导入目标清单”.

  Add an assertion that entering a task remark does not alter target-preview text or `params`, and that a nonempty trimmed remark is sent as a top-level `remark`. Assert a blank or whitespace-only remark is omitted. Test the 2,000-code-point counter/validation and ensure no language selector is present in the dedicated route.

- [ ] **Step 2: Run the focused create-page tests and confirm they fail.**

  Run: `pnpm --dir web/console test:run -- src/features/tasks/TaskPages.test.tsx`

  Expected: FAIL because the page currently labels the textbox as target-or-note, uses a separate attachment step, requires nonempty `content`, and has no `remark` payload.

- [ ] **Step 3: Update dedicated-only submission rules and state.**

  Add `remark` state. At submit time, before creating `TaskSubmission`, use logic equivalent to:

  ```ts
  const hasManualTarget = content.trim().length > 0
  const hasImportedTargetList = attachments.length > 0
  if (isDedicatedAI && !hasManualTarget && !hasImportedTargetList) {
    throw new Error('请填写扫描目标或导入目标清单。')
  }
  const normalizedRemark = remark.trim()
  const input: TaskCreateRequest = {
    task_type: effectiveTaskType,
    content,
    params,
    attachment_ids: attachments.map((item) => item.id),
    country_iso_code: isDedicatedAI ? 'zh_CN' : language,
    ...(isDedicatedAI && normalizedRemark ? { remark: normalizedRemark } : {}),
  }
  ```

  Keep the local target-expression preview as a check on manual content only. It must not parse or reject a remark. Keep the generic form’s current nonempty-content validation and attachment behavior unchanged. Before `preflightAttachments`, apply the dedicated target-list UI limit of 1 MiB per selected file and give a fixed retryable error; the server remains final authority for UTF-8, attachment ownership/readiness, merge, deduplication, and expression limits.

- [ ] **Step 4: Render the dedicated three-section workbench layout.**

  Use `AIInfraWorkbenchHeader` and local workbench styles only on `isDedicatedAI`:

  - The page header provides a return-to-workbench link, title/subtitle, and a three-item non-interactive progress guide. It is an information architecture indicator, not a hidden-field wizard.
  - “扫描对象” is a white surface with a responsive two-column grid: a manually entered “手工填写扫描目标（可选）” textarea on the left and “导入目标清单（可选）” file controls/uploaded-list rows on the right. Both source descriptions say the server merges, validates, and deduplicates them. Place the preexisting target-format guidance/preview in a blue information panel beneath or beside the manual field.
  - Put “任务说明 / 备注（可选）” below the two sources as a full-width plain-text textarea, with a visible Unicode character counter and clear statement that it is retained as task description rather than scan configuration.
  - “扫描配置” uses a responsive field grid for the governed model selector, timeout, and exact port-mode control. Preserve the fixed-China language behavior by rendering no language dropdown. Keep the fixed AI port explanation and high-emphasis full-TCP warning.
  - “确认并提交” is a final white card with idempotency/authorization wording, a secondary cancel button, and a 48-px-class cobalt “创建 AI 基础设施扫描任务” primary button. At narrow widths, stack controls and make the primary action full-width.

  Retain the current generic fieldsets verbatim as much as possible; hide the generic attachment/confirmation fieldsets only for the dedicated AI route so their existing UI and tests stay stable.

- [ ] **Step 5: Run create/detail and accessibility regressions.**

  Run: `pnpm --dir web/console test:run -- src/features/tasks/TaskPages.test.tsx src/features/tasks/TaskWorkflow.test.tsx src/features/tasks/Attachments.test.tsx`

  Run: `pnpm --dir web/console typecheck`

  Expected: PASS. Dedicated AI uses three visual sections, target sources are parallel, note semantics are independent, and generic creation remains unchanged.

- [ ] **Step 6: Commit the creation workflow.**

  ```bash
  git add web/console/src/features/tasks/TaskCreatePage.tsx web/console/src/features/tasks/TaskPages.test.tsx
  git commit -m "feat: streamline AI infrastructure task creation"
  ```

### Task 8: Synchronize API references and checked-in Swagger

**Files:**
- Modify: `docs/api/reference.md:72-93`
- Modify: `docs/api/reference.en.md:72-93`
- Modify: `internal/apidocs/swagger.yaml`
- Modify: `internal/apidocs/swagger.json`
- Modify: `internal/apidocs/docs.go`
- Modify: `internal/apidocs/swagger_sync_test.go:499-503, 613-670, 742-800`
- Test: `internal/apidocs` package tests

- [ ] **Step 1: Write/update the Swagger contract test expectation before editing artifacts.**

  Update the existing exact-property checks in `internal/apidocs/swagger_sync_test.go` before editing artifacts. Assert that the task-create schema accepts optional top-level `remark` with `maxLength: 2000`, `tasks.TaskDetail` exposes optional `remark`, and `tasks.TaskSummary` does not. Update the API-guide required-text expectations from schema v9 to v10 and add the new required remark-boundary wording so the human docs cannot silently drift.

- [ ] **Step 2: Run the Swagger test and confirm it fails.**

  Run: `go test ./internal/apidocs -count=1`

  Expected: FAIL until all three checked-in Swagger outputs agree on the new field and description.

- [ ] **Step 3: Update the API prose and the Swagger trio deliberately.**

  In both human API references, say that `remark` is optional task-level text (maximum 2,000 Unicode characters), returned only in authorized `TaskDetail`, omitted from list summaries when absent, and never a target expression, scan parameter, engine input, audit metadata, or report snapshot. Document that newly created AI task `input_summary.target_count` is the server-calculated merged/deduplicated count, not a raw manual line count. Update the documented schema version to v10 while preserving the existing raw-input hardening language.

  In `swagger.yaml`, `swagger.json`, and `docs.go`, add the same bounds and boundary language to the protected task-create body and `TaskDetail` definition. Keep the historical response-hardening statements accurate: `remark` is a narrowly approved detail field, while raw `content`, `params`, attachments, identities, and engine diagnostics remain absent. Do not run a default `swag init` that could overwrite unrelated runtime artifacts.

- [ ] **Step 4: Re-run API documentation verification.**

  Run: `go test ./internal/apidocs -count=1`

  Expected: PASS, with yaml/json/docs.go describing the same request and response shape.

- [ ] **Step 5: Commit documentation and generated contract artifacts.**

  ```bash
  git add docs/api/reference.md docs/api/reference.en.md internal/apidocs/swagger.yaml internal/apidocs/swagger.json internal/apidocs/docs.go internal/apidocs/swagger_sync_test.go
  git commit -m "docs: describe task remark API boundary"
  ```

### Task 9: Complete cross-layer verification and visual acceptance

**Files:**
- Verify only; do not add production files solely for a mock.
- Preserve untracked local preview helper `.tmp_ai_infra_preview.py` as a local-only aid; do not stage or commit it.

- [ ] **Step 1: Run Go checks in a Go-enabled environment.**

  Run: `go test ./internal/platform/tasks ./pkg/database ./internal/apidocs -count=1`

  Run: `go test ./...`

  Expected: PASS. If the current workstation has no Go toolchain, explicitly report these as unrun here and obtain the results from a qualified environment before merging.

- [ ] **Step 2: Run the complete console quality suite.**

  Run: `pnpm --dir web/console lint`

  Run: `pnpm --dir web/console typecheck`

  Run: `pnpm --dir web/console test:run`

  Run: `pnpm --dir web/console build`

  Expected: PASS with no TypeScript, lint, unit-test, or production-build regressions.

- [ ] **Step 3: Inspect the actual page at desktop and narrow widths.**

  Use the local mock only with de-identified task data and inspect `/tasks/ai-infra` plus `/tasks/ai-infra/new` at approximately 1440 px, 768 px, and 320 px widths in both the configured light and dark console themes. Confirm:

  - no sidebar/global-theme code change, while both existing theme modes render the local workbench with appropriate surfaces and contrast;
  - cobalt action, status filter toolbar, and four semantically accurate colored cards are visible;
  - table card keeps caption, column headers, exact status labels, and real pagination;
  - manual targets and target-list upload are visually parallel, the note is clearly separate, and the language picker is hidden;
  - focus styles, labels, text contrast, horizontal table scrolling, and single-column narrow layouts remain usable.

- [ ] **Step 4: Run repository cleanliness checks.**

  Run: `git diff --check`

  Run: `git status --short`

  Expected: no whitespace errors; only intentional source/docs/lockfile changes are staged or committed, and `.tmp_ai_infra_preview.py` remains untracked/uncommitted unless the user explicitly asks to retain it.

- [ ] **Step 5: Create the final verification commit only if prior logical commits left edits.**

  ```bash
  git add -u
  git commit -m "test: verify AI infrastructure workbench"
  ```

  Do not include `.tmp_ai_infra_preview.py`. Do not merge `codex/ai-infra-scan` into `develop`; that remains a separate user-authorized integration action.
