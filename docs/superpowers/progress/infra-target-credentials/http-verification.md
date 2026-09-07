# HTTP 显式许可增量验收

用户明确要求内网 HTTP 请求可用。沿用 codex/infra-target-credentials 工作树，未更改数据库结构或已有 HTTPS 密文。

## 已验证

- 资源测试 RED：明确许可的 HTTP 创建仍被旧版 HTTPS-only 服务拒绝；实现后 HTTP 创建、编辑留空保留、每次写入许可、协议变更换密钥、AAD 防协议篡改和安全 View 均通过。
- 资源 / 任务 / WebSocket 定向集成 GREEN：targetcredentials 0.558s、tasks 0.019s、websocket 1.008s。包括隔离 PostgreSQL、HTTP 私有 assignment、无许可拒绝、v1 Agent 在解密前拒绝。
- 传输 / Agent / runner 定向 GREEN：`go test ./pkg/httpx ./common/agent ./common/runner -run 'TargetAuth|TargetCredential|NormalizeTargetOrigin|ValidateTargetURLs' -count=1`；0.072s / 25.293s / 0.249s。覆盖真实 HTTP 认证、反射脱敏、默认端口、同源跳转、越界请求零拨号、同端口跨协议拒绝及 TLS 信任不放宽。
- 前端 RED 12 failed / 77 passed，GREEN 3 文件 89/89；typecheck 通过。开关默认关闭、显式 HTTP 提交、编辑预填、旧 HTTPS 响应兼容与任务范围均覆盖。
- Swagger 契约 RED 捕获缺少 allow_insecure_http；同步三件套后 internal/apidocs 整包通过（1.012s）。
- 规格、后端和前端独立复核均 Approved；git diff --check 通过。

## 最终检查

固定 Node 22.18 / pnpm 10.15 的 Docker frontend-builder 完成 prepare:fonts、lint、typecheck、test:run、build：50 个文件、743 个测试通过。

完整 `go test -p 1 ./... -timeout 180s` 在隔离 PostgreSQL 执行完毕，退出码 1。14 个失败测试与未修改 develop 的基线逐项对应，无新增失败；原因仍为大消息超时、缺本地测试素材/Chromium、旧接口状态预期。凭据资源、任务服务、数据库、httpx、runner、apidocs 等相关包通过。不能将全量 Go 结果宣称为全绿。

## 本地 HTTP 实测

- UI：开关默认关闭，HTTP 未确认保存被阻止；勾选后创建成功。编辑时开关自动回填，密钥保持空白，直接保存可保留原认证并使版本 1→2。已有用户 HTTPS 配置未修改。
- 扫描任务 `c9ef9dd0-a7f1-5ac6-a90f-2d8c3efdcaef` 实际完成，报告 `08e2edc2-acc1-4a2e-ab9e-1abebf5cab1d`；靶场收到 103 次正确认证请求、0 次拒绝。任务选择器按 HTTP 显示同源要求。
- 靶场故意在正文、响应头和 Cookie 回显虚构 Token；task/session 参数、21 条事件、原始/渲染报告均无 Token，session 无 target_auth 字段，凭据密文检查无明文。
- 临时 HTTP 凭据通过 UI 删除，本次靶场、开发和测试数据库容器已移除。带明确验收备注的任务/报告保留；用户配置和预览数据卷保留。
- 最终镜像 `aig-infra-credentials:local` 摘要 `sha256:3e93ecd142be6caf8716c63d4bbba8450b5f573f2a4bda62b0a7131008b50589`，Web 与 PostgreSQL 健康、Agent 运行。仅 `127.0.0.1:8089` 监听，8088/8090/4176 空闲。
- 根工作区原有未跟踪文件保持不变，实现留在独立工作树，未提交、合并或推送。

本轮产物：credentials-http-service-tests.log、credentials-http-go-tests.log、credentials-http-console-build.log、credentials-http-final-build.log；日志位于本任务外部产物目录。
