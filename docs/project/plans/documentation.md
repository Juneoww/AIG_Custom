# 项目文档信息架构整理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `docs/` 整理为只包含人读文档的分类入口，把 Swagger 运行时代码/生成物迁入 `internal/apidocs/`，归档旧设计与计划，并建立可验证的项目状态页。

**Architecture:** 人工文档按产品、项目、架构、API、部署和归档分类；`docs/README.md` 是统一入口，`docs/project/status.md` 是唯一实时进度来源。Swagger 四件套作为一个 Go 运行时模块迁入 `internal/apidocs/`，只做内容等价搬迁和包/import 修正，不从当前不完整注解重新生成。

**Tech Stack:** Markdown、Go 1.23、Swaggo、PowerShell/`rg`、Git、Docker PostgreSQL 测试 Compose。

---

## 实施约束与文件职责

- 所有修改在专用工作树 `.worktrees/enterprise-platform` 中执行，不得改动主工作树。
- 移动现有文件使用 `git mv`，不复制后留下重复正文。
- `docs/` 最终不能包含 `.go`、Go 测试、Swagger JSON/YAML 或运行时生成物。
- 不运行 `swag init`：当前源码只有 6 条旧 `/app/taskapi` 注解，而现有 Swagger 含约 37 条平台路径，重新生成会丢接口。
- 不删除 `NOTICE`、`LICENSE`、规则数据、扫描器文件或无关用户文件。
- 归档正文保留历史内容，只加归档头和替代文档链接。
- 本实施计划在开始 Task 1 前已由规划阶段提交到 Git；Task 3 对它执行 `git mv` 时源文件必须处于 tracked 状态。若 `git ls-files --error-unmatch docs/superpowers/plans/2026-08-13-documentation-information-architecture.md` 失败，停止执行，不用文件系统移动绕过。

| 路径 | 职责 |
| --- | --- |
| `docs/README.md` | 人读文档总入口、分类和维护规则 |
| `docs/product/prd.md` | 当前有效产品需求 |
| `docs/project/status.md` | 唯一实时项目状态与里程碑证据 |
| `docs/project/plans/documentation.md` | 本次文档整理的当前实施计划 |
| `docs/project/plans/` | 其他当前有效实施计划目标目录 |
| `docs/architecture/*.md` | 当前架构、控制台和文档治理设计 |
| `docs/api/*.md` | 人工维护 API 参考与专项说明 |
| `docs/deployment/*.md` | 当前部署和归属说明 |
| `docs/archive/2026-08-enterprise-platform/*` | 旧设计和旧计划，只读历史 |
| `internal/apidocs/*` | Swagger Go 注册源码、YAML/JSON 规格和同步测试 |

### Task 1: 为目标结构写失败检查

**Files:**
- Create: `scripts/check-docs-layout.ps1`
- Modify: `AGENTS.md`

- [ ] **Step 1: 创建布局检查脚本**

新增带完整中文脚本头的 `scripts/check-docs-layout.ps1`。核心检查：

```powershell
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$docs = Join-Path $repo 'docs'

$forbidden = Get-ChildItem -LiteralPath $docs -Recurse -File | Where-Object {
    $_.Extension -eq '.go' -or $_.Name -in @('swagger.json', 'swagger.yaml')
}
if ($forbidden) {
    throw "docs 目录仍包含代码或 Swagger 生成物: $($forbidden.FullName -join ', ')"
}

$required = @(
    'docs/README.md',
    'docs/product/prd.md',
    'docs/project/status.md',
    'docs/project/plans/documentation.md',
    'docs/architecture/evolution.md',
    'docs/architecture/enterprise-console.md',
    'docs/architecture/documentation.md',
    'docs/api/reference.md',
    'docs/api/reference.en.md',
    'docs/api/data-sync.md',
    'docs/archive/2026-08-enterprise-platform/README.md',
    'internal/apidocs/docs.go',
    'internal/apidocs/swagger.json',
    'internal/apidocs/swagger.yaml',
    'internal/apidocs/swagger_sync_test.go'
)
foreach ($relative in $required) {
    if (-not (Test-Path -LiteralPath (Join-Path $repo $relative))) {
        throw "缺少目标文件: $relative"
    }
}
```

