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
| `GET /api/v1/platform/mcp-workbench` | `mcpworkbench.View` | 只读 MCP 安全投影。普通用户仅本人任务/报告；审计员/管理员全局。它不是原始报告、任务、目标或日志 API。 |
| `GET /api/v1/platform/reports` | `ReportListResponse` | 普通用户仅本人；审计员/管理员全局。item 是不可变安全摘要。 |
| `GET /api/v1/platform/admin/users` | `UserListResponse` | 仅管理员；不含任何凭据材料。 |
| `GET /api/v1/platform/admin/audit-events` | `AuditListResponse` | 仅审计员/管理员；metadata 递归脱敏。 |
| `GET /api/v1/platform/models` | `CatalogPage` | 普通用户看全局和本人私有 platform 行；审计员只读全局行；管理员看全部 platform 行。token 始终为 `********`，`source` 为 `platform` 或 `yaml`，并显式返回 `read_only`。只读 YAML 行与同 ID platform 行发生碰撞时仍分别保留；目录加载失败时失败关闭。 |

`GET /api/v1/platform/tasks/{taskID}` 返回 `TaskDetail`，其 `input_summary` 仅含有界展示元数据。只有 `ai_infra_scan` 和 `skills_scan` 的详情可以包含 `model_id`：它是用于恢复当前模型目录标签的 opaque、已持久化/已验证模型引用。模型被删除、禁用或对当前用户不可见时，必须使用安全 ID 回退展示。该字段不是 Token、Base URL、凭据、原始参数对象，也不代表当前可用性；原始 params、嵌套凭据和 model 对象仍不会返回。Skills 摘要只含 `language`、`model_id` 与固定为 `static` 的 `scan_mode`；其他类型不返回 `scan_mode`，Skills 不返回名称、文件数、技能数或 `target_count`。普通用户只看本人任务，审计员/管理员拥有全局读取权限；任务不存在或对普通用户不可见时返回 `404`。`GET /api/v1/platform/tasks/{taskID}/result` 已退役：通过认证与首次改密门禁后恒定返回 `410 Gone`，且绝不读取引擎输出。

只有 `agent_scan` 的 `input_summary` 可以额外包含 `agent_id`、`eval_model_id`，两者都是已持久化引用的安全投影，不代表当前目录或网络可用。服务端先验证完整参数合同，再省略非法、超长、类似凭据或路径的历史引用；不会从内联配置中抽取展示值。Agent 名称的解析使用任务所有者上下文；当前目录无法确认时显示安全 ID，不能拿管理员自己的同名配置替换它。详情可带去首尾空白、最多 2,000 Unicode 码点的 `remark`；空白或不合法的历史备注省略，列表不返回备注。

详情 GET 可选返回 `report_id`，条件是任务为 `succeeded`，该任务已有不可变报告快照，且当前 Subject 获准读取该快照。尚未成功或没有快照时省略；浏览器不能根据任务 ID 猜测报告 ID，也不能扫描报告列表寻找关联。创建的 `202` 与 `503.task` 均省略 `report_id`，包括对已完成历史任务的幂等确认；查看报告应使用详情 GET 返回的引用访问 `/api/v1/platform/reports/{reportID}`。

### 模型连通性测试

控制台“供应商模型”现显示为“模型ID”，接口字段仍为 `provider_model`；它与配置记录的 `modelID` 不同。表单灰色示例不作为实际值提交。调用限制留空按既有默认值 `0` 保存。

| 接口 | 用途 |
| --- | --- |
| `POST /api/v1/platform/models/test` | 测试当前未保存的连接参数；必须提供 Token。 |
| `POST /api/v1/platform/models/{modelID}/test` | 测试已保存模型的当前表单参数；必须拥有该模型的可写权限。 |

