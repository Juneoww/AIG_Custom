# 企业控制台彻底重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建成默认简体中文、A“均衡监管台账”视觉、浅色导航和三种主题模式的企业控制台，接入真实平台 API，并在完整验收后一次性替换旧 AIG 浏览器页面。

**Architecture:** 先补齐浏览器所需的身份、分页、安全 DTO、附件审计和总览聚合契约，再在 `web/console` 建立 React 单页应用。前端构建产物由 Go `embed` 提供；开发期间新旧产物隔离，最终通过可复现构建和 E2E 后删除旧静态资源并一次性切换根路径。

**Tech Stack:** Go 1.23、Gin、PostgreSQL、React、TypeScript、Vite、Fluent UI React v9、React Router、TanStack Query、Vitest、Testing Library、Playwright、pnpm、Docker Compose。

---

## 实施约束

- 全部工作在 `.worktrees/enterprise-console-ui` 的 `codex/enterprise-console-ui` 分支完成，不修改主工作树中的 `.pet-runs/`、`.superpowers/`、`findings.md`、`progress.md` 或 `task_plan.md`。
- 每项生产改动使用 `@test-driven-development`：先写行为测试并取得预期 RED，再写最小实现，随后运行范围测试。
- 前端视觉实现使用 `@frontend-design`，但必须服从 [企业控制台设计](../../architecture/enterprise-console.md) 中已确认的 A“均衡监管台账”，不得重新选择视觉方向。
- 新增或修改 TypeScript、PowerShell、Shell 脚本时使用 `@chinese-script-comments` 和 `@script-header-comments`，文件头说明目的、输入、输出、依赖和失败语义。
- 浏览器只调用 `/api/v1/**` 公共契约，不连接 Agent WebSocket，不调用旧 `/api/v1/app/**`，不读取原始任务结果。
- 任何“已实现”页面必须由真实 API、组件测试和环境验收共同证明；禁止以静态假数据、本地 JSON 或只画页面占位替代。
- 每个任务只暂存该任务列出的文件；提交前执行 `git diff --cached --check` 和相应测试。
- 最终完成声明前使用 `@verification-before-completion`，合并前使用 `@requesting-code-review`。

## 文件职责与目标结构

| 路径 | 职责 |
| --- | --- |
| `internal/platform/identity/browser.go` | 匿名 CSRF 初始化、当前主体安全 DTO |
| `internal/platform/brand/public.go` | 登录前安全品牌摘要，不暴露治理字段 |
| `internal/platform/dashboard/` | 按服务端 Subject 聚合总览数据 |
| `internal/platform/tasks/dto.go` | 任务摘要、详情和分页 wire DTO |
| `internal/platform/reports/handler.go` | 报告安全分页 envelope |
| `internal/platform/admin/handler.go` | 用户和审计安全分页 envelope |
| `common/websocket/server.go` | 浏览器 API 装配、SPA 静态资源和切换边界 |
| `web/console/package.json` | 前端脚本和锁定依赖入口 |
| `web/console/src/app/` | 应用提供器、路由、错误边界和会话守卫 |
| `web/console/src/shared/` | API 客户端、DTO、权限、主题和通用组件 |
| `web/console/src/features/` | dashboard、tasks、reports、models、knowledge、admin 等领域页面 |
| `web/console/e2e/` | Playwright 三角色真实主流程 |
| `deploy/compose/docker-compose.frontend-test.yml` | 固定 Node 容器中的前端测试入口 |
| `scripts/check-console-assets.ps1` | 构建产物新旧资源、敏感字面量和离线依赖门禁 |
| `common/websocket/static/` | 最终一次性切换后的新控制台生产构建产物 |

## 全局验证命令

后端测试统一复用测试 PostgreSQL：

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/platform/... ./common/websocket -count=1"
```

前端测试统一使用固定 Node 容器：

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm install --frozen-lockfile && pnpm test:run"
```

浏览器验收使用隔离的测试平台，不连接开发者日常数据库：

```powershell
docker compose -f deploy/compose/docker-compose.console-e2e.yml up --build --abort-on-container-exit --exit-code-from console-e2e
```

### Task 1: 增加浏览器身份启动契约

**Files:**
- Create: `internal/platform/identity/browser.go`
- Create: `internal/platform/identity/browser_test.go`
- Modify: `internal/platform/identity/middleware.go`
- Modify: `internal/platform/identity/middleware_test.go`
- Modify: `common/websocket/server.go`

- [ ] **Step 1: 写匿名 CSRF 和当前主体 RED 测试**

测试锁定以下行为：

```go
func TestBrowserBootstrapCSRFAndCurrentSubject(t *testing.T) {
    // GET /auth/csrf: 200，设置非 HttpOnly CSRF Cookie，body 返回同值；不创建 session。
    // POST /auth/login: 缺 Cookie/Header 为 403；成功后轮换 CSRF 并签发 HttpOnly session。
    // GET /auth/me: 匿名 401；登录后只返回 id/username/role/must_change_password。
}
```

