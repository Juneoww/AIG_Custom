# Knowledge Catalog Governance Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use @subagent-driven-development (recommended) or @executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the shared knowledge-catalog shell and four rule-asset ledgers into a light, honest governance workspace that distinguishes server-backed scope from the current visible catalog without exposing raw content.

**Architecture:** `KnowledgeLayout` owns only grouped navigation. `RawResourceLedger` remains the owner of URL normalization, queries, raw-content lifecycle and mutation state machines, but derives one current-success `catalog` value so retained TanStack Query data never renders after a failed refetch. A pure `KnowledgeCatalogBrief` receives only deliberately scoped, non-sensitive display facts; complete MCP directories can never receive or render pagination facts.

**Tech Stack:** React 19, TypeScript, Fluent UI v9/Griffel, TanStack Query v5, React Router, Vitest, Testing Library, Docker Node 22 front-end gate.

---

## File map

| File | Responsibility |
| --- | --- |
| `web/console/src/features/knowledge/components/KnowledgeCatalogBrief.tsx` | Pure, accessible brief for a paginated rule catalog or a complete MCP directory; accepts only safe display scope. |
| `web/console/src/features/knowledge/components/KnowledgeCatalogBrief.test.tsx` | Tests exact paginated/complete wording, role state, empty behavior and the absence of false MCP pagination. |
| `web/console/src/features/knowledge/KnowledgeLayout.tsx` | Groups the six existing category links without changing their URLs or role routing. |
| `web/console/src/features/knowledge/components/RawResourceLedger.tsx` | Owns safe catalog gate, brief placement, honest empty/out-of-range state, local table viewport and responsive controls. |
| `web/console/src/features/knowledge/MCPPage.tsx` | Supplies its existing unpaged API as a complete-directory fetcher that cannot receive URL pagination/search inputs. |
| `web/console/src/features/knowledge/KnowledgePages.test.tsx` | Integration contracts for grouped navigation, catalog hierarchy, complete MCP wording and stale-refetch safety. |

Do not modify `knowledge/api.ts`, route definitions, session code, rule data, legacy write endpoints, `DataTable`, global theme, raw-content cache rules, or Prompt/Agent pages in this implementation plan.

## Task 1 test command

Run from `web/console`:

~~~
.\node_modules\.bin\vitest.CMD run src\features\knowledge\KnowledgePages.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

Use this command only before Task 2 creates the brief test file. If the shared Windows runner cannot read Vite's ancestor configuration or completes unreliably, run the exact one-file test **from the repository root** in a clean Node 22 compose container:

~~~
docker compose -f deploy\compose\docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm install --frozen-lockfile && pnpm exec vitest run src/features/knowledge/KnowledgePages.test.tsx --reporter=verbose"
~~~

## Shared test command (Task 2 onward)

After Task 2 creates the component test, run from `web/console`:

~~~
.\node_modules\.bin\vitest.CMD run src\features\knowledge\KnowledgePages.test.tsx src\features\knowledge\components\KnowledgeCatalogBrief.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

If the shared Windows runner cannot read Vite's ancestor configuration or completes unreliably, run the same two-file test **from the repository root** in a clean Node 22 compose container:

~~~
docker compose -f deploy\compose\docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm install --frozen-lockfile && pnpm exec vitest run src/features/knowledge/KnowledgePages.test.tsx src/features/knowledge/components/KnowledgeCatalogBrief.test.tsx --reporter=verbose"
~~~

Do not call a sandbox startup failure or an incomplete worker session a green result.

### Task 1: Define the shared catalog hierarchy and error-boundary contracts

**Files:**
- Modify: `web/console/src/features/knowledge/KnowledgePages.test.tsx`

- [ ] **Step 1: Add the grouped-navigation red contract.**

In the existing route test, assert that the named `知识库分类` navigation exposes visible group labels `扫描规则`、`评测与扩展`、`配置资产`; each current link must retain its current route and accessible selected state. Keep the six existing route/role assertions unchanged.