请求是最大 16 KiB 的 JSON，字段为 `provider_model`（最多 512 字节）、`base_url`（最多 2048 字节）和仅写入的 `token`（最多 8192 字节）。不要求模型名称、备注或调用限制。基础 URL 支持 HTTP/HTTPS 和合法内网地址，填写 API 基础路径，不含账号、查询或片段。

```json
{
  "provider_model": "internal-chat",
  "base_url": "http://inference.internal:8000/v1",
  "token": "fictional-example-api-key"
}
```

编辑测试时 `token` 省略或留空，可由服务端使用保存的凭据，但规范化后的完整基础 URL 必须保持一致。**更改基础 URL 时，测试和保存都必须提供新的有效 Token**；API 路径变化也算地址变化，仅尾斜线、默认端口等语义等价差异不算。掩码不能作为新的 Token。

入口遵循 Cookie 会话、首次改密、角色/可写资源、CSRF 校验。审计员不可测试；普通用户不可使用他人的私有凭据。每用户最多 1 个进行中的测试、启动间隔 5 秒；进程内全局最多 8 个并发、启动间隔 100 毫秒，用户限流状态有界。过频/并发冲突返回 `429`。无效参数返回 `400`，会话/权限为 `401/403`，记录不存在为 `404`，凭据或审计不可用为固定安全 `500`。

平台服务端发送一次兼容 Chat Completions 的简短请求，30 秒超时，响应上限 64 KiB，不使用代理、不重试、不跟随跳转，HTTPS 验证证书。地址解析及实际拨号拒绝环回、链路本地、未指定、组播和元数据地址。仅 HTTP 200 不足以判定成功，必须获得有效非空文本响应。

已执行的探测返回 HTTP `200`，响应只有 `status`（`success` 或 `error`）、`code`、固定安全 `message`、非负整数 `elapsed_ms`。成功 `code=ok`；错误包括 `invalid_config`、`authentication_failed`、`model_not_found`、`rate_limited`、`timeout`、`network_error`、`invalid_response`、`upstream_error`、`redirect_blocked`、`busy`、`unavailable`。模型服务返回的 401/403 是探测结果 `authentication_failed`，不代表平台登录失效。

测试不保存模型、不创建任务，不回传 Token 或模型原始响应；审计只含安全元数据。浏览器按固定代码显示中文结果和耗时，取消/离开时取消请求，更改连接字段后清除旧结果。测试代表平台服务端当时的调用结果，不承诺其他 Agent 网络环境的可达性；保存不强制依赖测试成功。

### 受治理的 MCP 扫描创建

MCP 只使用专属接口 `/api/v1/platform/mcp-scans`，不调用 AI 基础设施扫描或通用任务接口。通用 tasks 的创建、MCP 筛选、MCP 详情和取消返回 `409 MCP_SPECIALIZED_ENDPOINT_REQUIRED` 与 `specialized_path`；默认列表在统计和分页前排除 `mcp_scan` / `Mcp-Scan`。

`POST /api/v1/platform/mcp-scans` 接受最大 64 KiB 的严格、平铺 JSON：MCP 要求 `source_kind` 为 `repository` 或 `service`，不接受任务类型、语言、content、Token 或 Header。语言固定 `zh_CN`，可选 `model_id` 必须通过受治理模型校验，`thread` 默认 4、范围 1–32。

模型保持可选：省略 `model_id` 时不读取默认模型或模型环境凭据，仅进行无 LLM 基础检查。仓库来源只读检查代码规则；服务来源只通过任务网关进行协议及工具元信息检查，不执行服务工具。提供 `model_id` 后启用模型辅助分析。基础结果只代表规则线索，报告覆盖范围、结论及评分说明会明确标注“基础检查（未使用模型）”，参考分不是全面安全评估或安全认证。