同时断言 `POST /auth/password-resets/confirm` 缺匿名 CSRF 时为 `403`，Token 不出现在响应和日志。

- [ ] **Step 2: 运行测试确认 RED**

Run:

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/platform/identity -run 'TestBrowserBootstrap|TestLoginRequiresInitializedCSRF|TestPasswordResetConfirmationRequiresCSRF' -count=1"
```

Expected: FAIL，缺 `/csrf`、`/me` 或登录/重置确认未校验 CSRF。

- [ ] **Step 3: 实现安全 DTO 和路由**

核心 wire model：

```go
type CurrentSubjectResponse struct {
    ID                 string `json:"id"`
    Username           string `json:"username"`
    Role               Role   `json:"role"`
    MustChangePassword bool   `json:"must_change_password"`
}

type CSRFResponse struct {
    Token string `json:"csrf_token"`
}
```

`/auth/me` 必须在改密守卫之前可访问，使前端能恢复 `must_change_password`；业务路由仍由 `RequirePasswordChangeCompleted` 拒绝。

- [ ] **Step 4: 运行身份测试确认 GREEN**

Run: 同 Step 2。

Expected: PASS。

- [ ] **Step 5: 提交**

```powershell
git add internal/platform/identity/browser.go internal/platform/identity/browser_test.go internal/platform/identity/middleware.go internal/platform/identity/middleware_test.go common/websocket/server.go
git commit -m "feat: add browser identity bootstrap contract"
```

### Task 2: 提供登录前品牌摘要与安全版本 DTO

**Files:**
- Create: `internal/platform/brand/public.go`
- Create: `internal/platform/brand/public_test.go`
- Modify: `internal/platform/brand/service.go`
- Modify: `internal/platform/brand/service_test.go`
- Modify: `common/websocket/version_api.go`
- Modify: `common/websocket/version_api_test.go`
- Modify: `common/websocket/server.go`
- Test: `common/websocket/public_api_test.go`

- [ ] **Step 1: 写公共品牌和版本 RED 测试**

断言：

```go
// GET /api/v1/public/brand 无需会话，只返回 product_name/primary_color/logo_data_url。
// 不返回 watermark、updated_by、logo bytes、存储路径。
// GET /api/v1/version 只返回 version/commit/build_time，禁止文件读取和公网请求。
```

- [ ] **Step 2: 运行测试确认 RED**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/platform/brand ./common/websocket -run 'TestPublicBrand|TestSafeVersion' -count=1"
```

Expected: FAIL，公共路由不存在或响应含额外字段。

- [ ] **Step 3: 实现白名单响应**

`logo_data_url` 只允许由已验证的 PNG/JPEG 品牌数据生成；空 Logo 返回空字符串。版本值通过 `-ldflags` 注入，缺省使用固定 `unknown`，不读取 `.git` 或远端 tag。

同时把 `brand.defaultConfig()` 的产品名从“企业安全平台”迁移为“AI 安全治理平台”，并更新默认值、空 repository 回退和持久化重启测试；数据库中已有管理员品牌配置不覆盖。

- [ ] **Step 4: 运行范围测试并提交**

```powershell
git add internal/platform/brand/public.go internal/platform/brand/public_test.go internal/platform/brand/service.go internal/platform/brand/service_test.go common/websocket/version_api.go common/websocket/version_api_test.go common/websocket/public_api_test.go common/websocket/server.go
git commit -m "feat: expose safe public console metadata"
```

### Task 3: 收紧任务浏览器 DTO、分页和退役结果路由

**Files:**
- Create: `internal/platform/tasks/dto.go`
- Create: `internal/platform/tasks/browser_contract_test.go`
- Modify: `internal/platform/tasks/handler.go`
- Modify: `internal/platform/tasks/service.go`
- Modify: `internal/platform/tasks/adapter.go`
- Modify: `internal/platform/tasks/entity.go`
- Modify: `internal/platform/tasks/handler_test.go`
- Modify: `common/websocket/route_security_test.go`

- [ ] **Step 1: 写任务列表/详情安全 DTO RED**

目标 envelope：

```go
type TaskListResponse struct {
    Items    []TaskSummary `json:"items"`
    Total    int64         `json:"total"`
    Page     int           `json:"page"`
    PageSize int           `json:"page_size"`
}
```

`TaskSummary` 只包含 ID、owner 显示字段、task type、status、created/updated；不得包含 `params`、content、engine session、dispatch claim/error、附件 ID。详情使用显式 `TaskDetail`，按任务类型输出安全字段，不直接序列化 `Task`。

- [ ] **Step 2: 写结果退役 RED**

匿名访问仍 `401`；未完成改密为 `403`；完成守卫后的 `GET /platform/tasks/:id/result` 固定 `410`，且不调用 EngineAdapter。

