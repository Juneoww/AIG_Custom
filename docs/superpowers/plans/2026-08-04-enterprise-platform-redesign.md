# 企业安全平台二开 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在保留 AI-Infra-Guard 扫描引擎及规则判定的前提下，交付具备本地账号登录、三角色治理、管理层报告、独立品牌 UI 和麒麟/PostgreSQL Docker 部署能力的单租户企业安全平台。

**Architecture:** 在 Go 单体服务内新增 `internal/platform` 作为认证、授权、审计、品牌、报告和引擎适配边界；既有 `common/websocket` 任务与 Agent 代码只在受控适配层后运行。以可维护的 React/TypeScript 控制台替换当前无源码的嵌入式构建产物，并构建后嵌入同一 Go 二进制。

**Tech Stack:** Go 1.23、Gin、GORM/`database/sql`、PostgreSQL（开发、测试与当前交付）、React、TypeScript、Vite、Vitest、Playwright、Docker Buildx、中文字体/PDF 渲染运行时。

---

## 实施前约束

- 目标代码仓库为 `github.com/Juneoww/AIG_Custom`；开始实现前将其配置为可推送的 `origin`，并将腾讯原仓库保留为只读 `upstream`。在此操作完成前不要推送。
- 必须保留根目录 `NOTICE`，并在新控制台 About 页及部署/发行说明中加入其中要求的“Based on Tencent Zhuque Lab AI-Infra-Guard”及原仓库链接；该声明不参与产品品牌和主导航视觉。
- 不接受客户端 `username` 请求头、公开 Agent WebSocket 或公开旧 API 作为新平台的信任边界。浏览器仅访问新的受保护 API；旧端点仅作为内网兼容通道。
- 当前阶段所有运行服务与构建/测试工具均通过 Docker 运行；Compose 常驻服务仅为 `platform`、受控 `agent` 与 `postgres`，`migrate` 为一次性服务。达梦驱动、目标版本、镜像和兼容性验证均延期，待供应商交付物确定后另立任务，不在当前代码中猜测或接入。
- 计划中的 Go、Node、Playwright 与 YAML 校验不得依赖开发机安装运行时：测试进程必须在 Docker 容器中执行。Task 2 建立 Go 测试容器脚本，Task 7 建立前端构建/单测容器脚本，Task 8 建立 E2E Compose 测试服务；后续任务必须复用它们。

## 计划文件结构

| 文件 | 职责 |
| --- | --- |
| `pkg/database/config.go`、`pkg/database/migrate.go` | 显式选择 PostgreSQL 和迁移入口；去除 SQLite 专有启动假设，并保持业务层不绑定特定方言。 |
| `internal/platform/identity/*` | 用户、角色、会话、密码、CSRF、授权上下文和引导管理员。 |
| `internal/platform/audit/*` | 结构化、可查询、不可由普通用户修改的审计日志。 |
| `internal/platform/models/*` | 全局/私有模型的可见性、加密密钥和安全的任务解析。 |
| `internal/platform/tasks/*` | 任务所有权、平台状态、对既有 `TaskManager`/Agent 的内部适配。 |
| `internal/platform/reports/*` | 不可变报告快照、风险映射、HTML/PDF 渲染和导出记录。 |
| `common/websocket/server.go` | 组装平台依赖、挂载新路由，限制旧路由和 Agent WebSocket。 |
| `common/websocket/task_manager.go`、`agent.go` | 仅保留给内部适配器的安全任务分发与 Agent 身份校验。 |
| `web/console/*` | 可维护、可测试的企业工作台控制台源码及构建配置。 |
| `Dockerfile*`、`docker-compose*.yml`、`deploy/*` | 多架构、离线导入、麒麟与 PostgreSQL Docker 部署配置。 |

### Task 1: 锁定分支、归属和可复现基线

**Files:**
- Modify: `go.mod`, 所有 Go 文件中的模块导入
- Modify: `NOTICE`, `README.md`, `api.md`, `api_zh.md`, `docs/swagger.yaml`
- Create: `docs/deployment/attribution.md`
- Test: `pkg/database/database_test.go`

- [ ] **Step 1: 建立目标仓库与上游远程的只读检查清单**

记录 `origin` 指向 `Juneoww/AIG_Custom`、`upstream` 指向腾讯上游、当前分支及干净/已知未跟踪文件；不得删除用户已有未跟踪文件。

- [ ] **Step 2: 写入归属文档回归检查**

在 `scripts/check-attribution.ps1` 中断言 `NOTICE` 与 `docs/deployment/attribution.md` 均含要求的上游名称和链接。控制台 About 文案检查在 Task 7 创建页面后再加入。

