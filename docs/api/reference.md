# AIG Custom Platform API 文档


## 概述

AIG Custom Platform 是基于 Tencent Zhuque Lab AI-Infra-Guard（https://github.com/Tencent/AI-Infra-Guard）构建的独立定制平台，提供了一套完整的API接口，用于AI基础设施扫描、MCP安全扫描、大模型安全体检和模型配置管理。本文档详细介绍了各个API接口的使用方法、参数说明和示例代码。

项目通过可信本地 TLS 终止反向代理运行后，可使用受信任的本地 CA 访问 `https://localhost:8443/docs/index.html` 查看 Swagger 文档。

## 文档目录

### 受保护平台任务
- Cookie session 认证与 Subject 授权
- 平台任务集合和 owner 隔离操作
- opaque 私有附件
- 已退役浏览器任务迁移边界

### 模型管理 API
1. 获取模型列表
2. 获取模型详情
3. 创建模型
4. 更新模型
5. 删除模型
6. YAML配置模型

### 运维说明
- 错误处理
- 迁移与部署说明

## 基础信息

- **Base URL**: 本地示例使用 `https://localhost:8443`。生产部署必须由可信反向代理终止 TLS；认证 Cookie 带 `Secure` 属性，客户端必须校验部署 CA。
- **Content-Type**: `application/json`
- **认证方式**: 浏览器请求使用登录接口签发的 `aig_session` HttpOnly Cookie。`username` 请求头不是认证机制，不能用于建立身份。已认证的状态变更请求还必须携带与 `aig_csrf` Cookie 相同的 `X-CSRF-Token` 请求头。

## 通用响应格式

下列 `{status,message,data}` 形状仅属于保留的旧兼容接口。企业控制台端点使用后续章节明确记录的 DTO 与分页 envelope。

```json
{
  "status": 0,           // 状态码: 0=成功, 1=失败
  "message": "操作成功",  // 响应消息
  "data": {}             // 响应数据
}
```

## 浏览器身份、CSRF 与公开初始化

安全方法是 `GET`、`HEAD` 和 `OPTIONS`，CSRF 中间件不要求它们携带 token。其他所有浏览器方法都必须发送与 `aig_csrf` Cookie 完全一致的 `X-CSRF-Token` 请求头。缺少会话返回 `401`；已认证 Subject 被首次改密门禁、角色策略或 CSRF 策略拒绝时返回 `403`。生产环境的凭据端点要求 HTTPS，并可能返回 `426`；下文列出的基础设施失败统一使用脱敏 `500`。浏览器自报的身份、角色、路径或授权请求头不能替代 Cookie 会话。

- `GET /api/v1/auth/csrf` 是匿名初始化：不创建 session，设置可读的 `aig_csrf` 双提交 Cookie（`Path=/`、`SameSite=Lax`，生产环境启用 `Secure`），响应仅为 `{ "csrf_token": "<csrf-token>" }`。登录与重置确认客户端必须先调用它。
- `POST /api/v1/auth/login` 与 `POST /api/v1/auth/password-resets/confirm` 在处理凭据或一次性重置 token 前，都要求上述 Cookie/请求头配对。登录设置 HttpOnly `aig_session`、轮换 `aig_csrf`，仅返回 `must_change_password`；重置确认返回 `204`，绝不回显 token。
- `GET /api/v1/auth/me` 精确返回 `id`、`username`、`role`、`must_change_password`。匿名调用返回 `401`。它特意位于首次改密门禁之前，使强制改密会话可以恢复 Subject；改密完成前，受保护业务端点仍返回 `403`。
- `GET /api/v1/public/brand` 匿名可用，精确返回 `product_name`、`primary_color`、`logo_data_url`。Logo 值只能为空，或是已验证的 `data:image/png;base64,...` / `data:image/jpeg;base64,...` 表示。
- `GET /api/v1/version` 匿名可用，精确返回 `version`、`commit`、`build_time`。值来自构建注入或固定的 `unknown`；该端点不读取文件，也不发起公网请求。

## 企业控制台集合契约

`GET /api/v1/platform/dashboard` 返回服务端计算且按 Subject 限定的快照。普通用户只聚合本人任务/报告；审计员与管理员读取全局范围。`trend` 恰好 30 个 UTC 自然日桶并以当天结束，`recent_tasks` 与 `attention` 最多各 5 项；每个 attention 项仅有 `report_id`、`task_id`、`task_type`、`completed_at`、`score`、`high`、`medium`、`low`。空态是 `has_data=false`、`security_score=null`、风险计数全零、恰好 30 个补零 UTC 桶和空 attention，绝不表示为 100 分。接口返回 `200`，或 `401`/`403`/脱敏 `500`。

列表端点统一使用包含 `items`、int64 `total`、`page`、`page_size` 的明确 envelope。它们接受 `page=1..1000`（默认 1）与 `page_size=1..100`（默认 20）；大于 100 的正 `page_size` 会截断为 100，格式错误、非正数或大于 1000 的 page 返回 `400`。

