# MCP 连接的受控出站部署

MCP 服务连接的真实端点、认证材料和自定义 Header 只保存在平台的加密版本记录中。
浏览器、任务选项、审计摘要和 Agent 均不得获得这些材料。部署时必须将 MCP 的实际
出站能力交给受控的 `mcpegress` gateway；Git 来源则必须由同一受控边界中的 Fetcher
获取并产出内部代码归档引用。Agent 不得直接连接 MCP 端点、不得直接 clone Git URL，
也不得获得可绕过 gateway/Fetcher 的代理、凭据或 TLS 配置。

## 服务端允许集

以下变量只能由平台部署配置提供，使用英文逗号分隔；它们不是浏览器、任务或连接
配置可填写的字段。空值表示没有允许地址，运行时必须拒绝出站请求。

| 变量 | 作用 |
| --- | --- |
| `MCP_OUTBOUND_ALLOWED_CIDRS` | MCP 服务端点可连接的 CIDR 集合。 |
| `MCP_GIT_OUTBOUND_ALLOWED_CIDRS` | Git Fetcher 可连接的 CIDR 集合。 |
| `MCP_GIT_ALLOWED_HOSTS` | Git Fetcher 可访问的精确主机名集合。 |

允许集变更应走受控部署变更流程并重启相应 gateway/Fetcher 进程。不要在应用配置、
任务参数、前端状态、日志或版本库中放入真实端点、认证 Header、Cookie、Token 或私有
Git 凭据。网络防火墙规则应与这些允许集保持一致，但不能替代应用内的逐连接校验。

## 不可绕过的执行期策略

gateway 和 Fetcher 必须使用受控 `DialContext`/dialer；没有该实现时，平台必须 fail
closed：连接不能探测、不能启用、不能进入任务选项，也不能创建使用该连接的任务。DNS
预检不等同于 egress 控制，绝不能退化为普通 `net.Dialer`、环境代理或 Agent 直连。

每次请求和每次真实拨号前均须重新解析并校验目标地址。仅允许 HTTPS URL，拒绝
UserInfo、query 和 fragment；解析结果必须同时满足相应 CIDR 允许集，并拒绝 loopback、
link-local、multicast、unspecified、云元数据地址（包括 `fd00:ec2::254`）及其他不在允许集内的地址。受控
dialer 应将已验证 IP 作为实际目标并保留原主机的 TLS SNI，防止 DNS rebinding。

HTTP 探测和 gateway 转发必须禁用重定向、环境代理、TLS 跳过验证和自定义 CA 绕过，
并设置连接/请求超时及响应体上限。客户端不能提供 allowlist、proxy、TLS bypass、OAuth
或 mTLS 私钥、私有 Git 凭据等覆盖字段。Git Fetcher 还必须禁止重定向、子模块自动
拉取和凭据交互，并只返回内部归档引用。

## 认证与自定义 Header 边界

连接可使用 Bearer、API key Header 或自定义 Header 认证；Bearer/API key 可以附带独立
的自定义 Header。探测时自定义 Header 先写入，受管理的 Bearer/API key 随后覆盖同名
认证 Header，MCP 协议 Header 最后写入。因此内网服务可使用 `Authorization` 或普通业务
Header，但用户材料不能改变探测协议本身。

每个自定义 Header 名必须是经 trim 后不超过 64 字节的 HTTP token；最多 10 个，每个值
不超过 8 KiB，且名称按大小写无关去重。拒绝 `Host`、`Content-Length`、`Transfer-Encoding`、
`Connection`、`Keep-Alive`、`Upgrade`、`TE`、`Trailer`、`Proxy-*`、`Content-Type`、`Accept`、
`Cookie`、`Set-Cookie`、会话控制 Header 以及所有 `MCP-*` 名称。还必须拒绝会影响可信
代理、来源或路由判定的 `Forwarded`、`X-Forwarded-*`、`X-Real-IP`、`X-Original-URL`、
`X-Rewrite-URL`、`Via` 与 HTTP method override Header。Header 名中一律不允许 `_`，并
拒绝 `X-Host`/Host override、`X-Original-*`、`X-Rewrite-*` 及受控代理实现的路由别名。
不得以 Header 传入路由、会话或协议覆盖；名称和值同样不得被记录、回显或投影到任务/审计页面。