- [ ] **Step 3: 运行归属检查，确认其因新文档尚不存在而失败**

Run: `docker run --rm -v "${PWD}:/src:ro" -w /src mcr.microsoft.com/powershell:lts-alpine pwsh -File scripts/check-attribution.ps1`

Expected: FAIL，报告缺少新的部署归属文档。

- [ ] **Step 4: 切换模块路径并补齐归属文档**

将模块路径及内部 import 改为 `github.com/Juneoww/AIG_Custom`；保留原许可证头，创建部署归属文档并更新 README/API 文档指向新产品说明。不得删除根目录 `NOTICE`。

- [ ] **Step 5: 运行基础构建与归属检查**

Run: `docker run --rm -e GOPROXY=https://goproxy.cn,direct -v "${PWD}:/src" -v aig-platform-go-mod:/go/pkg/mod -v aig-platform-go-build:/root/.cache/go-build -w /src golang:1.23.2-alpine sh -c "go build ./cmd/cli"; docker run --rm -v "${PWD}:/src:ro" -w /src mcr.microsoft.com/powershell:lts-alpine pwsh -File scripts/check-attribution.ps1`

Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add go.mod common pkg cmd README.md NOTICE api.md api_zh.md docs/swagger.yaml docs/deployment scripts/check-attribution.ps1
git commit -m "chore: establish custom platform baseline"
```

### Task 2: PostgreSQL 配置、版本化迁移与容器运行基线

**Files:**
- Modify: `cmd/cli/main.go`, `pkg/database/config.go`, `pkg/database/database_test.go`
- Create: `pkg/database/migrate.go`, `pkg/database/migrate_test.go`, `pkg/database/testutil_test.go`
- Create: `scripts/docker-go-test.ps1`
- Create: `deploy/compose/docker-compose.postgres-test.yml`, `deploy/compose/docker-compose.postgres.yml`, `docs/deployment/postgres.md`

- [x] **Step 1: 写入失败测试：数据库配置和迁移仅支持 PostgreSQL**

在 `pkg/database/migrate_test.go` 覆盖缺少 DSN、未知驱动、PostgreSQL DSN、空库首次迁移、已迁移库的幂等回读与迁移版本表；集成测试使用隔离的 Docker PostgreSQL，不得引入 SQLite 驱动、临时库或生产环境变量。

- [x] **Step 2: 建立 PostgreSQL 容器测试支架**

创建 `docker-compose.postgres-test.yml`，其中 `postgres-test` 为隔离、临时的 PostgreSQL 服务，`database-test` 为一次性 Go 容器，挂载源码、注入专用测试 DSN，并运行 `go test ./pkg/database -run 'Test(DatabaseConfig|Migration)' -count=1`。该支架不得复用交付数据库卷，且必须在测试进程结束后清理。

- [x] **Step 3: 运行数据库配置测试并确认失败**

Run: `docker compose -f deploy/compose/docker-compose.postgres-test.yml up --abort-on-container-exit --exit-code-from database-test; docker compose -f deploy/compose/docker-compose.postgres-test.yml down -v`

Expected: FAIL，尚无 PostgreSQL 驱动选择、平台迁移入口和版本表。

- [x] **Step 4: 实现显式数据库配置和版本化迁移**

扩展 `Config` 为 `Driver`、`DSN`、连接池参数；新增 `Migrate(db)`，用迁移版本表而不是仅依赖 `AutoMigrate`。当前启动与测试仅接受 PostgreSQL，不保留或新增 SQLite 迁移/运行路径；业务层不使用 PostgreSQL 专有 SQL，为达梦后续适配保留清晰边界，但本任务不得引入达梦驱动或镜像。为 `cmd/cli/main.go` 增加显式 `aig migrate` 子命令，它读取相同的 PostgreSQL 配置并只调用 `Migrate(db)` 后退出。新增带完整中文脚本头注释的 `scripts/docker-go-test.ps1`，其仅负责启动固定 Go 容器、挂载源码与命名缓存卷并传递包/测试筛选参数，禁止回退到主机 Go。

- [x] **Step 5: 为 PostgreSQL 编排最小运行验证服务**

新增交付 Compose，包含持久化 `postgres` 和使用同一平台镜像执行 `aig migrate` 的一次性 `migrate` 服务，传入 `DB_DRIVER=postgres` 与 `DB_DSN`；`migrate` 必须设置 `restart: "no"` 并等待 PostgreSQL 健康，后续 `platform`/`agent` 必须以 `service_completed_successfully` 依赖其完成。迁移集成测试只使用 Step 2 的独立测试 Compose，不属于交付运行编排。在 `docs/deployment/postgres.md` 固化镜像版本、初始化账号权限、持久化卷、备份、回滚与迁移失败处置要求。

- [x] **Step 6: 运行测试和容器迁移验证**

Run: `docker compose -f deploy/compose/docker-compose.postgres-test.yml up --abort-on-container-exit --exit-code-from database-test; docker compose -f deploy/compose/docker-compose.postgres-test.yml down -v; docker compose -f deploy/compose/docker-compose.postgres.yml up -d postgres; docker compose -f deploy/compose/docker-compose.postgres.yml run --rm migrate; docker compose -f deploy/compose/docker-compose.postgres.yml config`

Expected: PostgreSQL 集成测试通过；`aig migrate` 创建迁移版本且可回读；Compose 明确 `migrate` 一次性语义和服务启动依赖。

- [x] **Step 7: Commit**

```bash
git add cmd/cli/main.go pkg/database scripts/docker-go-test.ps1 go.mod go.sum deploy/compose docs/deployment/postgres.md
git commit -m "feat: add postgres migration support"
```

### Task 3: 身份、会话和三角色授权核心

**Files:**
- Create: `internal/platform/identity/entity.go`, `password.go`, `session.go`, `csrf.go`, `repository.go`, `service.go`, `middleware.go`, `bootstrap.go`
- Create: `internal/platform/identity/service_test.go`, `middleware_test.go`, `bootstrap_test.go`
- Modify: `cmd/cli/main.go`, `common/websocket/server.go`

- [x] **Step 1: 写入身份服务的失败测试**

覆盖 Argon2id 校验、错误密码、禁用账号、首次/重置后强制改密、会话轮换/撤销、CSRF 缺失，以及 `admin`、`user`、`auditor` 的最小权限矩阵。另覆盖生产环境会话 Cookie 始终为 `Secure`，仅来自配置的可信反向代理 CIDR 的 `X-Forwarded-Proto: https` 可被接受，以及仅测试环境的显式 `ALLOW_INSECURE_TEST_COOKIE=true` 例外；生产环境设置该例外必须启动失败。

- [x] **Step 2: 运行身份测试并确认失败**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/identity'`

