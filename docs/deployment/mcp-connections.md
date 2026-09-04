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
link-local、multicast、unspecified、云元数据地址及其他不在允许集内的地址。受控
dialer 应将已验证 IP 作为实际目标并保留原主机的 TLS SNI，防止 DNS rebinding。

HTTP 探测和 gateway 转发必须禁用重定向、环境代理、TLS 跳过验证和自定义 CA 绕过，
并设置连接/请求超时及响应体上限。客户端不能提供 allowlist、proxy、TLS bypass、OAuth
或 mTLS 私钥、私有 Git 凭据等覆盖字段。Git Fetcher 还必须禁止重定向、子模块自动
拉取和凭据交互，并只返回内部归档引用。

## 运行与可观测性

探测仅执行最小 `initialize` 握手，不调用 MCP tools。选择 `auto` 时先测试 streamable
HTTP，再测试 SSE；指定 transport 不能回退。只有当前版本探测成功、连接已启用且受控
dialer 可用时，连接才可被任务选择。

日志、审计记录和错误响应只能记录不透明的配置/任务标识、版本和策略结果；不得记录
真实 URL、主机、Header 名称或值、响应体、Cookie、Token、Git URL 或认证失败详情。
发现受控 dialer 不可用、允许集为空或 DNS/策略校验失败时，应保持连接不可用并由部署
人员修复 gateway/Fetcher 配置，而不是临时放宽策略。
