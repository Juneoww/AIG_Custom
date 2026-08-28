# Web 扫描目标表达式（第一批改造） Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 AI 基础设施 / Web 扫描安全、统一地支持 IPv4 连字符范围与受限通配符表达式，并在控制台、平台 API、Agent 和 CLI 中获得一致结果。

**Architecture:** 在 `common/runner` 建立唯一的目标表达式解析与展开入口，CLI 和 Agent 都只调用该入口，不再分别判断 CIDR 或把不支持的表达式原样传入扫描器。平台任务创建接口在入库、分发前执行同一语义校验；控制台仅做即时提示和预览，服务端仍是最终校验边界。

**Tech Stack:** Go、Gin、现有 `common/runner` 扫描引擎、React 19、TypeScript、Vitest、Go testing。

---

## 需求与边界

### 本批支持的输入

| 输入类型 | 示例 | 语义 |
| --- | --- | --- |
| 单个 URL、域名或 IPv4 | `https://ai.example.com`、`192.168.10.2` | 保持当前行为 |
| IPv4 CIDR | `192.168.10.0/24` | 展开为其中的 IPv4 地址 |
| IPv4 连字符范围 | `192.168.10.2-192.168.10.10` | 展开闭区间内的每一个 IPv4 地址 |
| IPv4 末尾通配符 | `22.2.*.*`、`22.2.10.*` | 分别等价于 `22.2.0.0/16`、`22.2.10.0/24` |

### 本批不支持的输入

- IPv6 CIDR、IPv6 范围和 IPv6 通配符；
- 部分八位组通配符，例如 `22.*.10.*`；
- 非法混写、空格分隔多个目标、端口范围与主机名范围；
- 包含反斜杠的表达式及波浪号范围，例如 `104.147.75.1\~104.147.75.10`。范围必须使用标准连字符 `-`。

### 安全与容量口径

1. 解析仅改变目标表达式，不绕过现有认证、授权、附件或模型治理边界。
2. 一个扫描任务中来自命令行参数、目标文件、任务文本和附件的全部表达式，合并、稳定去重后的展开目标超过 **65,536** 个时必须被拒绝；错误信息应说明范围过大，并建议缩小网段或拆分任务。该上限允许 `22.2.*.*`（即 `/16`）直接展开，但会显著增加扫描耗时和网络压力。
3. 不排除 CIDR 的网络地址和广播地址，以维持当前 CIDR 处理语义；连字符范围按用户指定的闭区间精确展开。
4. 同一任务内保持输入出现顺序并去重；仅允许扫描明确获授权的目标。
5. AI 基础设施扫描的附件一律按 UTF-8 纯文本目标清单处理；每个参与解析的附件最大 1 MiB，超出、不可读或非 UTF-8 时必须在创建任务时拒绝。
6. 无效输入必须在创建任务时返回 400，不得写入或分发为一个必然失败的任务。

## 文件结构

| 文件 | 责任 |
| --- | --- |
| `common/runner/ipnet.go` | 替换只识别 CIDR 的分支，提供 IPv4 表达式批量解析、展开、稳定去重和总容量错误。 |
| `common/runner/ipnet_test.go` | 覆盖表达式的单元测试与容量边界。 |
| `common/runner/runner.go` | 将 CLI 的目标和目标文件统一接入可返回错误的解析入口。 |
| `common/runner/runner_test.go` | 验证 Runner 接收范围、通配符和非法输入时的行为。 |
| `common/agent/tasks.go` | 在端口发现前调用共享解析器，使展开后的每个 IPv4 都进入 AI 服务端口发现和 Web 扫描流程。 |
| `common/agent/tasks_test.go` | 覆盖 Agent 任务的多行输入、范围展开和非法表达式失败。 |
| `internal/platform/tasks/service.go` | 在 `ai_infra_scan` 任务持久化前，安全读取已就绪附件、合并正文与附件目标，并执行服务端目标校验和 65,536 上限。 |
| `internal/platform/tasks/service_test.go` | 覆盖 API 服务层拒绝非法/超量目标且不创建任务。 |
| `web/console/src/features/tasks/TaskCreatePage.tsx` | 仅在 AI 基础设施扫描时显示目标格式说明、合法性提示和展开数量预览。 |
| `web/console/src/features/tasks/targetExpressionPreview.ts` | 控制台用的纯 TypeScript 预览器；实现与 Go 相同的受限语法和 65,536 总量规则，但不作为服务端信任边界。 |
| `web/console/src/features/tasks/TaskPages.test.tsx` | 覆盖控制台提示、预览及服务端错误的可见反馈。 |
| `cmd/cli/cmd/scan.go` | 更新 `--target`、`--file` 的帮助文本，说明支持范围和通配符。 |
| `docs/product/core-risk-discovery.md` | 将当前“不支持”描述更新为第一批改造后的准确输入格式，并链接本计划。 |

### Task 1: 统一 IPv4 目标表达式解析器

