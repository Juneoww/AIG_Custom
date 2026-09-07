# Agent 工作流扫描开发核查与建议

核查日期：2026-09-05。第 1–8 节完整保留实施前的代码核查和建议，描述当时缺口；第 9 节补充本次实现状态与验证边界。历史建议中的“尚未提供”“需新增”等表述不代表当前分支状态，也不是已通过真实扫描验收的声明。

阅读依据是用户指定的 [扫描工作台开发说明](C:/Users/52223/.codex/worktrees/94d2/AIG/docs/architecture/scan-workbench-development.md)。代码核查根目录为 `C:/Users/52223/.codex/worktrees/94d2/AIG`，分支 `develop`，HEAD `ddb045545`。该 HEAD 相对文档代码基线 `883ecbb95` 只修改了文档。当前主工作目录的 `main` 为 `30e02bf2b`，开发前应对齐最新 `develop` 和已有专属工作台改动。

## 1. 推荐范围

第一版围绕“已配置 Agent 的动态安全扫描”完成闭环：选择智能体配置，填写参与执行的检测说明，选择受治理扫描/裁判模型，创建任务，跟踪状态，取消任务，查看受治理报告。

| 路线 | 工作量与结果 | 建议 |
| --- | --- | --- |
| 专属工作台 + 补通现有动态扫描链路 | 复用平台任务服务，修复传参和失败判定，交付真实可执行流程 | 第一版采用 |
| 仅新增列表、表单和路由 | 页面较快可见，但执行说明丢失、报告完整性等问题仍存在 | 可作为中间里程碑，不能作为交付完成 |
| 同时建设工作流文件分析、节点拓扑和实时运行视图 | 需要新的输入解析、结构模型、隔离执行和展示合同 | 独立后续阶段 |

统一产品名为“Agent 工作流扫描”；平台继续使用 `agent_scan`，引擎继续使用 `Agent-Scan`。

现有配置模板包含 HTTP、Dify、Coze，适配器还有其他 provider 分支；这些是代码能力线索，具体协议、应用类型和版本须逐个验收。不能承诺所有 Dify 工作流或任意工作流文件都可扫描。

## 2. 已有基础与确认的缺口

| 部分 | 已有实现 | 本轮应开发或核实的内容 |
| --- | --- | --- |
| 平台任务 | `agent_scan` 类型、精确参数校验、授权、幂等、调度、取消和可信事件协调 | 保持复用，不再建立另一套 Agent 任务服务 |
| 创建合同 | `params.agent_id`、`params.eval_model_id` 必填 | 保持浏览器只提交引用；明确执行说明、备注及附件政策 |
| 治理引用 | 创建时检查引用；分派时按任务所有者解析配置与模型 | Agent 配置当前主要验证存在和可读，需补格式、provider、必填字段的结构校验 |
| Agent 目录 | `/api/v1/knowledge/agent/names` 返回当前用户与公共配置名 | 建独立选择器；不能假设存在模型目录同样的分页、启停、provider、scope 字段 |
| 新建页面 | 通用表单手填 Agent ID 和裁判模型 ID | 替换为治理目录选择；独立接入备注和专属返回路径 |
| 专属工作台 | AI 专属列表、新建、详情及 Fluent UI 组件 | 参数化共享框架，新增 Agent 三条路由；不能只替换 fixedTaskType |
| 安全详情 | Agent 的 `input_summary` 目前仅有 `language` | 设计并增加安全 Agent/模型摘要，保持响应白名单 |
| Go → Python | 已传模型参数、provider 临时文件和语言 | 未传 `request.Content` 到 `--prompt`；未消费附件并映射为 `--repo` |
| Python 引擎 | 信息收集 → 漏洞检测 → 漏洞复核，随后生成结构化报告 | 补阶段完成判定、异常传播、超限及空报告验收 |
| 报告 | Python 输出 `agent-security-report@1`；平台已有 Agent 风险映射和快照路径 | 验证真实结果被正确转换；如需要详情直达报告，补明确、安全的关联字段 |

主要证据：