Expected: FAIL，包不存在。

- [x] **Step 3: 实现安全身份服务**

实现用户、角色、会话与密码重置状态实体；密码只存 Argon2id 哈希。将随机会话 token 的哈希入库，Cookie 设置 `HttpOnly`、`Secure`、`SameSite=Lax`，状态变更请求使用双提交 CSRF token。生产环境仅接受由 `TRUSTED_PROXY_CIDRS` 指定的 TLS 终止代理发送的 HTTPS 转发头，其他转发头一律忽略；`ALLOW_INSECURE_TEST_COOKIE=true` 仅在明确测试环境可用，不能用于开发或交付。新增 `aig bootstrap-admin`，只在无管理员时创建首个管理员，密码经 stdin 或一次性环境变量读取且不写日志。

- [x] **Step 4: 实现请求主体和策略中间件**

中间件只从会话读取主体，拒绝 `username` 头伪造；`RequireRole`/`RequireOwnerOrRole` 在 handler 前执行。审计员只能读，普通用户只能操作本人资源，管理员全局治理。

- [x] **Step 5: 运行身份和路由测试**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/identity','./common/websocket'`

Expected: PASS；未登录为 401、越权为 403、首次登录只能访问改密与登出。

- [x] **Step 6: Commit**

```bash
git add internal/platform/identity cmd/cli/main.go common/websocket/server.go
git commit -m "feat: add local authentication and rbac core"
```

### Task 4: 管理员后台、审计和模型密钥治理 API

**Files:**
- Create: `internal/platform/admin/handler.go`, `dto.go`, `handler_test.go`
- Create: `internal/platform/audit/entity.go`, `repository.go`, `service.go`, `service_test.go`
- Create: `internal/platform/models/entity.go`, `crypto.go`, `service.go`, `service_test.go`
- Create: `internal/platform/knowledge/handler.go`, `service.go`, `service_test.go`
- Modify: `common/websocket/model_api.go`, `common/websocket/knowledge_api.go`, `common/websocket/knowledge2_api.go`, `common/websocket/server.go`, `api.md`, `api_zh.md`, `docs/swagger.yaml`

- [x] **Step 1: 写入失败测试：管理员用户/角色变更必须审计，模型密钥不能出现在 API 响应**

测试管理员创建用户、分配角色、禁用、重置密码；审计员不能写；普通用户无法读取别人私有模型；全局模型仅管理员可写；模型 token 在列表、详情、日志序列化中永不明文出现。另测试仅管理员可修改现有指纹、漏洞与知识库内容，每次变更均写审计；普通用户/审计员不能写；既有已完成任务/报告仍读取其快照而不被规则变更影响。