- `repository`：严格 Git 引用或 ready 代码附件二选一；Git 必须是受服务器允许集约束的 HTTPS `repository_url`，附件为 1–10 个 `attachment_ids`。禁止服务字段和 `authorization_confirmed`，即使字段为空也拒绝。
- `service` 要求 `authorization_confirmed=true` 且不允许附件；必须选择 `connection_config_id` 和精确 `connection_config_version`。配置须可见、已测试、已启用，并通过当前出站策略，不在任务页输入端点或凭据。

创建成功返回 `202 {"task_id":"<opaque-id>","status":"pending"}`；同键同载荷重放返回 200 和 `Idempotent-Replay: true`。不同载荷复用键返回 409。写操作要求 CSRF；创建、取消、配置变更和合并还要求 `Idempotency-Key`。网络失败或成功状态的正文读取/校验失败均视为结果未知，不自动 POST；用户显式重试复用原键。

创建事务提交后，即使调度失败或分配结果暂时未知，也返回 202 和原 `task_id`，不能作为创建失败换键重提。响应状态是受理快照；通过 MCP 专属详情查询实际调度状态。

客户端收到代理或服务端 5xx 时仍不能证明事务未提交，也必须作为结果未知保留同一幂等键；4xx 的明确校验、权限和版本拒绝按对应错误处理。

| 接口 | 用途 |
| --- | --- |
| GET /api/v1/platform/mcp-scans | MCP 历史；page 1–1000、page_size 1–100、status 筛选 |
| GET /api/v1/platform/mcp-scans/{taskID} | 专属安全详情与已有授权 report_id |
| POST /api/v1/platform/mcp-scans/{taskID}/cancel | 空对象请求，返回 task_id/status，终态与幂等结果原子提交 |
| GET/POST /api/v1/platform/mcp-connection-configs | 安全配置列表 / 创建私有配置 |
| GET/PATCH /api/v1/platform/mcp-connection-configs/{configID} | 所有者/管理员编辑详情 / 条件修改 |
| POST /api/v1/platform/mcp-connection-configs/{configID}/test | 最小连接测试，不执行工具 |
| GET /api/v1/platform/mcp-connection-options | 仅返回可用于任务的连接 ID/版本及安全标签 |

配置创建字段：`name`（必填、最多 80 字）、`description`（可选、最多 500 字）、`server_url`、`transport`（auto/http/sse）、`authentication.kind`（none/bearer/api_key_header/custom_headers）以及条件必填的 `secret`、`header_name`、`headers[{name,value}]`。无需认证不能带 Header；自定义 Header 至少一项。不得提交 scope、owner、代理或 TLS 绕过开关。

编辑 GET 是唯一允许返回端点和 Header 名的管理详情例外，仍无秘密值，使用 no-store；审计员只能读安全列表、任务和报告，不能取得管理详情或 options。PATCH/test 要求 `If-Match` 为加引号的 `resource_revision`（十进制字符串，不是版本号）；缺失/格式错误 428，版本冲突 409。省略秘密值保留同认证类型/同 Header 名的旧值；更换类型或名称需新秘密，`headers:[]` 清除请求头。连接材料变化生成新版本并禁用；改名称/描述不改变版本。测试先禁用旧结果，最小 initialize 成功/失败均返回安全状态，失败不回显上游内容；同配置至少间隔一分钟（429），测试通过后需独立启用，`{"enabled":true}` 必须独占 PATCH。

附件只走 `/api/v1/platform/mcp-scan-attachments`：POST 单文件，POST `/chunked` 开始，POST `/{attachmentID}/chunks` 提交 chunk_index/chunk，POST `/{attachmentID}/merge` 提交 total_chunks/file_size，DELETE `/{attachmentID}` 中止未绑定附件。返回 ID、状态、大小和服务器大小限制，不返回原始文件名，且**没有浏览器下载端点**。分片重放按上传 ID、索引和摘要确定；不同内容冲突。已存在但无法确认幂等结果的分片失败关闭，需中止后重新上传，不覆盖已有内容。

