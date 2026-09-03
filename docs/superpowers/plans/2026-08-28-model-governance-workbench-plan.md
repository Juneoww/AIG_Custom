# Model Governance Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Turn /models into a light, query-aware governance workbench that distinguishes server-backed catalog scope from current-page configuration signals without exposing credentials.

**Architecture:** ModelListPage remains the owner of routing, query lifecycle and write-state machines. A pure ModelCatalogGovernanceSummary derives only current-page signals from safe catalog items and a supplied manageability predicate. One current-success modelCatalog value gates every data-derived surface so retained React Query data cannot appear after a failed refetch.

**Tech Stack:** React 19, TypeScript, Fluent UI v9/Griffel, TanStack Query v5, React Router, Vitest, Testing Library, Docker Node 22 front-end gate.

---

## File map

| File | Responsibility |
| --- | --- |
| web/console/src/features/models/components/ModelCatalogGovernanceSummary.tsx | Pure, accessible current-query/current-page governance brief; no fetches, routes, storage or credentials. |
| web/console/src/features/models/components/ModelCatalogGovernanceSummary.test.tsx | Tests summary counts, boundaries, labels and empty behavior. |
| web/console/src/features/models/ModelListPage.tsx | Owns query state, role-gated actions, URL pagination and placement of brief, workspace, ledger and pager. |
| web/console/src/features/models/ModelForm.tsx | Keeps credential lifecycle while collapsing its layout at 960px. |
| web/console/src/features/models/ModelPages.test.tsx | Integration coverage for success, empty, out-of-range, error and cached-refetch failures plus existing write safety. |

Do not modify models/api.ts, route definitions, backend APIs, DataTable, session code, global theme or rule data.

## Shared test command

Run from web/console:

~~~
.\node_modules\.bin\vitest.CMD run src\features\models\ModelPages.test.tsx src\features\models\components\ModelCatalogGovernanceSummary.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

If the workspace sandbox cannot read Vite's ancestor config directory, rerun the identical command with approved local test permission. A sandbox startup error is neither a product red nor green result.

### Task 1: Define the model catalog view contract

**Files:**
- Modify: web/console/src/features/models/ModelPages.test.tsx

- [ ] **Step 1: Write a failing success-state contract.**

Use three safe server-page-2 rows with total 45: a manageable private platform model, a disabled manageable platform model, and a role-limited read-only platform or YAML model. Build every raw response row from the existing catalogItem() fixture (or retain its token: '********' sentinel), because parseModelCatalog deliberately rejects raw rows without that masked field. Assert:
- region 模型治理态势 appears before native table 受治理模型台账;
- group 当前查询 exposes server 匹配模型 total and returned page, never a client total;
- group 本页治理信号 exposes exact independent counts for 可配置模型, 已停用 and 只读项;
- source/scope/status are semantic text or Badges, role actions remain unchanged, and URL pagination stays server-driven.

- [ ] **Step 2: Add failing state-boundary contracts.**

Add independent tests for:
- total 0 / empty items: current query plus 暂无可见模型, no zero-value signal group;
- total 45 / empty items on page 3: current query plus 当前页没有模型, no signal group, enabled previous button;
- unresolved request: no summary, ledger or pager before success;
- cold 403 and 500: fixed StatePanel only;
- successful page-2 query then queryClient.invalidateQueries({ queryKey: ['models'] }) resolving 403 and separately 500: await the second request and assert summary, ledger, stale action links, server count/page text and pager are all absent.

Return QueryClient from renderWithProviders only as needed. Do not inspect internals or use sleep synchronization.

- [ ] **Step 3: Run the focused integration test before source changes.**

Run:

~~~
.\node_modules\.bin\vitest.CMD run src\features\models\ModelPages.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

Expected: FAIL because no 模型治理态势 exists and retained query.data remains usable after a failed refetch.

- [ ] **Step 4: Commit the red contract.**

~~~
git add web/console/src/features/models/ModelPages.test.tsx
git commit -m "test: define model governance workbench"
~~~

### Task 2: Build the pure governance summary

**Files:**
- Create: web/console/src/features/models/components/ModelCatalogGovernanceSummary.tsx
- Create: web/console/src/features/models/components/ModelCatalogGovernanceSummary.test.tsx

- [ ] **Step 1: Write focused failing component tests.**

Create files with Chinese headers. Test an exported derivation helper and rendered output:
- it uses only current items;
- manageable is the caller-supplied predicate;
- disabled is item.disabled and readonly is item.read_only, so counts may overlap;
- YAML and role-limited readonly rows are not manageable unless the supplied predicate explicitly says so;
- root is role=region / aria-label=模型治理态势; 当前查询 is always present; 本页治理信号 is absent for empty items.

- [ ] **Step 2: Run the component test red.**

~~~
.\node_modules\.bin\vitest.CMD run src\features\models\components\ModelCatalogGovernanceSummary.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

Expected: FAIL because the module does not exist.

- [ ] **Step 3: Implement the smallest pure summary.**

