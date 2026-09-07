# Agent 工作流扫描验收记录

日期：2026-09-05。分支：`codex/agent-workflow-scan`；基线：`ddb045545`。

本次交付已配置 Agent 的动态扫描工作台，复用平台任务、权限、幂等、取消和报告快照。支持范围为单个 HTTP/HTTPS、WebSocket 或 Dify chat/workflow 配置；不包含 Coze、工作流文件静态分析、节点拓扑或原始执行日志页面。

## 已验证的数据链路

隔离 PostgreSQL 与 Go 平台，通过真实 WebSocket 派发到 Windows Go Agent，再由 `uv` 启动 Python 三阶段扫描。目标与 OpenAI 兼容模型接口是本机受控 HTTP 服务，使用合成账号及凭据，没有调用生产服务。

| 用例 | 实际结果 |
| --- | --- |
| 完整扫描、明确零发现 | `succeeded`，报告含 0 项发现，已授权详情提供 `report_id`。 |
| 完整扫描、1 项发现 | `succeeded`，报告含 1 项发现，Python 的 OWASP 类别数组成功进入不可变快照。 |
| 模型接口失败 | `failed`，没有成功报告关联。 |
| 执行过程中取消 | `cancelled`，Go Agent 收到取消，未发布成功报告。 |
| 同一逻辑创建重复提交 | 相同幂等键与载荷返回相同任务 ID。 |
| 执行说明与备注 | 说明实际进入信息搜集、漏洞检测、漏洞复核三个阶段；备注未进入模型请求。 |
| 报告授权 | 所有者和审计员可读；另一普通用户得到 404。审计员不能创建或取消。 |

最终完整验收运行记录了 20 次模型请求、8 次目标请求。审计员创建沿用现有平台合同返回 400，取消返回 403；验收未修改这些既有状态码。

## 自动化验证

| 范围 | 结果与边界 |
| --- | --- |
| 前端全套 | Node 22.18.0、pnpm 10.15.0，41 个文件、600 项通过；后续侧栏和模型选择器调整定向回归 39 项通过，Agent 合同回归 12 项通过。 |
| 前端静态检查与构建 | `pnpm lint`、`pnpm typecheck`、`pnpm build` 通过。依赖已有 sourcemap 和产物体积提示不影响退出码。 |
| Python 基础受控套件 | 流式补充修复前完整运行 80 项通过、2 个需要外部配置的手工演示用例排除；覆盖真实 localhost HTTP、模型流、WebSocket、入口执行、阶段失败与复核解析。 |
| Python 最后补充回归 | SSE/WS 66 项通过（HTTP 24、WebSocket execution 38、原 provider 4）；既有 Dify/HTTP SSE 与两种真实 main 子进程 smoke 共 13 项通过；入口 `--help` 通过。正常带 type/event/status 字段的 JSON 回答保留兼容。 |
| Go 平台与规格 | `internal/platform/...`、`internal/apidocs`、`cmd/cli` 通过；报告 OWASP 兼容修复后，reports/tasks/apidocs 再次通过。 |
| Go 执行器 | Linux 定向回归通过；交叉编译后的 Windows 测试二进制在宿主实际运行通过，覆盖取消和超长日志时杀死子进程树、临时文件、环境凭据、缺失/重复/伪造结果事件。 |
| Provider 静态校验 | 创建前和调度时校验相关定向测试通过，覆盖结构、YAML 歧义、单目标、URL、Dify 明确模式及不支持配置。 |
| 独立审查 | 输入/报告关联、报告快照、前端生命周期和最终流式兼容审查均通过；最终结论为无新增实质阻断。文档补齐 pytest-asyncio 前置依赖。 |

前端全套后增加了 3 项侧栏断言；没有将“600 项全套 + 定向测试”相加冒称为一次完整运行数量。

Python 复现命令（已安装 `agent-scan/requirements.txt`、`pytest` 与 `pytest-asyncio` 的环境）：

```powershell
$env:PYTHONUTF8 = '1'
python -m pytest agent-scan/test_scan_execution.py agent-scan/test_llm_error_handling.py agent-scan/test_websocket_provider.py agent-scan/test_websocket_execution.py agent-scan/tools/test_dispatcher.py -k 'not test_call_tool_dialogue and not test_call_run_task' -q --tb=short -p no:cacheprovider
```