脚本分两类检查：

1. 对 `.go`、`.ps1`、`.yml/.yaml` 和非治理文档执行旧路径内容扫描；
2. 对 Markdown 链接只匹配 `](` 后的实际链接目标，不扫描反引号代码、迁移表源路径或命令示例。

以下两份治理文档允许以反引号或命令参数记录旧迁移源，但其 Markdown 链接仍必须使用新路径：

```text
docs/architecture/documentation.md
docs/project/plans/documentation.md
```

归档正文允许保留历史命令。不得通过删除迁移映射让检查变绿。

- [ ] **Step 2: 运行检查并确认失败**

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1
```

Expected: FAIL，至少报告 `docs/docs.go`、`docs/swagger.*` 或缺少目标目录文件。

- [ ] **Step 3: 更新 AGENTS 的文档职责约束**

写入：API/任务变更需同步检查 `docs/api/reference.md`、`docs/api/reference.en.md` 和 `internal/apidocs/swagger.yaml`，运行 `go test ./internal/apidocs`；`docs/` 仅存放人读文档。

- [ ] **Step 4: Commit**

```bash
git add AGENTS.md scripts/check-docs-layout.ps1
git commit -m "test: define documentation layout contract"
```

- [ ] **Step 5: 记录 Swagger 迁移前基线**

必须在 Task 3 移动根 API 指南之前执行，因为当前 `docs/swagger_sync_test.go` 仍读取 `../api.md` 与 `../api_zh.md`：

```powershell
git ls-files --error-unmatch docs/superpowers/plans/2026-08-13-documentation-information-architecture.md
Get-FileHash docs/swagger.json -Algorithm SHA256
Get-FileHash docs/swagger.yaml -Algorithm SHA256
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test go test ./docs -count=1
```

Expected: 计划文件为 tracked；记录两个 SHA256；`go test ./docs` PASS。将哈希和测试结果保留在任务执行日志，供 Task 5 对比。

### Task 2: 建立文档入口和项目状态页

**Files:**
- Create: `docs/README.md`
- Create: `docs/project/status.md`
- Create: `docs/project/plans/README.md`

- [ ] **Step 1: 创建 `docs/README.md`**

入口必须链接项目状态、PRD、架构演进、控制台设计、文档治理设计、中英文 API、数据同步、部署和归档，并明确：

```markdown
- `docs/` 只存放人读文档。
- Swagger 运行时契约在 `internal/apidocs/`。
- 归档文档不再更新，当前进度只看 `project/status.md`。
```

“当前计划”还需链接 `project/plans/documentation.md`；企业控制台计划不存在时只显示“待编写”，不能生成坏链接。

- [ ] **Step 2: 创建 `docs/project/status.md`**

状态枚举仅使用 `已完成 / 正在进行 / 待开发 / 阻塞`。初始里程碑按提交证据记录：

| 里程碑 | 状态 | 主要证据 |
| --- | --- | --- |
| PostgreSQL 与迁移 | 已完成 | 数据库提交与测试 |
| 身份、三角色与 CSRF | 已完成 | 身份服务提交与测试 |
| 治理、审计与模型 | 已完成 | `2f227805` 至 `414dbc1d` |
| 任务、附件与 Agent | 已完成 | `509890ea`、`9600b4c5`、`73c4ae6f` |
| 不可变报告、PDF 与品牌 | 已完成 | `3cbf64a5` |
| 企业控制台设计 | 已完成 | `b9300e8f` |
| 文档信息架构整理 | 正在进行 | 本计划与布局检查 |
| 企业控制台实施计划 | 待开发 | 尚未生成 |
| 企业控制台实现 | 待开发 | 不得误报完成 |
| 完整 OpenAPI 单一生成源 | 待开发 | 当前非完整注解生成链 |
| 交付与发布候选 | 待开发 | 等待后续验收 |

页面还包含当前结论、已完成、正在进行、下一步 3–5 项、后续计划、阻塞/风险、文档导航和最后更新时间。不得写主观完成百分比。

- [ ] **Step 3: 创建计划目录说明**

`docs/project/plans/README.md` 明确：当前有效计划放此处；本次整理计划将迁入 `documentation.md`；企业控制台计划待生成；归档旧计划不得移回。不得创建空的 `enterprise-console.md`。

- [ ] **Step 4: 再运行布局检查**

Expected: 仍 FAIL，但原因缩小为尚未迁移文件或旧路径存在。

- [ ] **Step 5: Commit**

```bash
git add docs/README.md docs/project/status.md docs/project/plans/README.md
git commit -m "docs: add documentation index and project status"
```

### Task 3: 迁移当前人工文档与 API 指南

**Files:**
- Move: `docs/prd.md` → `docs/product/prd.md`
- Move: `docs/architecture_evolution.md` → `docs/architecture/evolution.md`
- Move: `docs/api_data_update.md` → `docs/api/data-sync.md`
- Move: `api_zh.md` → `docs/api/reference.md`
- Move: `api.md` → `docs/api/reference.en.md`
- Move: `docs/superpowers/specs/2026-08-13-enterprise-console-rebuild-design.md` → `docs/architecture/enterprise-console.md`
- Move: `docs/superpowers/specs/2026-08-13-documentation-information-architecture-design.md` → `docs/architecture/documentation.md`
- Move: `docs/superpowers/plans/2026-08-13-documentation-information-architecture.md` → `docs/project/plans/documentation.md`

- [ ] **Step 1: 使用 `git mv` 移动上述八份文件**

只创建目标目录，不复制内容，不留下旧路径副本。

- [ ] **Step 2: 修正 PRD 关联文档**

`docs/product/prd.md` 第 18 节改为链接：当前控制台设计、项目状态、中英文 API、新部署路径、架构演进和历史归档入口。旧设计/计划不再列为当前依据。

- [ ] **Step 3: 修正移动后的相对链接**

只修改目录层级变化造成的断链，不重写 API 示例、产品需求或架构正文。

- [ ] **Step 4: 扫描重复与旧路径**

Run:

```powershell
rg --files docs | Sort-Object
rg -n 'docs/prd\.md|docs/architecture_evolution\.md|docs/api_data_update\.md' . --glob '!docs/archive/**'
```

Expected: 新路径各一份；第二条只允许治理设计和本实施计划中的说明性映射/命令，不允许当前链接、Go import、脚本或其他文档引用。

- [ ] **Step 5: Commit**

```bash
git add docs
git commit -m "docs: organize current product and API documentation"
```

`git mv` 已自动暂存根 `api.md`、`api_zh.md` 的删除，不能再把不存在的旧路径作为 pathspec。执行前用 `git status --short` 确认没有无关文件。

### Task 4: 归档旧设计与旧实施计划

**Files:**
- Move: `docs/superpowers/specs/2026-08-04-enterprise-platform-redesign-design.md` → `docs/archive/2026-08-enterprise-platform/design.md`
- Move: `docs/superpowers/plans/2026-08-04-enterprise-platform-redesign.md` → `docs/archive/2026-08-enterprise-platform/implementation-plan.md`
- Create: `docs/archive/2026-08-enterprise-platform/README.md`

- [ ] **Step 1: 使用 `git mv` 移动两份历史文档**

- [ ] **Step 2: 为归档正文增加统一头部**

```markdown
> **归档文档：非当前实施依据。** 本文保留 2026-08 初始设计/计划和当时勾选状态，不再更新。当前需求见 [PRD](../../product/prd.md)，当前进度见 [项目状态](../../project/status.md)，当前控制台设计见 [企业控制台设计](../../architecture/enterprise-console.md)。
```

历史任务正文与命令保持不变。

- [ ] **Step 3: 创建归档 README**

记录归档时间、原因、两份文件和当前替代文档；明确旧勾选不代表当前状态。

- [ ] **Step 4: 确认 `docs/superpowers` 不再含文件**

```powershell
if (Test-Path docs/superpowers) { Get-ChildItem docs/superpowers -Recurse -File }
```

Expected: 无输出。空目录自然消失，不运行递归删除命令。

- [ ] **Step 5: Commit**

```bash
git add docs/archive
git commit -m "docs: archive superseded platform design and plan"
```

`git mv` 已自动暂存 `docs/superpowers/**` 源路径删除；不得对已为空或不存在的旧目录执行显式 `git add`。

### Task 5: 将 Swagger 运行时模块移出 docs

**Files:**
- Move: `docs/docs.go` → `internal/apidocs/docs.go`
- Move: `docs/swagger.json` → `internal/apidocs/swagger.json`
- Move: `docs/swagger.yaml` → `internal/apidocs/swagger.yaml`
- Move: `docs/swagger_sync_test.go` → `internal/apidocs/swagger_sync_test.go`
- Modify: `common/websocket/server.go:35`

- [ ] **Step 1: 确认 Task 1 已记录迁移前基线**

```powershell
git log --oneline -- scripts/check-docs-layout.ps1
Get-FileHash docs/swagger.json -Algorithm SHA256
Get-FileHash docs/swagger.yaml -Algorithm SHA256
```

Expected: Task 1 提交存在；当前两个 SHA256 与 Task 1 Step 5 记录一致。这里不再运行 `go test ./docs`，因为 API 指南已迁移且旧测试路径按预期暂时失效。

- [ ] **Step 2: 使用 `git mv` 移动四件套**

不得运行 `swag init`，不得重新格式化 JSON/YAML。

- [ ] **Step 3: 修改 Go 包名和运行时导入**

```go
// internal/apidocs/docs.go
package apidocs

// internal/apidocs/swagger_sync_test.go
package apidocs

// common/websocket/server.go
_ "github.com/Juneoww/AIG_Custom/internal/apidocs"
```

`SwaggerInfo`、`swag.Register`、JSON 模板和实例名不变。

- [ ] **Step 4: 更新同步测试的人工 API 路径**

将原 `../api.md` 和 `../api_zh.md` 改为：

```go
path: "../../docs/api/reference.en.md"
path: "../../docs/api/reference.md"
```

同目录 `swagger.json`、`swagger.yaml` 读取保持不变。

- [ ] **Step 5: 验证内容等价与包测试**

```powershell
Get-FileHash internal/apidocs/swagger.json -Algorithm SHA256
Get-FileHash internal/apidocs/swagger.yaml -Algorithm SHA256
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test go test ./internal/apidocs -count=1
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test go test ./common/websocket -run '^$' -count=1
```

Expected: 两个哈希与迁移前相同；apidocs PASS；websocket 编译成功。

- [ ] **Step 6: Commit**

```bash
git add internal/apidocs common/websocket/server.go docs
git commit -m "refactor: move swagger runtime module out of docs"
```

### Task 6: 更新仓库入口和所有当前链接

**Files:**
- Modify: `README.md`
- Modify: `readme/README_ZH.md`
- Modify: `readme/README_DE.md`
- Modify: `readme/README_ES.md`
- Modify: `readme/README_FR.md`
- Modify: `readme/README_JA.md`
- Modify: `readme/README_KR.md`
- Modify: `readme/README_PT.md`
- Modify: `readme/README_RU.md`
- Modify: `AGENTS.md`
- Modify: current docs/scripts found by scan

- [ ] **Step 1: 更新根 README**

架构链接改为 `./docs/architecture/evolution.md`，英文 API 改为 `./docs/api/reference.en.md`，新增 `./docs/README.md` 文档入口。

- [ ] **Step 2: 更新各语言 README**

架构链接统一为 `../docs/architecture/evolution.md`；中文 API 用 `../docs/api/reference.md`，原英文 API 链接用 `../docs/api/reference.en.md`。不重写其他正文。

- [ ] **Step 3: 修正剩余当前链接和工程治理路径**

归档正文的历史命令允许保留。设计文件中的旧路径迁移表允许保留；当前链接、代码 import 和脚本路径不得保留。

- [ ] **Step 4: 运行旧路径扫描**

```powershell
rg -n 'docs/(prd\.md|architecture_evolution\.md|api_data_update\.md|superpowers/|swagger\.(yaml|json))|github\.com/Juneoww/AIG_Custom/docs|\.\./api(_zh)?\.md' . --glob '!docs/archive/**' --glob '!common/websocket/static/**'
```

Expected: 除 `docs/architecture/documentation.md` 与 `docs/project/plans/documentation.md` 中明确的迁移映射/命令外，无当前链接、代码或脚本引用。

- [ ] **Step 5: Commit**

```bash
git add README.md readme AGENTS.md docs scripts
git commit -m "docs: update documentation navigation links"
```

### Task 7: 全量验证并回填状态证据

**Files:**
- Modify: `docs/project/status.md` only if evidence needs correction

- [ ] **Step 1: 运行布局检查**

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1
```

Expected: PASS。

- [ ] **Step 2: 运行 Swagger 和 Web 构建验证**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/apidocs -count=1 && go test ./common/websocket -run '^$' -count=1"
```

Expected: 两个包 exit 0。

- [ ] **Step 3: 运行 Markdown 相对链接检查**

使用固定版本离线链接检查器：

```powershell
docker run --rm -v "${PWD}:/work:ro" -w /work lycheeverse/lychee:0.18.1 --offline --no-progress "docs/**/*.md" "README.md" "readme/*.md"
```

Expected: exit 0。若 PowerShell 不展开 glob，改用工具支持的 glob 参数或在容器内运行，但不得跳过相对链接验证。

- [ ] **Step 4: 运行相关 Go 回归**

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec "go test ./internal/apidocs ./common/websocket -count=1"
```

Expected: apidocs PASS；websocket 如有既有基线失败，必须与整理前同命令逐项一致，不能把新增导入/编译失败归为基线。本次不改 `data/`，无需 `yamlcheck`。

- [ ] **Step 5: 检查差异、敏感信息和最终目录**

```powershell
git diff --check
git status --short
rg -n 'sk-[A-Za-z0-9_-]{10,}|Bearer\s+[A-Za-z0-9._-]{10,}' docs internal/apidocs scripts
Get-ChildItem -LiteralPath docs -Recurse -File | Where-Object { $_.Extension -eq '.go' -or $_.Name -in @('swagger.json','swagger.yaml') }
```

Expected: diff-check 无错误；无真实凭据；最后命令无输出；状态只含本计划范围。

- [ ] **Step 6: 回填状态页证据**

只把实际完成的“文档信息架构整理”写为已完成并附提交/测试证据。企业控制台实现和 OpenAPI 单一生成源保持待开发。

- [ ] **Step 7: Commit or skip empty commit**

```bash
git add docs/project/status.md
git commit -m "docs: record documentation reorganization status"
```

若状态页无需修改则跳过空提交，并在交付报告说明证据已记录。

## 最终验收清单

- [ ] `docs/` 只含人读文档，不含 `.go`、测试或 Swagger 生成物。
- [ ] `internal/apidocs/` 四件套齐全，JSON/YAML 哈希与迁移前相同。
- [ ] Web 服务只导入 `internal/apidocs`。
- [ ] Swagger 三件套同步测试通过，未运行不完整 `swag init`。
- [ ] `docs/README.md` 可达所有当前文档和归档入口。
- [ ] `docs/project/status.md` 是唯一实时进度来源，未误报前端完成。
- [ ] 旧设计与旧计划已归档并标注非当前依据。
- [ ] 根 README、各语言 README、AGENTS 和 PRD 使用新路径。
- [ ] 非归档当前引用无旧路径。
- [ ] 布局检查、相对链接、Go 构建和 `git diff --check` 通过。
- [ ] 无敏感信息、规则数据删除或无关改动。
