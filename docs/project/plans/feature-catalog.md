# Platform Feature Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `docs/product/features.md` 补齐为可面向产品、客户和管理人员使用的全量平台功能清单，并以运行入口和验证证据区分已实现、部分实现、待修与待开发能力。

**Architecture:** 以现有 12 个产品模块为主结构，每个原子能力或边界一致的能力簇占一行；主表只呈现产品价值、角色、入口和状态，代码/API/测试细节集中放在文末证据索引。盘点同时覆盖 Go Web/CLI、Agent、Python 扫描组件、规则数据、数据库迁移和部署编排，并用保守状态规则处理文档与实现冲突。

**Tech Stack:** Markdown、PowerShell、Git、Go 测试、Docker、Lychee 0.24.2

---

## 文件职责

- `docs/product/features.md`：正式产品功能总览、功能矩阵、兼容边界、未交付能力和证据索引。
- `docs/README.md`：把功能清单加入“项目与产品”人读入口。
- `docs/project/status.md`：补充功能清单导航和本次盘点状态，不改变企业控制台前端、OpenAPI 单一源或发布候选的真实里程碑。
- `scripts/check-docs-layout.ps1`：把功能清单加入必需路径，并校验 `docs/README.md` 存在实际链接。

## 状态判定约束

- “已实现”必须同时具备受支持运行入口和自动化测试或环境验收证据。
- 代码、路由、Schema、配置和需求只能佐证，不能单独证明已交付。
- 来源冲突时使用更保守状态，并在证据索引记录冲突。
- 企业控制台前端重构、完整 OpenAPI 单一生成源和正式发布候选固定为“待开发”。
- 通用 Web 指纹/版本提取列为“已实现”；反向代理后的后端应用识别列为“部分实现”；Dify 专用多信号规则、代理层/应用层分层、置信度和版本范围列为“待开发”。对应稳定编号为 `SCAN-05`、`SCAN-06`、`SCAN-07`、`SCAN-08`。

### Task 1: 建立可复核的能力与证据盘点

**Files:**
- Modify: `docs/product/features.md`
- Reference: `docs/product/prd.md`
- Reference: `docs/project/status.md`
- Reference: `docs/api/reference.md`
- Reference: `common/websocket/server.go`
- Reference: `common/websocket/server_datastores.go`
- Reference: `cmd/cli/main.go`
- Reference: `cmd/agent/main.go`
- Reference: `docker-compose.yml`
- Reference: `docker-compose.images.yml`

- [ ] **Step 1: 记录修改前主表尚不存在的 RED**

Run:

```powershell
rg -n '^\| ID-[0-9]+ |^\| SCAN-[0-9]+ |^## 证据索引$' docs/product/features.md
```

Expected: exit 1；当前文件只有设计骨架，没有正式能力行和证据索引。

- [ ] **Step 2: 枚举所有受支持入口**

Run:

```powershell
rg -n 'Group\(|GET\(|POST\(|PUT\(|DELETE\(' common/websocket internal/platform
rg -n 'Use:|Command\{|Register|TaskType' cmd/cli cmd/agent common/agent agent-scan mcp-scan AIG-PromptSecurity
rg -n 'services:|image:|build:|migrate|webserver|agent' docker-compose.yml docker-compose.images.yml deploy/compose
```

Expected: 输出 Web/API、CLI、Agent、Python 任务和部署入口候选；只把当前实际注册入口纳入交付清单。

- [ ] **Step 3: 枚举行为验证证据**

Run:

```powershell
rg -n '^func Test|^def test_' common internal pkg cmd agent-scan mcp-scan AIG-PromptSecurity
rg -n '已完成|正在进行|待开发|阻塞|已交付|待修' docs/project/status.md docs/product/prd.md
```

Expected: 每个准备列为“已实现”的能力至少能映射一项运行入口和一项自动化测试或环境验收。

- [ ] **Step 4: 在功能清单中写入盘点口径与证据编号规则**

在 `docs/product/features.md` 中保留现有状态定义，并增加以下稳定编号前缀：

```markdown
`ID` 身份与会话；`RBAC` 用户与权限；`MODEL` 模型治理；`SCAN` 扫描检测；
`TASK` 任务；`AGENT` Agent；`FILE` 附件；`KB` 规则知识库；`REPORT` 报告；
`BRAND` 品牌；`AUDIT` 审计恢复；`OPS` 迁移部署。
```

