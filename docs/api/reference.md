# AIG Custom Platform API 文档


## 概述

AIG Custom Platform 是基于 Tencent Zhuque Lab AI-Infra-Guard（https://github.com/Tencent/AI-Infra-Guard）构建的独立定制平台，提供了一套完整的API接口，用于AI基础设施扫描、MCP安全扫描、大模型安全体检和模型配置管理。本文档详细介绍了各个API接口的使用方法、参数说明和示例代码。

项目运行后，可访问 `http://localhost:8088/docs/index.html` 查看Swagger文档。

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

- **Base URL**: `http://localhost:8088` (根据实际部署调整)
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
| `GET /api/v1/platform/tasks` | `TaskListResponse` | 普通用户仅本人；审计员/管理员全局。`TaskSummary` 仅含 ID、owner 展示名、规范化类型/状态与时间戳。 |
| `GET /api/v1/platform/reports` | `ReportListResponse` | 普通用户仅本人；审计员/管理员全局。item 是不可变安全摘要。 |
| `GET /api/v1/platform/admin/users` | `UserListResponse` | 仅管理员；不含任何凭据材料。 |
| `GET /api/v1/platform/admin/audit-events` | `AuditListResponse` | 仅审计员/管理员；metadata 递归脱敏。 |
| `GET /api/v1/platform/models` | `CatalogPage` | 普通用户看全局和本人私有 platform 行；审计员只读全局行；管理员看全部 platform 行。token 始终为 `********`，`source` 为 `platform` 或 `yaml`，并显式返回 `read_only`。只读 YAML 行与同 ID platform 行发生碰撞时仍分别保留；目录加载失败时失败关闭。 |

`GET /api/v1/platform/tasks/{taskID}` 返回 `TaskDetail`，其 `input_summary` 仅含有界展示元数据。普通用户只看本人任务，审计员/管理员全局只读；任务不存在或对普通用户不可见时返回 `404`。`GET /api/v1/platform/tasks/{taskID}/result` 已退役：通过认证与首次改密门禁后恒定返回 `410 Gone`，且绝不读取引擎输出。

附件变更请求要求 CSRF。普通用户只能创建/写入本人 opaque 附件，管理员可治理任意附件，审计员只读。下载时，owner 成功，其他普通用户得到 `404`，审计员得到 `403`，管理员可跨 owner 下载。管理员跨 owner 打开存储前，服务端必须先持久化脱敏的 `attachment.download_authorized` 授权事件；该事件证明授权而非后续流传输成功，响应也不返回存储位置。

## 任务 API 迁移边界

浏览器任务执行只能使用上文受保护的平台任务 API。原 `/api/v1/app/taskapi*` 和 `/api/v1/app/tasks*` 浏览器路由族仅是历史名称，不是可调用的兼容 API；只有通过正常会话、首次改密与 CSRF 校验后才返回 `410 Gone`（CSRF 适用于变更请求）。它们不能用于创建任务、上传、查询状态、获取结果、流式更新，也不能在连接中断后作为回退。

平台任务创建仅引用受治理模型 ID；明文模型凭据和旧 model 对象会被拒绝。浏览器附件只使用 opaque 附件 ID，并按 owner 隔离。内部 Agent WebSocket 与旧形状制品传输属于独立的 internal-token 边界，不是浏览器 API。

### 受保护平台边界

浏览器客户端使用安全 Cookie session 认证。匿名请求返回 `401`；已认证但无权限的 Subject 返回 `403`。身份和角色请求头绝不是认证回退。

平台任务使用 `GET /api/v1/platform/tasks`、`POST /api/v1/platform/tasks`，以及按 owner 授权的详情、取消、结果和 opaque 附件操作。创建必须携带 `Idempotency-Key`；任务 owner 始终来自认证 Subject。普通用户只能看到本人任务，审计员全局只读，管理员可治理全部任务。网络 ACK 无法可信确认时进入 `dispatch_unknown`，且绝不自动再次提交。

独立受治理模型 API 是 `/api/v1/platform/models`。已弃用的 `/api/v1/app/models/{modelId}` facade 仅保留模型兼容：集合 DELETE 与嵌套请求体继续使用 `{status,message,data}` envelope 和 HTTP `200` 应用错误约定。响应凭据始终脱敏。YAML 模型 ID 不能遮蔽加密平台行；YAML 加载失败时，在数据库或审计变更前失败关闭。

只有 `aig migrate` 可以执行数据库 DDL。全新或升级后的 PostgreSQL schema 到达 v8；runtime 启动只做校验。迁移可安全处理旧表为空的情况，且不依赖 runtime AutoMigrate。版本 8 新增 `idx_platform_tasks_updated_at` 与 `idx_platform_tasks_owner_updated_at`，分别支持全局和 owner 范围按 `updated_at DESC, id DESC` 稳定读取最近任务。

## 不可变报告与品牌 API

以下接口均使用 Cookie 中的认证 Subject，并经过首次改密门禁。普通用户只能读取和导出本人报告；审计员可以全局只读与导出，但不能变更；管理员可以全局读取/导出，并可治理品牌或补建缺失快照。浏览器提交的身份请求头和原始引擎结果一律不可信。