- [ ] **Step 2: Add a paginated catalog-brief red contract.**

Create a safe fingerprint fixture with three list items, `total: 45`, server `page: 2`, `size: 20`; render `/knowledge/fingerprints?page=2&q=dify` as admin. Assert all of the following:

  - `region` named `资产目录概览` occurs before table `指纹规则台账`;
  - group `当前资源范围` contains `匹配资源 45` and `服务器第 2 页`;
  - `本页资产 3` and a readable `可治理` boundary appear, without any global-risk/health wording;
  - the table and server-driven previous/next controls remain present.

- [ ] **Step 3: Add safe empty and MCP-complete red contracts.**

Add coverage for:

  - a total-zero fingerprint response: summary context plus `暂无指纹规则`, no zero-value `本页资产` signal;
  - a nonzero, empty server page: `当前页没有指纹规则`, returned total/page and enabled previous button, but no zero-value signal;
  - the existing MCP fixture entered through `/knowledge/mcp?page=2&q=x`: it normalizes to `/knowledge/mcp`, calls the unpaged endpoint without query parameters, renders `当前目录 1 项` (or the exact final complete-directory wording), has no search form, no `本页`/`服务器第`/`第 1 页`, and no `MCP 插件分页` navigation. Add a small test-only `useLocation` probe to `renderRoute` if needed; do not inspect router internals.

- [ ] **Step 4: Add red cached-refetch security contracts.**

Make `renderRoute` return its QueryClient (it already does) and use a `fetchMock` that resolves the successful fingerprint page once, then returns a real HTTP 403; repeat with a 500 response in a separate parameterized case. After the first table is visible, invalidate `['knowledge', 'fingerprints']` inside `act`, await the second request and assert the correct `StatePanel` appears while every directory-derived element disappears. In particular, `资产目录概览`, `指纹规则台账`, old row `查看`/`编辑`/`删除` controls, `匹配资源 45`, `服务器第 2 页` and pager must be absent. Page header and URL-driven filter controls may remain because they do not derive from cached catalog data. Do not use sleeps or inspect Query internals.

Add a separate parameterized 403/500 interaction contract: successfully load the fingerprint page, open `查看 dify`, wait for `指纹规则原文` to reach ready state, then invalidate `['knowledge', 'fingerprints']`. Assert the original editor remains mounted and readable while the brief, table, row actions, total/page text and pager disappear behind the error state. This verifies that catalog data is removed without force-closing user-started local work.

- [ ] **Step 5: Run the focused test before source changes.**

Run the Task 1 test command.

Expected: FAIL because grouped navigation and `资产目录概览` do not exist, MCP currently receives a generic pager, and `listQuery.data` remains usable after an error refetch.

- [ ] **Step 6: Commit the red contract.**

~~~
git add web/console/src/features/knowledge/KnowledgePages.test.tsx
git commit -m "test: define knowledge catalog governance workbench"
~~~

### Task 2: Build the pure brief with complete-directory isolation

**Files:**
- Create: `web/console/src/features/knowledge/components/KnowledgeCatalogBrief.tsx`
- Create: `web/console/src/features/knowledge/components/KnowledgeCatalogBrief.test.tsx`

- [ ] **Step 1: Write failing pure-component tests.**

Create both TSX files with the repository's required Chinese file-header comments. Define the test data as primitive safe scope only—never raw list rows, raw YAML/JSON, `RawData`, paths or credentials. Cover:

  - paginated scope `{ kind: 'paginated', total: 45, page: 2, visibleItems: 3 }` renders `匹配资源 45`, `服务器第 2 页`, `本页资产 3` and the supplied admin/reader wording;
  - complete scope `{ kind: 'complete', total: 3, visibleItems: 3 }` renders `当前目录 3 项` and optional `当前显示 3 项`, but never page, size, `本页`, previous or next wording;
  - zero total renders the resource-specific empty copy without a zero-value signal;
  - a paginated nonzero empty scope renders the out-of-range copy without a signal;
  - root is `role="region" aria-label="资产目录概览"`, and colors are not the only representation of write/read boundaries.