证据索引采用 `E-<能力编号>`，每项明确写出“运行入口”和“验证来源”。

- [ ] **Step 5: 运行结构检查**

Run:

```powershell
rg -n '`ID` 身份与会话|`OPS` 迁移部署|E-<能力编号>' docs/product/features.md
```

Expected: 三项均匹配。

- [ ] **Step 6: 提交盘点框架**

```powershell
git add docs/product/features.md
git commit -m "docs: establish feature catalog evidence model"
```

### Task 2: 补齐身份、权限、模型和扫描能力

**Files:**
- Modify: `docs/product/features.md`
- Reference: `internal/platform/identity/`
- Reference: `internal/platform/models/`
- Reference: `common/websocket/model_api.go`
- Reference: `common/fingerprints/`
- Reference: `common/runner/runner.go`
- Reference: `data/fingerprints/`
- Reference: `data/vuln/`
- Reference: `data/vuln_en/`

- [ ] **Step 1: 写出缺少前四模块能力行的 RED**

Run:

```powershell
rg -n '^\| ID-|^\| RBAC-|^\| MODEL-|^\| SCAN-' docs/product/features.md
```

Expected: 至少一个模块没有正式能力行，命令不能证明四组均完整。

- [ ] **Step 2: 写入 `ID-01` 至 `ID-02`**

`ID-01` 记录登录、登出和服务端会话；`ID-02` 记录用户改密、首次/重置后强制改密。逐行填写角色、入口、交付状态和证据编号。

- [ ] **Step 3: 写入 `ID-03` 至 `ID-04`**

`ID-03` 记录管理员密码重置及旧会话撤销；`ID-04` 记录会话轮换、过期和安全 Cookie。

- [ ] **Step 4: 写入 `RBAC-01` 至 `RBAC-02`**

`RBAC-01` 记录用户创建和列表；`RBAC-02` 记录用户启停和角色变更。

- [ ] **Step 5: 写入 `RBAC-03` 至 `RBAC-04`**

`RBAC-03` 记录普通用户 owner 隔离、安全审计员全局只读和管理员治理；`RBAC-04` 记录 Cookie/CSRF 校验与伪造 username/role header 拒绝。

- [ ] **Step 6: 写入 `MODEL-01` 至 `MODEL-02`**

`MODEL-01` 记录平台模型加密存储与 Token 掩码；`MODEL-02` 记录 owner/授权解析、禁用和撤销。

- [ ] **Step 7: 写入 `MODEL-03` 至 `MODEL-04`**

`MODEL-03` 记录默认模型按最新授权项选择；`MODEL-04` 记录 YAML 模型只读兼容、ID 防遮蔽和不落旧表。

- [ ] **Step 8: 写入 `SCAN-01` 至 `SCAN-04`**

分别记录 Web/AI 基础设施扫描、MCP 扫描、Agent 工作流扫描、模型红队/提示词安全评测；仅列 `cmd/agent` 和当前服务实际注册的任务类型。

- [ ] **Step 9: 写入 `SCAN-05`**

记录通用组件指纹和版本识别的多路径请求、Header/Body/Icon/Hash 匹配、精确/模糊版本提取，状态为“已实现”。

- [ ] **Step 10: 写入反向代理和 Dify 增强行**

加入以下三条，且不得与通用组件识别合并成虚假的“全部已实现”：

```markdown
| SCAN-06 | 扫描类型与安全检测 | 反向代理后的后端应用识别 | 在代理隐藏 Server 等表层信息时，继续利用可见路径、响应体、Cookie、静态资源和图标等信号识别后端应用 | 普通用户、安全审计员 | CLI/API/Agent | 部分实现 | 原生能力 | E-SCAN-06 |
| SCAN-07 | 扫描类型与安全检测 | 代理层与应用层分层展示 | 分别呈现 Nginx、Traefik 等代理组件和后端应用，避免把代理误报为业务应用 | 普通用户、安全审计员 | 扫描结果/报告 | 待开发 | 原生能力 | E-SCAN-07 |
| SCAN-08 | 扫描类型与安全检测 | Dify 多信号指纹、置信度与版本范围 | 综合 Dify 专用端点和稳定特征给出应用识别、证据、置信度以及精确版本或范围 | 普通用户、安全审计员 | 规则引擎/扫描结果 | 待开发 | 原生能力 | E-SCAN-08 |
```