Implement deriveModelCatalogGovernanceSignals(items, isManageable) with:
- manageable = items.filter(isManageable).length;
- disabled = items.filter((item) => item.disabled).length;
- readonly = items.filter((item) => item.read_only).length.

Render a Fluent Card with heading 模型治理态势, current query text 匹配模型 {catalog.total} and 服务器第 {catalog.page} 页, and signals only for nonempty current pages. Use 暂无可见模型 for total 0 and 当前页没有模型 for an empty nonzero page. Use Fluent tokens and semantic brand/informative/warning values—not success green or hard-coded colors. Add a 960px one-column signals rule. Do not derive credential state, health or global availability.

- [ ] **Step 4: Run component tests green and commit.**

Run the command from Step 2; expected PASS.

~~~
git add web/console/src/features/models/components/ModelCatalogGovernanceSummary.tsx web/console/src/features/models/components/ModelCatalogGovernanceSummary.test.tsx
git commit -m "feat: add model governance summary"
~~~

### Task 3: Integrate the workbench and responsive ledger

**Files:**
- Modify: web/console/src/features/models/ModelListPage.tsx
- Modify: web/console/src/features/models/ModelForm.tsx
- Modify: web/console/src/features/models/ModelPages.test.tsx

- [ ] **Step 1: Confirm integration tests are still red.**

Run the shared command after Task 2. Component tests should be green; list-page contracts remain red until integration is complete.

- [ ] **Step 2: Create the single safe catalog source.**

After the query, derive exactly:

~~~
const modelCatalog = query.isSuccess ? query.data : null
const hasEmptyPage = modelCatalog?.items.length === 0
const hasEmptyCatalog = hasEmptyPage && modelCatalog.total === 0
const hasOutOfRangePage = hasEmptyPage && modelCatalog.total > 0
~~~

All summary, empty copy, table, model links, total/page text and pager rendering must use modelCatalog. Preserve StatePanel rendering from query state. Leave no query.data conditional for a data-derived element.

- [ ] **Step 3: Integrate the information order.**

After the existing header/action error, render:
1. ModelCatalogGovernanceSummary on a successful catalog;
2. optional section aria-label=模型配置工作区 containing the existing ModelForm when explicitly opened;
3. empty collection or out-of-range StatePanel;
4. native 受治理模型台账 for current-page rows;
5. 模型分页 for a successful catalog.

Do not close create/edit/delete/rotate state simply because the catalog refreshes; preserve each existing abort/mutex/error lifecycle.

- [ ] **Step 4: Refine status and responsive presentation.**

Import Fluent Badge for source, scope and status:
- source remains explicit database/platform versus YAML;
- scope remains explicit global/private;
- disabled uses warning, read-only uses informative, manageable/configurable uses brand or neutral—not success green.

Keep canManage as sole action authority. Do not introduce token, mask, base URL or owner-ID fields.

Give the page and table wrapper minWidth 0; wrap only DataTable in tableViewport with overflowX auto/minWidth 0. At max-width 960px stack/start-align pagination and allow actions to wrap. In ModelForm, turn its two-column grid into one column at 960px and let footer actions wrap or stack without changing token cleanup/submit/cancel behavior. Do not modify DataTable or global theme.

- [ ] **Step 5: Run focused tests, lint and typecheck.**

Run the shared command, then:

~~~
pnpm run lint
pnpm run typecheck
git diff --check
~~~

Expected: all model contract, summary and existing write-safety tests pass; no lint/type errors or whitespace errors.

- [ ] **Step 6: Commit the integrated workbench.**

~~~
git add web/console/src/features/models/ModelListPage.tsx web/console/src/features/models/ModelForm.tsx web/console/src/features/models/ModelPages.test.tsx
git commit -m "feat: refine model governance workbench"
~~~

### Task 4: Verify, review and visually accept the page

**Files:**
- No planned source changes unless review finds a concrete defect.

- [ ] **Step 1: Verify current HEAD.**

Run the shared command, pnpm run lint, pnpm run typecheck and:

~~~
git diff --check 17a27f3b..HEAD
~~~

- [ ] **Step 2: Run the Docker Node 22 full front-end gate.**

~~~
docker compose -f deploy\compose\docker-compose.frontend-test.yml run --rm console-test
~~~

Expected: lint, TypeScript, all Vitest files and production vite build exit 0. Record non-failing external warnings rather than suppressing them.

- [ ] **Step 3: Request independent reviews.**

First dispatch a spec reviewer against docs/superpowers/specs/2026-08-28-model-governance-workbench-design.md; then a quality reviewer for retained-query safety, landmarks, mobile styles, role permissions, TypeScript quality and test realism. Route every P0/P1/P2 finding to the original implementer and re-review its repair.

- [ ] **Step 4: Browser acceptance after user login.**

Do not enter or inspect credentials. In the existing local preview, test /models at 1280px and 390px as an administrator and, only if an available session can safely switch, an auditor. Verify hierarchy, local-only table scrolling, role actions, honest current-page labels and absence of saved token/mask/credential text. If no authenticated session exists, record pending user-mediated visual acceptance rather than simulate authentication.