**Files:**
- Modify: `common/runner/ipnet.go:29-72`
- Modify: `common/runner/ipnet_test.go:29-122`

- [ ] **Step 1: 写出失败的解析器测试**

为 `192.168.10.2-192.168.10.10` 断言返回 9 个按序 IPv4。增加用户提供的以下 7 行连字符范围粘贴夹具，断言总计得到 100 个目标：`104.147.75.1-104.147.75.10`、`104.147.75.12-104.147.75.13`、`104.147.75.15-104.147.75.30`、`104.147.75.32-104.147.75.34`、`104.147.75.37-104.147.75.39`、`104.147.75.41-104.147.75.102`、`104.147.75.104-104.147.75.107`。为 `22.2.*.*` 和 `22.2.10.*` 断言其结果与相应 CIDR 相同。再增加批量输入测试：两个单独展开不超过上限、合并去重后超过 65,536 的表达式必须失败。最后增加以下失败测试：起始 IP 大于结束 IP、跨越 IPv4 地址空间的无效范围、`22.*.10.*`、`104.147.75.1\~104.147.75.10`。

Run: `go test ./common/runner -run 'Test(Parse|Expand|Targets)_' -count=1`

Expected: FAIL，范围和通配符尚未被识别。

- [ ] **Step 2: 实现最小且唯一的解析 API**

在 `common/runner/ipnet.go` 增加一个接收完整表达式列表、返回 `([]string, error)` 的导出批量解析函数及专用错误类型或哨兵错误。每一项的处理顺序固定为：去除首尾空白、单目标/URL 保持原样、CIDR、连字符范围、末尾通配符；合并所有结果后再稳定去重，并在总数超过 65,536 前停止。范围分隔符只接受单个标准连字符 `-`，包含反斜杠或波浪号的范围一律拒绝。只接受 IPv4；不要以字符串比较 IP 大小，使用 `net/netip` 或等价的数值比较。

保留 `Targets` 作为**遗留、尽力而为**的 channel 兼容包装，但让其调用新批量解析 API；非法输入仅返回空 channel。所有创建任务、CLI 装载、Agent 和其他验证边界不得调用 `Targets`，必须调用可返回错误的批量解析 API。

- [ ] **Step 3: 验证解析器**

Run: `go test ./common/runner -count=1`

Expected: PASS。

- [ ] **Step 4: 提交解析器改动**

```powershell
git add common/runner/ipnet.go common/runner/ipnet_test.go
git commit -m "feat: parse IPv4 range and wildcard targets"
```

### Task 2: 将 CLI 与扫描引擎接入统一解析器

**Files:**
- Modify: `common/runner/runner.go:173-236`
- Modify: `common/runner/runner_test.go`
- Modify: `cmd/cli/cmd/scan.go:96-104`

- [ ] **Step 1: 写出失败的 Runner 测试**

创建包含 CIDR、连字符范围和通配符的目标集合，断言 `Runner` 只接收去重后的具体地址；针对非法表达式和超量表达式断言启动前返回解析错误，而非静默执行。

Run: `go test ./common/runner -run 'TestRunner.*Target' -count=1`

Expected: FAIL，现有 `processTargetList` 无法返回错误。

- [ ] **Step 2: 让目标装载过程传播错误**

先收集命令行数组、目标文件和其他现有来源的全部原始表达式，再用一次 Task 1 的批量解析 API 生成最终目标；`processTargetList` 改为返回错误。不能按来源分别展开，否则会绕过总数上限。更新 `--target` 和 `--file` 帮助文本，给出 `192.168.10.2-192.168.10.10`、`104.147.75.1-104.147.75.10` 与 `22.2.10.*` 示例，并提醒仅限授权目标。

- [ ] **Step 3: 验证 CLI 与 Runner 行为**

Run: `go test ./common/runner -count=1`

Expected: PASS。

- [ ] **Step 4: 提交 Runner 改动**

```powershell
git add common/runner/runner.go common/runner/runner_test.go cmd/cli/cmd/scan.go
git commit -m "feat: apply target expressions to CLI scans"
```

### Task 3: 在 Agent 与平台任务入口执行同一校验

**Files:**
- Modify: `common/agent/tasks.go:108-145,264-346`
- Modify: `common/agent/tasks_test.go`
- Modify: `internal/platform/tasks/service.go:116-151`
- Modify: `internal/platform/tasks/service_test.go`

- [ ] **Step 1: 写出失败的服务端与 Agent 测试**

在服务层为 `ai_infra_scan` 创建请求：合法范围成功、非法通配符返回 `ErrInvalid`、两条输入合计超量时返回 `ErrInvalid` 且记录引擎未提交。上传一个已就绪目标清单附件，验证其与正文合并后能被校验；再验证附件中的非法通配符、合并后超量、超过 1 MiB 或非 UTF-8 内容都会返回 `ErrInvalid`，并且不创建、不分发任务。为 Agent 断言任务内容和附件中的多行范围会在端口发现前合并展开，并让每个展开的 IPv4 进入既有端口发现/后续扫描目标集合。