- [ ] **Step 3: 运行测试确认 RED**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/platform/tasks ./common/websocket -run 'TestTaskBrowser|TestRetiredPlatformTaskResult' -count=1"
```

- [ ] **Step 4: 实现 owner 过滤、稳定分页和安全映射**

普通用户 repository 查询必须在 SQL 层带 owner 条件；管理员/审计员全局查询。排序固定为 `created_at DESC, id DESC`，`page_size` 默认 20、最大 100。

- [ ] **Step 5: 运行范围测试并提交**

```powershell
git add internal/platform/tasks common/websocket/route_security_test.go
git commit -m "feat: add safe paged browser task contract"
```

### Task 4: 关闭附件角色缺口并审计管理员跨 owner 下载

**Files:**
- Modify: `internal/platform/audit/entity.go`
- Modify: `internal/platform/tasks/service.go`
- Modify: `internal/platform/tasks/service_test.go`
- Modify: `internal/platform/tasks/handler_test.go`
- Modify: `common/websocket/resource_authorization_integration_test.go`

- [ ] **Step 1: 写附件权限 RED**

覆盖：owner 下载成功；普通用户跨 owner 为 `404`；审计员一律拒绝且不读文件；管理员跨 owner 只有在持久化脱敏审计成功后才返回字节；审计失败时下载失败。

- [ ] **Step 2: 写防泄漏断言**

审计 metadata 只包含 attachment ID、owner ID、actor ID 和治理动作，不含原始文件名、storage name、路径、请求 Header 或文件内容。

- [ ] **Step 3: 运行测试确认 RED**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/platform/tasks ./common/websocket -run 'TestAttachment.*(Owner|Auditor|Admin|Audit)' -count=1"
```

- [ ] **Step 4: 实现 `attachment.downloaded` 审计边界并 GREEN**

管理员跨 owner 请求先完成 durable 审计写入再打开文件；任何 audit error fail closed。

- [ ] **Step 5: 提交**

```powershell
git add internal/platform/audit/entity.go internal/platform/tasks common/websocket/resource_authorization_integration_test.go
git commit -m "fix: enforce governed attachment downloads"
```

### Task 5: 统一报告、用户、审计和模型分页目录

**Files:**
- Modify: `internal/platform/reports/entity.go`
- Modify: `internal/platform/reports/snapshot.go`
- Modify: `internal/platform/reports/handler.go`
- Modify: `internal/platform/reports/handler_test.go`
- Modify: `internal/platform/admin/dto.go`
- Modify: `internal/platform/admin/handler.go`
- Modify: `internal/platform/admin/handler_test.go`
- Modify: `internal/platform/audit/repository.go`
- Modify: `internal/platform/identity/repository.go`
- Modify: `internal/platform/identity/repository_migration_test.go`
- Modify: `internal/platform/identity/service.go`
- Modify: `internal/platform/identity/service_test.go`
- Modify: `internal/platform/models/entity.go`
- Modify: `internal/platform/models/service.go`
- Modify: `common/websocket/model_api.go`
- Modify: `common/websocket/legacy_model_compatibility_test.go`

- [ ] **Step 1: 写统一分页 envelope RED**

所有列表返回：

```json
{"items": [], "total": 0, "page": 1, "page_size": 20}
```

报告列表继续禁止 `raw_result`、内部 `render_data`、Logo 字节和 owner 内部字段；审计 metadata 经过服务端 sanitizer；模型目录始终掩码 Token，并输出 `source`、`read_only`。

- [ ] **Step 2: 运行四领域 RED 测试**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/platform/reports ./internal/platform/admin ./internal/platform/audit ./internal/platform/models ./common/websocket -run 'Test.*(Pagination|ListEnvelope|SafeCatalog)' -count=1"
```

- [ ] **Step 3: 在 repository 层实现 count + page 查询**

禁止 handler 先全量读取再切片；普通用户 owner 限制必须进入 SQL。用户分页必须把 `page/pageSize` 从 `admin.Handler` 传到 `identity.Service` 和 `identity.Repository`，由 repository 同时执行稳定分页与 count，不能继续调用无参数 `ListUsers` 后切片。模型合并顺序为可写数据库模型优先，但 YAML 同 ID 冲突不得遮蔽或变为可写。

- [ ] **Step 4: 运行领域全包并提交**

```powershell
git add internal/platform/reports internal/platform/admin internal/platform/audit internal/platform/identity internal/platform/models common/websocket/model_api.go common/websocket/legacy_model_compatibility_test.go
git commit -m "feat: standardize console list contracts"
```

### Task 6: 新增按角色限定的治理总览

**Files:**
- Create: `internal/platform/dashboard/entity.go`
- Create: `internal/platform/dashboard/service.go`
- Create: `internal/platform/dashboard/service_test.go`
- Create: `internal/platform/dashboard/handler.go`
- Create: `internal/platform/dashboard/handler_test.go`
- Modify: `internal/platform/tasks/service.go`
- Modify: `internal/platform/reports/snapshot.go`
- Modify: `internal/platform/reports/repository_test.go`
- Modify: `internal/platform/reports/service.go`
- Modify: `common/websocket/server.go`
- Modify: `common/websocket/server_datastores.go`

- [ ] **Step 1: 写 dashboard 服务 RED**

响应只包含服务端聚合结果：

```go
type View struct {
    HasData         bool                `json:"has_data"`
    SecurityScore   *int                `json:"security_score"`
    MappingVersions []string            `json:"mapping_versions"`
    Risk            reports.RiskSummary `json:"risk"`
    Trend           []TrendPoint        `json:"trend"`
    RecentTasks     []tasks.TaskSummary `json:"recent_tasks"`
    Attention       []AttentionItem     `json:"attention"`
}
```

普通用户只聚合本人；审计员和管理员按全局读取；窗口固定为含今天的 30 个 UTC 自然日。不得从 raw result 在请求时重算。空数据必须返回 `has_data=false`、`security_score=null`、风险计数全零、30 个补零桶和空 attention，不能显示为 100 分。

- [ ] **Step 2: 运行测试确认 RED**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/platform/dashboard -count=1"
```