- [x] **Step 2: 运行测试并确认失败**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/admin','./internal/platform/audit','./internal/platform/models','./internal/platform/knowledge'`

Expected: FAIL，包不存在。

- [x] **Step 3: 实现后台与审计服务**

实现受控的用户、角色、密码重置、审计查询 API；审计事件包括登录成功/失败、角色与账号变更、任务变更、报告导出、系统配置和规则/知识库内容变更。用由环境变量注入、可轮换的主密钥加密模型 token；正常用户仅管理本人私有模型，管理员管理全局模型并仅看密钥掩码。新增 `internal/platform/knowledge` 作为既有规则和知识库读写的受控门面：保持现有文件格式与扫描协议，管理员修改只影响后续扫描，服务层为每次写入记录审计。

- [x] **Step 4: 迁移并替换旧模型 API 的信任来源**

将现有 `model_api.go`、`knowledge_api.go` 和 `knowledge2_api.go` 变为内部兼容实现或由平台 handler 调用，删除请求头用户名依赖；新平台规则/知识库端点经过管理员授权和审计，路由与 Swagger 文档将旧端点标记为 internal/deprecated，浏览器无法绕过平台权限直接调用。

- [x] **Step 5: 运行 API 回归测试**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/admin','./internal/platform/audit','./internal/platform/models','./internal/platform/knowledge','./common/websocket'`

Expected: PASS，且响应快照不包含 token。

- [x] **Step 6: Commit**

```bash
git add internal/platform/admin internal/platform/audit internal/platform/models internal/platform/knowledge common/websocket/model_api.go common/websocket/knowledge_api.go common/websocket/knowledge2_api.go common/websocket/server.go api.md api_zh.md docs/swagger.yaml
git commit -m "feat: add admin governance and audit logging"
```

### Task 5: 受控任务适配器和内部 Agent 边界

**Files:**
- Create: `internal/platform/tasks/entity.go`, `adapter.go`, `service.go`, `handler.go`, `service_test.go`, `handler_test.go`
- Modify: `common/websocket/task.go`, `common/websocket/task_manager.go`, `common/websocket/agent.go`, `common/websocket/server.go`, `common/websocket/api_test.go`, `cmd/agent/main.go`, `common/agent/agent.go`, `common/agent/agent_test.go`

- [ ] **Step 1: 写入失败测试：平台任务所有权、幂等分发和私有附件**

测试普通用户仅能读取/取消本人任务，审计员全局只读，管理员可治理；相同幂等键不会重复提交；分发临时失败保留状态而不删除任务；附件 URL 不能由未授权用户读取。另测试新的受保护任务状态查询端点只基于会话和任务所有权返回状态，拒绝匿名与旧浏览器 WebSocket 回退。

- [ ] **Step 2: 写入失败测试：Agent 必须通过内网凭据认证且不能抢占重复 ID**

在 `common/websocket/api_test.go` 为缺失/错误 Agent token、重复 Agent ID、未经平台授权的 WebSocket 建连断言 401/403 或显式拒绝；在 `common/agent/agent_test.go` 为 Agent 客户端从 `AIG_AGENT_TOKEN` 读取 token、将其作为 WebSocket 握手 header 发送、缺失 token 时拒绝连接写失败测试。

- [ ] **Step 3: 运行测试并确认失败**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/tasks','./common/websocket' -Run 'Test(PlatformTask|AgentAuth|TaskOwnership)'`

Expected: FAIL，旧实现仍信任 header、默认共享并会清理失败任务。

- [ ] **Step 4: 实现适配器与状态机**

平台先事务保存任务、所有者、附件引用、幂等键和 `pending` 状态，再通过 `EngineAdapter.SubmitTask/GetTaskStatus/GetResult/CancelTask` 调用现有 `TaskManager`。仅适配器可解密模型 token 并发送给内部 Agent；所有日志使用密钥掩码。回调/轮询按平台任务 ID 更新状态，网络重试有上限且不会自动重跑已进入扫描的任务。浏览器实时状态使用经 Cookie 会话和所有权授权的任务详情轮询 API（短轮询并支持断线后重试），不连接旧 Agent WebSocket 或旧公开任务接口。

- [ ] **Step 5: 实现双端 Agent 认证并封闭旧入口**

在 `cmd/agent/main.go` 和 `common/agent/agent.go` 中从 `AIG_AGENT_TOKEN` 读取凭据，以 `X-Internal-Agent-Token` 在 WebSocket HTTP 握手发送；凭据缺失或错误时客户端与服务端均显式失败且不输出 token。随后将 `/api/v1/app/*` 和 `/api/v1/agents/ws` 从浏览器公开路由移至内部 token/网络策略保护组；关闭默认 `Share: true`，为上传、分片、合并和下载统一做所有权及大小限制校验。