- [ ] **Step 2: Run the component test red.**

Run:

~~~
.\node_modules\.bin\vitest.CMD run src\features\knowledge\components\KnowledgeCatalogBrief.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

Expected: FAIL because the component module does not exist.

- [ ] **Step 3: Implement the smallest pure display component.**

Implement a discriminated scope type so complete-directory callers cannot pass `page` or `size`:

~~~ts
type PaginatedScope = { kind: 'paginated'; total: number; page: number; visibleItems: number }
type CompleteScope = { kind: 'complete'; total: number; visibleItems: number }
~~~

The component receives this scope, `resourceLabel`, and `canManage`. It may render Fluent `Card`, `Badge`, `Text`, `makeStyles`, and `tokens`, but it must not fetch, read route/session state, sort, filter or store any data. Use named groups for `当前资源范围` and the read/write boundary; use brand/neutral/informative semantic presentation, never `success` green or hard-coded colors. At `max-width: 960px`, collapse signals to one column.

- [ ] **Step 4: Run the component tests green and commit.**

Run the Step 2 command; expected PASS.

~~~
git add web/console/src/features/knowledge/components/KnowledgeCatalogBrief.tsx web/console/src/features/knowledge/components/KnowledgeCatalogBrief.test.tsx
git commit -m "feat: add knowledge catalog brief"
~~~

### Task 3: Integrate the shared layout, safe ledger and responsive table

**Files:**
- Modify: `web/console/src/features/knowledge/KnowledgeLayout.tsx`
- Modify: `web/console/src/features/knowledge/components/RawResourceLedger.tsx`
- Modify: `web/console/src/features/knowledge/MCPPage.tsx`
- Modify: `web/console/src/features/knowledge/KnowledgePages.test.tsx` only if a production-semantic selector must be made precise

- [ ] **Step 1: Confirm integration tests remain red.**

Run the shared test command after Task 2. The pure brief tests should be green; shared layout/ledger contracts should still fail.

- [ ] **Step 2: Group existing navigation without changing its route matrix.**

Replace the flat `categories` rendering with three static groups:

~~~ts
扫描规则: 指纹规则, 漏洞规则
评测与扩展: 安全评测集, MCP 插件
配置资产: Prompt 集合, Agent 配置
~~~

Keep all six absolute `to` paths, `NavLink` active semantics, `aria-label="知识库分类"` and existing route protections. Use Fluent tokens only; at 960px group/link layouts may wrap or stack, and at 390px no root overflow is allowed.

- [ ] **Step 3: Establish one current-success catalog boundary.**

Split the ledger props into a discriminated fetch contract so complete directories cannot be called with pagination/search inputs:

~~~ts
type PaginatedLedgerProps<T> = LedgerBase<T> & {
  catalogMode?: 'paginated'
  fetchPage: (query: KnowledgePageQuery, signal?: AbortSignal) => Promise<KnowledgePage<T>>
}
type CompleteCatalog<T> = { items: T[]; total: number }
type CompleteLedgerProps<T> = LedgerBase<T> & {
  catalogMode: 'complete'
  fetchComplete: (signal?: AbortSignal) => Promise<CompleteCatalog<T>>
}
~~~

When `catalogMode === 'complete'`, normalize incoming `page` and `q` parameters to an empty search string before querying; use only a complete-directory query key such as `['knowledge', resourceKey, 'complete']`, and call `fetchComplete(signal)` with no `KnowledgePageQuery`. Do not render a filter form or pager. For paginated mode, retain URL normalization and `fetchPage({ page, size: 20, query }, signal)`.

After `useQuery`, derive exactly one list source:

~~~ts
const catalog = listQuery.isSuccess ? listQuery.data : null
const hasEmptyPage = catalog?.items.length === 0
const hasEmptyCatalog = catalog !== null && hasEmptyPage && catalog.total === 0
const hasOutOfRangePage = catalog !== null && hasEmptyPage && catalog.total > 0
~~~

