# MCP 连接配置设计（首期）

**状态：** 待用户评审

**分支：** `codex/mcp-scan-workbench`（基于 `develop`）

**日期：** 2026-09-03
**范围：** 在“凭证配置”中增加 MCP 服务连接配置，并让 MCP 服务扫描任务仅引用该配置。

## 1. 目标与边界

MCP 连接配置用于保存“如何连接被扫描的 MCP 服务”：服务端点、传输方式和目标服务认证信息；TLS 信任始终使用平台部署时维护的信任库。它与扫描模型配置不同：

- **模型配置**决定扫描器使用哪个大模型进行分析，包含模型供应商、模型名称、模型 Base URL 和模型 API 凭据；
- **MCP 连接配置**决定扫描器如何接入被扫描的 MCP 服务，包含 MCP 服务端点和该服务的访问凭据；
- 两者同属“凭证配置”工作区，可复用加密、权限、审计和连通性验证能力，但必须使用独立类型、独立字段和独立 API。

本设计只覆盖 `mcp_scan` 的 `service` 来源。代码/仓库来源仍使用 Git 地址或代码附件，不使用 MCP 服务连接配置；私有 Git 凭据、OAuth、mTLS 客户端证书、自定义 CA 上传和 MCP 资产台账不在首期范围。TLS 证书链仅使用平台部署时由运维维护的系统信任库；证书不受信任的内部服务在首期保持不可用，而不是允许任务页绕过校验。

## 2. 用户流程

1. 用户或管理员在“凭证配置 → MCP 连接配置”中新建、测试、启用或停用一个 MCP 连接配置；
2. 配置保存后，密钥只保留在服务端加密存储中，后续页面仅显示配置名称、认证类型、启用状态和最近测试结果；
3. 用户进入 `/tasks/mcp/new`，选择“受控运行服务扫描”后，仅从可见且启用的 MCP 连接配置中选择一项；
4. 页面仍要求用户确认已获得安全测试授权；连接配置证明的是访问方式，不等同于扫描授权；
5. 创建请求提交 `connection_config_id` 与在选择时取得的 `connection_config_version`，服务端在同一事务中校验并固定该版本的端点和密钥，再在任务执行时注入给 MCP Agent；
6. 任务详情只展示连接配置的安全标签和认证类型，不展示端点、Token、Header、Cookie、证书或原始连通性响应。

“无需认证”的服务也必须建立一条认证方式为 `none` 的 MCP 连接配置，从而保持服务扫描始终通过同一受治理选择器进入。

## 3. 配置字段

### 3.1 用户可填写字段

| 分组 | 字段 | 是否必填 | 规则与用途 |
| --- | --- | --- | --- |
| 基本信息 | `name`（配置名称） | 是 | 1–80 字符；用于任务选择器、详情安全摘要和审计标签，不能包含密钥。 |
| 基本信息 | `description`（说明） | 否 | 最多 500 字符；说明用途或所属系统，不得把 Token、Header 值写入其中。 |
| MCP 服务 | `server_url`（服务端点） | 是 | 仅允许完整 HTTP(S) URL；不允许 URL UserInfo、Query、Fragment 或内嵌凭据。端点只在受权限控制的配置详情/编辑页中显示。 |
| MCP 服务 | `transport`（传输方式） | 是 | 枚举：`auto`、`streamable_http`、`sse`；`auto` 为默认值，由服务端在测试连接时协商/识别。 |
| 认证 | `auth_type`（认证方式） | 是 | 枚举：`none`、`bearer_token`、`api_key_header`、`custom_headers`。 |
| 认证 | `bearer_token` | 条件必填 | 仅当 `auth_type=bearer_token`；写入后加密保存，只显示“已配置”。 |
| 认证 | `api_key_header_name`、`api_key_value` | 条件必填 | 仅当 `auth_type=api_key_header`；Header 名称必须是受限合法 HTTP Header 名，值加密保存。 |
| 认证 | `headers[]` | 条件必填 | 仅当 `auth_type=custom_headers`；每项为 `name` + `value`，值加密保存，最多 10 项。 |

### 3.2 系统管理字段

