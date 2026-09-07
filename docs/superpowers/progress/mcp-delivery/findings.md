# MCP 交付发现

- 源计划为 2026-09-03-mcp-dedicated-workbench-and-task-creation；Task 1–4 已提交，Task 5 的服务网关/运行时部分已提交 9597846f4。
- origin/develop 当前 b5ed7473e，包含 AI、Skills、Agent 专属模块。合并必须保留 taskWorkbenchFor 以及对应路由和 API。
- MCP 早期工作台已存在，但新建仍进入通用 TaskCreatePage，语言与类型选择尚未移除。
- 最终页面沿用产品现有资产、Fluent UI、主题 token 与本地字体；布局稳定、低动效、中高密度，不加入原型 Tweaks 或虚构数据。
- MCP 的连接管理 GET detail 才可显示端点及 Header 名；任务和选项 DTO 不能显示它们。
- 服务运行时签发只在分配 Agent 后发生；MCP 任务 Params 和 Session Params 不含运行时凭据。