| 端点 | Envelope | Scope 与安全 item 契约 |
|---|---|---|
| `GET /api/v1/platform/tasks` | `TaskListResponse` | 普通用户仅本人；审计员/管理员全局。`TaskSummary` 仅含 ID、owner 展示名、规范化类型/状态与时间戳。可选 `status` 与 `task_type` 仅接受文档列出的精确规范值，并在 total 与分页前由服务端筛选；非法值返回固定 `400`。 |
| `GET /api/v1/platform/reports` | `ReportListResponse` | 普通用户仅本人；审计员/管理员全局。item 是不可变安全摘要。 |
| `GET /api/v1/platform/admin/users` | `UserListResponse` | 仅管理员；不含任何凭据材料。 |
| `GET /api/v1/platform/admin/audit-events` | `AuditListResponse` | 仅审计员/管理员；metadata 递归脱敏。 |
| `GET /api/v1/platform/models` | `CatalogPage` | 普通用户看全局和本人私有 platform 行；审计员只读全局行；管理员看全部 platform 行。token 始终为 `********`，`source` 为 `platform` 或 `yaml`，并显式返回 `read_only`。只读 YAML 行与同 ID platform 行发生碰撞时仍分别保留；目录加载失败时失败关闭。 |

`GET /api/v1/platform/tasks/{taskID}` 返回 `TaskDetail`，其 `input_summary` 仅含有界展示元数据。只有 `ai_infra_scan` 的详情可以包含 `model_id`：它是用于恢复当前模型目录标签的 opaque、已持久化/已验证模型引用。模型被删除、禁用或对当前用户不可见时，必须使用安全 ID 回退展示。该字段不是 Token、Base URL、凭据、原始参数对象，也不代表当前可用性；原始 params、嵌套凭据和 model 对象仍不会返回。普通用户只看本人任务，审计员/管理员全局只读；任务不存在或对普通用户不可见时返回 `404`。`GET /api/v1/platform/tasks/{taskID}/result` 已退役：通过认证与首次改密门禁后恒定返回 `410 Gone`，且绝不读取引擎输出。

只有 `agent_scan` 的 `input_summary` 可以额外包含 `agent_id`、`eval_model_id`，两者都是已持久化引用的安全投影，不代表当前目录或网络可用。服务端先验证完整参数合同，再省略非法、超长、类似凭据或路径的历史引用；不会从内联配置中抽取展示值。Agent 名称的解析使用任务所有者上下文；当前目录无法确认时显示安全 ID，不能拿管理员自己的同名配置替换它。详情可带去首尾空白、最多 2,000 Unicode 码点的 `remark`；空白或不合法的历史备注省略，列表不返回备注。

详情 GET 可选返回 `report_id`，条件是任务为 `succeeded`，该任务已有不可变报告快照，且当前 Subject 获准读取该快照。尚未成功或没有快照时省略；浏览器不能根据任务 ID 猜测报告 ID，也不能扫描报告列表寻找关联。创建的 `202` 与 `503.task` 均省略 `report_id`，包括对已完成历史任务的幂等确认；查看报告应使用详情 GET 返回的引用访问 `/api/v1/platform/reports/{reportID}`。

### 任务创建响应的安全加固迁移

**破坏性变更：** `POST /api/v1/platform/tasks` 不再返回旧的内部任务 `View`。成功的 `202` 现在返回 `TaskDetail`，必含 `id`、用于展示的安全 `owner`、规范化 `task_type`、`status`、`created_at`、`updated_at` 和有界 `input_summary`，可带安全 `remark`，始终省略 `report_id`。此变更阻止持久化请求与引擎内部字段越过浏览器边界。

| 旧 `View` 字段 | 新客户端行为 |
|---|---|
| `owner_user_id`、`owner_username` | 已删除；展示 `owner`，且不得用响应值做授权判断。 |
| `content`、`params`、`attachment_ids` | 已删除；展示时只使用 `input_summary` 中获准的元数据；只有 UI 确有需要时才在本地保留已提交表单。 |
| `country_iso_code` | 已删除；存在时使用规范化的 `input_summary.language`。 |
| `engine_session_id`、`dispatch_error`、`dispatch_attempts` | 已删除；只展示公开 `status` 与泛化客户端提示。 |

可信分发失败以 `503` 返回 `{ "error": "task dispatch unavailable", "task": { ...TaskDetail } }`，绝不返回分发诊断。客户端必须同时升级 `202` 解码与 `503` 错误路径，停止读取已删除字段，并把未知任务类型视为 `unknown` 和空 `input_summary`。

附件变更请求要求 CSRF。普通用户只能创建/写入本人 opaque 附件，管理员可治理任意附件，审计员只读。分片必须非空，索引和合并数量不得超过 `ceil(AIG_MAX_UPLOAD_BYTES/AIG_MAX_CHUNK_BYTES)`（默认 `50 MiB/5 MiB=10`）。`DELETE /api/v1/platform/tasks/attachments/{attachmentID}` 可中止并删除 owner 范围内尚未绑定任务的 uploading 或 ready 附件：owner 或管理员成功，其他普通用户得到 `404`，审计员得到 `403`；已绑定任务的附件永不由该端点删除。删除先写入 deleting 墓碑，存储清理成功后才最终删记录，失败会保留可重试墓碑。未绑定 uploading/ready 记录的 TTL 默认 24 小时，可由正 duration 的 `AIG_ATTACHMENT_UPLOAD_TTL` 配置；开始普通或分片上传时，服务端均以最多 100 条的批次回收过期未绑定记录与历史 deleting 墓碑及其私有文件。下载时，owner 成功，其他普通用户得到 `404`，审计员得到 `403`，管理员可跨 owner 下载。管理员跨 owner 打开存储前，服务端必须先持久化脱敏的 `attachment.download_authorized` 授权事件；该事件证明授权而非后续流传输成功，响应也不返回存储位置。

## 任务 API 迁移边界

浏览器任务执行只能使用上文受保护的平台任务 API。原 `/api/v1/app/taskapi*` 和 `/api/v1/app/tasks*` 浏览器路由族仅是历史名称，不是可调用的兼容 API；只有通过正常会话、首次改密与 CSRF 校验后才返回 `410 Gone`（CSRF 适用于变更请求）。它们不能用于创建任务、上传、查询状态、获取结果、流式更新，也不能在连接中断后作为回退。