Run: `go test ./internal/platform/tasks ./common/agent -run 'Test.*(Target|Range|Wildcard)' -count=1`

Expected: FAIL，当前平台服务只校验长度，Agent 只将原始文本拆成行。

- [ ] **Step 2: 在服务端创建前校验 AI 基础设施目标**

在 `createLocked` 中完成附件所有权及 `ready` 状态校验后、审计 mutation 和持久化前，仅为 `ai_infra_scan` 安全读取每个已就绪附件的最多 1 MiB UTF-8 文本。新增窄接口（例如 `ReadReadyTargetExpressions`），它必须复用受限存储路径和常规文件检查，不能使用下载 HTTP 路径或暴露物理路径。将附件的行按附件 ID 顺序追加到正文的非空行后，用一次共享批量解析 API 校验。任何读取、编码、解析失败或合并后的总数超过 65,536 都返回 `ErrInvalid`；不要修改原始 `Content` 或附件，确保审计保留用户输入。MCP、模型红队和 Agent 扫描不调用此校验。

- [ ] **Step 3: 在 Agent 中复用解析结果**

把 `prepareTargets` 得到的正文行和附件行合并后，用一次共享批量解析器输出具体目标；附件中的每一行也走同一规则。服务端已经在分发前校验附件，Agent 仍必须把解析错误作为防御性失败处理，不能把原始表达式传给 Runner。将展开后的 IPv4 传给 `scanPortsAndPrepareTargets`，保证范围目标能走既有 AI 端口发现逻辑。移除本地重复的表达式判断，不输出原始目标内容到日志。

- [ ] **Step 4: 验证服务与 Agent**

Run: `go test ./internal/platform/tasks ./common/agent -count=1`

Expected: PASS。

- [ ] **Step 5: 提交服务端改动**

```powershell
git add common/agent/tasks.go common/agent/tasks_test.go internal/platform/tasks/service.go internal/platform/tasks/service_test.go
git commit -m "feat: validate expanded infrastructure scan targets"
```

### Task 4: 控制台引导、文档与发布产物

**Files:**
- Modify: `web/console/src/features/tasks/TaskCreatePage.tsx:175-216`
- Create: `web/console/src/features/tasks/targetExpressionPreview.ts`
- Modify: `web/console/src/features/tasks/TaskPages.test.tsx`
- Modify: `docs/product/core-risk-discovery.md`
- Modify: `common/websocket/static/*` (only generated by the console build)

- [ ] **Step 1: 写出失败的控制台测试**

为 `targetExpressionPreview.ts` 分别测试单个目标、CIDR、连字符范围、`22.2.*.*`、稳定去重、用户提供的 7 行连字符夹具得到 100 个目标、合并后 65,537 个目标、非法通配符和无效反斜杠/波浪号形式。随后在页面测试中，断言 `ai_infra_scan` 显示支持格式、最大 65,536 个展开目标的提示，并针对 `192.168.10.2-192.168.10.10` 显示 `9 个目标` 预览。切换至其他扫描类型时，不显示该提示。非法客户端格式应在提交前显示可读提示；服务端返回 400 时仍显示服务端错误，不信任浏览器校验替代服务端校验。

Run: `pnpm --dir web/console test:run -- TaskPages.test.tsx`

Expected: FAIL，当前 UI 只校验输入非空。

- [ ] **Step 2: 实现最小控制台体验**

在 `targetExpressionPreview.ts` 中实现纯前端、无网络请求的同语义预览器：逐行处理、合并稳定去重，并按 65,536 总量失败。它只负责帮助用户，不修改提交的 `content`。在 `TaskCreatePage.tsx` 显示的示例必须是 `192.168.10.2-192.168.10.10`、`104.147.75.1-104.147.75.10` 与 `22.2.10.*`，并明确 `104.147.75.1\~104.147.75.10` 无效。提交时根据预览阻止明显非法输入；后端仍进行完整校验。

- [ ] **Step 3: 更新产品说明与构建离线前端**

将 `docs/product/core-risk-discovery.md` 的目标格式段落改为“已支持”，加入标准通配符和范围示例，并链接本计划。不要手工编辑 `common/websocket/static`；在控制台通过类型检查、测试和构建后，由构建输出更新该嵌入式静态产物。

Run: `pnpm --dir web/console lint; pnpm --dir web/console typecheck; pnpm --dir web/console test:run -- TaskPages.test.tsx; pnpm --dir web/console build`

Expected: 全部 PASS，生成静态文件。

- [ ] **Step 4: 做跨层回归验证**

Run: `go test ./common/runner ./common/agent ./internal/platform/tasks -count=1`

Expected: PASS。

Run: `go test ./internal/apidocs -count=1`