- [ ] **Step 3: 实现 bounded SQL/服务聚合**

在 `reports` repository 新增 SQL 聚合/投影查询并用真实 PostgreSQL 测试锁定 owner 过滤和窗口边界。`security_score` 是有效快照固化分数的四舍五入平均值，`mapping_versions` 去重稳定排序；趋势固定 30 个按日期升序的 UTC 桶。最近任务按 `updated_at DESC, id DESC` 最多 5 条且不受 30 日窗口限制。attention **只**来自窗口内 `high > 0` 或 `score < 60` 的有效报告快照，按 `high DESC, score ASC, completed_at DESC, report_id DESC` 最多 5 条；失败/取消/运行中任务不得进入评分、风险、趋势或 attention。严禁 `List()` 全量加载。

- [ ] **Step 4: 注册 `/api/v1/platform/dashboard` 并做集成测试**

确保它复用 platform 身份、改密、CSRF（GET 不要求 Header）和角色中间件。

- [ ] **Step 5: 运行测试并提交**

```powershell
git add internal/platform/dashboard internal/platform/tasks/service.go internal/platform/reports/snapshot.go internal/platform/reports/repository_test.go internal/platform/reports/service.go common/websocket/server.go common/websocket/server_datastores.go
git commit -m "feat: add governed dashboard aggregation"
```

### Task 7: 同步浏览器 API 文档与安全合同

**Files:**
- Modify: `internal/apidocs/swagger.yaml`
- Modify: `internal/apidocs/swagger.json`
- Modify: `internal/apidocs/docs.go`
- Modify: `internal/apidocs/swagger_sync_test.go`
- Modify: `docs/api/reference.md`
- Modify: `docs/api/reference.en.md`

- [ ] **Step 1: 先写 Swagger 同步和禁字段 RED**

锁定新增 `/auth/csrf`、`/auth/me`、`/public/brand`、`/platform/dashboard`、分页 envelope、任务 result 410 和附件角色边界。

- [ ] **Step 2: 运行 RED**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/apidocs -count=1"
```

- [ ] **Step 3: 手工同步三件套**

不得运行默认 `swag init` 覆盖现有完整规范。YAML、JSON 和 `docs.go` 三件套必须内容等价。

- [ ] **Step 4: 运行文档门禁并提交**

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\check-docs-layout.ps1
git add internal/apidocs docs/api
git commit -m "docs: define enterprise console api contracts"
```

### Task 8: 建立可复现的前端工程与测试容器

**Files:**
- Create: `web/console/package.json`
- Create: `web/console/pnpm-lock.yaml`
- Create: `web/console/tsconfig.json`
- Create: `web/console/tsconfig.app.json`
- Create: `web/console/vite.config.ts`
- Create: `web/console/vitest.setup.ts`
- Create: `web/console/playwright.config.ts`
- Create: `web/console/index.html`
- Create: `web/console/src/main.tsx`
- Create: `web/console/src/app/App.tsx`
- Create: `web/console/src/app/App.test.tsx`
- Create: `deploy/compose/docker-compose.frontend-test.yml`
- Modify: `.gitignore`

- [ ] **Step 1: 写最小应用 RED**

```tsx
it('renders the configured Chinese product shell without old AIG branding', () => {
  render(<App />)
  expect(screen.getByText('AI 安全治理平台')).toBeInTheDocument()
  expect(screen.queryByText(/^A\.I\.G$/)).not.toBeInTheDocument()
})
```

- [ ] **Step 2: 创建 pnpm 工程并锁定依赖**

在固定 `node:22.18.0-alpine` 容器内使用 Corepack，`packageManager` 固定 pnpm 版本；所有 dependencies/devDependencies 使用 `--save-exact` 并提交 lockfile。安装 React、Vite、TypeScript、Fluent UI v9、React Router、TanStack Query、Vitest、Testing Library、Playwright、MSW 和 ESLint。

- [ ] **Step 3: 运行测试确认 RED 后实现最小入口**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm install --frozen-lockfile && pnpm vitest run src/app/App.test.tsx"
```

- [ ] **Step 4: 运行 lint/typecheck/test/build**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm install --frozen-lockfile && pnpm lint && pnpm typecheck && pnpm test:run && pnpm build"
```