平台任务创建 JSON body 最大 256 KiB，`content` 最大 32 KiB；支持附件的任务类型最多引用 10 个不重复且不超过 128 字节的 opaque 附件 ID，新 Agent 扫描不支持附件。只接受 canonical `mcp_scan`、`ai_infra_scan`、`model_redteam_report`、`agent_scan`，服务端在私有 Adapter 边界分别映射为真实 Agent Alias。参数采用逐类型白名单：MCP 仅 `model_id`/`thread`；基础设施仅 `model_id`/`timeout`/`port_scan_mode`；模型红队要求 `model_id` 字符串数组和 `eval_model_id`，可带 `dataset.numPrompts/randomSeed/promptColumn` 与 `techniques`；Agent 扫描仅接受必填的 `agent_id` 与 `eval_model_id`。`ai_infra_scan.params.port_scan_mode` 只能精确为 `fixed_ai` 或 `full_tcp`，省略时规范化为 `fixed_ai`；前者对裸 IPv4 发现 `11434,1337,7000-9000,18789`（2,004 个）TCP 端口，后者发现全部 `1-65535` TCP 端口。它不接受自定义端口、UDP 或版本识别选项，且 URL、域名、带端口 IP、IPv6 不触发该端口发现步骤。安全 `TaskDetail.input_summary.port_scan_mode` 仅在可验证时返回上述规范化枚举值，绝不返回原始参数。所有 `model_id`/`eval_model_id` 必须在持久化任务前通过受治理模型解析器验证，`agent_id` 必须解析到该用户或公共只读 Agent 配置；未知或不可见引用固定拒绝且不写入任务。未知字段、嵌套凭据对象、明文模型凭据、旧 model 对象和任务 Alias 均被拒绝。浏览器附件只使用 opaque 附件 ID，并按 owner 隔离。内部 Agent WebSocket 与旧形状制品传输属于独立的 internal-token 边界，不是浏览器 API。

### Agent 工作流扫描合同

`agent_scan` 对已配置的单个 Agent 进行动态安全扫描。新建任务的 `content` 必须是非空白、有效 UTF-8 的执行说明，最多 32 KiB（32,768 字节），会传入信息收集、漏洞检测、漏洞复核三个阶段；这不是工作流文件或代码仓库导入接口。`attachment_ids` 应省略或设为 `[]`，非空附件会在保存前拒绝。可选 `remark` 独立保存，去首尾空白后最多 2,000 Unicode 码点，不进入引擎、审计元数据或报告快照。字节上限与备注码点上限不同。

`params` 只能包含 `agent_id`、`eval_model_id` 两个必填引用。Agent 从 `/api/v1/knowledge/agent/names` 选择，模型从受治理模型目录选择；页面确认目录引用不等于连通性测试通过。新任务保存前与下发时均校验 provider，配置必须只有一个目标。当前允许 HTTP/HTTPS、WebSocket 和 Dify 适配器；Dify 必须显式提供 `config.apiKey`、`config.apiBaseUrl` 和 `config.extra.dify_type`（`chat` 或 `workflow`），不能夹带 `url`、`endpoint`、`method`、`body`、`headers`、`message_template` 等冲突路由字段。服务端拒绝未知模式、多目标、重复键、YAML 别名、歧义隐式标量以及格式或字段类型错误。Coze 尚未验证，当前拒绝；允许这些适配器不代表任意外部产品版本已验收。

同 `Idempotency-Key` 的历史请求先与已持久化载荷比较，再进入仅针对新任务的说明/附件校验；原先存在的空说明或附件任务仍可确认。更改说明、备注、Agent 或模型即是不同载荷，必须使用新的逻辑提交；不能自动重发状态不确定的任务。

平台模式中 `eval_model_id` 是整个扫描的模型来源，主模型和 thinking/coding 辅助模型统一使用该治理配置。只有三个阶段完整完成、最终复核包含 `<review_complete>true</review_complete>` 且漏洞块结构完整时，才可生成 `agent-security-report@1`。零发现还必须显式包含 `<no_findings>true</no_findings>`；空字符串、截断复核、连接失败、模型错误或迭代耗尽均不能当作安全结果。执行器仅在子进程成功、无错误事件且得到一个有效结果后发布报告，失败或取消不发布成功报告。

以下示例 ID 为占位值；请求还需携带有效会话、CSRF 和 `Idempotency-Key`：

```json
{
  "task_type": "agent_scan",
  "content": "检查测试客服 Agent 的数据泄漏、工具滥用和权限边界。",
  "remark": "上线前复测",
  "country_iso_code": "zh_CN",
  "attachment_ids": [],
  "params": {
    "agent_id": "customer-service-test",
    "eval_model_id": "model-example"
  }
}
```

该功能复用现有任务、备注和报告快照数据结构，不新增任务表或数据库迁移。

### 受保护平台边界

浏览器客户端使用安全 Cookie session 认证。匿名请求返回 `401`；已认证但无权限的 Subject 返回 `403`。身份和角色请求头绝不是认证回退。