Expected: PASS；本批未修改 API 请求体结构或 Swagger，但用于确认平台构建未受影响。

- [ ] **Step 5: 提交 UI、文档和构建产物**

```powershell
git add web/console/src/features/tasks/TaskCreatePage.tsx web/console/src/features/tasks/targetExpressionPreview.ts web/console/src/features/tasks/TaskPages.test.tsx docs/product/core-risk-discovery.md common/websocket/static
git commit -m "feat: guide expanded infrastructure scan targets"
```

## 完成定义

- 控制台、平台 API、Agent 和 CLI 对同一合法输入（含已就绪目标清单附件）得到相同的 IPv4 目标集合。
- `192.168.10.2-192.168.10.10` 与用户提供的 7 行连字符范围被准确展开；后者合计得到 100 个目标，`22.2.10.*` 被准确展开为 256 个目标。
- 解析或附件读取失败、目标超过 65,536 时，平台不创建、不分发任务；CLI 在实际网络请求前失败并报告原因。
- Go、前端的定向测试与前述回归命令全部通过，嵌入式控制台静态产物由构建生成。

---

## 已确认的下一批改造：平台端口扫描模式

### 目标

让 **AI 基础设施扫描** 在平台端到端支持两种仅针对裸 IPv4 的端口发现模式：

| 模式值 | 平台名称 | Nmap TCP 端口表达式 | 默认值 |
| --- | --- | --- | --- |
| `fixed_ai` | 固定 AI 端口扫描 | `11434,1337,7000-9000,18789`（共 2,004 个端口号） | 是 |
| `full_tcp` | 全量 TCP 端口扫描 | `1-65535` | 否，必须由用户在平台明确选择 |

“固定 AI 端口扫描”保持现有裸 IP 行为：`11434`、`1337`、`7000–9000`、`18789`。`7000–9000` 为闭区间，共 2,001 个端口；加上另外 3 个端口后总计 2,004 个端口号。

### 已确认设计

1. 平台控制台、创建 API、任务持久化、Agent 和任务详情必须使用同一个 `port_scan_mode` 契约；不能只在 CLI 或 Agent 中支持全量模式。
2. `port_scan_mode` 缺省时必须规范化并持久化为 `fixed_ai`，使任务详情、审计与重试都能显示真实模式；仅接受 `fixed_ai` 与 `full_tcp`。
3. 端口发现仅针对通过现有目标解析器得到的**裸 IPv4**。URL、域名和显式 `IP:port` 保持原有 Web 扫描路径，不因选择全量模式而对它们额外执行 Nmap。
4. `full_tcp` 必须传递 `-p 1-65535`，不新增按全量模式区分的 IP/目标数量上限。此前已建立的通用输入表达式、附件和发现端点保护仍然有效；本批不移除这些跨模式安全边界。
5. 本批只定义 TCP 端口发现模式；不添加 UDP 扫描、服务版本探测、任意自定义端口列表或自动升级为全量扫描。
6. 控制台必须在选择 `full_tcp` 时清楚提示其会显著增加耗时和网络压力，并保留“仅扫描明确获授权目标”的提醒；该提示不是新的数量上限。
7. 任务详情、进度事件、创建审计记录和报告元数据必须能够显示实际选择的模式与端口范围，避免把全量扫描误报为固定端口扫描。报告与审计只能从已规范化并持久化的任务参数派生，不能信任 Agent 自报的模式。

### 文件结构