`E-SCAN-06` 要说明通用多路径、Header/Body/Icon/Hash 和版本提取引擎已经存在，但无 Dify 专用规则与统一分层/置信度模型；`E-SCAN-07`、`E-SCAN-08` 只能引用需求和现状缺口，不得伪造测试证据。

- [ ] **Step 11: 验证前四模块与 Dify 边界**

Run:

```powershell
$catalog = Get-Content -LiteralPath docs/product/features.md -Raw
@('ID-01','ID-04','RBAC-01','RBAC-04','MODEL-01','MODEL-04','SCAN-01','SCAN-05','SCAN-06','SCAN-07','SCAN-08','Dify','Nginx','Traefik') |
  ForEach-Object { if (-not $catalog.Contains($_)) { throw "缺少功能清单内容: $_" } }
if ($catalog -notmatch '(?m)^\| SCAN-05 .*\| 已实现 \|') { throw 'SCAN-05 状态错误' }
if ($catalog -notmatch '(?m)^\| SCAN-06 .*\| 部分实现 \|') { throw 'SCAN-06 状态错误' }
if ($catalog -notmatch '(?m)^\| SCAN-07 .*\| 待开发 \|') { throw '代理/应用分层状态错误' }
if ($catalog -notmatch '(?m)^\| SCAN-08 .*\| 待开发 \|') { throw 'Dify 专用增强状态错误' }
```

Expected: exit 0。

- [ ] **Step 12: 提交前四模块**

```powershell
git add docs/product/features.md
git commit -m "docs: catalog identity models and scanning capabilities"
```

### Task 3: 补齐任务、Agent、附件和知识库能力

**Files:**
- Modify: `docs/product/features.md`
- Reference: `internal/platform/tasks/`
- Reference: `common/websocket/task_manager.go`
- Reference: `common/websocket/agent.go`
- Reference: `common/agent/`
- Reference: `internal/platform/knowledge/`
- Reference: `data/`

- [ ] **Step 1: 写出中间四模块缺失的 RED**

Run:

```powershell
rg -n '^\| TASK-|^\| AGENT-|^\| FILE-|^\| KB-' docs/product/features.md
```

Expected: 至少一个模块没有正式能力行。

- [ ] **Step 2: 写入 `TASK-01` 至 `TASK-02`**

`TASK-01` 记录幂等创建、稳定任务 ID 和 owner；`TASK-02` 记录 owner/全局列表、详情、状态和结果。

- [ ] **Step 3: 写入 `TASK-03` 至 `TASK-04`**

`TASK-03` 记录取消、CAS 状态收敛、分发总预算和未知 ACK；`TASK-04` 记录旧浏览器任务/SSE 入口退役。

- [ ] **Step 4: 写入 `AGENT-01` 至 `AGENT-02`**

`AGENT-01` 记录内部 Token、空值 fail-closed 和握手；`AGENT-02` 记录重复 Agent ID 拒绝和连接指针安全清理。

- [ ] **Step 5: 写入 `AGENT-03` 至 `AGENT-04`**

`AGENT-03` 记录任务分配、事件归属和结果绑定；`AGENT-04` 记录断连失败、持久原因和已分配任务防重复下发。

- [ ] **Step 6: 写入 `FILE-01` 至 `FILE-03`**

`FILE-01` 记录完整上传、opaque ID 与 owner；`FILE-02` 记录分片、累计大小与合并校验；`FILE-03` 记录私有下载和任务附件绑定。

- [ ] **Step 7: 写入 `FILE-04` 至 `FILE-06`**

`FILE-04` 记录内部 Agent 上传/下载；`FILE-05` 记录旧公开图片和浏览器上传入口退役；`FILE-06` 单独记录审计员/管理员跨 owner 原始附件权限，按 PRD 当前结论标为“待修”。

- [ ] **Step 8: 写入 `KB-01` 至 `KB-03`**

`KB-01` 记录组件指纹库；`KB-02` 记录中英文漏洞库；`KB-03` 记录评测规则与提示词集合的受控读取/编辑。

- [ ] **Step 9: 写入 `KB-04` 至 `KB-05`**

`KB-04` 记录 MCP、Agent 配置与越狱规则；`KB-05` 记录规则数据版本和同步。只对当前注册且权限受控的入口标“已实现”。

- [ ] **Step 10: 验证中间四模块**

Run:

```powershell
$catalog = Get-Content -LiteralPath docs/product/features.md -Raw
@('TASK-01','TASK-04','AGENT-01','AGENT-04','FILE-01','FILE-06','KB-01','KB-05','幂等','opaque','分片','知识库','退役边界','待修') |
  ForEach-Object { if (-not $catalog.Contains($_)) { throw "缺少功能清单内容: $_" } }
```

