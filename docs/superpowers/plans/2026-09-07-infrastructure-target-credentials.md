# 基础设施访问凭据实现计划

**Goal:** 提供管理、引用和安全执行完整流程。
**Architecture:** 私有加密资源 + 版本化任务引用 + assignment 临时认证 + 同源 HTTPS 传输边界。
**Tech Stack:** Go/Gin/GORM/PostgreSQL，React/Fluent UI/React Query，Vitest。

## 1. 传输边界
- [x] 在 pkg/httpx 写并运行失败测试：作用域、HTTPS、Host、跳转和反射脱敏。
- [x] 实现独立 TargetAuth 类型/验证/传输/脱敏；HTTPOptions、common/options、runner 接线。
- [x] 运行相关测试，完成规格与代码评审。

## 2. 加密资源
- [x] 在 internal/platform/targetcredentials 增加失败测试，覆盖 AES-GCM、所有者、版本、敏感输入和响应。
- [x] 实现实体、仓库、服务、Gin handler，复用治理审计和主密钥但隔离 AAD。
- [x] pkg/database 追加 migration 13；服务器启动验证表并注册受保护路由。
- [x] 测试持久化和非 DDL 启动；复核并修正问题。

## 3. 任务到 Agent
- [x] 添加任务 ID/version 参数失败测试，覆盖禁用、跨源、附件、幂等和秘密不持久化。
- [x] 实现任务引用验证与延迟 runtime issuer；扩展 TaskManager 私有通道白名单及事件脱敏。
- [x] Agent 接收 target_auth，校验目标并传递 runner；保持模型认证独立。
- [x] 运行 tasks、websocket、agent、runner 测试；复核并修正问题。

## 4. 控制台
- [x] 添加安全 DTO 和页面行为测试，覆盖不缓存 secret、编辑保留、冲突和任务选择。
- [x] 添加 credentials/target-credentials 列表/表单，复用 Fluent UI 和导航。
- [x] TaskCreatePage 增加可选目标凭据并提交 ID/revision，提示同源 HTTPS 与手动 URL 要求。
- [x] lint/typecheck/test/build；浏览器检查表单和导航。

## 5. 文档与完整验证
- [x] 同步 docs/api/reference.md/.en.md 与 internal/apidocs Swagger 三件套；补操作说明。
- [x] 运行 go test ./...（隔离 PostgreSQL）、前端校验和 apidocs；记录结果。
- [x] 完成规格和安全代码评审，修复后重测受影响范围。
- [x] 更新本地 8089 预览，保留数据，确认仅预期端口监听；交付功能、范围、验证与代码路径。

不自动合并或推送；工作分支 codex/infra-target-credentials 基于 develop f5073af12。