| 文件 | 责任 |
| --- | --- |
| `common/portscan/mode.go` | 新建无副作用的共享端口扫描模式契约：模式值、默认值、TCP 端口表达式、规范化与错误。 |
| `common/portscan/mode_test.go` | 覆盖默认、固定、全量和非法模式的纯单元测试。 |
| `internal/platform/tasks/service.go` | 在 `ai_infra_scan` 创建前严格校验、规范化并持久化 `port_scan_mode`，将受限模式/端口规格写入创建审计元数据，并在报告桥接处从可信 `Task.Params` 填充报告输入。 |
| `internal/platform/tasks/service_test.go` | 覆盖缺省规范化、非法模式在持久化/派发前拒绝、全量模式透传、审计元数据与幂等重试。 |
| `internal/platform/tasks/dto.go` | 向安全输入摘要投影实际端口扫描模式，供任务详情展示。 |
| `common/agent/tasks.go` | 防御性读取模式；仅为裸 IPv4 调用对应 TCP 端口表达式，并在进度中报告真实命令。 |
| `common/agent/tasks_test.go` | 通过 Nmap seam 覆盖固定/全量端口参数、URL/域名不做端口发现、缺省兼容与既有端点保护。 |
| `web/console/src/shared/api/types.ts` | 为任务创建请求和详情输入摘要增加受限端口扫描模式类型。 |
| `web/console/src/features/tasks/api.ts` | 在任务详情响应白名单解析中接受且仅接受两个模式值，拒绝任意原始字符串。 |
| `web/console/src/features/tasks/TaskCreatePage.tsx` | 仅在 AI 基础设施任务显示模式选择、默认值和全量 TCP 风险提示，并提交 `port_scan_mode`。 |
| `web/console/src/features/tasks/TaskDetailPage.tsx` | 显示任务实际采用的端口扫描模式和端口范围。 |
| `web/console/src/features/tasks/TaskPages.test.tsx` | 覆盖控制台默认值、全量模式提交、风险提示、任务详情显示与其他任务类型不受影响。 |
| `internal/platform/reports/entity.go`、`internal/platform/reports/service.go` | 将已规范化的任务模式/端口规格纳入受控报告模型与导出数据流。 |
| `internal/platform/reports/snapshot.go`、`internal/platform/reports/pdf.go`、`internal/platform/reports/handler.go` | 从可信任务参数生成不可变报告快照、PDF 与报告 API 响应；不回显原始 `params`。 |
| `internal/platform/reports/*_test.go` | 覆盖固定/全量报告元数据、快照/PDF/API 展示，以及非法或缺失值不被信任。 |
| `web/console/src/features/reports/api.ts`、`web/console/src/features/reports/ReportDetailPage.tsx` | 解析受限报告端口元数据并在报告详情显示模式与范围。 |
| `web/console/src/features/reports/api.test.ts`、`web/console/src/features/reports/ReportPages.test.tsx` | 覆盖报告 API 白名单解析与前端展示。 |
| `docs/api/reference.md`、`docs/api/reference.en.md` | 记录 `ai_infra_scan.params.port_scan_mode` 的枚举、默认值和裸 IP 适用范围。 |
| `internal/apidocs/swagger.yaml`、`internal/apidocs/swagger.json`、`internal/apidocs/docs.go` | 同步 API 运行时三件套；不得直接运行默认 `swag init` 覆盖现有文件。 |
| `docs/product/core-risk-discovery.md` | 在实现落地后更新用户可见说明，区分固定 AI 端口与全量 TCP 扫描。 |
| `common/websocket/static/*` | 仅由通过检查的控制台构建脚本生成，绝不手工编辑。 |

### Task 5: 建立共享端口扫描模式契约与平台参数规范化

**Files:**
- Create: `common/portscan/mode.go`
- Create: `common/portscan/mode_test.go`
- Modify: `internal/platform/tasks/service.go:320-360`
- Modify: `internal/platform/tasks/service_test.go`
- Modify: `internal/platform/tasks/dto.go:29-35,103-130`

- [ ] **Step 1: 写出失败的共享契约与平台服务测试**

为共享契约断言：空字符串规范化为 `fixed_ai`；`fixed_ai` 映射 `11434,1337,7000-9000,18789`；`full_tcp` 映射 `1-65535`；其他值返回错误。为平台服务断言：

- 省略 `port_scan_mode` 的 `ai_infra_scan` 成功创建，持久化的 `params` 中为 `fixed_ai`；
- `full_tcp` 成功创建并保留该值；
- `"full"`、大小写变体、数组、数字和额外 JSON 字段在审计、持久化和分发前返回 `ErrInvalid`；
- 使用相同幂等键重试省略模式的请求仍与已规范化的 `fixed_ai` 任务相等；
- 任务详情的 `input_summary` 显示受限模式值，而非回显任意原始 JSON。
- 成功的 `task.created` 审计事件仅为 AI 基础设施任务带有受控的 `port_scan_mode` 与对应 TCP `port_spec`；非 AI 任务和非法模式不能伪造这些字段。

Run: `go test ./common/portscan ./internal/platform/tasks -run 'Test.*PortScanMode' -count=1`

Expected: FAIL，因为尚无共享契约、严格校验或摘要字段。

- [ ] **Step 2: 实现最小共享模式包**

在 `common/portscan/mode.go` 定义：

```go
type Mode string

const (
    FixedAI Mode = "fixed_ai"
    FullTCP Mode = "full_tcp"
    FixedAIPortSpec = "11434,1337,7000-9000,18789"
    FullTCPPortSpec = "1-65535"
)

func Normalize(value string) (Mode, error) // "" -> FixedAI
func PortSpec(mode Mode) string
```

不要把 Nmap 调用、WebSocket、用户输入或 UI 文案放入该包；它只能表达已批准的两种模式及其 TCP 端口字符串。

- [ ] **Step 3: 在平台创建边界规范化 `port_scan_mode`**

扩展 `infrastructureTaskParams`，在现有严格 JSON 解码之后增加 AI 基础设施专用规范化函数。该函数必须：

1. 保留已允许的 `model_id`、`timeout`；
2. 通过 `portscan.Normalize` 得到规范模式；
3. 将规范化后的 JSON 写回创建流程，使缺省请求也持久化 `"port_scan_mode":"fixed_ai"`；
4. 在附件读取、审计 mutation、持久化和调度之前返回 `ErrInvalid`；
5. 维持非 AI 任务的现有参数行为。