- [ ] **Step 5: 提交**

```powershell
git add web/console deploy/compose/docker-compose.frontend-test.yml .gitignore
git commit -m "feat: scaffold enterprise console frontend"
```

### Task 9: 实现 API 客户端、会话状态和身份页面

**Files:**
- Create: `web/console/src/shared/api/client.ts`
- Create: `web/console/src/shared/api/errors.ts`
- Create: `web/console/src/shared/api/types.ts`
- Create: `web/console/src/app/providers/AppProviders.tsx`
- Create: `web/console/src/app/routes.tsx`
- Create: `web/console/src/features/auth/session.ts`
- Create: `web/console/src/features/auth/LoginPage.tsx`
- Create: `web/console/src/features/auth/ChangePasswordPage.tsx`
- Create: `web/console/src/features/auth/ResetPasswordPage.tsx`
- Test: `web/console/src/features/auth/*.test.tsx`

- [ ] **Step 1: 写身份流程 RED**

覆盖匿名 CSRF 初始化、登录后 Token 轮换、`/me` 恢复、强制改密、退出、401 回登录、403 保留上下文、网络错误不自动重发写请求。

- [ ] **Step 2: 实现同源 fetch 客户端**

```ts
export async function apiRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  // credentials: 'same-origin'; 写操作读取 CSRF Cookie 并设置 Header；
  // 只解析白名单错误 envelope；不把 request body、Token 或响应原文写日志。
}
```

不得把 session、密码、reset token 或 CSRF token 写入 local/session storage。

- [ ] **Step 3: 实现路由守卫和表单**

Reset token 由用户从受控带外交付渠道取得后手工粘贴到密码重置表单，只放在 `POST /auth/password-resets/confirm` 请求体。前端不得从 URL、query、fragment、storage 或剪贴板后台读取 Token；测试必须断言带 `?token=` 或 `#token=` 的访问不会自动填充或提交。

- [ ] **Step 4: 运行身份测试并提交**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm vitest run src/features/auth"
git add web/console/src
git commit -m "feat: add secure console authentication flow"
```

### Task 10: 建立 A“均衡监管台账”设计系统和三主题

**Files:**
- Create: `web/console/src/shared/theme/tokens.ts`
- Create: `web/console/src/shared/theme/ThemeProvider.tsx`
- Create: `web/console/src/shared/theme/theme-storage.ts`
- Create: `web/console/src/shared/theme/ThemeProvider.test.tsx`
- Create: `web/console/src/shared/theme/fonts.test.ts`
- Create: `web/console/src/shared/styles/global.css`
- Create: `web/console/scripts/prepare-fonts.mjs`
- Create: `web/console/assets/fonts/IBMPlexSans-Regular.woff2`
- Create: `web/console/assets/fonts/IBMPlexSans-SemiBold.woff2`
- Create: `web/console/assets/fonts/IBM_PLEX_LICENSE.txt`
- Create: `web/console/assets/fonts/FONT_ASSETS.md`
- Create: `web/console/src/shared/components/PageHeader.tsx`
- Create: `web/console/src/shared/components/StatePanel.tsx`
- Create: `web/console/src/shared/components/DataTable.tsx`
- Create: `web/console/src/shared/components/MetricCard.tsx`

- [ ] **Step 1: 写主题 RED**

覆盖：默认 light；system 解析 `prefers-color-scheme`；系统变化实时响应；显式 light/dark 覆盖系统；刷新只恢复 `aig-console-theme`，不存其他业务数据。

- [ ] **Step 2: 实现 Fluent v9 主题令牌**

浅色：浅侧栏、冷灰画布、白卡、钴蓝主色；深色：深蓝灰而非纯黑。两套布局同构，8px 网格，风险色只用于状态。

- [ ] **Step 3: 固定并准备离线字体资产**

中文复用 `internal/platform/reports/assets/DroidSansFallbackFull.ttf` 及其已固定 AOSP 来源、SHA-256 和 Apache-2.0 归属；`prepare-fonts.mjs` 在开发/测试/构建前把它复制到 ignored 的 `web/console/.generated/fonts/`，不在仓库重复保存 4MiB 字体。IBM Plex Sans 的 Regular/SemiBold WOFF2 及完整 OFL 文本存放在 `web/console/assets/fonts/`；`FONT_ASSETS.md` 记录精确上游 release/commit、字节数和 SHA-256。Vite `publicDir` 指向 `.generated`，脚本同时复制 IBM 字体。

`global.css` 仅使用本地 `@font-face`，中文字体为正文首选，IBM Plex Sans 仅用于数字和英文指标；禁止公网字体、CDN 和 `common/websocket/static/fonts/Tencentsans.ttf`。`fonts.test.ts` 校验源文件 hash、许可文件存在和 CSS 无 `http(s)` URL。

- [ ] **Step 4: 做可访问性组件测试**

键盘焦点、语义 heading/table、对比度 token、`prefers-reduced-motion` 必须覆盖。

- [ ] **Step 5: 运行测试并提交**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm run prepare:fonts && pnpm vitest run src/shared/theme src/shared/components"
git add web/console/src/shared web/console/scripts web/console/assets/fonts
git commit -m "feat: add regulatory ledger design system"
```