Expected: exit 0。

- [ ] **Step 11: 提交中间四模块**

```powershell
git add docs/product/features.md
git commit -m "docs: catalog tasks agents attachments and knowledge"
```

### Task 4: 补齐报告、品牌、审计和部署能力

**Files:**
- Modify: `docs/product/features.md`
- Reference: `internal/platform/reports/`
- Reference: `internal/platform/brand/`
- Reference: `internal/platform/audit/`
- Reference: `pkg/database/migrate.go`
- Reference: `pkg/database/runtime_schema.go`
- Reference: `deploy/compose/`
- Reference: `.github/workflows/`

- [ ] **Step 1: 写出后四模块缺失的 RED**

Run:

```powershell
rg -n '^\| REPORT-|^\| BRAND-|^\| AUDIT-|^\| OPS-' docs/product/features.md
```

Expected: 至少一个模块没有正式能力行。

- [ ] **Step 2: 写入 `REPORT-01` 至 `REPORT-03`**

`REPORT-01` 记录成功任务不可变快照；`REPORT-02` 记录四类结果风险映射和技术发现脱敏；`REPORT-03` 记录安全摘要/详情 DTO 与 owner/RBAC。

- [ ] **Step 3: 写入 `REPORT-04` 至 `REPORT-06`**

`REPORT-04` 记录 30 日 UTC 趋势；`REPORT-05` 记录中文多页 PDF、嵌入字体、水印和同快照重试；`REPORT-06` 记录管理员补建和持久结果恢复。

- [ ] **Step 4: 写入 `BRAND-01` 至 `BRAND-02`**

`BRAND-01` 记录产品名、主色、PNG/JPEG Logo、水印和图片校验；`BRAND-02` 记录历史快照品牌冻结与管理员变更审计。

- [ ] **Step 5: 写入 `AUDIT-01` 至 `AUDIT-02`**

`AUDIT-01` 记录 pending/completion/outbox 与失败补偿；`AUDIT-02` 记录敏感字段/自由文本脱敏和日志白名单。

- [ ] **Step 6: 写入 `AUDIT-03` 至 `AUDIT-04`**

`AUDIT-03` 记录任务完成恢复、并发 CAS/lease；`AUDIT-04` 记录服务端权限判定与只读角色边界。

- [ ] **Step 7: 写入 `OPS-01` 至 `OPS-03`**

`OPS-01` 记录版本化迁移与运行时 Schema 只读校验；`OPS-02` 记录 PostgreSQL/Compose、Web 与 Agent 内部 Token 和附件限制；`OPS-03` 记录镜像、Release、字体和第三方许可证随包。

- [ ] **Step 8: 写入 `OPS-04` 至 `OPS-06`**

`OPS-04` 企业控制台前端重构、`OPS-05` OpenAPI 单一生成源、`OPS-06` 正式发布候选，三项均标“待开发”。

- [ ] **Step 9: 验证后四模块**

Run:

```powershell
$catalog = Get-Content -LiteralPath docs/product/features.md -Raw
@('REPORT-01','REPORT-06','BRAND-01','BRAND-02','AUDIT-01','AUDIT-04','OPS-01','OPS-06','不可变快照','30 日','PDF','outbox','只读校验','待开发') |
  ForEach-Object { if (-not $catalog.Contains($_)) { throw "缺少功能清单内容: $_" } }
```

Expected: exit 0。

- [ ] **Step 10: 提交后四模块**

```powershell
git add docs/product/features.md
git commit -m "docs: catalog reports governance and operations"
```

### Task 5: 完成产品总览、典型流程和证据索引

**Files:**
- Modify: `docs/product/features.md`

- [ ] **Step 1: 写出总览章节缺失的 RED**

Run:

```powershell
rg -n '^## 产品能力总览$|^## 角色能力总览$|^## 典型使用流程$|^## 兼容与退役边界$|^## 尚未交付$|^## 证据索引$' docs/product/features.md
```

Expected: 一个或多个正式章节缺失。

- [ ] **Step 2: 写产品能力总览和角色能力总览**

用非技术语言说明平台从“配置治理—发起扫描—Agent 执行—结果收敛—报告与审计”的闭环；角色表必须明确普通用户、安全审计员、系统管理员和运维交付人员的边界，尤其是原始附件访问限制。