平台任务使用 `GET /api/v1/platform/tasks`、`POST /api/v1/platform/tasks`，以及按 owner 授权的详情、取消、结果和 opaque 附件操作。创建必须携带 `Idempotency-Key`；任务 owner 始终来自认证 Subject。相同持久化请求载荷的重试直接返回既有任务，不再校验实时治理引用；同一 owner 复用该键但改变载荷时固定返回 `400 invalid task request`。普通用户只能看到本人任务，审计员全局只读，管理员可治理全部任务。网络 ACK 无法可信确认时进入 `dispatch_unknown`，且绝不自动再次提交。取消只有精确 `204` 才能直接视为成功；网络错误、服务端错误或非合同 2xx 都只允许执行一次 GET 状态确认，明确 4xx 直接返回，任何分支都不得自动再次 POST。

独立受治理模型 API 是 `/api/v1/platform/models`。`POST /api/v1/platform/models/{modelID}/rotate-encryption` 只允许管理员对可写全局 platform 模型执行存量 Token 主密钥重加密；私有模型和只读 YAML 模型均不可轮换。已弃用的 `/api/v1/app/models/{modelId}` facade 仅保留模型兼容：集合 DELETE 与嵌套请求体继续使用 `{status,message,data}` envelope 和 HTTP `200` 应用错误约定。响应凭据始终脱敏。YAML 模型 ID 不能遮蔽加密平台行；YAML 加载失败时，在数据库或审计变更前失败关闭。

只有 `aig migrate` 可以执行数据库 DDL。全新或升级后的 PostgreSQL schema 到达 v10；runtime 启动只做校验。迁移可安全处理旧表为空的情况，且不依赖 runtime AutoMigrate。版本 8 新增 `idx_platform_tasks_updated_at` 与 `idx_platform_tasks_owner_updated_at`，分别支持全局和 owner 范围按 `updated_at DESC, id DESC` 稳定读取最近任务；版本 9 将已被历史任务引用的 ready 附件幂等回填为 attached，未绑定 ready 附件保持可回收。已有版本 10 增加 `platform_tasks.remark` 与 `platform_tasks.target_count`；Agent 工作流扫描复用该基线，不新增迁移。

## 知识库兼容治理 API

`/api/v1/knowledge` 保留 `{status,message,data}` 兼容 envelope。指纹、漏洞、评测集、MCP 插件、Prompt 集合和 Agent 配置均允许已完成首次改密的 `user`、`auditor`、`admin` 读取；只有管理员可调用现有写路由，且所有持久化写操作都要求当前 `aig_csrf` Cookie 与 `X-CSRF-Token` 匹配并经过受治理审计。前端必须把非零 `status` 映射为固定安全错误，不得显示服务端 `message`、文件路径或原始诊断。

指纹、漏洞和评测集列表只接受唯一十进制 `page=1..1000` 与 `size=1..100`（默认分别为 1、20）；空值、重复值、非十进制、越界和溢出值固定返回 `400`。漏洞与评测集写入的 `file_content` 最大 1 MiB：服务端先执行 YAML/JSON 与业务校验，再原子保存原始 UTF-8 字节，不重排注释、锚点、未知字段、空白或字段顺序；评测集 `count` 必须与 `data` 长度一致。Agent Prompt 测试成功响应只允许返回最大 256 KiB 的显式 `provider_response.output`；缺失或超限时返回固定成功说明，绝不回退到 Provider `raw`、`message`、路径或错误诊断。

`POST /api/v1/knowledge/agent/connect` 仅限管理员，要求当前 Cookie 会话、首次改密完成和匹配的 CSRF Cookie/Header；请求体只含 `content`。它返回固定的连通性结论，不返回或记录 Provider 原始响应、路径和诊断。`POST /api/v1/knowledge/agent/prompt_test` 使用同一身份与 CSRF 边界。

为保持注释、锚点、空白和字段顺序，控制台原文编辑使用以下只读路由，而不是从列表 DTO 重新序列化：

| 端点 | 精确成功数据 | 安全边界 |
|---|---|---|
| `GET /api/v1/knowledge/fingerprints/{name}/raw` | `data: { "content": "<exact-yaml>" }` | 在固定 `data/fingerprints` 根内按 YAML `info.name` 唯一递归匹配，规则文件最大 1 MiB。 |
| `GET /api/v1/knowledge/vulnerabilities/{id}/raw` | `data: { "content": "<exact-yaml>" }` | 固定中文漏洞根中唯一匹配、规则文件、最大 1 MiB。 |
| `GET /api/v1/knowledge/evaluations/{name}/raw` | `data: { "content": "<exact-json>" }` | 固定 `data/eval` 根、规则文件、最大 1 MiB。 |

三个端点都拒绝空标识、`.`、`..`、斜杠、反斜杠、控制字符、根目录或文件符号链接、根外文件，以及不唯一的指纹或漏洞匹配。无效或不安全解析返回固定 `400`，不存在返回 `404`，超过上限返回 `413`，文件系统故障返回不含路径的固定 `500`。

Prompt 删除的规范路由是 `DELETE /api/v1/knowledge/prompt_collections/{id}`。它仅接受路径中的 opaque ID；旧的无 ID `DELETE /api/v1/knowledge/prompt_collections` 只为兼容已部署调用方保留，并固定返回 `400`，不能推断或批量删除资源。成功仍返回 legacy `200` envelope，资源不存在返回 `404`。

## 不可变报告与品牌 API

以下接口均使用 Cookie 中的认证 Subject，并经过首次改密门禁。普通用户只能读取和导出本人报告；审计员可以全局只读与导出，但不能变更；管理员可以全局读取/导出，并可治理品牌或补建缺失快照。浏览器提交的身份请求头和原始引擎结果一律不可信。

### 报告列表、趋势与详情