来源相关审计元数据包含 source kind 与布尔授权确认；通用任务审计流程可保留安全的 phase 元数据。列表/详情/报告不返回端点、Git 地址、秘密、原始参数或附件名。历史缺少可信来源的 MCP 任务仍通过专属详情只读展示为 `legacy_unknown`，不重新分派。

运行配置：独立 `MCP_CONNECTION_MASTER_KEY_ID` / `MCP_CONNECTION_MASTER_KEY`（base64 编码 32 字节）及可选 `MCP_CONNECTION_PREVIOUS_MASTER_KEYS`；服务出站 `MCP_OUTBOUND_ALLOWED_CIDRS`，Git 出站 `MCP_GIT_OUTBOUND_ALLOWED_CIDRS` 与 `MCP_GIT_ALLOWED_HOSTS`，空允许集默认拒绝。远端 Agent 必须配置其可达的 `MCP_GATEWAY_BASE_URL`；Compose 默认 http://webserver:8088。系统信任链验证 TLS，禁止环境代理、重定向、特殊用途地址和 Git 子模块。平台只向 Agent 发短时 capability 或不透明 archive 引用，模型/MCP 运行配置经私有 stdin 交接。内部网关与归档要求 `AIG_AGENT_TOKEN`，不是浏览器 API。归档快照最长 15 分钟，单文件 16 MiB、最多 10,000 文件、总计/归档 64 MiB；进程重启后必须重新生成引用。

### MCP 安全扫描工作台

`GET /api/v1/platform/mcp-workbench` 是只读、由服务端拥有的安全投影。它使用固定 30 个 UTC 日窗口 `[utcDay(now)-29d, utcDay(now)+1d)`：普通用户只读取本人任务/报告范围，审计员与管理员读取全局范围。`running` 计数窗口内 `created_at` 的 `dispatching`/`running` MCP 任务，`pending` 计数 `pending`/`dispatch_unknown` MCP 任务；`completed_30d` 计数窗口内 `completed_at` 的 MCP 报告，`high_risk` 汇总这些报告的安全高风险计数。`active_tasks` 最多 10 项，按 `updated_at DESC, id DESC` 排序的非终态任务；`recent_risks` 最多 5 项，按高、中、低，再按 `completed_at DESC`、报告 ID 倒序排列。

响应顶层精确只有 `metrics`、`active_tasks` 与 `recent_risks`。活跃项只含 `task_id`、服务端生成的泛化 `label`、`source_kind`、可空 `phase`、`status` 与 `updated_at`。风险项只含 `report_id`、`task_id`、`severity`、`category`、有界泛化 `summary` 与 `completed_at`。`source_kind` 仅为 `repository`、`service` 或 `legacy_unknown`；风险 `category` 只能是固定类别或 `other`。历史报告缺少可信类别/严重度时，服务端只用已存风险计数生成泛化 `other` 摘要。该端点绝不返回请求 `content`、仓库或服务端点、原始结果、扫描器原始发现、模型 ID、headers、授权材料、附件引用、参数或日志。

成功返回 `200`；未认证调用方返回 `401`；首次改密门禁或不支持角色返回 `403`；聚合失败返回脱敏 `500`。它没有变更、分页、筛选或原始详情模式。

### 任务创建响应的安全加固迁移

**破坏性变更：** `POST /api/v1/platform/tasks` 不再返回旧的内部任务 `View`。成功的 `202` 现在返回 `TaskDetail`，必含 `id`、用于展示的安全 `owner`、规范化 `task_type`、`status`、`created_at`、`updated_at` 和有界 `input_summary`，可带安全 `remark`，始终省略 `report_id`。此变更阻止持久化请求与引擎内部字段越过浏览器边界。列表仍只有六个安全字段，不返回备注。

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