- [任务参数校验](C:/Users/52223/.codex/worktrees/94d2/AIG/internal/platform/tasks/service.go:362)
- [Agent 目录客户端](C:/Users/52223/.codex/worktrees/94d2/AIG/web/console/src/features/knowledge/api.ts:410)
- [通用新建页的手填字段](C:/Users/52223/.codex/worktrees/94d2/AIG/web/console/src/features/tasks/TaskCreatePage.tsx:464)
- [Agent 详情现有投影](C:/Users/52223/.codex/worktrees/94d2/AIG/internal/platform/tasks/dto.go:161)
- [Go 执行器构造 Python 参数](C:/Users/52223/.codex/worktrees/94d2/AIG/common/agent/agent_task.go:96)
- [Python 实际三阶段入口](C:/Users/52223/.codex/worktrees/94d2/AIG/agent-scan/core/agent.py:355)

## 3. 第一版输入合同

新建页保留三个同时可见的区域：

1. **扫描对象**：必选智能体配置；填写检测说明，例如业务能力、测试账号角色及允许检测的范围。说明进入 `content` 并参与执行，旁边独立提供可选任务备注。
2. **扫描配置**：必选受治理模型；中文界面与中文输出。第一版只有一个 `eval_model_id`，不新增没有执行支持的并发数、端口、节点数或风险类别选择字段。
3. **确认并提交**：执行范围提示、创建/返回操作及失败后的显式重试。

检测说明与备注分别建模：`remark` 不传入扫描引擎、审计元数据或报告快照。沿用 2,000 Unicode 码点限制。

建议请求如下；示例 ID 只是占位值：

```json
{
  "task_type": "agent_scan",
  "content": "检查测试环境客服 Agent 的数据泄漏、工具滥用和权限边界。",
  "attachment_ids": [],
  "country_iso_code": "zh_CN",
  "remark": "客服助手上线前复测",
  "params": {
    "agent_id": "customer-service-test",
    "eval_model_id": "model-example"
  }
}
```

继续使用 `POST /api/v1/platform/tasks` 与既有 `Idempotency-Key`。建议专属页要求非空检测说明，并在服务端同步业务校验；这属于新增约束，目前通用任务服务主要限制内容长度。沿用 32 KiB 内容上限，不按 AI 资产目标解析。

第一版建议暂不开放附件。必须同时明确新 `agent_scan` 请求的服务端附件政策，避免接收文件后静默忽略。若兼容已有附件任务，保留历史记录与幂等确认行为；文件导入另行设计用途、格式、大小、解包安全、隔离目录、清理及 Python 参数传递。

## 4. 治理选择与安全详情

新增 `GovernedAgentSelector`，复用现有 `fetchAgentNames` 读取可引用名称。处理加载、空目录、去重、刷新失败、选择失效与重试；选择器状态中的“可用”表示目录引用已确认，不等于网络连接测试成功。任务页不读取完整 YAML 来拼标签或做连接测试。

Agent 配置按当前用户目录优先、公共目录回退解析，同名项可能被私有配置覆盖。第一版如果沿用配置名作为 `agent_id`，选择与执行必须保持同一解析规则。若需要展示 provider、归属范围、启停状态，应先补服务端安全目录合同；当前接口并没有这些字段。

扩展 `GovernedModelSelector` 的标签和必选模式，复用现有分页、去重、停用过滤及待确认状态，Agent 场景不提供“不使用模型”。浏览器选择后，服务端仍需重新验证引用。

建议新增 Agent 和裁判模型的安全详情摘要。`agent_id`、`eval_model_id` 若进入浏览器，须经过针对引用的安全投影和历史数据校验；不要直接展开 `params`。现有浏览器合同测试专门构造了敏感 `agent_id`，扩展字段不能移除这类保护。对于管理员查看其他用户任务，Agent 名称解析需使用任务所有者上下文，避免映射到管理员自己的同名配置。

显示当前治理目录名称时说明其为当前名称，不宣称是创建时快照。目录不可用时回退为已经过安全投影的引用或“名称暂不可用”。

## 5. 引擎应先完成的工作