- `GET /api/v1/platform/reports?page=1&page_size=20` 返回 `ReportListResponse` 分页安全摘要 envelope。`page` 从 1 开始；`page_size` 默认 20、最大 100。摘要仅包含报告/任务 ID、时间、风险摘要与快照中的产品名。
- `GET /api/v1/platform/reports/trends?days=30` 返回服务端计算、补零且包含当天的 UTC 自然日桶；`days` 范围为 1 到 30。浏览器不得从不完整列表自行推导趋势。
- `GET /api/v1/platform/reports/{reportID}` 返回安全详情，其中 `render` 是任务完成时保存的不可变 RenderModel。

不可变 `report-render-v2` RenderModel 固化风险映射版本、生成/完成时间、任务元数据、产品名/主色/水印、风险评分及评分说明、风险分布、`risk_trend` 中 30 个固定 UTC 日桶、Top 风险、含证据/影响/修复的技术发现、建议、覆盖范围和结论。对于可信的 AI 基础设施任务，它可选地同时包含 `port_scan_mode` 与 `port_spec`，且只允许 `fixed_ai` + `11434,1337,7000-9000,18789` 或 `full_tcp` + `1-65535`；字段缺失、孤立、未知或不匹配时必须整体省略。技术发现仅按四类可信引擎的显式 schema 白名单映射，完成脱敏和严重度排序后最多保留 50 条；提示词、会话、附件、截图、凭据、URL 查询参数和用户绝对路径都不会进入 RenderModel。在线详情与自动分页 PDF 重试只消费同一个模型。列表与详情契约明确分离：绝不暴露原始引擎结果与 Logo 字节，也不暴露存储的渲染载荷、owner ID、文件路径或当前可变品牌记录。列表仅接受 `page=1..1000`、`page_size=1..100`（默认 20）。

读取成功返回 `200`；分页或趋势参数无效返回 `400`；未认证返回 `401`；角色不支持返回 `403`；报告不存在或对普通用户不可见返回 `404`。

### 受审计 PDF 导出

`POST /api/v1/platform/reports/{reportID}/exports/pdf` 必须携带匹配的 `X-CSRF-Token`。成功以 `200 application/pdf` 返回，始终从同一不可变快照渲染，不重新读取引擎或当前品牌。每次导出均使用持久化 pending/completion 审计 outbox，完成事件投递失败可被对账恢复且不会泄露敏感结果。授权失败使用 `401`/`403`，不存在或不可见使用 `404`，渲染或持久化完成失败使用脱敏 `500`。

### 管理员补建

`POST /api/v1/platform/admin/reports/backfill` 接收 `{ "task_id": "..." }`，仅管理员可用且要求 CSRF，成功以 `201` 返回安全不可变详情。它只能为可信的已完成平台任务补建缺失快照，不接受浏览器结果，也不重写已有报告。无效输入返回 `400`，未认证返回 `401`，角色或 CSRF 不足返回 `403`，补建失败返回脱敏 `500`；整个治理操作均被持久化审计。

### 当前品牌

- `GET /api/v1/platform/brand` 对已认证普通用户、审计员和管理员开放，返回当前产品名（最多 128 个 Unicode 字符）、主色、可选 Logo、水印（最多 64 个 Unicode 字符）与更新元数据。
- `PUT /api/v1/platform/brand` 仅管理员可用、要求 CSRF 并记录持久化审计；更新只影响未来快照，历史报告继续保留当时品牌。

主色必须为 `#RRGGBB`。Logo 必须是声明 MIME 与解码格式一致的真实 PNG 或 JPEG，解码后不大于 1 MiB，任一边不超过 4096 像素，总像素不超过 16,777,216；空 Logo 会同时清空 MIME。无效数据返回 `400`，未认证返回 `401`，角色或 CSRF 不足返回 `403`，持久化/审计失败返回脱敏 `500`。

## 模型管理 API

> **已弃用的兼容 API。** 本节端点保留浏览器契约：`/api/v1/app/models` 的 `GET`/`POST`/集合 `DELETE`，以及 `/api/v1/app/models/{modelId}` 的详情 `GET`/`PUT`。请求必须使用登录会话建立的 `Subject`，变更请求还必须通过 CSRF 校验；`username` 请求头会被忽略。POST/PUT 保留嵌套 `model` 对象，DELETE 保留 `{ "model_ids": [...] }`，所有操作使用上述旧 HTTP `200` envelope。列表/详情会合并只读 YAML 模型并保留其 `default` 字符串数组；加密平台模型返回空数组。token 永远是 `********`，YAML 模型不能被修改或被平台行遮蔽；重复 YAML ID 返回 `status: 1`，YAML 加载错误会在数据库和审计写入前失败关闭。非弃用的扁平契约请使用 `/api/v1/platform/models`。

### 所有示例都必须先建立浏览器会话

此兼容 API 不支持匿名访问，生产凭据路由强制 HTTPS。本地示例假设 8443 端口前有可信本地 TLS 终止反向代理、客户端信任其 CA bundle，且服务使用生产 `Secure` Cookie；绝不能关闭证书校验。登录前先 GET `/api/v1/auth/csrf`，在持久 Cookie jar 中保留所有轮换 Cookie，再读取 `/api/v1/auth/me`。`must_change_password=true` 时必须先调用改密接口；改密成功会清除 session，因此客户端随后重新登录。以下每个变更请求都会把当前 `aig_csrf` Cookie 值放入 `X-CSRF-Token`。

