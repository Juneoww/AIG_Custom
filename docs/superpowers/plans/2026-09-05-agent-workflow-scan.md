# Agent 工作流扫描实施计划

> **For agentic workers:** Use subagent-driven-development for bounded implementation/review tasks in this session. Preserve the existing platform task lifecycle. Steps use checkboxes for verification.

**Goal:** 交付可创建、运行、取消和查看报告的 Agent 动态安全扫描专属工作台。

**Architecture:** 沿用 platform/tasks、TaskManager、AgentTask 和 agent-scan 三阶段扫描。Agent 表单独立，共享 AI 工作台框架、治理模型选择、任务 API 和详情生命周期；使用安全的引用摘要与报告关联。

**Tech Stack:** Go 1.23.2、PostgreSQL 16.4、Python、React/TypeScript、Fluent UI v9、TanStack Query、Vitest。

**Accepted design:** docs/architecture/agent-workflow-scan-development-review.md（用户已同意按推荐动态扫描方案开发）。

**Workspace:** D:/June/E/code/codex/AI_Safe/security-product-main/AIG/.worktrees/agent-workflow-scan；branch codex/agent-workflow-scan；base ddb045545。

## Task 1：执行器与 Python 扫描成功判定

Files: common/agent/agent_task.go、agent.go 与新增执行/日志测试；common/utils/agent_command_unix.go、agent_command_windows.go；agent-scan/main.py、core/base_agent.py、core/agent.py、core/agent_adapter/adapter.py、core/report/review.py、utils/llm.py 等实际执行边界；新增 agent-scan/test_scan_execution.py、test_http_stream_events.py、test_websocket_execution.py。

- [x] 先新增失败用例：执行说明到 Python、错误事件、退出成功但无结果；Python 连续错误/迭代耗尽抛出失败；finish 后最终格式化模型失败、复核文本无效或漏洞块解析不完整不得输出成功结果；正常完成且明确零漏洞仍能成功。
- [x] 验证测试先失败于缺失行为，再实现最小修复。Python 入口接受受控 prompt 文件与环境凭据，Go 不把密钥/说明放在完整命令日志中。
- [x] AgentTask 使用可测试的命令构造/运行边界，传递 content 并拒绝不支持附件；缓冲有效 resultUpdate，只有进程成功退出且无错误事件才发布一次结果，避免先成功后失败。
- [x] 最终复核使用明确完成标记与零发现标记（同步 reviewer 提示词），或等价的结构化完成合同。无标记、截断/无效漏洞块、格式化错误与有效零发现分别测试；普通空字符串不能等价于零漏洞，最终模型失败不得回退历史文本生成报告。
- [x] 平台模式所有主/辅助模型使用选择的 eval_model 配置；保留独立 CLI 的明确配置兼容性。
- [x] 运行 Python 受控扫描用例和入口冒烟；运行 Go Agent 定向回归。记录真实网络与受控测试的区别。

## Task 2：平台输入、治理校验和安全输出

Files: internal/platform/tasks/service.go、dto.go、adapter.go、新增 agent_workflow.go 与测试；common/websocket/task_manager.go、新增 agent_workflow_test.go；internal/platform/reports/service.go/snapshot.go（按实际报告查询能力最小修改）。

- [x] 新增合同测试：新 Agent 任务要求非空 content，拒绝附件；已存在的同幂等键历史任务先比较并返回，不因新增限制被破坏。
- [x] 在任务保存前及调度时校验单个 Agent provider 配置，限制为已验证的 HTTP/HTTPS、WebSocket、Dify chat/workflow；Coze 暂拒绝，禁止把未支持 provider 静默当成成功。
- [x] 新增安全摘要 agent_id、eval_model_id；保留历史敏感字符串、超长/无效引用不出浏览器的回归。
- [x] 新增已就绪 report_id，通过已授权任务对应快照读取，禁止猜测、扫描所有报告或泄露其他任务关联；创建与不存在快照时省略。
- [x] 增量测试通过，再运行任务/报告/治理相关包；保持现有所有者权限、审计、幂等与生命周期不变。