- [ ] **Step 3: 写典型使用流程**

至少包含：管理员初始化与模型治理、用户提交含附件任务、Agent 执行并回传、平台生成报告、审计员查看全局任务/报告、管理员恢复或补建。

- [ ] **Step 4: 写兼容与退役边界**

明确 YAML 模型只读兼容、旧模型 API 兼容门面、旧浏览器任务/SSE/上传入口 410、历史日文 API 归档，以及内部 Agent 路由不属于浏览器入口。

- [ ] **Step 5: 写尚未交付清单**

至少列出：企业控制台前端重构、完整 OpenAPI 单一生成源、正式发布候选、Dify 专用多信号指纹和代理/应用分层、已确认附件权限修复项。每项写清价值和完成条件。

- [ ] **Step 6: 填写身份、权限、模型和扫描证据索引**

为 `ID-*`、`RBAC-*`、`MODEL-*`、`SCAN-*` 各能力写单行证据项；每行同时包含“运行入口——”和“验证来源——”。

- [ ] **Step 7: 填写任务、Agent、附件和知识库证据索引**

为 `TASK-*`、`AGENT-*`、`FILE-*`、`KB-*` 各能力写单行证据项；待修/待开发项明确写“现状限制——”，不能伪造运行验证。

- [ ] **Step 8: 填写报告、品牌、审计和部署证据索引**

为 `REPORT-*`、`BRAND-*`、`AUDIT-*`、`OPS-*` 各能力写单行证据项。示例：

每个主表证据编号至少映射：

```markdown
- **E-SCAN-06**：运行入口——通用 Web 扫描的多路径与 Header/Body/Icon/Hash 规则；验证来源——指纹解析和 Runner 自动化测试。现状限制——尚无 Dify 专用规则、代理/应用分层与统一置信度输出。
```

不得在证据索引中复制 Token、真实地址、数据库字段值或原始扫描结果。

- [ ] **Step 9: 校验所有能力行都能追溯证据索引**

Run:

```powershell
$text = Get-Content -LiteralPath docs/product/features.md -Raw
$ids = [regex]::Matches($text, '^\| ([A-Z]+-[0-9]+) \|', 'Multiline') | ForEach-Object { $_.Groups[1].Value }
$parts = [regex]::Split($text, '(?m)^## 证据索引\s*$')
if ($parts.Count -ne 2) { throw '证据索引章节缺失或重复' }
$evidence = $parts[1]
$missing = foreach ($id in $ids) {
    $pattern = '(?m)^- \*\*E-' + [regex]::Escape($id) + '\*\*：.*运行入口——.*验证来源——'
    if ($evidence -notmatch $pattern) { $id }
}
if ($missing) { throw "缺少完整证据索引: $($missing -join ', ')" }
if (($ids | Sort-Object -Unique).Count -ne $ids.Count) { throw '存在重复功能编号' }
```

Expected: exit 0。

- [ ] **Step 10: 校验 12 个模块都进入正式矩阵**

Run:

```powershell
$matrixParts = [regex]::Split((Get-Content -LiteralPath docs/product/features.md -Raw), '(?m)^## 证据索引\s*$')
$matrix = $matrixParts[0]
@('身份、会话与密码','用户、角色与权限','模型与密钥治理','扫描类型与安全检测','任务管理','Agent 接入与运行','附件管理','规则与知识库','风险报告、趋势与 PDF','品牌配置','审计、恢复与安全防护','数据迁移、部署与运维') |
  ForEach-Object { if ($matrix -notmatch "\| $([regex]::Escape($_)) \|") { throw "矩阵缺少模块: $_" } }
```

Expected: exit 0。

- [ ] **Step 11: 提交总览与证据索引**

```powershell
git add docs/product/features.md
git commit -m "docs: complete platform feature catalog"
```

### Task 6: 接入文档导航和布局门禁

**Files:**
- Modify: `scripts/check-docs-layout.ps1`
- Modify: `docs/README.md`
- Modify: `docs/project/status.md`

- [ ] **Step 1: 先给布局门禁增加功能清单合同**

在 `scripts/check-docs-layout.ps1` 的必需路径中加入：

```powershell
'docs/product/features.md'
```

并增加对 `docs/README.md` 实际 Markdown 链接的精确检查：

```powershell
$docsReadmePath = Join-Path $repositoryRoot 'docs/README.md'
$docsReadme = [System.IO.File]::ReadAllText($docsReadmePath)
if ($docsReadme -notmatch '\]\(product/features\.md\)') {
    $issues.Add('docs/README.md 缺少平台功能清单入口')
}
```