```python
import requests

BASE_URL = "https://localhost:8443"
CA_BUNDLE = "<trusted-local-ca.pem>"
session = requests.Session()  # 持久 Cookie jar
session.verify = CA_BUNDLE
bootstrap = session.get(f"{BASE_URL}/api/v1/auth/csrf")
bootstrap.raise_for_status()

def login_with_password(password):
    response = session.post(
        f"{BASE_URL}/api/v1/auth/login",
        json={"username": "<username>", "password": password},
        headers={"X-CSRF-Token": session.cookies.get("aig_csrf")},
    )
    response.raise_for_status()
    return response

login_with_password("<current-password>")
me_response = session.get(f"{BASE_URL}/api/v1/auth/me")
me_response.raise_for_status()
subject = me_response.json()
if subject["must_change_password"]:
    changed = session.post(
        f"{BASE_URL}/api/v1/auth/change-password",
        json={"old_password": "<current-password>", "new_password": "<new-password>"},
        headers={"X-CSRF-Token": session.cookies.get("aig_csrf")},
    )
    changed.raise_for_status()  # 204；此时 aig_session 已清除
    login_with_password("<new-password>")
    me_response = session.get(f"{BASE_URL}/api/v1/auth/me")
    me_response.raise_for_status()
    subject = me_response.json()
csrf_headers = {"X-CSRF-Token": session.cookies.get("aig_csrf")}
```

下面的等价 cURL 流程使用 `jq` 与 `awk`，持久化每次 Cookie 更新，并校验可信本地 CA。只替换尖括号占位符。

```bash
BASE_URL="https://localhost:8443"
CA_BUNDLE="<trusted-local-ca.pem>"
COOKIE_JAR="cookies.txt"
BOOTSTRAP_CSRF="$(curl -fsS --cacert "$CA_BUNDLE" -c "$COOKIE_JAR" "$BASE_URL/api/v1/auth/csrf" | jq -r '.csrf_token')"
curl -fsS --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -c "$COOKIE_JAR" -X POST "$BASE_URL/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $BOOTSTRAP_CSRF" \
  -d '{"username":"<username>","password":"<current-password>"}'
ME_JSON="$(curl -fsS --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" "$BASE_URL/api/v1/auth/me")"
if [ "$(printf '%s' "$ME_JSON" | jq -r '.must_change_password')" = "true" ]; then
  SESSION_CSRF="$(awk '$6 == "aig_csrf" {value=$7} END {print value}' "$COOKIE_JAR")"
  curl -fsS --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -c "$COOKIE_JAR" -X POST "$BASE_URL/api/v1/auth/change-password" \
    -H "Content-Type: application/json" \
    -H "X-CSRF-Token: $SESSION_CSRF" \
    -d '{"old_password":"<current-password>","new_password":"<new-password>"}'
  curl -fsS --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -c "$COOKIE_JAR" -X POST "$BASE_URL/api/v1/auth/login" \
    -H "Content-Type: application/json" \
    -H "X-CSRF-Token: $SESSION_CSRF" \
    -d '{"username":"<username>","password":"<new-password>"}'
  ME_JSON="$(curl -fsS --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" "$BASE_URL/api/v1/auth/me")"
fi
SESSION_CSRF="$(awk '$6 == "aig_csrf" {value=$7} END {print value}' "$COOKIE_JAR")"
```

### 1. 获取模型列表

#### 接口信息
- **URL**: `/api/v1/app/models`
- **方法**: `GET`
- **Content-Type**: `application/json`

#### 响应字段
| 字段名 | 类型 | 说明 |
|--------|------|------|
| model_id | string | 模型ID |
| model | object | 模型配置信息 |
| model.model | string | 模型名称 |
| model.token | string | API密钥（已脱敏显示为********） |
| model.base_url | string | 基础URL |
| model.note | string | 备注信息 |
| model.limit | integer | 请求限制 |
| default | array | YAML 中的任务类型默认值；加密平台模型返回空数组 |

#### Python 示例
```python
import requests

def get_model_list():
    url = f"{BASE_URL}/api/v1/app/models"
    headers = {
        "Content-Type": "application/json"
    }
    
    response = session.get(url, headers=headers)
    return response.json()

# 使用示例
result = get_model_list()
if result['status'] == 0:
    print("模型列表获取成功:")
    for model in result['data']:
        print(f"模型ID: {model['model_id']}")
        print(f"模型名称: {model['model']['model']}")
        print(f"基础URL: {model['model']['base_url']}")
        print(f"备注: {model['model']['note']}")
        print("---")
```

#### cURL 示例
```bash
curl --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -X GET "$BASE_URL/api/v1/app/models" \
  -H "Content-Type: application/json"
```

#### 响应示例
```json
{
  "status": 0,
  "message": "获取模型列表成功",
  "data": [
    {
      "model_id": "gpt4-model",
      "model": {
        "model": "gpt-4",
        "token": "********",
        "base_url": "https://api.openai.com/v1",
        "note": "GPT-4模型",
        "limit": 1000
      },
      "default": []
    },
    {
      "model_id": "system_default",
      "model": {
        "model": "deepseek-chat",
        "token": "********",
        "base_url": "https://api.deepseek.com/v1",
        "note": "系统默认模型",
        "limit": 1000
      },
      "default": ["mcp_scan", "ai_infra_scan"]
    }
  ]
}
```

### 2. 获取模型详情

#### 接口信息
- **URL**: `/api/v1/app/models/{modelId}`
- **方法**: `GET`
- **Content-Type**: `application/json`

#### 参数说明
| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| modelId | string | 是 | 模型ID（路径参数） |

#### 响应字段
| 字段名 | 类型 | 说明 |
|--------|------|------|
| model_id | string | 模型ID |
| model | object | 模型配置信息 |
| model.model | string | 模型名称 |
| model.token | string | API密钥（已脱敏显示为********） |
| model.base_url | string | 基础URL |
| model.note | string | 备注信息 |
| model.limit | integer | 请求限制 |
| default | array | YAML 中的任务类型默认值；加密平台模型返回空数组 |