为 `TaskInputSummary` 增加受限字符串字段（例如 `PortScanMode`），只由已规范化的 AI 基础设施参数填充。

构造 `task.created` 审计 metadata 时，保留既有 `task_type`，并且仅当任务为 AI 基础设施扫描时附加 `port_scan_mode` 与由共享契约导出的 `port_spec`。不得将原始 `params`、目标、URL 或 Agent 回调内容写入审计。

- [ ] **Step 4: 运行平台回归测试**

Run: `go test ./common/portscan ./internal/platform/tasks -count=1`

Expected: PASS。

- [ ] **Step 5: 提交共享契约与平台改动**

```powershell
git add common/portscan internal/platform/tasks/service.go internal/platform/tasks/service_test.go internal/platform/tasks/dto.go
git commit -m "feat: add infrastructure port scan modes"
```

### Task 6: 让 Agent 按模式执行裸 IPv4 端口发现

**Files:**
- Modify: `common/agent/tasks.go:83-105,351-410`
- Modify: `common/agent/tasks_test.go`

- [ ] **Step 1: 写出失败的 Agent 模式传播测试**

经由现有 `nmapScan` seam 构造受控测试，断言：

- 缺省参数在裸 IPv4 上调用 `nmapScan(host, "11434,1337,7000-9000,18789")`；
- `full_tcp` 在裸 IPv4 上调用 `nmapScan(host, "1-65535")`；
- 工具/进度事件中的 `-p` 字符串与实际 Nmap 参数相同；
- URL、域名和显式 `IP:port` 不调用 Nmap，无论模式为何；
- Agent 收到非法模式时防御性失败，不将其降级为全量或固定扫描；
- 既有 `maxDiscoveredScanEndpoints` 保护仍生效，且不因全量模式被移除或绕过。

Run: `go test ./common/agent -run 'TestAIInfraScanAgent.*PortScanMode' -count=1`

Expected: FAIL，因为 Agent 当前硬编码固定端口字符串。

- [ ] **Step 2: 在 Agent 中防御性规范化并复用共享端口字符串**

给 `ScanRequest` 增加 `PortScanMode string \`json:"port_scan_mode,omitempty"\``。在 `Execute` 解码后立即调用共享 `portscan.Normalize`；平台已规范化的参数仍必须在 Agent 再验证一次。让 `scanPortsAndPrepareTargets` 接收模式或端口规格，而不是硬编码字符串。

对每个裸 IPv4：

```go
portSpec := portscan.PortSpec(mode)
portScanResult, err := nmapScan(host, portSpec)
```

工具事件必须使用同一个 `portSpec` 构造 `-T4 -p ...` 描述。不得对 URL、域名、显式端口目标或 IPv6 目标发起端口发现；不得在 `full_tcp` 中隐式增加目标数限制。

- [ ] **Step 3: 运行 Agent 定向与包编译测试**

Run: `go test ./common/agent -run 'TestAIInfraScanAgent.*(PortScanMode|PortDiscovery)' -count=1`

Run: `go test ./common/agent -run '^$' -count=1`

Expected: PASS。

- [ ] **Step 4: 提交 Agent 改动**

```powershell
git add common/agent/tasks.go common/agent/tasks_test.go
git commit -m "feat: select infrastructure port scan profile"
```

### Task 7: 在平台控制台选择并展示端口扫描模式

**Files:**
- Modify: `web/console/src/shared/api/types.ts:91-105`
- Modify: `web/console/src/features/tasks/api.ts`
- Modify: `web/console/src/features/tasks/TaskCreatePage.tsx:55-75,138-150,260-282`
- Modify: `web/console/src/features/tasks/TaskDetailPage.tsx:20-110`
- Modify: `web/console/src/features/tasks/TaskPages.test.tsx`
- Modify: `web/console/src/features/tasks/TaskWorkflow.test.tsx`

- [ ] **Step 1: 写出失败的控制台测试**

覆盖以下可观察行为：

- 仅 `ai_infra_scan` 显示“端口扫描模式”选择器，首次渲染值为 `fixed_ai`；
- 固定模式显示固定端口清单及“共 2,004 个端口”；
- 选择 `full_tcp` 后显示“TCP 1–65535”以及耗时/网络压力和授权范围提示，不显示虚构的全量模式目标数量上限；
- 提交固定模式和全量模式时，`createTaskSubmission` 分别收到 `params.port_scan_mode: "fixed_ai"` 与 `"full_tcp"`；
- 切换任务类型后该字段不提交给 MCP、红队或 Agent 扫描；
- 任务详情只显示安全映射后的“固定 AI 端口（11434、1337、7000–9000、18789）”或“全量 TCP（1–65535）”，不回显任意原始参数值。
- 任务详情 API 解析器只接受 `fixed_ai` 与 `full_tcp`；未知 `input_summary.port_scan_mode` 必须作为异常响应拒绝，而不能传给页面。