### P0：执行说明真正生效

Go 执行器必须把 `request.Content` 传到 Python 的 `--prompt`，并验证它进入三个阶段。`remark` 保持不参与执行。命令构造使用独立参数或受控文件，避免 shell 拼接和敏感全文日志。

### P0：失败与未完成不能生成正常安全结论

[BaseAgent._run](C:/Users/52223/.codex/worktrees/94d2/AIG/agent-scan/core/base_agent.py:160) 在连续模型错误达到阈值时结束循环，达到迭代上限时也只记录警告，随后返回文本。[报告生成](C:/Users/52223/.codex/worktrees/94d2/AIG/agent-scan/core/report/report.py:313) 对没有漏洞条目的输入会输出 `risk_type: safe`。从静态代码可见，阶段完成与失败需要更明确的合同；具体错误序列应通过可控失败用例复现。

推荐第一版让必需阶段失败或未完成进入已有 `failed`，禁止生成成功报告；有效完成且没有发现才能生成零漏洞结果。以后如支持部分完成，应新增覆盖度/未完成原因的结果合同，不借用“安全”表示未检测。不要未经设计新增平台任务状态枚举。

[main.py](C:/Users/52223/.codex/worktrees/94d2/AIG/agent-scan/main.py:117) 的连接失败分支会发出错误事件后普通返回；Go 的错误回调及平台可信事件已有失败映射，所以不能仅凭退出码 0 断言平台会显示成功。仍建议统一 Python 退出结果与执行器状态，并验证错误事件、退出码、缺失 resultUpdate 的组合。

### P0：明确扫描模型的实际用途

当前 `eval_model_id` 解析出的模型用于 Python 主 LLM，贯穿整个扫描，不仅用于最后裁判。另有 [LLMManager](C:/Users/52223/.codex/worktrees/94d2/AIG/agent-scan/utils/llm_manager.py:116) 为 thinking/coding 读取专用配置并优先采用其地址/密钥。

第一版应明确所有扫描模型调用的治理来源，推荐缺少独立治理引用时统一沿用所选模型。若以后区分攻击模型、裁判模型、思考模型，需先扩展平台合同，不能由未显式展示的环境配置改变实际使用模型。

### P1：结果、取消与报告闭环

复用现有 `resultUpdate → 可信状态协调 → 不可变报告快照`。验证有发现、无发现、格式错误、取消竞态和重复结果事件。任何任务详情直达报告的入口都应使用服务端提供的安全关联，例如已就绪的 `report_id`；该字段是待设计建议，当前 TaskDetail 尚未提供，不能在浏览器猜测报告 ID 或逐页搜索报告。

当前主路径没有调用 `run_parallel_detection`，不要根据函数名或四项技能常量宣称已支持四路并行或完整固定覆盖。第一版保持实际三阶段执行，后续再单独改造检测策略和覆盖统计。

## 6. 页面与组件组织

建议新增路由：

- `/tasks/agent-workflow`
- `/tasks/agent-workflow/new`
- `/tasks/agent-workflow/:taskId`

侧栏指向专属列表。列表固定 `agent_scan`；错误类型的详情停止轮询并隐藏内容和取消操作。审计员只读，普通用户仅自己的任务，管理员按服务端权限操作。

提取 AI 已有标题、指标、表格和样式中确实共用的部分，将业务标题、可访问名称和路由作为配置传入；保留 AI 回归。Agent 的表单和摘要独立维护。复用任务 API、幂等提交、取消和轮询逻辑，不把全部类型差异继续塞入一个大表单。

工作台保持标题/创建入口、状态筛选、四张指标卡、六列任务表和分页。只有“匹配任务”来自服务端总数，运行/等待/需关注来自当前页；列表不临时增加目标名、模型名、漏洞数等缺少合同的数据。

第一版详情展示状态、负责人、时间、Agent/模型安全摘要与备注。执行阶段日志、逐节点拓扑或实时消息视图需要新的白名单合同，作为后续功能。

## 7. 建议交付顺序