| 字段 | 说明 |
| --- | --- |
| `id` | 不透明配置 ID；任务引用时与只读 `version` 组成不可变的配置版本引用。 |
| `version` | 每次有效配置更新递增；任务创建时固定解析/记录版本，避免之后的配置修改影响已创建或运行中的任务。 |
| `owner_scope` | 服务端根据主体、管理员策略和共享规则决定可见范围，浏览器不能伪造。 |
| `enabled` | 只有启用配置可被新任务选择；停用不泄露密钥，也不改变已创建任务的配置快照。 |
| `auth_summary` | 仅返回认证类型和“已配置/无需认证”等安全状态，不返回任何值。 |
| `last_tested_at`、`last_test_status` | 最近一次安全连通性验证的时间和结果摘要。 |
| `created_by`、`created_at`、`updated_at` | 审计和管理元数据。 |

所有影响实际连接的字段（`server_url`、`transport`、`auth_type`、任何 secret 或 Header 名称）更新时，服务端必须创建一个新 `version`，将该版本标为 `not_tested` 并自动设为 `enabled=false`。只有该版本成功测试后，才允许显式启用。仅修改 `name` 或 `description` 不改变有效连接版本和测试状态。任务创建固定引用已启用、测试成功的版本；运行中的任务继续使用创建时已解析的版本。

`GET /mcp-connection-options` 的每个可选项必须返回安全的 `id` 与 `version`（不返回 secret）；响应可带集合 ETag 用于缓存失效。新建任务页将 `id + version` 一同提交。创建事务重新校验主体权限、配置当前版本、启用状态、当前版本测试成功状态与出站策略；若版本已变化或已失效，返回 `409 MCP_CONNECTION_VERSION_CONFLICT`，不静默改用新版本。测试结果只能写回其开始测试时的不可变版本；更新/启用/停用使用 `If-Match` 或等价的期望版本条件，避免并发覆盖。

### 3.3 Header 安全约束

- Header 名必须符合 HTTP token 语法，最长 64 字符；Header 值最长 8 KiB；
- 拒绝 `Host`、`Content-Length`、`Transfer-Encoding`、`Connection`、`Upgrade` 等会影响传输语义的 Header；
- Header 值、Bearer Token 和 API Key 均为写入即加密的 secret，任何 `GET`、任务详情、工作台、报告、日志或审计事件都不得返回原文；
- 编辑时只能“保留现有值”或“替换新值”，不能读取旧值；
- `description`、`name`、任务备注和错误信息均不得回显由服务端识别出的 secret 值。

### 3.4 TLS 与内网出站策略

- 连接配置始终校验服务端 TLS 证书，不提供“跳过证书校验”开关；首期不支持用户上传 CA 或 mTLS 客户端证书；
- 平台允许内网 MCP 服务，但仅当其解析后的地址落在部署管理员配置的 **MCP 出站允许 CIDR 集合** 中；该集合是服务端运行时策略，不是用户可填写字段；
- 环回、链路本地、组播、未指定地址、云元数据地址和不在允许集合内的私有地址始终拒绝；对 DNS 名称在连接前解析并校验实际地址，禁止重定向，以防 DNS 重绑定和 SSRF 绕过；
- 公开地址仍要求任务发起人确认安全测试授权；内网地址同时要求通过上述平台出站策略和任务授权声明；
- `auto` 传输配置新建后默认禁用。只有服务端“测试连接”成功并将识别出的有效传输方式写入**当前配置版本**后才可启用；连接字段更新会生成 `not_tested` 的新版本，不能沿用旧版本的测试结果；任务创建时固定配置版本和该版本已验证的有效传输，不在执行阶段重新猜测协议。

## 4. 凭证配置页面

在“凭证配置”下增加 **MCP 连接配置** 二级入口，建议路由为：

```text
/credentials/mcp-connections
/credentials/mcp-connections/new
/credentials/mcp-connections/:connectionConfigId
```

列表只显示安全字段：名称、传输方式、认证类型、启用状态、最近测试状态、更新时间和管理操作。密钥、完整 Header、Cookie、证书内容和原始端点响应不进入列表。

`GET /mcp-connection-configs/:id` 只面向具有该配置管理权限的主体，返回可编辑的非 secret 字段：名称、说明、服务端点、传输方式、认证类型、Header 名称和“是否已配置值”等状态；不会返回任何认证值。安全审计员只能获得列表级安全摘要，不能读取端点或编辑详情。

上述管理详情是**唯一**允许把 `server_url` 与 Header 名称返回浏览器的例外：它只面向配置管理者，不属于 MCP 工作台、任务新建、任务详情、报告或“任务可选连接”接口。任何 Token、API Key、Header 值、Cookie、证书和原始连通性响应始终不返回浏览器。