- [ ] **Step 6: 运行任务适配回归**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/tasks','./common/websocket'`

Expected: PASS；给定既有任务输入仍生成相同类型任务与引擎事件。

- [ ] **Step 7: Commit**

```bash
git add internal/platform/tasks common/websocket/task.go common/websocket/task_manager.go common/websocket/agent.go common/websocket/server.go common/websocket/api_test.go cmd/agent/main.go common/agent/agent.go common/agent/agent_test.go
git commit -m "feat: secure platform task adapter"
```

### Task 6: 报告快照、在线渲染和 PDF 导出

**Files:**
- Create: `internal/platform/reports/entity.go`, `risk.go`, `snapshot.go`, `render.go`, `pdf.go`, `handler.go`
- Create: `internal/platform/reports/snapshot_test.go`, `risk_test.go`, `handler_test.go`
- Create: `internal/platform/brand/entity.go`, `service.go`, `handler.go`, `service_test.go`
- Modify: `internal/platform/tasks/service.go`, `common/websocket/server.go`, `api.md`, `api_zh.md`, `docs/swagger.yaml`

- [ ] **Step 1: 写入失败测试：完成任务只生成一次不可变快照**

测试引擎 `done` 事件生成一个报告快照；规则/品牌更新不会改变历史快照；PDF 失败只重试同一快照；显式重跑创建新任务和新快照；导出写入审计记录。

- [ ] **Step 2: 写入风险映射和 30 天趋势的失败测试**

以固定引擎结果 fixture 断言高/中/低风险分布、评分、映射版本和最近 30 天已完成任务趋势，避免前端自行推导。

- [ ] **Step 3: 运行测试并确认失败**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/reports','./internal/platform/brand'`

Expected: FAIL，包不存在。

- [ ] **Step 4: 实现报告与品牌域服务**

将完成任务的原始结果、风险映射版本、品牌快照和渲染数据事务化保存。HTML 在线报告与 PDF 均从同一快照生成；PDF 显示产品名、Logo、生成时间、水印占位且嵌入中文字体。品牌配置可更新当前产品名、主色和 Logo，但不得回写历史报告。

- [ ] **Step 5: 实现受保护的报告 API**

添加列表、详情、PDF 下载和管理员补建缺失快照端点；普通用户受所有权限制，审计员可只读/导出，管理员可全局读取和受审计补建。

- [ ] **Step 6: 运行报告测试及 PDF 冒烟**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/reports','./internal/platform/brand'; powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/tasks'`

Expected: PASS；生成的 PDF 可打开且包含中文标题与固定快照内容。

- [ ] **Step 7: Commit**

```bash
git add internal/platform/reports internal/platform/brand internal/platform/tasks common/websocket/server.go api.md api_zh.md docs/swagger.yaml
git commit -m "feat: add immutable security reports"
```

### Task 7: 可维护的独立品牌控制台基础

**Files:**
- Create: `web/console/package.json`, `vite.config.ts`, `tsconfig.json`, `index.html`
- Create: `web/console/DESIGN.md`
- Create: `scripts/docker-console.ps1`
- Create: `web/console/src/main.tsx`, `app/App.tsx`, `app/routes.tsx`, `api/client.ts`, `api/types.ts`
- Create: `web/console/src/styles/tokens.css`, `styles/global.css`, `components/BrandMark.tsx`, `components/AppShell.tsx`
- Create: `web/console/src/pages/AboutPage.tsx`, `web/console/src/pages/NotFoundPage.tsx`, `web/console/src/**/*.test.tsx`
- Modify: `common/websocket/static/index.html`, `common/websocket/server.go`, `.gitignore`

- [ ] **Step 1: 先完成企业控制台设计审查**

使用已安装的 `design-taste-frontend` 技能写入 `web/console/DESIGN.md`：为登录、品牌展示和 About 明确受监管组织的 B2B 定位、浅色蓝色品牌及 Design Read，设计参数固定为视觉变化度 3、动效 2、信息密度 5。高密度工作台和后台统一采用 Fluent UI React v9；Taste Skill 仅用于前述页面，不将营销页面版式或规则套入仪表盘、后台或数据表格。工作台/后台的可访问性、聚焦、语义状态与动效由 Fluent UI 和平台规范验证。前端资源必须本地随 Docker 镜像交付，不使用公网 CDN。

- [ ] **Step 2: 写入失败的控制台路由和品牌组件测试**

使用 Vitest/Testing Library 断言默认蓝色主题、运行时品牌名称/Logo 回退、About 页归属声明和受保护路由重定向。

- [ ] **Step 3: 运行前端测试并确认失败**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-console.ps1 -Command test`