平台任务创建 JSON body 最大 256 KiB，`content` 最大 32 KiB；支持附件的任务类型最多引用 10 个不重复且不超过 128 字节的 opaque 附件 ID，新 Agent 扫描不支持附件。只接受 canonical `ai_infra_scan`、`model_redteam_report`、`agent_scan`、`skills_scan`，服务端在私有 Adapter 边界分别映射为真实 Agent Alias。MCP 使用上文的专属接口。其余参数采用逐类型白名单：基础设施允许 `model_id`/`timeout`/`port_scan_mode`，以及成对的 `target_credential_id`/`target_credential_revision`；模型红队要求 `model_id` 字符串数组和 `eval_model_id`，可带 `dataset.numPrompts/randomSeed/promptColumn` 与 `techniques`；Agent 扫描仅接受必填的 `agent_id` 与 `eval_model_id`；Skills 仅允许必填字符串 `model_id`，且必须使用恰好一个 ready ZIP 附件和空 `content`。`ai_infra_scan.params.port_scan_mode` 只能精确为 `fixed_ai` 或 `full_tcp`，省略时规范化为 `fixed_ai`；前者对裸 IPv4 发现 `11434,1337,7000-9000,18789`（2,004 个）TCP 端口，后者发现全部 `1-65535` TCP 端口。它不接受自定义端口、UDP 或版本识别选项，且 URL、域名、带端口 IP、IPv6 不触发该端口发现步骤。安全 `TaskDetail.input_summary.port_scan_mode` 仅在可验证时返回上述规范化枚举值，绝不返回原始参数。所有 `model_id`/`eval_model_id` 必须在持久化任务前通过受治理模型解析器验证，`agent_id` 必须解析到该用户或公共只读 Agent 配置；未知或不可见引用固定拒绝且不写入任务。未知字段、嵌套凭据对象、明文模型凭据、旧 model 对象和任务 Alias 均被拒绝。浏览器附件只使用 opaque 附件 ID，并按 owner 隔离。内部 Agent WebSocket 与旧形状制品传输属于独立的 internal-token 边界，不是浏览器 API。

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

### Skills ZIP 静态扫描

`skills_scan` 是独立平台类型，内部映射为 `Skills-Scan`，列表筛选与报告均保留 Skills 身份。使用当前用户已上传且处于 ready 状态的单个 ZIP，`params` 恰好包含可用受治理模型的 `model_id`；未知参数会被拒绝。`content` 必须省略或为空字符串。首期不接受 URL、仓库地址、自定义审计提示词、并发、端口或动态扫描选项。中文 Skills 界面明确提交 `country_iso_code: "zh_CN"`，API 本身仍允许既有空值、`zh`、`zh_CN`、`en`。

```json
{
  "task_type": "skills_scan",
  "params": { "model_id": "governed-model-id" },
  "attachment_ids": ["ready-attachment-id"],
  "country_iso_code": "zh_CN",
  "remark": "待发布 Skill 的静态审计"
}
```

ZIP 压缩大小最多 20 MiB，实际解压总量最多 100 MiB，单文件最多 5 MiB，原始 ZIP 条目和规范化后的文件/目录总数（含隐式目录）均最多 2,000。必须只有一个 `SKILL.md`，位于 ZIP 根目录或单个顶层包裹目录；所有文件都在同一 Skill 根目录下。`SKILL.md` 必须为有效 UTF-8，YAML frontmatter 是对象，`name` 和 `description` 为非空字符串，分别不超过 128 与 2,000 个 Unicode 码点。无脚本的说明型 Skill 也可提交。路径穿越、绝对路径、反斜杠、链接、特殊文件、重复或大小写冲突路径、文件/目录冲突、重复 YAML 键、加密、不支持的压缩方式及损坏 ZIP/CRC 均被拒绝；服务端按实际解压流计算限制，不能只依赖元数据。

创建前检查附件但不在 Web 服务目录解压，附件与任务原子绑定；相同幂等键及请求的重试先返回已存在任务，不因附件已绑定而失败。执行器在独立临时目录复检并解压，结束或取消后清理。扫描只允许受根目录约束且输出有界的静态读取、目录与搜索，禁止执行包内代码、安装依赖或访问任意外部目标；全部模型阶段使用已选治理模型。模型、解析或结果事件失败时任务失败，不产生成功空报告。

