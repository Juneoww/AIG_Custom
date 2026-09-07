# HTTP 目标凭据实现计划

## 后续简化：用户已明确确认

- [x] 更新回归用例并见 RED：默认 HTTP 创建、编辑、解析，无开关且不发送许可字段，API 缺省/false 可用。
- [x] 移除 UI 开关和许可提示，公共 API 按协议接受 HTTP/HTTPS；保留内部派生许可和同源边界。
- [x] 同步中英文档和 Swagger，运行相关 Go、前端全量检查并独立复核。
- [x] 更新 8089 预览，实测无开关的 HTTP 保存及扫描，清理临时资源并记录。

> 使用 subagent-driven-development 执行独立传输任务，根任务实现资源和 UI；按 requesting-code-review 完成独立复核。

**Goal:** 支持用户内网 HTTP 服务的显式认证扫描。
**Architecture:** 写请求显式许可、origin 持久化协议与 AAD、runtime 许可、同源 HTTP/HTTPS 传输。
**Tech Stack:** Go / React / Vitest / PostgreSQL / Docker。

- [x] 审查上述规格及本计划；用户此前已确认 HTTP 开关方案，无新增待确认决策。
- [x] 传输：pkg/httpx/target_auth.go、target_auth_transport.go 和 tests 添加可选 HTTP 许可、端口和重定向边界。先运行 HTTP 用例看到拒绝，再实现并运行 `go test ./pkg/httpx`。
- [x] 接线：common/agent/target_credentials.go、common/runner/runner.go 显式传递 runtime 许可，Agent 能力 v2；补 Agent/runner 用例。
- [x] 资源：internal/platform/targetcredentials/{entity,service,handler}.go 增加 Input/View 许可，HTTP 写入强制确认、View/runtime 从 origin 推导；先添 JSON 输入测试并见拒绝，再实现。运行 `go test ./internal/platform/targetcredentials ./internal/platform/tasks`。
- [x] UI：features/target-credentials/{api,TargetCredentialFormPage,TargetCredentialSelector,TargetCredentialListPage} 和 tasks/TaskCreatePage 更新开关、解析和提示；先 Vitest RED 再实现，验证 HTTPS 兼容和 HTTP 完整流程。
- [x] 同步 docs/api/reference.md/.en.md 以及 Swagger YAML/JSON/docs.go，运行 `go test ./internal/apidocs`；不运行默认 swag init。
- [x] 按修改范围运行 Go 和控制台 lint/typecheck/test/build，独立代码复核。
- [x] 构建并运行最终 8089 预览；UI 和隔离 HTTP 靶场完成端到端验证，清理本次临时资源并记录。

实现保留在当前工作树，不自动提交、合并或推送。