### Task 11: 实现角色化应用壳层和导航

**Files:**
- Create: `web/console/src/app/layout/AppShell.tsx`
- Create: `web/console/src/app/layout/Sidebar.tsx`
- Create: `web/console/src/app/layout/Topbar.tsx`
- Create: `web/console/src/app/navigation.ts`
- Create: `web/console/src/app/ForbiddenPage.tsx`
- Create: `web/console/src/app/NotFoundPage.tsx`
- Test: `web/console/src/app/layout/*.test.tsx`

- [ ] **Step 1: 写三角色导航 RED**

固定顺序：治理总览、扫描任务、安全报告、模型与凭据、规则与知识库、用户管理、审计日志、品牌设置、系统信息。无权限项不显示，直接路由仍显示新 403。

- [ ] **Step 2: 实现 1280px 优先壳层**

侧栏浅色且可折叠，顶栏包含产品名、主题、个人中心和退出；不出现旧 Logo、营销区、帮助中心或英文切换。

- [ ] **Step 3: 运行测试并提交**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm vitest run src/app"
git add web/console/src/app
git commit -m "feat: add role-aware console shell"
```

### Task 12: 接入治理总览、任务和附件

**Files:**
- Create: `web/console/src/features/dashboard/api.ts`
- Create: `web/console/src/features/dashboard/DashboardPage.tsx`
- Create: `web/console/src/features/dashboard/components/RiskTrend.tsx`
- Create: `web/console/src/features/tasks/api.ts`
- Create: `web/console/src/features/tasks/TaskListPage.tsx`
- Create: `web/console/src/features/tasks/TaskCreatePage.tsx`
- Create: `web/console/src/features/tasks/TaskDetailPage.tsx`
- Create: `web/console/src/features/tasks/attachments.ts`
- Test: `web/console/src/features/dashboard/*.test.tsx`
- Test: `web/console/src/features/tasks/*.test.tsx`

- [ ] **Step 1: 写总览四区 RED**

同屏展示核心指标、30 日趋势、高风险待办和最近任务；加载、空、失败、权限不足均有独立状态。

- [ ] **Step 2: 写任务主流程 RED**

覆盖服务端分页、筛选、详情短轮询、终态停止、取消、同一次逻辑提交复用 `Idempotency-Key`、失败不自动创建第二任务。

- [ ] **Step 3: 写附件 RED**

普通/分片上传、大小限制、opaque ID、下载权限；审计员无下载按钮，管理员只有 API 明确允许时显示治理下载。

- [ ] **Step 4: 实现并运行测试**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm vitest run src/features/dashboard src/features/tasks"
```

- [ ] **Step 5: 提交**

```powershell
git add web/console/src/features/dashboard web/console/src/features/tasks
git commit -m "feat: add dashboard and governed task workflows"
```

### Task 13: 接入不可变报告和 PDF 导出

**Files:**
- Create: `web/console/src/features/reports/api.ts`
- Create: `web/console/src/features/reports/ReportListPage.tsx`
- Create: `web/console/src/features/reports/ReportDetailPage.tsx`
- Create: `web/console/src/features/reports/components/RiskSummary.tsx`
- Create: `web/console/src/features/reports/components/TechnicalFindings.tsx`
- Test: `web/console/src/features/reports/*.test.tsx`

- [ ] **Step 1: 写安全 DTO RED**

测试响应即使意外含 `raw_result`、内部 `render_data`、Logo bytes、绝对路径或 Token sentinel，页面也不渲染、不记录。

- [ ] **Step 2: 写列表/详情/趋势/PDF RED**

PDF 使用 POST + CSRF，文件名固定安全；失败显示固定中文错误并允许用户显式重试。

- [ ] **Step 3: 实现报告页面并 GREEN**

在线详情与 PDF 只使用同一 immutable RenderModel；技术发现按高/中/低稳定排序并支持长中文折行。

- [ ] **Step 4: 提交**

```powershell
git add web/console/src/features/reports
git commit -m "feat: add immutable report experience"
```

### Task 14: 接入模型与凭据治理

**Files:**
- Create: `web/console/src/features/models/api.ts`
- Create: `web/console/src/features/models/ModelListPage.tsx`
- Create: `web/console/src/features/models/ModelForm.tsx`
- Create: `web/console/src/features/models/RotateCredentialDialog.tsx`
- Test: `web/console/src/features/models/*.test.tsx`

- [ ] **Step 1: 写凭据防泄漏 RED**

Token 永不回填；掩码不能作为更新值提交；错误、DOM、URL、storage 和测试日志都不含 sentinel。

- [ ] **Step 2: 写角色和 YAML 模型 RED**

用户只管理私有模型，管理员只管理全局模型，审计员只读；`read_only=true` 的 YAML 模型无编辑/删除按钮且同 ID 不遮蔽数据库模型。

- [ ] **Step 3: 实现并提交**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm vitest run src/features/models"
git add web/console/src/features/models
git commit -m "feat: add governed model credential management"
```

### Task 15: 原样迁移规则与知识库编辑能力

**Files:**
- Create: `web/console/src/features/knowledge/api.ts`
- Create: `web/console/src/features/knowledge/KnowledgeLayout.tsx`
- Create: `web/console/src/features/knowledge/FingerprintPage.tsx`
- Create: `web/console/src/features/knowledge/VulnerabilityPage.tsx`
- Create: `web/console/src/features/knowledge/EvaluationPage.tsx`
- Create: `web/console/src/features/knowledge/MCPPage.tsx`
- Create: `web/console/src/features/knowledge/PromptCollectionPage.tsx`
- Create: `web/console/src/features/knowledge/AgentConfigPage.tsx`
- Create: `web/console/src/features/knowledge/components/StructuredEditor.tsx`
- Test: `web/console/src/features/knowledge/*.test.tsx`

- [ ] **Step 1: 写能力矩阵 RED**

指纹、漏洞、评测、MCP、Prompt 集合、Agent 配置均为全角色可读、管理员按现有路由写；Prompt 管理员必须能创建/编辑/删除，普通用户和审计员只读。

- [ ] **Step 2: 写兼容适配器 RED**

只在 `knowledge/api.ts` 将 `{status,message,data}` 转为前端领域结果；页面组件不得判断兼容响应格式。

- [ ] **Step 3: 实现结构化编辑器**

支持行号、YAML/JSON 高亮、格式校验、错误定位和文件导入；保存不改变 schema。覆盖二次确认、CSRF、审计失败、远端同步失败。

- [ ] **Step 4: 运行测试和 yamlcheck**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm vitest run src/features/knowledge"
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go build -o /tmp/yamlcheck ./cmd/yamlcheck && /tmp/yamlcheck data/fingerprints data/vuln data/vuln_en"
```

- [ ] **Step 5: 提交**

```powershell
git add web/console/src/features/knowledge
git commit -m "feat: migrate governed knowledge editing"
```

### Task 16: 接入用户、审计、品牌、系统和关于页

**Files:**
- Create: `web/console/src/features/admin/users/UserListPage.tsx`
- Create: `web/console/src/features/admin/audit/AuditListPage.tsx`
- Create: `web/console/src/features/admin/brand/BrandSettingsPage.tsx`
- Create: `web/console/src/features/admin/system/SystemPage.tsx`
- Create: `web/console/src/features/about/AboutPage.tsx`
- Create: `web/console/src/features/profile/ProfilePage.tsx`
- Test: `web/console/src/features/admin/**/*.test.tsx`
- Test: `web/console/src/features/about/*.test.tsx`

- [ ] **Step 1: 写用户与审计 RED**

管理员创建/禁用/改角色/发起密码重置；HTTP 不显示 reset token。审计员只读；审计 metadata 不渲染敏感字段；完成/恢复动作仅管理员。

- [ ] **Step 2: 写品牌 RED**

PNG/JPEG、1MiB、4096 像素和总像素限制在客户端给即时提示，但最终以服务端校验为准；预览不得使用 SVG 或任意 HTML。

- [ ] **Step 3: 写系统/关于 RED**

管理员触发数据同步；管理员/审计员读状态；关于页只读安全版本 DTO、本地归属清单和配置产品名，不加载公网内容。

- [ ] **Step 4: 实现并提交**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm vitest run src/features/admin src/features/about src/features/profile"
git add web/console/src/features/admin web/console/src/features/about web/console/src/features/profile
git commit -m "feat: add enterprise governance pages"
```

### Task 17: 建立真实浏览器 E2E 与可访问性门禁

**Files:**
- Create: `web/console/e2e/auth.spec.ts`
- Create: `web/console/e2e/tasks.spec.ts`
- Create: `web/console/e2e/reports.spec.ts`
- Create: `web/console/e2e/knowledge.spec.ts`
- Create: `web/console/e2e/admin.spec.ts`
- Create: `web/console/e2e/theme.spec.ts`
- Create: `web/console/e2e/security.spec.ts`
- Create: `deploy/compose/docker-compose.console-e2e.yml`
- Create: `scripts/seed-console-e2e.ps1`

- [ ] **Step 1: 写 E2E RED**

按设计文档第 11 节覆盖管理员、普通用户、审计员、强制改密、任务、报告、附件、知识编辑、审计、三主题、403/404 和旧路由。

- [ ] **Step 2: 建立隔离 PostgreSQL/平台/Agent fixture**

测试数据只能由 seed 脚本写入 test compose；脚本拒绝非测试 DSN，使用固定测试凭据且不输出密码/Token。

- [ ] **Step 3: 增加网络请求断言**

浏览器请求中禁止 `/api/v1/app/`、`/legacy`、Agent WS、CDN、raw result 和绝对文件路径。

- [ ] **Step 4: 运行 E2E 并提交**

```powershell
docker compose -f deploy/compose/docker-compose.console-e2e.yml up --build --abort-on-container-exit --exit-code-from console-e2e
git add web/console/e2e web/console/playwright.config.ts deploy/compose/docker-compose.console-e2e.yml scripts/seed-console-e2e.ps1
git commit -m "test: add enterprise console end-to-end coverage"
```

### Task 18: 一次性切换 Go 嵌入资源和生产镜像

**Files:**
- Create: `scripts/build-console.ps1`
- Create: `scripts/check-console-assets.ps1`
- Create: `common/websocket/static_manifest_test.go`
- Modify: `Dockerfile`
- Modify: `common/websocket/server.go`
- Replace: `common/websocket/static/**`
- Modify: `.github/workflows/docker-publish.yml`
- Modify: `.github/workflows/create-release.yml`

- [ ] **Step 1: 写旧资产清退 RED**

检查必须拒绝：`A.I.G`、旧 hashed bundle、`aigdocs`、TencentSans、旧 Logo、旧 marketing 图片、`/api/v1/app/` 和公网 CDN。

- [ ] **Step 2: 增加 frontend-builder 阶段**

Docker 顺序：固定 Node 镜像安装 frozen lockfile → `prepare:fonts` 校验/复制离线字体 → test/typecheck/build → 将 dist 复制到 Go builder 的 `common/websocket/static` → Go build。任何前端或字体校验失败都阻止镜像生成。运行镜像 `/app/licenses/` 和 Release 压缩包必须同时包含现有 Droid/AOSP 归属、IBM Plex OFL 与前端 `FONT_ASSETS.md`；构建测试校验许可随二进制分发。

- [ ] **Step 3: 原子替换静态资源**

构建脚本先输出到临时目录并验证 manifest，再替换 `common/websocket/static`；不得把旧文件与新 dist 混合。提交新构建产物以保持宿主 `go build` 可用，CI 校验源码构建结果与已提交产物一致。

- [ ] **Step 4: 加 SPA 与缓存测试**

`index.html` 使用 no-cache；带 hash 的 JS/CSS/font immutable；未知 SPA 深链返回 index；真实不存在的 `/api/**` 不回退 HTML。静态 manifest 测试必须确认 Droid 和 IBM Plex 字体均存在、hash 与清单一致，CSS 无公网 font URL，旧 TencentSans 不存在。

- [ ] **Step 5: 构建并扫描镜像**

```powershell
docker build --target migrate -t aig-console-migrate:test .
docker build -t aig-console-webserver:test .
docker run --rm aig-console-webserver:test sh -ec "test -f /app/ai-infra-guard && ! grep -R -E 'A\.I\.G|aigdocs|TencentSans' /app"
```

- [ ] **Step 6: 提交**

```powershell
git add Dockerfile common/websocket/server.go common/websocket/static common/websocket/static_manifest_test.go scripts/build-console.ps1 scripts/check-console-assets.ps1 .github/workflows/docker-publish.yml .github/workflows/create-release.yml
git commit -m "feat: replace legacy ui with enterprise console"
```

### Task 19: 完成全量验证、文档和项目状态

**Files:**
- Modify: `docs/product/features.md`
- Modify: `docs/project/status.md`
- Modify: `docs/project/plans/README.md`
- Modify: `docs/project/plans/enterprise-console.md`
- Modify: `docs/README.md`
- Modify: `README.md`

- [ ] **Step 1: 运行后端 fresh 验证**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/platform/... ./common/websocket ./internal/apidocs -count=1"
```

Expected: 新增范围全部 PASS；若遇既有基线失败，必须用同一 Go/镜像在基线提交复现，不能直接标为无关。

- [ ] **Step 2: 运行前端 fresh 验证**

```powershell
docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test "corepack enable && pnpm install --frozen-lockfile && pnpm lint && pnpm typecheck && pnpm test:run && pnpm build"
docker compose -f deploy/compose/docker-compose.console-e2e.yml up --build --abort-on-container-exit --exit-code-from console-e2e
```

- [ ] **Step 3: 运行静态和安全门禁**

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\check-console-assets.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\check-docs-layout.ps1
git diff --check
```

扫描源码、构建产物和测试日志，禁止真实 API Key/Token、Cookie、私钥、绝对用户路径、raw result 和旧页面标识。

- [ ] **Step 4: 更新文档状态**

只有以上证据全部通过，才把 UI-01..UI-07、M6、M8 和 OPS-04 从待开发改为已实现；保留未完成项，不因页面存在而提升状态。

- [ ] **Step 5: 完成代码审查和提交**

使用 `@requesting-code-review` 做规格和质量双审，关闭 P0/P1/P2 后提交：

```powershell
git add docs README.md
git commit -m "docs: record enterprise console delivery"
```

- [ ] **Step 6: 准备合并**

使用 `@finishing-a-development-branch` 给出本地合并、推送 PR 或保留分支选项；未经用户明确选择不 push、不创建 PR。