可选 `remark` 沿用现有任务规则：去除首尾空白，最多 2,000 个 Unicode 码点，参与幂等请求比较，仅在授权 `TaskDetail` 中返回，不进入引擎、审计元数据或报告。

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
- `GET /api/v1/platform/reports/{reportID}` 返回安全详情，其中 `render` 源自任务完成时保存的不可变 RenderModel。对于 MCP 报告，在线详情与导出 PDF 在展示边界把技术发现替换为服务端生成的固定类别/严重度摘要，不返回真实目标、仓库标识或扫描器自由文本。

不可变 `report-render-v2` RenderModel 固化风险映射版本、生成/完成时间、任务元数据、产品名/主色/水印、风险评分及评分说明、风险分布、`risk_trend` 中 30 个固定 UTC 日桶、Top 风险、含证据/影响/修复的技术发现、建议、覆盖范围和结论。对于可信的 AI 基础设施任务，它可选地同时包含 `port_scan_mode` 与 `port_spec`，且只允许 `fixed_ai` + `11434,1337,7000-9000,18789` 或 `full_tcp` + `1-65535`；字段缺失、孤立、未知或不匹配时必须整体省略。技术发现仅按四类可信引擎的显式 schema 白名单映射，完成脱敏和严重度排序后最多保留 50 条；提示词、会话、附件、截图、凭据、URL 查询参数和用户绝对路径都不会进入 RenderModel。MCP 在线详情和自动分页 PDF 在展示前还会把技术发现投影为固定的泛化摘要，以省略真实目标、仓库标识和扫描器自由文本。列表与详情契约明确分离：绝不暴露原始引擎结果与 Logo 字节，也不暴露存储的渲染载荷、owner ID、文件路径或当前可变品牌记录。列表仅接受 `page=1..1000`、`page_size=1..100`（默认 20）。

Skills 使用与 MCP 相同的经验证 `score`/`results`/`level` 结果转换，但快照、在线详情与报告列表的 `task_type` 仍为 `skills_scan`；既有 MCP 报告及历史任务类型别名保持原值，不重新归类。

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

## 基础设施目标访问凭据

