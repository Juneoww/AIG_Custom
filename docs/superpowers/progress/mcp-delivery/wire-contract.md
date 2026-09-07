# MCP 页面与服务端联调合同

本文件记录实现时的精确 wire 名称；产品功能仍以已批准的两份 MCP 设计为准。

## 扫描

- 基址 `/api/v1/platform/mcp-scans`；GET 列表参数 `page/page_size/status`，响应 `{items,total,page,page_size}`。
- 列表项 `{id,owner,status,source_kind,created_at,updated_at}`；详情另加 `input_summary:{language,source_kind,model_id?,thread?}`、`report_id?`。不返回通用 task_type、remark、端点、附件名称、任务原始参数。
- POST 为扁平 JSON：`source_kind` 必须 repository/service，`model_id?`、`thread?`（默认 4，1–32）。仓库恰选 `repository_url` 或非空 `attachment_ids`；服务只用 `connection_config_id`、`connection_config_version`、`authorization_confirmed:true`。禁止 task_type、content、country_iso_code 等额外字段，语言由后端固定 zh_CN。
- 创建响应 `{task_id,status}`，202；重放 200 + `Idempotent-Replay:true`。取消 POST `/:id/cancel`，body `{}`，响应同样 `{task_id,status}`。
- `/api/v1/platform/mcp-scan-attachments` 支持 POST file、POST `/chunked`（filename/size）、POST `/:id/chunks`（chunk_index/chunk）、POST `/:id/merge`（total_chunks/file_size）、DELETE `/:id`。安全响应 `{id,state,size,max_file_bytes,max_chunk_bytes}`，不含名称与下载 URL。

## 连接配置

- `/api/v1/platform/mcp-connection-configs` GET 返回 `{items}`，POST 创建；`/:id` GET 管理详情、PATCH 更新；`/:id/test` POST 测试。
- 创建：`{name,description?,server_url,transport,authentication:{kind,header_name?,secret?},headers?:[{name,value}]}`。服务端固定个人范围 private，拒绝客户端 scope。传输枚举沿用领域 auto/http/sse（UI 显示自动识别/Streamable HTTP/SSE）；认证枚举 none/bearer/api_key_header/custom_headers。
- 列表摘要：`id,name,description,scope,current_version,resource_revision,enabled,transport,detected_transport?,probe_status,authentication_kind,created_at,updated_at`。
- 管理详情另加 `server_url,authentication_header_name?,authentication_configured,headers:[{name,configured}]`。仅配置管理者可读；任何 secret/header value 都不返回。
- PATCH 允许上述字段的部分更新；不填 secret 或 Header value 表示保留同名原值。启停单独发 `{enabled:boolean}`，不可混入其他修改。连接材料变化追加不可变版本并撤销启用/探测状态；只改名称说明不影响版本/测试。
- PATCH、test 使用当前 `If-Match: "<resource_revision>"`；写操作均携带 `Idempotency-Key` 与现有 CSRF。配置写响应为 `{id,current_version,resource_revision,status}`，测试 status 为 passed/failed；前端成功后重新 GET，无 secret 进入 Query cache。
- `/api/v1/platform/mcp-connection-options` GET `{items}`；每项 `{connection_id,connection_version,name,scope,transport,authentication_kind}`，只含可用连接，不含 URL/Header。

## 交互与错误

- 写入网络结果未知时不自动重复；显式重试同载荷复用幂等键，修改载荷才换键。
- 服务端错误仅返回固定 `{error,code}`；配置变更/过期为 409 `MCP_CONNECTION_VERSION_CONFLICT`，幂等冲突为 409 `IDEMPOTENCY_KEY_REUSED`。页面不要打印或渲染原始响应文本。
- 旧通用 MCP task endpoint 返回 409 `MCP_SPECIALIZED_ENDPOINT_REQUIRED` 和安全 `specialized_path`。
- 旧 `/tasks/new?task_type=mcp_scan` 只 replace 到 `/tasks/mcp/new` 并移除原 query，不从 URL 填充秘密。
