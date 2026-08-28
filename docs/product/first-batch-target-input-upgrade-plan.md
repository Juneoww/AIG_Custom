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