本次在受控环境运行上述套件；`test_call_tool_dialogue` 与 `test_call_run_task` 是现存的外部配置演示，不计入通过数。命令需允许绑定 localhost。

新增 SSE 事件与扩展 WebSocket 兼容回归可单独执行：

```powershell
python -m pytest agent-scan/test_http_stream_events.py agent-scan/test_websocket_execution.py agent-scan/test_websocket_provider.py -q --tb=short -p no:cacheprovider
python -m pytest agent-scan/test_scan_execution.py -k 'dify or http_sse_error or main_subprocess_smoke' -q --tb=short -p no:cacheprovider
```

## 全量 Go 回归限制

执行了 `go test -p 1 ./...`，全项目未全绿。对下列失败使用干净基线 `ddb045545` 复现，未将其混入本次新增功能通过结论：

- `common/agent/TestLargeDataSend` 依赖占位 WebSocket 地址或测试令牌配置。
- `common/fingerprints/preload/TestRunner_RunFpReqs` 依赖其运行目录下的规则路径。
- `common/utils` 的 Favicon 用例及 `common/utils/chromium` 的截图用例依赖外部网络或浏览器环境，出现既有 panic。
- `internal/mcp/utils` 的 5 项用例依赖固定 `/mcp-server` 文件路径。
- `common/websocket` 的 5 项旧接口断言与既有错误文案、字段及状态码不一致，基线同样失败。

此外，`common/runner` 在 Windows bind mount 上反复读取规则导致超时。将**当前代码编译的同一个测试二进制**在容器本地同版规则数据目录执行后通过；未修改 runner 或规则库来绕过测试。

没有修改 `data/`，因此未运行额外规则 `yamlcheck`；API 文档与 Swagger 三件套增量同步，`internal/apidocs` 全量一致性校验通过，未运行默认 `swag init`。

## 浏览器验收

真实 Chromium 登录隔离平台；浅色与深色主题分别检查 1440、768、320px。六种布局的 `document.scrollWidth` 均等于视口宽度，审计员新建路由显示无权访问，页面无未处理 JavaScript 错误。

浏览器仅请求 Agent 名称目录与平台白名单 DTO；不获取 Agent 配置原文、不请求旧任务原始结果接口或 Agent WebSocket。提交使用 localhost 安全上下文，符合现有幂等键生成所需的 Web Crypto 条件。

真实浏览器提交已通过：选择治理引用、填写说明与备注、收到创建响应、进入 Agent 专属详情、等待成功、打开零发现报告，再打开有漏洞报告。请求载荷、侧栏状态、报告链接及无原始数据请求均有断言。

截图及三个 JSON 验收记录保存在本次证据目录：`C:/Users/52223/.codex/visualizations/2026/09/05/01a0705a-1c8c-7a51-bc16-df4dac957da2/agent-workflow-scan`。`fullstack-result.json` 记录四种终态，`browser-layout-result.json` 记录六种视口/主题组合，`browser-flow-result.json` 记录真实提交和报告跳转。它们是本地验收证据，不是生产扫描结果。

## 兼容性边界

这些测试证明了本仓库受控协议与错误序列的行为，不代表所有外部模型或 Dify 部署版本均兼容。正式接入具体 Agent 前仍需用其实际配置完成连接与扫描验收。未完成、失败、取消、无实际对话或复核格式无效均不能作为“零发现”成功报告。

实现入口见 [开发核查](agent-workflow-scan-development-review.md) 与 [实施计划](../superpowers/plans/2026-09-05-agent-workflow-scan.md)。

本次自行启动的 Windows Agent、受控 HTTP 服务和六个专用 Docker 容器已在验收后关闭。截图、JSON 和 Go 全量/基线日志保留于证据目录；本地测试缓存已排除出 Git 改动。

## 合并前复核

用户随后授权合并到 `develop` 并推送 GitHub。2026-09-05 的合并前复核完成前端 41 个文件、603 项测试，以及 lint、typecheck、build；Go 平台全部包、API 规格、CLI、Agent 执行器和 provider 定向测试通过。

2026-09-07 恢复工作后，确认功能源码与上述验证版本一致，重新完整执行 Python 受控套件（增加 `test_http_stream_events.py`）：131 项通过、2 项外部手工演示排除，耗时 331.08 秒。此记录针对 Agent 功能分支；与后续并行功能的合并结果需要另行验证。