#### Python 示例
```python
def get_model_detail(model_id):
    url = f"{BASE_URL}/api/v1/app/models/{model_id}"
    headers = {
        "Content-Type": "application/json"
    }
    
    response = session.get(url, headers=headers)
    return response.json()

# 使用示例
result = get_model_detail("gpt4-model")
if result['status'] == 0:
    model_data = result['data']
    print(f"模型ID: {model_data['model_id']}")
    print(f"模型名称: {model_data['model']['model']}")
    print(f"基础URL: {model_data['model']['base_url']}")
    print(f"备注: {model_data['model']['note']}")
```

#### cURL 示例
```bash
curl --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -X GET "$BASE_URL/api/v1/app/models/gpt4-model" \
  -H "Content-Type: application/json"
```

#### 响应示例
```json
{
  "status": 0,
  "message": "获取模型详情成功",
  "data": {
    "model_id": "gpt4-model",
    "model": {
      "model": "gpt-4",
      "token": "********",
      "base_url": "https://api.openai.com/v1",
      "note": "GPT-4模型",
      "limit": 1000
    },
    "default": []
  }
}
```

### 3. 创建模型

#### 接口信息
- **URL**: `/api/v1/app/models`
- **方法**: `POST`
- **Content-Type**: `application/json`

#### 请求参数
| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| model_id | string | 是 | 模型ID，全局唯一 |
| model | object | 是 | 模型配置信息 |
| model.model | string | 是 | 模型名称 |
| model.token | string | 是 | API密钥 |
| model.base_url | string | 是 | 基础URL |
| model.note | string | 否 | 备注信息 |
| model.limit | integer | 否 | 请求限制，默认1000 |

系统会先去除 `model_id` 首尾空白，再与只读 YAML 源比较。命中 YAML ID 时不能遮蔽该共享模型：接口返回 HTTP `200`、`status: 1`，且不产生数据库或审计变更。若 YAML 源无法加载，创建操作同样失败关闭并返回旧 envelope，不执行任何写入。

#### Python 示例
```python
def create_model():
    url = f"{BASE_URL}/api/v1/app/models"
    headers = {**csrf_headers, "Content-Type": "application/json"}
    data = {
        "model_id": "my-gpt4-model",
        "model": {
            "model": "gpt-4",
            "token": "<api-key>",
            "base_url": "https://api.openai.com/v1",
            "note": "我的GPT-4模型",
            "limit": 2000
        }
    }
    
    response = session.post(url, json=data, headers=headers)
    return response.json()

# 使用示例
result = create_model()
if result['status'] == 0:
    print("模型创建成功")
else:
    print(f"模型创建失败: {result['message']}")
```

#### cURL 示例
```bash
curl --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -X POST "$BASE_URL/api/v1/app/models" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $SESSION_CSRF" \
  -d '{
    "model_id": "my-gpt4-model",
    "model": {
      "model": "gpt-4",
      "token": "<api-key>",
      "base_url": "https://api.openai.com/v1",
      "note": "我的GPT-4模型",
      "limit": 2000
    }
  }'
```

#### 响应示例
```json
{
  "status": 0,
  "message": "模型创建成功",
  "data": null
}
```

### 4. 更新模型

#### 接口信息
- **URL**: `/api/v1/app/models/{modelId}`
- **方法**: `PUT`
- **Content-Type**: `application/json`

#### 参数说明
| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| modelId | string | 是 | 模型ID（路径参数） |
| model | object | 是 | 模型配置信息 |
| model.model | string | 否 | 模型名称 |
| model.token | string | 否 | API密钥（如不修改可传********或不传） |
| model.base_url | string | 否 | 基础URL |
| model.note | string | 否 | 备注信息 |
| model.limit | integer | 否 | 请求限制 |

**注意**: 
- 如果token字段传入`********`或空值，则不会更新token，保持原值
- 支持只更新部分字段，未传入的字段保持原值

#### Python 示例
```python
def update_model(model_id):
    url = f"{BASE_URL}/api/v1/app/models/{model_id}"
    headers = {**csrf_headers, "Content-Type": "application/json"}
    # 只更新备注和限制，不修改token
    data = {
        "model": {
            "model": "gpt-4-turbo",
            "token": "********",  # 不修改token
            "base_url": "https://api.openai.com/v1",
            "note": "更新后的备注信息",
            "limit": 3000
        }
    }
    
    response = session.put(url, json=data, headers=headers)
    return response.json()

# 使用示例
result = update_model("my-gpt4-model")
if result['status'] == 0:
    print("模型更新成功")
else:
    print(f"模型更新失败: {result['message']}")
```

#### 更新token示例
```python
def update_model_token(model_id, new_token):
    url = f"{BASE_URL}/api/v1/app/models/{model_id}"
    data = {
        "model": {
            "model": "gpt-4",
            "token": new_token,  # 传入新的token
            "base_url": "https://api.openai.com/v1",
            "note": "更新了API密钥",
            "limit": 2000
        }
    }
    
    response = session.put(url, json=data, headers=csrf_headers)
    return response.json()
```