## Task 3：独立 Agent 表单与治理选择

Files: 新增 web/console/src/features/tasks/AgentWorkflowTaskCreatePage.tsx、components/GovernedAgentSelector.tsx、对应测试；components/GovernedModelSelector.tsx；shared/api/types.ts、features/tasks/api.ts。

- [x] 用 Vitest 先验证 Agent 目录仅请求 /knowledge/agent/names、去重、待确认/失败/失效状态，必选模型没有“不使用模型”选项。
- [x] 扩展模型选择器 label/required，AI 现有默认行为不变。
- [x] 三段表单：Agent + 必填执行说明 + 独立备注；必选扫描/裁判模型；确认提交。中文，最多 2,000 码点备注，32 KiB content，无附件 UI。
- [x] 复用 createTaskSubmission、AbortSignal、相同提交重试和取消/卸载处理；修改任意字段或目录选择使旧提交失效，非 available 禁止提交。
- [x] 前端解析 agent_id/eval_model_id/report_id 使用白名单，类型特异解析，拒绝敏感/非法摘要。

## Task 4：专属路由、共享工作台与详情

Files: app/routes.tsx、app/navigation.ts；features/tasks/TaskListPage.tsx、TaskDetailPage.tsx、components/AIInfraWorkbenchHeader.tsx、AIInfraTaskOperationsSummary.tsx、AIInfraTaskTable.tsx、AIInfraWorkbench.styles.ts；新增 taskWorkbenches.ts；路由/页面测试。

- [x] 注册 /tasks/agent-workflow、/new、/:taskId；专属列表固定 agent_scan、侧栏匹配、审计员只读。
- [x] 用小型配置表参数化现有工作台文案/路径，保留旧组件兼容；Agent 专属表单独立，通用表单仍可使用。
- [x] 列表沿用四卡正确统计范围、六列表格、服务端分页与错误状态。
- [x] 详情展示安全 Agent/扫描模型摘要、备注及已就绪报告链接；不读取配置原文、原始输入或旧结果接口；类型不匹配停止轮询和隐藏操作。
- [x] 回归 AI、任务通用页、目录刷新、任务切换与迟到响应。

## Task 5：文档和 API 同步

Files: docs/api/reference.md、reference.en.md；internal/apidocs/swagger.yaml、swagger.json、docs.go、swagger_sync_test.go；docs/architecture/scan-workbench-development.md、agent-workflow-scan-development-review.md。

- [x] 明确新 Agent 创建约束、三阶段模型来源、摘要与 report_id 的可选边界、支持 provider 与附件未支持项。
- [x] 手工同步现有 Swagger 三件套，运行 go test ./internal/apidocs；不得默认 swag init。
- [x] 不新增任务表或迁移；只有持久化要求发生变化时才重新审视。

## Task 6：完整验证与交付

- [x] 使用独立 Compose 项目 aig-agent-workflow-check 测试 PostgreSQL；共享表清理测试串行运行，提供 AIG_TEST_CLI_BINARY。
- [x] 固定 Node 容器运行 pnpm lint、typecheck、test:run、build。Go 运行相关包、再执行 go test -p 1 ./...，记录既有失败，不以局部通过代替全项目。
- [x] 启动隔离测试平台和可控 Agent/模型服务；验证有漏洞、零漏洞、失败、取消、幂等和真实 HTTP 数据链路，不调用生产凭据。
- [x] 浏览器验收浅/深色 1440/768/320px，保存截图证据，修复本次造成的溢出/权限/交互问题。
- [x] 按技能完成独立方案符合性与代码质量审查，修复实质问题，git diff --check。
- [x] 保留隔离分支与可审阅改动，列明验证证据和 provider 实测边界；不擅自合并、推送或部署生产环境。

最终验收与完整 Go 回归限制见 [Agent 验收记录](../../architecture/agent-workflow-scan-verification.md)。实施与独立审查完成，改动保留在本隔离分支供审阅。