新建/编辑页按“基本信息 → MCP 服务 → 认证 → 测试并保存”组织。测试连接由服务端发起最小、非破坏性的 MCP 协议探测；首期不得调用危险工具、不得执行用户工具调用，也不得把原始响应体返回浏览器。用户只能在成功测试后启用配置。

权限建议如下：

- 普通用户：管理自己拥有或被授予管理权限的配置；
- 管理员：按既有治理策略管理组织范围配置；
- 安全审计员：仅查看允许其查看的安全摘要，不创建、编辑、测试或读取 secret。

## 5. MCP 扫描任务引用

服务扫描的新建页 `/tasks/mcp/new` 不显示端点、Token、Header 或 TLS 字段；它只显示连接配置选择器：

- 选项标签：配置名称、传输方式、认证类型、启用状态；
- 仅请求当前主体可使用、`enabled=true` 且当前版本测试成功的配置；每项携带不可编辑的 `version`，供创建时的乐观并发校验；响应 ETag 仅用于选项列表缓存失效；
- 无可用配置时，提供进入“凭证配置 → MCP 连接配置”的入口；
- 创建前保留“我确认已获得该目标的安全测试授权”声明；
- 代码/仓库扫描不加载或提交连接配置选择器。

MCP 专属创建接口使用以下安全形状：

```json
{
  "source": {
    "kind": "service",
    "connection_config_id": "opaque-connection-id",
    "connection_config_version": 7
  },
  "model_id": "optional-opaque-model-id",
  "thread": 4,
  "country_iso_code": "zh_CN",
  "authorization_confirmed": true
}
```

仓库来源使用：

```json
{
  "source": {
    "kind": "repository",
    "repository_url": "https://git.example.internal/team/mcp-server.git"
  },
  "attachment_ids": [],
  "model_id": "optional-opaque-model-id",
  "thread": 4,
  "country_iso_code": "zh_CN"
}
```

仓库来源允许以 `attachment_ids` 替代 `repository_url`，两者必须互斥；它不得提交 `connection_config_id`、`connection_config_version` 或 `authorization_confirmed`。服务来源必须提交 `connection_config_id`、`connection_config_version` 和 `authorization_confirmed=true`，且不得提交附件或原始端点。

## 6. MCP 专属 API

浏览器只调用 MCP 专属资源，不调用通用 `/api/v1/platform/tasks`。

| 操作 | 建议接口 | 说明 |
| --- | --- | --- |
| MCP 工作台概览 | `GET /api/v1/platform/mcp-workbench` | 保留现有指标、活跃任务和风险摘要安全投影。 |
| MCP 任务列表 | `GET /api/v1/platform/mcp-scans` | 支持分页和状态筛选；只返回 MCP 安全任务摘要。 |
| 新建 MCP 任务 | `POST /api/v1/platform/mcp-scans` | 只接受 MCP 专属创建 DTO；要求幂等键与 CSRF。 |
| MCP 任务详情 | `GET /api/v1/platform/mcp-scans/:taskId` | 只返回 MCP 任务的安全详情；非 MCP ID 不以 MCP 详情展示。 |
| 取消 MCP 任务 | `POST /api/v1/platform/mcp-scans/:taskId/cancel` | 复用有界确认与不自动重放语义。 |
| MCP 代码附件 | `POST /api/v1/platform/mcp-scan-attachments` 及其分片、合并和删除子路径 | 使用 MCP 专属上传能力，只产生 opaque 附件 ID，任务页不调用通用任务附件路径；首期不提供浏览器下载源代码附件的能力。 |
| MCP 连接配置管理列表 | `GET /api/v1/platform/mcp-connection-configs` | 返回当前主体可管理的配置（包括已停用和未测试版本）的安全摘要，供凭证配置页面使用；无管理权限的审计员仅在策略允许时读取同一摘要，不获得管理操作。 |
| 读取任务可选连接 | `GET /api/v1/platform/mcp-connection-options` | 只返回当前主体可使用、启用且当前版本测试成功的连接安全摘要及每项 `version`；响应 ETag 仅用于缓存失效。 |
| 创建连接配置 | `POST /api/v1/platform/mcp-connection-configs` | 接收 write-only secret 字段；服务端加密后持久化；要求 `Idempotency-Key`。 |
| 读取连接配置详情 | `GET /api/v1/platform/mcp-connection-configs/:id` | 仅配置管理者可读取非 secret 编辑字段；不返回任何认证值。 |
| 更新/停用连接 | `PATCH /api/v1/platform/mcp-connection-configs/:id` | 支持字段校验、版本化和启用状态管理；要求 `If-Match`/期望版本。 |
| 测试连接 | `POST /api/v1/platform/mcp-connection-configs/:id/test` | 返回脱敏的连通性/协议结果与被测试的版本；要求 `Idempotency-Key`。 |
| 读取扫描模型 | `GET /api/v1/platform/models` | 复用既有受治理模型目录；它是“凭证配置”的模型资源，不是通用任务 API。 |

