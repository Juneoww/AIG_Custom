# HTTP 默认支持验收

用户明确要求 HTTP 直接填写保存，移除“允许明文 HTTP”开关。此记录覆盖此前的显式许可交互，代码保留在同一独立工作树。

## 实现

- 创建和编辑表单默认支持 HTTP/HTTPS，无 HTTP 开关、许可错误或明文提示，写请求不再发送 allow_insecure_http。
- 公共接口直接校验 HTTP/HTTPS origin；旧版可选布尔字段接受但忽略，响应和内部运行时字段均由保存的协议推导。
- 协议、主机及端口边界、协议变更须更换密钥、AAD 绑定、TLS 校验和 Agent v2 私有通道保持有效。
- 中英文 API 文档及 Swagger JSON/YAML/Go 三件套同步；没有数据库迁移变更。

## 回归与复核

- 后端 RED：缺省字段 HTTP POST、false 字段 HTTP 创建及完整生命周期共 3 个预期失败。实现后目标凭据整包、任务服务整包、apidocs 整包和 httpx 整包通过，包含隔离 PostgreSQL 迁移和并发版本测试。
- Swagger RED：三份规格均被新增默认 HTTP/兼容字段契约捕获；更新后通过。
- 前端 RED：8 failed / 13 passed；实现后固定 Node 22.18 / pnpm 10.15 的完整 frontend-builder 执行 lint、typecheck、test:run、build 成功，50 个文件、743 个测试通过。
- 独立规格和最终代码复核通过，未发现此次修改引入的问题。
- 前一 HTTP 增量已运行完整 Go 套件，14 个失败与 develop 基线逐项相同；本轮针对受影响包验证，不将完整 Go 套件描述为全绿。

## 浏览器与运行时实测

- 新建页不显示 HTTP 开关；直接保存 http://infra-http-fixture:8888 的虚构 Bearer 凭据成功。编辑时密钥不回显，留空直接保存成功，版本 1→2。
- 任务 989ee8e6-d6c4-50d1-abeb-fec9f19d7ba6 完成，报告 6b9ef54e-0db4-4d67-9528-1317a3b3570b。隔离靶场收到 103 次正确认证请求、0 次拒绝。
- 靶场在响应中回显虚构 Token；数据库布尔检查确认 task/session、21 条事件、原始/渲染报告及凭据密文均不含 Token 明文，session 不保存 target_auth。
- 本轮临时凭据已通过 UI 删除；测试开发容器、数据库和靶场清理完毕。已有用户凭据保持版本 1，未更改；带明确验收备注的任务和报告保留。
- 最终预览镜像 aig-infra-credentials:local：sha256:930eb19db90bbb9ff07e3a2f97a57e969c10ed5d965529ca20f65a13369c81d0，服务已重启。浏览器留在新建凭据页。

日志位于本任务外部产物目录：http-default-ui-red.log、http-default-console-build.log、http-default-go-tests.log、http-default-final-build.log。