#### cURL 示例
```bash
# 只更新备注信息
curl --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -X PUT "$BASE_URL/api/v1/app/models/my-gpt4-model" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $SESSION_CSRF" \
  -d '{
    "model": {
      "model": "gpt-4-turbo",
      "token": "********",
      "base_url": "https://api.openai.com/v1",
      "note": "更新后的备注信息",
      "limit": 3000
    }
  }'

# 更新token
curl --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -X PUT "$BASE_URL/api/v1/app/models/my-gpt4-model" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $SESSION_CSRF" \
  -d '{
    "model": {
      "model": "gpt-4",
      "token": "<new-api-key>",
      "base_url": "https://api.openai.com/v1",
      "note": "更新了API密钥",
      "limit": 2000
    }
  }'
```

#### 响应示例
```json
{
  "status": 0,
  "message": "模型更新成功",
  "data": null
}
```

### 5. 删除模型

#### 接口信息
- **URL**: `/api/v1/app/models`
- **方法**: `DELETE`
- **Content-Type**: `application/json`

#### 请求参数
| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| model_ids | array | 是 | 要删除的模型ID列表，支持批量删除 |

#### Python 示例
```python
def delete_models(model_ids):
    url = f"{BASE_URL}/api/v1/app/models"
    headers = {**csrf_headers, "Content-Type": "application/json"}
    data = {
        "model_ids": model_ids
    }
    
    response = session.delete(url, json=data, headers=headers)
    return response.json()

# 删除单个模型
result = delete_models(["my-gpt4-model"])
if result['status'] == 0:
    print("模型删除成功")

# 批量删除多个模型
result = delete_models(["model1", "model2", "model3"])
if result['status'] == 0:
    print("批量删除成功")
```

#### cURL 示例
```bash
# 删除单个模型
curl --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -X DELETE "$BASE_URL/api/v1/app/models" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $SESSION_CSRF" \
  -d '{
    "model_ids": ["my-gpt4-model"]
  }'

# 批量删除多个模型
curl --cacert "$CA_BUNDLE" -b "$COOKIE_JAR" -X DELETE "$BASE_URL/api/v1/app/models" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $SESSION_CSRF" \
  -d '{
    "model_ids": ["model1", "model2", "model3"]
  }'
```

#### 响应示例
```json
{
  "status": 0,
  "message": "删除成功",
  "data": null
}
```

### 6. YAML配置模型

除了通过API创建的数据库模型外，系统还支持通过YAML配置文件定义系统级模型。

#### 配置文件位置
`db/model.yaml`

#### YAML配置格式
```yaml
- model_id: system_default
  model_name: deepseek-chat
  token: <api-key>
  base_url: https://api.deepseek.com/v1
  note: 系统默认模型
  limit: 1000
  default:
    - mcp_scan
    - ai_infra_scan

- model_id: eval_model
  model_name: gpt-4
  token: <evaluation-api-key>
  base_url: https://api.openai.com/v1
  note: 评估模型
  limit: 2000
  default:
    - model_redteam_report
```

#### 字段说明
| 字段名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| model_id | string | 是 | 模型ID |
| model_name | string | 是 | 模型名称 |
| token | string | 是 | API密钥 |
| base_url | string | 是 | 基础URL |
| note | string | 否 | 备注信息 |
| limit | integer | 否 | 请求限制 |
| default | array | 否 | 默认使用此模型的任务类型列表 |

#### 特点说明
- YAML配置的模型为**只读**，不支持通过API进行修改和删除
- YAML配置的模型在获取列表和详情时会与数据库模型合并返回
- `default`字段为YAML模型特有，用于标识该模型适用的默认任务类型
- 系统启动时自动加载YAML配置

---

## 已退役任务工作流迁移

不要复制或改造历史浏览器任务示例。客户端应迁移到受保护平台章节记录的平台任务集合、按 owner 授权的详情/取消、不可变报告与附件操作。历史浏览器任务路由返回 `410 Gone`；不存在 WebSocket、SSE、状态、结果、上传或携带凭据请求的回退。

## 错误处理

### 常见错误码
| 状态码 | 说明 | 解决方案 |
|--------|------|----------|
| 0 | 成功 | - |
| 1 | 失败 | 查看message字段获取详细错误信息 |

### 错误处理示例
```python
def handle_api_response(response):
    """处理API响应的通用函数"""
    data = response.json()
    
    if data['status'] == 0:
        return data['data']
    else:
        raise Exception(f"API调用失败: {data['message']}")

# 使用示例
try:
    result = handle_api_response(response)
    print("操作成功:", result)
except Exception as e:
    print("操作失败:", str(e))
```

## 注意事项

### 通用注意事项
1. **认证**: 确保在请求头中包含正确的认证信息
2. **文件大小**: 上传文件大小限制请参考服务器配置
3. **超时设置**: 根据任务复杂度合理设置超时时间
4. **并发限制**: 避免同时创建过多任务，以免影响系统性能
5. **结果保存**: 及时保存扫描结果，避免数据丢失

### 任务相关注意事项
6. **数据集选择**: 根据测试需求选择合适的数据集组合
7. **模型配置**: 确保测试模型和评估模型配置正确

### 模型管理注意事项
8. **模型ID唯一性**: 创建模型时，model_id必须全局唯一
9. **Token安全**: API密钥在返回时会自动脱敏显示为`********`，前端显示和编辑时需要注意
10. **Token更新**: 更新模型时，如果token字段为空或`********`，则不会更新token，保持原值
11. **模型验证**: 创建模型时系统会自动验证token和base_url的有效性
12. **YAML模型**: 通过YAML配置的模型为只读，不支持通过API修改或删除
13. **批量删除**: 删除模型时支持传入多个model_id进行批量删除
14. **权限控制**: 管理员可以跨所有者查看、修改和删除模型；审计员拥有全局只读权限；普通用户只能查看、修改和删除本人模型

## 技术支持

如有问题，请联系技术支持团队或查看项目文档。
