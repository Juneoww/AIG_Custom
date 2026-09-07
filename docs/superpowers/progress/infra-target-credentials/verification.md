# 验证记录

工作分支 codex/infra-target-credentials，基于 develop f5073af12。测试使用隔离 PostgreSQL，不修改预览用户数据库。

## 已通过

- 控制台：固定 Node 22.18 / pnpm 10.15 的 Docker frontend-builder 执行 prepare:fonts、lint、typecheck、test:run、build，50 个测试文件、733 个测试全部通过。
- Go 整包：internal/platform/targetcredentials、internal/platform/tasks、pkg/database、pkg/httpx、common/runner、internal/apidocs。
- 新增 Agent/TaskManager 定向回归：凭据能力协商、任务与 session 不保存密钥、响应/事件反射脱敏、缺上下文拒收、断连清理、认证扫描跳过 Chromium。
- UI 实测：凭据创建；编辑密钥留空保留且版本 1→2；不同源的 HTTPS 目标被本地阻止；新增凭据菜单与列表可见。
- 构建：主服务和 Agent 的 Go 二进制构建通过；集成镜像已在 8089 运行。数据库迁移前已有备份。
- 运行时：认证任务读取随 Agent 发布的本地规则，实际加载 110 个指纹、1,449 条中文 / 1,443 条英文漏洞规则；不再调用旧知识库 API。TLS/网络/同源校验错误、401/403 会让任务失败，等待已启动的分析结束后不生成成功报告。

## 完整 Go 检查的基线限制

已执行 go test -p 1 ./... -timeout 180s，退出码 1。剩余 6 个包、14 个失败测试均能在未修改 develop 基线中对应找到：

| 范围 | 原因 |
| --- | --- |
| common/agent | TestLargeDataSend 大消息读取超时 |
| common/fingerprints/preload | 测试相对路径下缺 anythingllm.yaml |
| common/utils | favicon 测试素材/环境缺失 |
| common/utils/chromium | 本地测试镜像没有 Chromium |
| common/websocket | 5 个旧接口测试仍预期早期的授权/参数返回状态 |
| internal/mcp/utils | 5 个文件检索测试依赖的本地文件路径不存在 |

任务产物目录保留 infra-target-credentials-go-tests.log 和 infra-target-credentials-baseline.log，便于逐项比对。本次数据库版本变更引起的测试清理/迁移重放问题已修复，数据库整包通过。

## 端到端状态

本地 Docker 网络中的 HTTPS 靶场使用单独测试 CA 和虚构 Token，无宿主端口映射。初次验证发现旧 Agent 的远程规则 API 返回 401，已通过认证任务读取本地规则解决，未放宽知识库鉴权。

- 正向任务 `ea3bc65a-aade-51a6-bffe-528ea504db26` 实际完成，靶场收到 103 次通过认证的请求，生成报告 `cabc624b-b018-4298-9553-8d15c1166d7c`。
- 靶场故意在正文、响应头及 Cookie 回显测试 Token；数据库核查 task/session 参数、21 条任务事件、报告原始和渲染快照，均无测试 Token，session 无 `target_auth` 字段。
- 浏览器可打开已完成任务和报告；密钥编辑留空保留、版本递增、跨源 URL 拒绝也已实测。
- 反向任务 `31ac525e-173e-5693-84c0-18abb105f829` 使用虚构错误 Token，靶场仅新增 1 次 401；任务状态 failed，成功报告数为 0，11 条事件无 Token。
- 临时测试凭据已通过界面删除，HTTPS 靶场容器已移除，Agent 额外测试 CA 已移除。带验收备注的任务与报告保留。
- 最后增量回归通过：13 类规则初始化错误、修复后同进程恢复、匿名宽松读取兼容及认证主请求失败；runner 0.437s、Agent 20.846s、vulstruct 0.034s。涵盖空白、仅注释、零值对象、混合可读与坏符号链接；实际中英文规则数量保持完整。独立复核结果 Approved。

## 最终预览与资源

- 最终镜像 `aig-infra-credentials:local`，摘要 `sha256:b15c50bbaff591d4bcecab4c44699557c9235cb48f3e7762c2911a463842ca0e`，主服务和 Agent 均构建并运行。
- Web 服务和 PostgreSQL 健康；Agent 运行，不再挂载测试证书或设置 SSL_CERT_FILE。
- 预览监听 `127.0.0.1:8089`；同次检查中 8088、8090、4176 无监听。
- 本次临时开发、基线、测试数据库和 HTTPS 靶场容器已移除。用户预览数据库、上传、日志卷保留；迁移前数据库备份仍在预览产物目录。
- 根工作区原有未跟踪内容未改。实现保留在独立工作树，未提交、合并或推送。