服务端可以在实现内部复用既有任务持久化、附件、身份、审计和 Agent 调度能力，但 MCP Handler、浏览器 DTO、URL 路径和类型校验必须保持专属边界。

## 7. 安全与错误处理

- 所有 secret 在持久化前加密；日志、审计、错误消息、任务/报告渲染和浏览器响应均执行脱敏；
- 创建任务时再次校验连接配置可见性、启用状态和版本，防止配置在选择后被停用、删除或失去权限；
- 连接测试失败不等同于“配置不存在”，页面分别呈现加载、无权限、无配置、连接失败和可重试错误状态；
- 用户授权声明与连接配置独立记录：前者证明发起人确认测试授权，后者只提供访问方式；
- 后端必须实施第 3.4 节的出站 CIDR 策略、DNS 解析校验、URL 校验、超时、无重定向和速率限制，避免配置接口成为 SSRF 或内网横向访问通道；
- 任务详情只保留来源类别、连接配置安全标签/认证类型、模型引用、并发和阶段等白名单字段；
- 取消、创建和测试连接的网络不确定结果不得自动重复写请求。

所有浏览器非安全方法均要求 CSRF 防护。`POST /mcp-scans`、创建配置、测试连接、取消任务与附件合并使用 `Idempotency-Key`：键作用域为“认证主体 + 租户/组织范围 + HTTP 方法 + 规范化路径”，保留至少 24 小时；同键、同规范化载荷返回原始结果（重复响应为 `200` 并带 `Idempotent-Replay: true`），同键、不同载荷返回 `409 IDEMPOTENCY_KEY_REUSED`。浏览器不自动重放未知结果；用户明确重试时复用同一键以查询/获得原结果。附件分片以 `upload_id + chunk_index + 内容摘要` 幂等，摘要不同时返回冲突。启用、停用和编辑使用版本条件更新；取消任务的重复请求返回同一任务的当前取消状态。

连接配置权限：普通用户只能管理自己拥有或被授予管理权的配置；管理员可按组织策略管理；安全审计员只在策略允许时读取列表级安全摘要，不能创建、编辑、测试、启停或读取详情。对不可见资源返回 `404`，对已知但被角色禁止的写操作返回 `403`，不泄露 secret 或端点。

## 8. 非目标

- 不在 MCP 新建任务页添加 Token、Header、Cookie、TLS 忽略开关或自由文本凭据；
- 不把 MCP 连接配置与模型配置合并为同一种 schema；
- 不支持 OAuth 授权码流、OAuth 客户端凭据、mTLS 客户端证书、自定义 CA 上传、代理配置或私有 Git 凭据；
- 不创建 MCP 资产台账、工具权限画像、告警中心或任意远程请求代理；
- 不向任务列表、工作台、报告或审计事件暴露端点、secret、Header、证书或原始测试响应。

## 9. 验收标准

1. 用户可在“凭证配置”中创建并测试 MCP 连接配置，且 secret 保存后不可读取；
2. MCP 服务扫描新建页只选择启用且可见的连接配置，不录入端点或任何密钥；
3. 服务扫描创建请求携带 `connection_config_id` 与 `connection_config_version`；版本变化会明确冲突，后端只从已校验并固定的对应版本安全解析真实连接信息；
4. 仓库与服务来源的字段、附件和授权声明组合均由服务端严格拒绝无效请求；
5. MCP 专属 API 不接受或返回通用任务 DTO 中不应暴露的字段；
6. Token、Header、Cookie、证书、原始端点和测试响应不会出现在浏览器接口、日志、报告、任务详情或审计文本中；
7. 配置更新/停用、权限变更和测试失败均有明确、无泄漏的用户可见状态。