控制台入口为“凭证配置 → 基础设施凭据”。目标凭据用于访问被扫描的服务；模型配置仍用于可选的分析模型。普通用户和管理员只管理各自的私有目标凭据，审计员不能读取或使用。所有写请求沿用 Cookie 身份、已完成强制改密和 `X-CSRF-Token` 校验，JSON 请求体上限为 16 KiB。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/v1/platform/target-credentials` | 返回 `{items: [...]}` 安全元数据，包含停用项 |
| POST | `/api/v1/platform/target-credentials` | 创建凭据，返回 201 和安全元数据 |
| GET | `/api/v1/platform/target-credentials/{id}` | 获取本人凭据安全元数据及 ETag |
| PUT | `/api/v1/platform/target-credentials/{id}` | 更新、替换密钥或停用，返回 200 |
| DELETE | `/api/v1/platform/target-credentials/{id}` | 删除，返回 204 |

PUT/DELETE 必须提交带双引号的 `If-Match: "1"`。缺失或格式无效返回 428，版本冲突返回 409。读取响应包含 `id,name,origin,auth_type,header_name,disabled,revision,created_at,updated_at,allow_insecure_http`，设置 `Cache-Control: no-store`，绝不回传密钥、Basic 用户名或加密材料。

创建示例（值均为示意，不是真实密钥）：

```json
{
  "name": "测试环境推理服务",
  "origin": "http://inference.internal:8080",
  "auth_type": "bearer",
  "secret": "REPLACE_WITH_TARGET_TOKEN",
  "disabled": false
}
```

支持 `bearer`、`api_key`、`basic`、`cookie`。Bearer 只填写 Token，系统添加前缀；API Key 同时填写 `header_name`（例如 `X-API-Key`）；Basic 同时填写 `username` 和作为密码的 `secret`；Cookie 的 `secret` 形如 `session=VALUE; tenant=VALUE`。禁止 Host、代理和传输控制类请求头、换行或控制字符。

`origin` 默认支持 HTTP 和 HTTPS，创建或编辑时可直接填写地址，无需额外开关或许可字段。请求中的旧版可选 `allow_insecure_http` 字段仅用于兼容，其值会被忽略；省略、false 或 true 都由目标地址的协议决定实际行为。读取响应的该字段由已保存的协议推导：HTTP 返回 true，HTTPS 返回 false。无需新增数据库列，协议仍受现有密文 AAD 保护。

目标地址仅含协议、主机与可选端口，不含路径、查询、片段或 URL 用户信息；根路径允许，HTTP 80 / HTTPS 443 归一化。更新时提交完整 `name,origin,auth_type`，`secret` 留空或省略保留原值；更改目标（包括 HTTP/HTTPS 协议）、认证类型或请求头名称时必须提供新密钥。替换 Basic 密码时同时提供用户名。每次更新递增版本，停用或删除会阻止新任务及尚未下发的旧任务继续解析，已发出的请求不被召回。

创建 `ai_infra_scan` 时增加成对的安全引用；下面的 ID 以创建凭据接口实际返回值替换：

```json
{
  "task_type": "ai_infra_scan",
  "content": "http://inference.internal:8080/api/version",
  "params": {
    "target_credential_id": "REPLACE_WITH_CREDENTIAL_ID",
    "target_credential_revision": 1,
    "timeout": 300,
    "port_scan_mode": "fixed_ai"
  },
  "country_iso_code": "zh_CN"
}
```

仍须提交任务 `Idempotency-Key`；相同请求重试先返回原任务，不重新校验当前凭据状态。带凭据任务仅支持手动填写的同源 HTTP/HTTPS URL（协议、主机、有效端口必须一致），可多行多个路径；不接受附件、裸主机、IP 范围或端口发现表达式。创建和分配前均校验所有者、启用状态、版本及目标范围；错误不会回退成匿名扫描。任务和引擎 session 仅保存 ID/版本，明文只在下发时通过私有 `target_auth` 通道发给声明 `infra-target-auth-v2` 能力的认证 Agent。运行时 HTTP 认证必须带由服务端派生的 `allow_insecure_http: true`，缺失即拒绝；任务参数不能自行提供此运行时字段。服务端与 Agent 需要一起升级。

认证扫描读取随 Agent 发布的指纹与漏洞规则库（默认 /app/data，可通过 AIG_DATA_DIR 明确指定），不借用浏览器知识库接口；规则更新需同步发布到 Agent。规则缺失、为空或格式错误时初始化失败。认证主目标出现证书、网络、范围校验错误或明确返回 401/403 时，任务失败且不生成成功报告。认证扫描使用 HTTP 证据和可选的文本模型分析，不生成网页截图或执行视觉分析；认证后可访问不能单独证明未授权访问漏洞。HTTPS 认证请求验证服务器证书；所有认证请求均拒绝跨源、跨端口、跨协议跳转和 Host 覆盖；响应中的认证反射在进入证据、模型分析或事件存储前脱敏。凭据使用现有 `MODEL_MASTER_KEY`/`MODEL_MASTER_KEY_ID`/`MODEL_PREVIOUS_MASTER_KEYS` 密钥环，以独立 AAD 绑定资源、所有者、目标和版本。部署前运行 `aig migrate` 应用迁移 13；运行时不执行 DDL。未使用凭据的现有扫描行为保持不变。

## 技术支持

如有问题，请联系技术支持团队或查看项目文档。