Every catalog-derived surface—brief, empty/out-of-range state, `DataTable`, old-row action controls, total/page text and pagination—must use `catalog`, not `listQuery.data`. Only the paginated branch may access `catalog.page` or `catalog.size`, compute an out-of-range page, or render a pager. Keep `StatePanel` predicates based on `listQuery` state. Keep the user-opened `mode`, raw editor, confirmation and delete state machines unchanged.

- [ ] **Step 4: Place the brief and preserve honest MCP semantics.**

After the page header/action error, render `KnowledgeCatalogBrief` only when `catalog` exists. Pass a paginated primitive scope for the three server-page resources. When `catalogMode === 'complete'`, construct and pass only `{ kind: 'complete', total, visibleItems }`; never forward its compatibility `page`/`size` fields. Render filters/editing workspace as their existing independent controls, then the correct empty/out-of-range state, table and only paginated pager.

Replace MCP's compatibility `fetchPage(_query, signal)` adapter with a `fetchComplete(signal)` adapter: call `fetchMCPPlugins(signal)` and return only `{ items, total: items.length }`, then pass `catalogMode="complete"`. Its API endpoint remains unchanged, but no raw URL `page`/`q` values or compatibility `page`/`size` fields reach its query key, fetcher, brief or UI. Do not add fake local filtering or client-side pagination.

- [ ] **Step 5: Add local-only responsive containment.**

Give the page root and table wrapper `minWidth: 0`; wrap only `DataTable` in `tableViewport` with `overflowX: 'auto'`. At 960px stack/start-align paginated controls, let action groups wrap, and make filter/editor controls fit one column. Do not modify `DataTable` or a global theme. Preserve original raw editor/download/action semantics.

- [ ] **Step 6: Run focused tests, lint and typecheck.**

Run the shared command, then from `web/console`:

~~~
pnpm run lint
pnpm run typecheck
git diff --check
~~~

Expected: all new catalog contracts and existing knowledge write-safety tests pass; no lint, TypeScript or whitespace errors.

- [ ] **Step 7: Commit the integrated workbench.**

~~~
git add web/console/src/features/knowledge/KnowledgeLayout.tsx web/console/src/features/knowledge/components/RawResourceLedger.tsx web/console/src/features/knowledge/MCPPage.tsx web/console/src/features/knowledge/KnowledgePages.test.tsx
git commit -m "feat: refine knowledge catalog governance workbench"
~~~

### Task 4: Verify, review and visually accept the first knowledge package

**Files:**
- No planned source changes unless review identifies a concrete defect.

- [ ] **Step 1: Verify current HEAD with the targeted suite.**

Run the shared command, `pnpm run lint`, `pnpm run typecheck`, and:

~~~
git diff --check 0a6ad173..HEAD
~~~

- [ ] **Step 2: Run the Docker Node 22 full front-end gate from the repository root.**

~~~
docker compose -f deploy\compose\docker-compose.frontend-test.yml run --rm console-test
~~~

Expected: lint, TypeScript, all Vitest files and production Vite build exit 0. Record existing third-party warnings without suppressing them.

- [ ] **Step 3: Request two independent reviews.**

First dispatch a spec reviewer against `docs/superpowers/specs/2026-08-28-knowledge-catalog-governance-workbench-design.md`; after it passes, dispatch a quality reviewer for stale-query safety, complete-MCP isolation, raw-content lifecycle, role boundaries, responsive containment, TypeScript quality and test realism. Route every P0/P1/P2 finding to the original implementer and re-review the repair.

- [ ] **Step 4: Browser acceptance after user login.**

Do not enter or inspect credentials. In the existing local preview, inspect at 1280px and 390px as an administrator and, only if a safely available session can switch, an auditor. Verify grouping, hierarchy, local-only table scrolling, honest complete-directory wording, role actions and absence of raw content/credentials. If no authenticated session exists, record user-mediated visual acceptance as pending instead of simulating authentication.