Expected: FAIL，控制台工程不存在。

- [ ] **Step 4: 初始化 React/TypeScript/Vite 工程和设计令牌**

实现企业工作台式浅色高密度样式、蓝色主题令牌、独立几何 SVG 默认标识（不复用 AIG/Tencent 视觉）、可配置名称和 Logo。将构建产物输出到 `common/websocket/static`，由 Go `embed` 提供 SPA 回退。新增带完整中文脚本头注释的 `scripts/docker-console.ps1`，用固定 Node 镜像运行 `pnpm` 安装、单测与生产构建，不允许使用主机 Node/Pnpm。

- [ ] **Step 5: 实现 API 客户端基础**

所有请求使用安全 Cookie 和 CSRF header，统一处理 401/403/首次改密状态；禁止在 `localStorage` 存储会话或模型密钥。

- [ ] **Step 6: 扩展归属检查并运行前端单元测试与生产构建**

扩展 `scripts/check-attribution.ps1`，使其同时校验 `web/console/src/pages/AboutPage.tsx` 的上游归属文案。随后运行：`powershell -ExecutionPolicy Bypass -File scripts/docker-console.ps1 -Command test; powershell -ExecutionPolicy Bypass -File scripts/docker-console.ps1 -Command build; docker run --rm -v "${PWD}:/src:ro" -w /src mcr.microsoft.com/powershell:lts-alpine pwsh -File scripts/check-attribution.ps1; powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './common/websocket'`

Expected: PASS，构建产物可被 `go:embed` 编译。

- [ ] **Step 7: Commit**

```bash
git add web/console scripts/docker-console.ps1 common/websocket/static common/websocket/server.go .gitignore
git commit -m "feat: add branded enterprise console shell"
```

### Task 8: 登录、管理员后台与角色化控制台页面

**Files:**
- Create: `web/console/src/pages/LoginPage.tsx`, `ChangePasswordPage.tsx`, `ProfilePage.tsx`
- Create: `web/console/src/pages/admin/UsersPage.tsx`, `RolesPage.tsx`, `AuditPage.tsx`, `BrandSettingsPage.tsx`
- Create: `web/console/src/components/DataTable.tsx`, `PermissionGate.tsx`, `FormDialog.tsx`
- Create: `web/console/e2e/auth-rbac.spec.ts`
- Create: `deploy/compose/docker-compose.e2e.yml`

- [ ] **Step 1: 写入端到端失败场景**

覆盖管理员创建普通用户/审计员、首次改密、审计员无法看到写按钮、普通用户访问管理员 URL 显示 403、品牌修改只影响当前导航；测试编排必须显式启用仅测试环境的 `ALLOW_INSECURE_TEST_COOKIE=true`，并断言生产配置不能启用该变量。

- [ ] **Step 2: 运行 E2E 并确认失败**

Run: `docker compose -f deploy/compose/docker-compose.e2e.yml run --rm e2e pnpm --dir web/console exec playwright test e2e/auth-rbac.spec.ts`

Expected: FAIL，页面不存在。

- [ ] **Step 3: 实现身份和后台页面**

完成登录、强制改密、个人中心以及用户、角色、审计、品牌设置。页面仅依据服务端 `/me` 权限显示入口，所有按钮仍由服务端授权二次保护。新增 E2E Compose：测试时启动临时 PostgreSQL、迁移后的平台和 Playwright `e2e` 服务；所有服务位于隔离内部网络，浏览器测试不使用主机 Node。E2E 平台只在该测试 Compose 下通过 `APP_ENV=test` 和 `ALLOW_INSECURE_TEST_COOKIE=true` 使用 HTTP Cookie；交付 Compose 不得设置此变量。

