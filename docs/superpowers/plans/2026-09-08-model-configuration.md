# Model Configuration Implementation Plan

> 使用 subagent-driven-development 实施独立后端任务，主任务负责前端、文档和集成；测试驱动并逐步复核。

**Goal:** 将供应商模型改为模型ID、补全示例并支持保存前模型连通性测试。
**Architecture:** 新增专用受治理模型测试接口；后端管理授权、密钥解析和受限网络请求，前端仅持有当前表单输入和固定测试结果。兼容现有模型 CRUD、字段和权限，不改数据库结构。
**Tech Stack:** Go/Gin/PostgreSQL、React/Fluent/Vitest、Docker。

## Task 1：接口及受控探测

文件：internal/platform/models/probe.go、probe_test.go，common/websocket/model_probe_api.go、model_probe_api_test.go，model_api.go 注册入口。

- [x] 先写用例验证缺少测试能力的 RED，再实现并检查 GREEN。
- [x] POST /api/v1/platform/models/test 与 /:modelID/test，输入 provider_model、base_url、token；后者 Token 留空时仅在可写权限和保存 URL 相同的情况下解析原 Token。
- [x] 在 models.Service.Update 和表单保存入口同步限制更改基础 URL 必须提供新有效 Token，覆盖先改址留空保存再测试的绕过。规范化比较完整基础 URL，保留路径；尾斜线及默认端口语义等价变化可保留原 Token。
- [x] 返回 {status,code,message,elapsed_ms}；status 为 success/error。成功 code=ok；固定错误类别 invalid_config、authentication_failed、model_not_found、rate_limited、timeout、network_error、invalid_response、upstream_error、redirect_blocked、busy、unavailable。请求/权限失败沿用 400/401/403/404/429/500，已执行的探测返回 200 的安全结果对象。
- [x] HTTP/HTTPS、合法内网地址、精确端点、DNS/拨号校验、禁用代理与重定向、TLS 校验、30 秒超时、64 KiB 响应上限、单次请求不重试。禁止环回、链路本地、组播、未指定地址及已知元数据地址；测试用注入 transport/dialer，生产无放宽开关。
- [x] 并发和频率限制、Cookie/CSRF/角色门禁、固定安全审计；无凭据/响应原文落盘。测试不可更改已保存模型。
- [x] go test ./internal/platform/models 以及 common/websocket 中新增探测用例；覆盖授权、脱敏、取消、各响应错误和跳转边界。

## Task 2：前端

文件：web/console/src/features/models/ModelForm.tsx、ModelListPage.tsx、api.ts，新增 ModelProbe.test.tsx/API 测试。

- [x] 先写表单真实交互用例，执行 Vitest 见 RED。
- [x] 表单与列表改为模型ID；所有空白输入增加示例；调用限制空白提交 0，已有值保留。
- [x] 实现测试 API 安全 DTO 投影和固定错误映射。
- [x] 测试按钮 type=button、使用当前参数且无需名称；测试中禁用重复点击/保存/请求字段，允许取消离开。编辑复用原 Token 的 URL 约束与后端一致。
- [x] 结果显示简明状态/耗时，影响连接的字段变化清除结果；不保存模型、不缓存 Token、退出取消、迟到结果不污染新表单。
- [x] 运行定向 Vitest，再运行完整 lint/typecheck/test/build。

## Task 3：接口文档与独立复核

- [x] 更新 docs/api/reference.md、reference.en.md 与 internal/apidocs/swagger.json、swagger.yaml、docs.go，增加契约回归测试；禁止默认 swag init。
- [x] 对照规格及代码质量独立审查并修复，运行 go test ./internal/apidocs 和本次涉及的包。

## Task 4：集成预览与清理

- [x] 构建新预览镜像、重启 8089 的服务/Agent，保留数据卷。
- [x] 用隔离、虚构模型 API 验证成功/认证失败/异常响应；浏览器检查按钮、示例和反馈；不调用真实模型。
- [x] 清理本次测试容器，记录验证结果；保留新分支供用户验收，不自动推送或合并。

## 验证记录（2026-09-08）

- 前端 lint、typecheck、生产构建通过；完整 Vitest 52 个文件、764 项测试通过。并行构建时旧任务页测试出现一次超时，限制为两个测试 worker 后完整重跑通过。
- Go 全量执行完成：models 与 apidocs 包通过；14 项失败与上次 credentials-http-go-tests.log 的失败名称完全相同，涉及旧任务接口断言、外部文件/Chromium 依赖等，未出现新增失败。
- 最终模型接口、日志脱敏及旧模型兼容性定向测试通过：common/websocket 4.328 秒。
- 规格、前端和后端独立复核通过；修复了清空 Token 后旧测试结果残留、错误映射、编码路径比较与日志脱敏问题。
- 已登录浏览器以隔离虚构服务验证成功、认证失败、无效响应；耗时分别为 2、2、1 ms。未保存虚构模型，也未调用用户真实模型。
- 页面已确认六个输入框均显示灰色提示，模型ID 文案正确，测试按钮可在保存前使用；已清空测试输入并保留新增表单。
- 新镜像 aig-model-configuration:local 已运行，Web 服务健康，预览端口为 127.0.0.1:8089。清理了三个本次临时测试容器，保留现有数据卷。
- 新分支为 codex/model-configuration；旧功能分支已删除。验收时改动尚未提交；用户随后明确授权提交、推送并合并到 develop。