| 顺序 | 交付项 | 验收门槛 |
| --- | --- | --- |
| 1 | 对齐最新 develop，确定 provider 支持范围及第一版输入合同 | 正式开发从对齐后的基线建立 `codex/agent-workflow-scan`；核对并行工作台改动 |
| 2 | 补执行说明传递、阶段失败判定和模型治理来源 | 使用可控目标与模型确认实际调用参数、失败事件和结果 |
| 3 | 接入 Agent/模型选择器、专属新建页与摘要合同 | 创建真实请求成功；目录变化与权限失败处理正确 |
| 4 | 接入列表、详情、侧栏与报告关联 | 固定类型、统计范围、轮询/取消/幂等复用及跨任务状态隔离正确 |
| 5 | 同步文档，进行浏览器与端到端验收 | 成功、有漏洞、无漏洞、失败、取消均有可解释证据 |

复用任务表、params、remark 与现有报告表时不需要另建 Agent 任务表。仅在新增持久化数据确有必要时追加迁移，并采用当时最新迁移版本。

## 8. 必测项目

- 执行说明准确进入三个阶段；备注、附件 ID、凭据不出现在未经授权的显示与日志中。
- Agent 引用不存在、不可读、格式不正确、provider 不支持；用户私有/公共同名解析一致。
- 裁判模型缺失、停用、目录刷新失败、任务创建前被修改；实际运行没有改用未约定的模型配置。
- 网络连接失败、模型连续错误、阶段迭代超限、缺少有效报告、真正完成但零发现；结果不能混淆。
- 同键重试不重复扫描，修改输入后更新逻辑提交；取消覆盖运行、晚到结果与网络待确认场景。
- 专属类型固定、伪造筛选值、错误类型详情、普通用户隔离、审计员只读；列表与详情字段白名单。
- 浅色/深色，约 1440/768/320px，长 ID、长文件名和键盘操作；保留 AI 页面回归。

前端按固定环境执行 lint、typecheck、test:run、build，并实际浏览器验收。后端覆盖 `internal/platform/tasks`、`common/agent`、`common/websocket`、`internal/platform/reports` 与 `internal/apidocs`；完整 Go 回归按 AGENTS.md 执行，在独立测试数据库上串行运行相关数据库用例。

修改 API 时同步中英文 API 参考和 Swagger 三件套，不运行默认 `swag init` 覆盖规格。修改 Python 后至少入口冒烟，并补上述受控运行场景；`--help` 不能证明真实扫描成功。规则库没有改动时不额外要求 yamlcheck。

本次只核查了文档、代码和版本差异，没有调用真实 Agent、模型或测试数据库，没有宣称端到端已通过。

## 9. 2026-09-05 本次实现状态

本轮在 `codex/agent-workflow-scan` 分支实施，基线 `ddb045545`；工作树为 `D:/June/E/code/codex/AI_Safe/security-product-main/AIG/.worktrees/agent-workflow-scan`。按用户批准的 [实施计划](../superpowers/plans/2026-09-05-agent-workflow-scan.md)，采用已配置 Agent 的动态扫描路线。以下是源码与合同的现状，不替代完整执行或浏览器验收证据。