- [ ] **Step 4: 运行 UI 和 API 回归**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-console.ps1 -Command test; docker compose -f deploy/compose/docker-compose.e2e.yml run --rm e2e pnpm --dir web/console exec playwright test e2e/auth-rbac.spec.ts; powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/...'`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/console/src web/console/e2e deploy/compose/docker-compose.e2e.yml
git commit -m "feat: add login and admin console"
```

### Task 9: 扫描、总览、模型、规则和报告工作台

**Files:**
- Create: `web/console/src/pages/DashboardPage.tsx`, `tasks/TaskListPage.tsx`, `tasks/TaskDetailPage.tsx`, `tasks/NewTaskPage.tsx`
- Create: `web/console/src/pages/ReportsPage.tsx`, `ReportDetailPage.tsx`, `ModelsPage.tsx`, `KnowledgePage.tsx`
- Create: `web/console/src/components/RiskSummary.tsx`, `TrendChart.tsx`, `ReportPreview.tsx`, `TaskStatus.tsx`
- Create: `web/console/e2e/tasks-reports.spec.ts`

- [ ] **Step 1: 写入失败的任务和报告 E2E 测试**

覆盖普通用户创建并只看到本人任务、审计员看到全局只读数据、任务完成后通过受保护任务详情轮询更新状态、断线恢复后继续轮询、PDF 下载触发审计、历史快照不随品牌更新改变；断言浏览器不连接旧 Agent WebSocket 或旧公开任务接口。

- [ ] **Step 2: 运行 E2E 并确认失败**

Run: `docker compose -f deploy/compose/docker-compose.e2e.yml run --rm e2e pnpm --dir web/console exec playwright test e2e/tasks-reports.spec.ts`

Expected: FAIL，页面和查询状态尚未实现。

- [ ] **Step 3: 实现工作台页面**

实现安全总览、扫描创建/列表/详情/受保护轮询状态、报告列表/在线详情/PDF、全局与个人模型、规则与知识库页面。风险评分和趋势只消费报告 API 预计算字段；审计员页面全程只读；规则/知识库写操作仅在服务端返回管理员授权后展示并调用 Task 4 的受控 API。

- [ ] **Step 4: 运行前后端回归**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-console.ps1 -Command test; docker compose -f deploy/compose/docker-compose.e2e.yml run --rm e2e pnpm --dir web/console exec playwright test e2e/tasks-reports.spec.ts; powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/...','./common/websocket'`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/console/src web/console/e2e
git commit -m "feat: add scanning and reporting workbench"
```

### Task 10: 麒麟、PostgreSQL、离线与多架构交付

**Files:**
- Modify: `Dockerfile`, `Dockerfile_Agent`, `docker-compose.yml`, `docker-compose.images.yml`, `.github/workflows/docker-publish.yml`
- Create: `deploy/compose/docker-compose.production.yml`, `deploy/offline/export-images.sh`, `deploy/offline/import-images.sh`
- Create: `docs/deployment/kylin-postgres.md`, `docs/deployment/offline-install.md`, `docs/deployment/upgrade.md`, `docs/deployment/tls-ingress.md`
- Test: `scripts/verify-image-platforms.ps1`

- [ ] **Step 1: 写入失败的镜像与配置验证脚本**

校验 compose 不再默认 SQLite/公开旧端口，且仅包含 `platform`、受控 `agent`、持久化 `postgres` 与一次性 `migrate` 服务；`migrate` 为 `restart: "no"`，等待数据库健康，`platform`/`agent` 以 `service_completed_successfully` 等待迁移成功；交付 Compose 禁止 `ALLOW_INSECURE_TEST_COOKIE`，只向受控入口暴露平台端口；环境变量含 PostgreSQL 连接/主密钥/内部 Agent token，镜像清单含 `linux/amd64` 与 `linux/arm64`、镜像中存在中文字体和非 root 运行用户。

- [ ] **Step 2: 运行验证并确认失败**

Run: `docker run --rm -v "${PWD}:/src:ro" -w /src mcr.microsoft.com/powershell:lts-alpine pwsh -File scripts/verify-image-platforms.ps1`

Expected: FAIL，现有镜像使用 SQLite、Alpine server 和公开旧服务编排。

- [ ] **Step 3: 改造部署制品**

采用适合麒麟的可复现 Linux 基础镜像，前端在 builder 阶段构建；所有运行和构建/测试步骤均在 Docker 中执行。服务和 Agent 使用最小权限、显式内部网络和健康检查；PostgreSQL 仅在内部网络持久化运行，`migrate` 用同一平台镜像一次性执行。交付 Compose 不增加反向代理常驻服务；`tls-ingress.md` 必须规定由组织现有的 HTTPS Ingress/反向代理终止 TLS、转发 `X-Forwarded-Proto: https`、配置其 CIDR 至 `TRUSTED_PROXY_CIDRS`、禁止平台端口直接对公网暴露及证书轮换/健康检查步骤。提供 Buildx 多架构构建、`docker save/load` 离线导入脚本、PostgreSQL 持久化与升级备份文档；第三方模型仅来自可配置的内网/私有 Base URL。

- [ ] **Step 4: 构建并验证部署制品**

Run: `New-Item -ItemType Directory -Force dist | Out-Null; docker buildx build --platform linux/amd64,linux/arm64 -f Dockerfile --output type=oci,dest=dist/aig-server.oci.tar .; docker buildx build --platform linux/amd64,linux/arm64 -f Dockerfile_Agent --output type=oci,dest=dist/aig-agent.oci.tar .; docker run --rm -v "${PWD}:/src:ro" -w /src mcr.microsoft.com/powershell:lts-alpine pwsh -File scripts/verify-image-platforms.ps1; docker compose -f deploy/compose/docker-compose.production.yml config`

Expected: 通过镜像/配置验证，Compose 配置可解析。

- [ ] **Step 5: 导入离线归档并在麒麟 + PostgreSQL 验收环境冒烟**

分别在 `linux/amd64` 与 `linux/arm64` 麒麟验收主机（或具备等价架构覆盖证据的 QEMU CI）执行 `deploy/offline/import-images.sh`，输入 Task 10 Step 4 生成的对应架构 OCI 归档；脚本须将镜像按 `docker-compose.production.yml` 使用的确切标签导入本地镜像仓库，并先以 `docker image inspect` 验证两个标签都存在。每个架构均运行：`docker compose -f deploy/compose/docker-compose.production.yml up -d; docker compose -f deploy/compose/docker-compose.production.yml ps`，并保留迁移完成、平台/Agent/PostgreSQL 健康和浏览器冒烟证据。

Expected: 两种架构均完成迁移，平台、受控 Agent 和 PostgreSQL 连接健康；首次管理员登录、建任务和 PDF 导出成功。

- [ ] **Step 6: Commit**

```bash
git add Dockerfile Dockerfile_Agent docker-compose.yml docker-compose.images.yml .github/workflows/docker-publish.yml deploy docs/deployment scripts/verify-image-platforms.ps1
git commit -m "feat: add kylin and postgres deployment support"
```

### Task 11: 全量验证、API 文档和发布候选

**Files:**
- Modify: `README.md`, `api.md`, `api_zh.md`, `docs/swagger.yaml`, `CHANGELOG.md`
- Create: `docs/acceptance/enterprise-platform.md`

- [ ] **Step 1: 汇总验收清单并写入缺口检查**

列出三角色矩阵、任务所有权、审计事件、报告不可变性、品牌配置、PostgreSQL 迁移、双架构镜像、麒麟离线安装和既有扫描回归的可验证证据。

- [ ] **Step 2: 运行全量自动化校验**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './...'; powershell -ExecutionPolicy Bypass -File scripts/docker-console.ps1 -Command test; docker compose -f deploy/compose/docker-compose.e2e.yml run --rm e2e pnpm --dir web/console exec playwright test; docker run --rm -e GOPROXY=https://goproxy.cn,direct -v "${PWD}:/src" -v aig-platform-go-mod:/go/pkg/mod -v aig-platform-go-build:/root/.cache/go-build -w /src golang:1.23.2-alpine sh -c "go build -o /tmp/yamlcheck ./cmd/yamlcheck && /tmp/yamlcheck data/fingerprints data/vuln data/vuln_en"`

