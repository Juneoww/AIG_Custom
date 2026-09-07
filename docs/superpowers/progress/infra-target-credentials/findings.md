# 发现

- 当前 ai_infra_scan params 仅 model_id/timeout/port_scan_mode；通用 Headers 不适合承载受治理目标认证。
- MCP 已有 assignment-only RuntimeIssuer，TaskManager 目前只准 MCP 使用；可扩展但必须保持严格白名单。
- HTTPX 默认跳转且关闭证书验证；runner 也手动跟随跳转。目标凭据模式必须在实际 transport 校验。
- 模型凭据已有 AES-GCM keyring 和治理审计；新资源使用独立 AAD 域。
- 主机无 Go，用缓存构建镜像；测试需独立 PostgreSQL。
- .worktrees 被根仓库忽略；用户原有 worktree 和未跟踪文件未改。
- 凭据失效不能自动清空选择，否则会隐式降级匿名扫描；现已保留失效选择并阻止提交。IPv4-mapped IPv6 需要规范化双方后比较。
- Chromium 截图不经过受控传输，认证模式已跳过截图和视觉分析；保留 HTTP 证据与文本分析。
- 迁移表重放使用 CreateTable/列验证/幂等索引，避开现有 GORM 对已存在表的 AutoMigrate 错误；数据库整包 69.416s 通过。
- Agent 的 LoadRemoteFingerPrints 使用旧 X-APIKey 和 size=9999，现有知识库路由返回 401；不能放宽浏览器鉴权。标准 Agent 镜像含完整 data，认证扫描改用随版本的本地规则。
- 完整 Go 测试剩余失败属于基线：Agent 大消息超时、preload/favicon/文件检索缺本地测试素材、缺 Chromium、旧 WebSocket 接口测试仍期待历史授权状态。
- 旧 runner 会忽略认证主请求失败并输出成功摘要；新认证路径汇总固定安全错误、排空已有结果并等待分析结束，401/403 不继续探测、不生成成功报告。
- YAML 语法解析成功不保证规则有效，旧漏洞库读取还会跳过不可读文件；认证路径已增加规则必需字段与逐文件严格读取，空规则或不完整库均返回初始化错误，匿名兼容保持。