| 实施前问题 | 当前实现 |
| --- | --- |
| 执行说明丢失 | Go 把 `content` 写入受控临时文件并传 `--prompt-file`，Python 向三个阶段传递；模型密钥使用环境变量，`remark` 不参与扫描。 |
| 新建输入不明确 | 新任务要求非空白、有效 UTF-8、最多 32 KiB 执行说明；`params` 严格为必填 `agent_id/eval_model_id`；拒绝附件。已有相同幂等载荷先返回历史任务，不因新说明/附件限制失败。 |
| Provider 只有引用验证 | 保存前和调度时验证单 provider 的结构、类型、URL 与模式。允许 HTTP/HTTPS、WebSocket、Dify；Dify 必须显式有 `apiKey/apiBaseUrl/extra.dify_type`，应用类型仅 `chat/workflow`。Coze 尚未验证并暂拒绝；未知模式、冲突路由字段、别名和歧义 YAML 被拒绝。 |
| 辅助模型可能绕开选择 | 平台执行器传 `--governed-model`，主/辅助模型统一使用所选 `eval_model_id`。未启用该模式的独立 CLI 保留原配置路径。 |
| 失败或空输出可能变成安全报告 | 必要阶段错误、迭代耗尽、最终格式化失败和不完整复核直接失败。最终 XML 需完成标记，零发现另需明确标记；唯一有效结果由 Go 缓冲，成功退出且无错误/取消才发布。 |
| Agent 摘要仅有语言 | DTO 增加仅 Agent 可有的安全 `agent_id/eval_model_id`；对完整参数结构与引用白名单校验，省略历史内联配置、敏感或非法引用。 |
| 没有报告关联 | 详情 GET 只在 `succeeded` 且现有任务快照获当前 Subject 授权时附 `report_id`；创建响应省略，快照未就绪则无链接。 |
| 没有专属页面 | 增加 `/tasks/agent-workflow`、`/new`、`/:taskId` 和侧栏入口；独立三段表单与 Agent 目录选择器，共享工作台框架、列表/详情生命周期。 |

最终复核完成合同是 `<review_complete>true</review_complete>`；有效零发现还需 `<no_findings>true</no_findings>`。包含发现时逐项验证漏洞字段、级别、OWASP ASI 类别与对话证据；截断块、空文本或解析数量不一致不能输出 `agent-security-report@1` 成功结论。信息收集、漏洞检测和漏洞复核保持顺序执行，本次没有新增四路并行、部分完成状态或覆盖度承诺。

`remark` 沿用已有 v10 列：去首尾空白后最多 2,000 Unicode 码点，只出现在授权任务详情，不进入列表、引擎、审计元数据或报告快照。本次复用 `platform_tasks` 和已有报告表，无新增任务表或数据库迁移；API 指南同时修正此前仍写 v9 的旧说明。

实现索引：

- [平台输入和安全引用](../../internal/platform/tasks/agent_workflow.go)、[DTO](../../internal/platform/tasks/dto.go)、[任务服务](../../internal/platform/tasks/service.go)
- [Provider 验证](../../common/websocket/agent_workflow.go)、[Go 执行器](../../common/agent/agent_task.go)、[Python 入口](../../agent-scan/main.py)、[最终复核合同](../../agent-scan/core/report/review.py)
- [报告授权关联](../../internal/platform/reports/task_reference.go)、[Agent 专属表单](../../web/console/src/features/tasks/AgentWorkflowTaskCreatePage.tsx)、[Agent 目录选择器](../../web/console/src/features/tasks/components/GovernedAgentSelector.tsx)
- [中文 API 参考](../api/reference.md)、[英文 API 参考](../api/reference.en.md)、[Swagger 合同测试](../../internal/apidocs/swagger_sync_test.go)

### 本轮验证记录与尚未确认项

API 合同断言已先观察失败：旧规格缺少 `remark/report_id`、Agent 安全引用和新建执行约束；随后只增量同步任务相关 Swagger 子树，并补双语说明。2026-09-05，`docker exec aig-agent-workflow-go go test ./internal/apidocs -count=1` 返回退出码 0，三件套全量一致性、新旧 API 合同与双语指南校验通过。没有运行默认 `swag init`。

实施验收已完成前端完整套件 600 项、后续受影响定向测试及 lint/typecheck/build；Python 完整受控套件 80 项通过，补充流式异常回归单列记录。相关 Go 包通过，Windows Go 执行器测试在宿主真实运行；完整 Go 回归中的既有失败已与干净基线对照。

真实平台经 Windows Agent 启动 Python，已验证零发现、有漏洞、失败、取消、幂等、说明进入三个阶段及报告授权。浏览器实际创建任务、等待完成并跳转报告通过；浅/深色 1440/768/320px 页面均无横向溢出。命令、截图与验证限制见 [验收记录](agent-workflow-scan-verification.md)。

受控服务的自动化测试不能证明所有真实外部 Agent 或模型版本兼容。Coze、工作流文件静态分析、节点拓扑和原始执行日志展示仍未开放。