即使载荷来自已加密的历史连接版本，探测端口也必须在发起任意 HTTP、SSE、initialized
notification 或 session cleanup 请求前重新规范化并严格验证认证 shape、secret、Header
数量、名称、值和大小写无关去重。任一旧载荷不合规时不得发起网络请求，并只返回固定的
探测失败结果；不能因其曾经落库而降低 Header 或认证策略。

## 运行与可观测性

探测仅执行最小 `initialize` 握手，不调用 MCP tools。Streamable HTTP 使用 POST，固定
`Content-Type: application/json` 和同时接受 JSON/SSE 的 `Accept`；它依据实际响应媒体类型
读取 JSON 或 SSE initialize 响应，但仍识别为 Streamable HTTP。合法响应必须匹配发出的
request id 和协议版本，并包含对象形态的 capabilities、带非空 name/version 的 serverInfo。
握手成功后必须发送 `notifications/initialized`；若响应创建了 MCP session，则无论后续成功
或失败都通过相同的受控 client 尽力发送同 origin DELETE 清理 session。session ID、上游响应
内容和错误详情永不输出。

legacy SSE 不是 POST 加 `Accept: text/event-stream` 的替代品：探测先以受控 GET 打开 SSE
流，读取 `endpoint` event，再只向同 scheme、host、port 的派生 message endpoint POST
initialize，并在原 SSE 流中等待匹配的 `message` response，随后发送 initialized notification。
派生 endpoint 可携带服务端创建的会话 query，但不能跳转到其他 origin；所有步骤受同一
request context 和响应字节上限约束。

选择 `auto` 时先测试 Streamable HTTP，再测试 legacy SSE；指定 transport 不能回退。探测默认
最短间隔为 1 分钟，按不透明连接配置 ID 独立限流，不能以 endpoint 作为限流键。只有当前版本
探测成功、连接已启用且受控 dialer 可用时，连接才可被任务选择。真实探测失败（限流除外）
必须把启动时的不可变版本写为 `failed` 并清空 detected transport；若该版本仍是 current，
同一事务必须撤销 enabled，避免留下 `enabled + failed` 的误导状态。限流判断必须发生在任何
持久化状态重置之前。通过限流后，探测启动事务会先按 config → current version 加锁、禁用连接、
清空旧结果并递增 `resource_revision`，把新 revision 作为不透明 attempt token；结果写回仅在
config、version 与该 token 仍精确匹配时允许，并在结算时再次推进 revision，使同一结果不能重放。
新探测、新版本、展示元数据或启用状态变更都会让旧 token 失效；任何跨 Engine/进程的迟到结果
必须返回安全冲突（或被安全忽略），不得改写任意版本的结果投影。

启用与探测结果写回都必须按 config 后 current/version 的顺序加锁。启用事务除了复核 expected
current version 和 resource revision，还必须锁定并检查该 current version 仍为 `passed` 且具有
具体 transport；若锁内发现 failed/not_tested，防御性禁用必须先提交，再返回 unavailable，不能
因事务内返回业务错误而回滚该禁用。探测失败与启用交错时，最终连接必须保持不可用。任务选项、任务连接验证和
重新启用还必须解密 current payload，并以**当前** `ValidateServerURL` 允许集/DNS 策略复核
endpoint：允许集收紧、清空、解析失败或材料不合规时，任务选项静默排除，验证/启用只返回
固定的 unavailable 或 denied 结果。历史 probe 通过不等于当前仍可使用。

连接名称和说明是受限的展示文本：不得包含 URL、认证/请求头关键词、疑似 token 或控制字符。
审计员只能读取安全摘要，不能读取任务选项或验证任务连接；任务选项不返回连接说明。

日志、审计记录和错误响应只能记录不透明的配置/任务标识、版本和策略结果；不得记录
真实 URL、主机、Header 名称或值、响应体、Cookie、Token、Git URL 或认证失败详情。
发现受控 dialer 不可用、允许集为空或 DNS/策略校验失败时，应保持连接不可用并由部署
人员修复 gateway/Fetcher 配置，而不是临时放宽策略。