Expected: PASS。

- [ ] **Step 3: 运行安全回归检查**

Run: `powershell -ExecutionPolicy Bypass -File scripts/docker-go-test.ps1 -Packages './internal/platform/...','./common/websocket' -Run 'Test(Unauthenticated|Unauthorized|AgentAuth|TaskOwnership|NoSecretLeak)'`

Expected: PASS；旧的无认证路径、默认共享、明文 token 日志和重复 Agent 接管均不可复现。

- [ ] **Step 4: 更新操作文档与变更日志**

将所有新 API、管理员引导、角色边界、PostgreSQL/麒麟部署、离线升级、归属声明和回滚步骤写入中英 API 文档、Swagger、README 和验收文档。

- [ ] **Step 5: Commit**

```bash
git add README.md api.md api_zh.md docs/swagger.yaml CHANGELOG.md docs/acceptance
git commit -m "docs: complete enterprise platform release checklist"
```

## 最终验收顺序

1. PostgreSQL 开发环境：引导管理员、三角色、任务/报告、审计与 PDF 全链路。
2. 麒麟 Linux + PostgreSQL：分别离线导入 `amd64` 和 `arm64` 对应镜像，在每种架构启动 `platform`、`agent`、`postgres` 与一次性 `migrate` 后完成同一验收流。
3. 扫描兼容性：以固定 fixture 对比改造前后 AI Infra、MCP、Agent 和 Prompt/Jailbreak 任务的引擎结果类型与关键结论。

## 延期项

达梦驱动、SQL 方言验证、达梦 Compose、离线镜像和麒麟 + 达梦验收不属于当前计划；待目标版本和供应商交付物确定后，新增独立设计和实施任务。