### 报告列表、趋势与详情

- `GET /api/v1/platform/reports?page=1&page_size=20` 返回 `ReportListResponse` 分页安全摘要 envelope。`page` 从 1 开始；`page_size` 默认 20、最大 100。摘要仅包含报告/任务 ID、时间、风险摘要与快照中的产品名。
- `GET /api/v1/platform/reports/trends?days=30` 返回服务端计算、补零且包含当天的 UTC 自然日桶；`days` 范围为 1 到 30。浏览器不得从不完整列表自行推导趋势。
- `GET /api/v1/platform/reports/{reportID}` 返回安全详情，其中 `render` 是任务完成时保存的不可变 RenderModel。

不可变 `report-render-v2` RenderModel 固化风险映射版本、生成/完成时间、任务元数据、产品名/主色/水印、风险评分及评分说明、风险分布、`risk_trend` 中 30 个固定 UTC 日桶、Top 风险、含证据/影响/修复的技术发现、建议、覆盖范围和结论。技术发现仅按四类可信引擎的显式 schema 白名单映射，完成脱敏和严重度排序后最多保留 50 条；提示词、会话、附件、截图、凭据、URL 查询参数和用户绝对路径都不会进入 RenderModel。在线详情与自动分页 PDF 重试只消费同一个模型。列表与详情契约明确分离：绝不暴露原始引擎结果与 Logo 字节，也不暴露存储的渲染载荷、owner ID、文件路径或当前可变品牌记录。列表仅接受 `page=1..1000`、`page_size=1..100`（默认 20）。

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

此兼容 API 不支持匿名访问。登录前先 GET `/api/v1/auth/csrf`；登录请求同时携带 `aig_csrf` Cookie 与 `X-CSRF-Token` 请求头，并在持久 Cookie jar 中保留登录后轮换的 `aig_session` 与 `aig_csrf` Cookie。`must_change_password=true` 的主体必须先改密，才能调用任何模型接口。以下每个变更请求都会把当前 `aig_csrf` Cookie 值再次放入 `X-CSRF-Token`。

```python
import requests

base_url = "http://localhost:8088"
session = requests.Session()  # 持久 Cookie jar
bootstrap = session.get(f"{base_url}/api/v1/auth/csrf")
bootstrap.raise_for_status()
login = session.post(
    f"{base_url}/api/v1/auth/login",
    json={"username": "<username>", "password": "<password>"},
    headers={"X-CSRF-Token": bootstrap.json()["csrf_token"]},
)
login.raise_for_status()
csrf_headers = {"X-CSRF-Token": session.cookies.get("aig_csrf")}
```

cURL 示例需要在两次初始化请求间持久化 Cookie。只替换尖括号占位符；登录后从 `cookies.txt` 读取更新后的 `aig_csrf` 值，作为 `<session-csrf-token>`。

```bash
curl -sS -c cookies.txt http://localhost:8088/api/v1/auth/csrf
curl -sS -b cookies.txt -c cookies.txt -X POST http://localhost:8088/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <bootstrap-csrf-token>" \
  -d '{"username":"<username>","password":"<password>"}'
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
    url = "http://localhost:8088/api/v1/app/models"
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
curl -b cookies.txt -X GET http://localhost:8088/api/v1/app/models \
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
    url = f"http://localhost:8088/api/v1/app/models/{model_id}"
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
curl -b cookies.txt -X GET http://localhost:8088/api/v1/app/models/gpt4-model \
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
    url = "http://localhost:8088/api/v1/app/models"
    headers = {**csrf_headers, "Content-Type": "application/json"}
    data = {
        "model_id": "my-gpt4-model",
        "model": {
            "model": "gpt-4",
            "token": "sk-your-api-key-here",
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
curl -b cookies.txt -X POST http://localhost:8088/api/v1/app/models \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <session-csrf-token>" \
  -d '{
    "model_id": "my-gpt4-model",
    "model": {
      "model": "gpt-4",
      "token": "sk-your-api-key-here",
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
    url = f"http://localhost:8088/api/v1/app/models/{model_id}"
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
    url = f"http://localhost:8088/api/v1/app/models/{model_id}"
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
curl -b cookies.txt -X PUT http://localhost:8088/api/v1/app/models/my-gpt4-model \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <session-csrf-token>" \
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
curl -b cookies.txt -X PUT http://localhost:8088/api/v1/app/models/my-gpt4-model \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <session-csrf-token>" \
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
    url = "http://localhost:8088/api/v1/app/models"
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
curl -b cookies.txt -X DELETE http://localhost:8088/api/v1/app/models \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <session-csrf-token>" \
  -d '{
    "model_ids": ["my-gpt4-model"]
  }'

# 批量删除多个模型
curl -b cookies.txt -X DELETE http://localhost:8088/api/v1/app/models \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <session-csrf-token>" \
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
  token: sk-your-api-key
  base_url: https://api.deepseek.com/v1
  note: 系统默认模型
  limit: 1000
  default:
    - mcp_scan
    - ai_infra_scan

- model_id: eval_model
  model_name: gpt-4
  token: sk-your-eval-key
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