- [ ] **Step 2: 运行门禁并确认 RED**

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1
```

Expected: exit 1，且仅新增的功能清单入口合同报错；功能清单文件本身已存在。

- [ ] **Step 3: 增加人读入口和状态导航**

在 `docs/README.md` 的“项目与产品”增加 `[平台功能清单](product/features.md)`；在 `docs/project/status.md` 的导航或文档成果处链接功能清单。状态页只记录“功能盘点文档已完成”，不得把控制台前端、OpenAPI 单一源或发布候选改成已完成。

- [ ] **Step 4: 运行门禁并确认 GREEN**

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1
```

Expected: 输出 `文档布局检查通过`，exit 0。

- [ ] **Step 5: 提交导航和门禁**

```powershell
git add scripts/check-docs-layout.ps1 docs/README.md docs/project/status.md
git commit -m "docs: publish platform feature catalog entry"
```

### Task 7: Fresh 验证、双重审查与交付

**Files:**
- Verify: `docs/product/features.md`
- Verify: `docs/README.md`
- Verify: `docs/project/status.md`
- Verify: `scripts/check-docs-layout.ps1`

- [ ] **Step 1: 运行文档布局门禁**

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1
```

Expected: `文档布局检查通过`。

- [ ] **Step 2: 验证运行时 API 文档未被破坏**

Run:

```powershell
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test go test ./internal/apidocs -count=1
```

Expected: PASS。

- [ ] **Step 3: 验证 Markdown 本地链接**

Run:

```powershell
docker run --rm -v "${PWD}:/input" -w /input lycheeverse/lychee:0.24.2 --offline --no-progress "docs/**/*.md" "README.md" "readme/*.md"
```

Expected: 0 errors。

- [ ] **Step 4: 验证状态真实性和 Dify 条目**

Run:

```powershell
$catalog = Get-Content -LiteralPath docs/product/features.md -Raw
if ($catalog -match '企业控制台前端重构[^\r\n|]*\| 已实现 \|') { throw '前端重构被误报为已实现' }
if ($catalog -match '完整 OpenAPI 单一[^\r\n|]*\| 已实现 \|') { throw 'OpenAPI 单一源被误报为已实现' }
if ($catalog -notmatch '(?m)^\| SCAN-08 .*Dify.*\| 待开发 \|') { throw 'Dify 专用增强未标为待开发' }
if ($catalog -notmatch '(?m)^\| SCAN-06 .*反向代理后的后端应用识别.*\| 部分实现 \|') { throw '代理后端识别状态不准确' }
```

Expected: exit 0。

- [ ] **Step 5: 扫描敏感信息和旧路径**

Run:

```powershell
rg -n 'sk-[A-Za-z0-9_-]{12,}|Bearer\s+[A-Za-z0-9._-]{16,}|AKIA[0-9A-Z]{16}|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY' docs/product/features.md
rg -n 'api\.md|api_zh\.md|docs/swagger\.(yaml|json)|docs/docs\.go' docs/product/features.md docs/README.md docs/project/status.md
```

Expected: 两条命令均无匹配；示例占位符若确需出现，必须显式排除并记录原因。

- [ ] **Step 6: 检查差异范围与格式**

Run:

```powershell
git status --short
git diff --check
git diff --stat
```

Expected: 只包含计划内 4 个交付文件；已有用户未跟踪文件不纳入暂存；`git diff --check` exit 0。

- [ ] **Step 7: 请求规格与质量双重审查**

规格审查重点：全量能力覆盖、角色边界、状态真实性、Dify/反向代理条目不误报。质量审查重点：主表可读性、证据可追溯性、重复/敏感内容和导航门禁。

- [ ] **Step 8: 修复审查问题并重复 Steps 1–6**

Expected: 无 P0/P1/P2 遗留，所有 fresh 验证保持通过。

- [ ] **Step 9: 提交最终审查修复**

仅在确有修复时执行：

```powershell
git add docs/product/features.md docs/README.md docs/project/status.md scripts/check-docs-layout.ps1
git commit -m "docs: finalize platform feature catalog"
```

- [ ] **Step 10: 汇报交付结果**

汇报文件路径、能力行数量、各状态数量、Dify/反向代理增强状态、验证结果、提交 SHA 和未推送状态；不得把未执行的测试写成通过。