Run: `pnpm --dir web/console test:run -- TaskPages.test.tsx TaskWorkflow.test.tsx`

Expected: FAIL，因为现有控制台没有端口模式字段或详情投影。

- [ ] **Step 2: 实现受限 UI 状态与请求参数**

在共享 API 类型中使用字面量联合类型：

```ts
export type InfrastructurePortScanMode = 'fixed_ai' | 'full_tcp'
```

在 `TaskCreatePage` 中将状态初始化为 `fixed_ai`，只在 AI 基础设施任务表单显示 `<Select>`。无论用户是否改选，提交时都显式发送规范模式，避免浏览器依赖后端缺省行为。全量提示必须说明它只适用于裸 IP 的 TCP 端口发现，URL 和域名仍走现有 Web 路径。

在 `TaskDetailPage` 使用服务器输入摘要中的受限模式值进行本地映射展示；没有合法值时显示无模式，不根据任意 `params` 字符串推断。同步更新 `features/tasks/api.ts` 的白名单解析器，保证该字段确实从 API 安全传递到页面。

- [ ] **Step 3: 运行前端质量检查并生成嵌入静态资源**

Run: `pwsh ./scripts/build-console.ps1`

Expected: PASS；该脚本必须完成冻结安装、字体准备、lint、类型检查、Vitest、Vite 构建和原子静态替换。

Run: `pwsh ./scripts/check-console-assets.ps1 -StaticDirectory ./common/websocket/static`

Expected: PASS。

- [ ] **Step 4: 提交控制台与生成产物**

```powershell
git add web/console/src/shared/api/types.ts web/console/src/features/tasks/api.ts web/console/src/features/tasks/TaskCreatePage.tsx web/console/src/features/tasks/TaskDetailPage.tsx web/console/src/features/tasks/TaskPages.test.tsx web/console/src/features/tasks/TaskWorkflow.test.tsx common/websocket/static
git commit -m "feat: expose infrastructure port scan modes"
```

### Task 8: 让审计与报告基于可信端口模式

**Files:**
- Modify: `internal/platform/tasks/service.go`
- Modify: `internal/platform/tasks/completed_task_source_test.go`
- Modify: `internal/platform/reports/entity.go`
- Modify: `internal/platform/reports/service.go`
- Modify: `internal/platform/reports/snapshot.go`
- Modify: `internal/platform/reports/pdf.go`
- Modify: `internal/platform/reports/handler.go`
- Modify: `internal/platform/reports/{snapshot_test.go,pdf_test.go,handler_test.go,service_export_test.go}`
- Modify: `web/console/src/features/reports/api.ts`
- Modify: `web/console/src/features/reports/api.test.ts`
- Modify: `web/console/src/features/reports/ReportDetailPage.tsx`
- Modify: `web/console/src/features/reports/ReportPages.test.tsx`

- [ ] **Step 1: 写出失败的报告与审计可见性测试**

在 Task 5 的创建审计测试之外，为报告数据流断言：

- 固定模式的报告快照、报告详情 API、导出 PDF 与控制台报告页显示“固定 AI 端口”及 `11434,1337,7000-9000,18789`；
- 全量模式显示“全量 TCP”及 `1-65535`；
- 任务服务在交给报告服务前从已持久化、已规范化的 AI 基础设施任务 `Params` 派生模式与端口规格，不采用 Agent 结果中同名字段；
- 非 AI 任务、历史无该字段的任务、未知模式或畸形参数不显示伪造模式，并保持现有报告可读；
- 报告前端解析器对未知模式安全拒绝，不将其渲染为 HTML 或原样文本。

Run: `go test ./internal/platform/reports -run 'Test.*PortScanMode' -count=1`

Expected: FAIL，因为当前报告模型、快照与渲染模型没有端口模式字段。

- [ ] **Step 2: 从可信任务参数派生受限报告字段**

扩展 `reports.CompletedTask`、报告实体/渲染模型和快照数据，只携带已验证的模式与由 `portscan.PortSpec` 导出的端口范围。在 `internal/platform/tasks` 的受信任边界（任务完成快照与报告回填的两条路径）从 `task.Params` 解析模式并填入 `CompletedTask`，使 `reports` 包只消费已验证字段，避免 `tasks` 与 `reports` 形成导入环。无合法值的历史任务可按此前实际默认的 `fixed_ai` 兼容展示；无法可信派生时明确省略字段，绝不猜测或使用 Agent 回调。

将安全字段贯穿报告详情 handler、PDF 模板和控制台报告 API/详情页。报告页使用固定的显示映射，不能直接渲染模式字符串或原始任务参数。

- [ ] **Step 3: 运行报告回归测试**

Run: `go test ./internal/platform/reports -count=1`

Run: `pnpm --dir web/console test:run -- src/features/reports/api.test.ts src/features/reports/ReportPages.test.tsx`

Expected: PASS。

- [ ] **Step 4: 提交审计与报告改动**

```powershell
git add internal/platform/tasks/service.go internal/platform/tasks/completed_task_source_test.go internal/platform/reports web/console/src/features/reports
git commit -m "feat: report infrastructure port scan modes"
```

### Task 9: 同步 API 与产品文档

**Files:**
- Modify: `docs/api/reference.md`
- Modify: `docs/api/reference.en.md`
- Modify: `internal/apidocs/swagger.yaml`
- Modify: `internal/apidocs/swagger.json`
- Modify: `internal/apidocs/docs.go`
- Modify: `docs/product/core-risk-discovery.md`

- [ ] **Step 1: 写出 API 文档断言或快照检查**

在已有 API 文档测试/断言处覆盖 `ai_infra_scan.params.port_scan_mode`、任务 `input_summary.port_scan_mode` 与报告详情安全渲染字段：输入枚举只含 `fixed_ai` 和 `full_tcp`，缺省为 `fixed_ai`，仅裸 IPv4 执行端口发现，全量模式为 TCP `1-65535`。不要在 API 文档中承诺 UDP、服务版本识别或不存在的独立数量上限。

- [ ] **Step 2: 更新三套 API 说明**

同步中英文 API 参考和 Swagger 运行时三件套，包括任务创建、任务详情输入摘要和报告详情安全渲染模型；禁止运行默认 `swag init` 覆盖现有定义。更新产品文档，将现有默认固定 2,004 端口与用户明确选择的全量 TCP 模式分开描述，并强调授权范围、耗时与网络压力。

- [ ] **Step 3: 运行文档与 API 包验证**

Run: `go test ./internal/apidocs -count=1`

Expected: PASS。

- [ ] **Step 4: 提交文档改动**

```powershell
git add docs/api/reference.md docs/api/reference.en.md internal/apidocs/swagger.yaml internal/apidocs/swagger.json internal/apidocs/docs.go docs/product/core-risk-discovery.md
git commit -m "docs: describe infrastructure port scan modes"
```

### Task 10: 跨层验证与发布前回归

**Files:**
- Verify only; no hand edits to generated static assets.

- [ ] **Step 1: 运行 Go 回归**

Run: `go test ./common/portscan ./common/runner ./common/agent ./internal/platform/tasks ./internal/platform/reports ./common/websocket ./internal/apidocs -count=1`

Expected: PASS。若完整 Agent 套件仍有未配置外部服务的既有集成测试，必须单独记录其前置条件，不能将本批定向测试误报为全套成功。

- [ ] **Step 2: 运行前端与嵌入资源验证**

Run: `pwsh ./scripts/build-console.ps1`

Run: `pwsh ./scripts/check-console-assets.ps1 -StaticDirectory ./common/websocket/static`

Run: `go test ./common/websocket -run TestEmbeddedConsole -count=1`

Expected: 全部 PASS。

- [ ] **Step 3: 进行受控行为验收**

在隔离测试环境中，以 Nmap mock 或受控测试主机验证：

1. 省略模式时，裸 IPv4 的工具事件和 Nmap 参数为 `-p 11434,1337,7000-9000,18789`；
2. 选择 `full_tcp` 时，裸 IPv4 的工具事件和 Nmap 参数为 `-p 1-65535`；
3. URL、域名、显式 `IP:port` 不触发 Nmap；
4. 任务详情显示与持久化参数一致的模式；
5. 审计记录、报告详情与导出 PDF 从可信任务参数显示相同模式，不信任 Agent 自报字段；
6. 非法模式在任务创建前被拒绝，未写入、未调度；
7. 不对未授权外部目标进行全量端口扫描。

- [ ] **Step 4: 提交发布候选验证记录**

```powershell
git add -A
git commit -m "test: verify infrastructure port scan modes"
```

仅在暂存区确认没有构建缓存、密钥、真实目标、扫描日志或不相关文件时执行该提交。

### 第二批完成定义

- 平台 AI 基础设施任务可明确选择 `fixed_ai` 或 `full_tcp`，缺省请求稳定持久化为 `fixed_ai`。
- 对裸 IPv4，固定模式准确扫描 2,004 个已列端口；全量模式准确传递 TCP `1-65535`。
- 全量模式不增加专属目标数量上限；既有跨模式解析、附件与发现端点安全保护仍有效。
- URL、域名和显式端口目标不触发新增端口发现；无效模式不会写入或派发任务。
- 创建审计、任务详情、报告快照/详情/PDF 均从受信任的持久化参数得到相同模式，Agent 结果不能覆盖该事实。
- 控制台创建页、任务详情、报告页、平台 API、Agent 工具事件、产品/API 文档和嵌入静态资源对模式值与端口范围一致。
- 所有定向与跨层验证通过，且任何外部扫描均仅在明确授权的受控环境中执行。
