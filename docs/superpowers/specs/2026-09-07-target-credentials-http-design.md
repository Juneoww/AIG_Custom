# 基础设施凭据显式 HTTP 支持

## 后续确认：默认支持 HTTP/HTTPS

用户明确要求直接填写 HTTP 地址即可使用，并移除“允许明文 HTTP”展示。以下早期显式开关方案由本节覆盖：表单不显示开关和相应许可提示，默认验证 HTTP/HTTPS；新写请求无需许可字段。后端按实际 origin 协议接受 HTTP/HTTPS。保留旧 API 输入 allow_insecure_http 为被忽略的兼容字段，安全 View/runtime 的该字段继续由已保存协议派生，避免破坏已有客户端和 Agent v2 通道。既有同源、AAD、TLS 校验、版本、敏感值脱敏保持。只改变用户和公共写接口的默认行为，不改变私有执行通道的校验边界。

回归要求：无开关且 HTTP 创建/编辑直接成功；请求不再发送许可字段；HTTP DTO 和任务匹配无需许可，仍拒绝不合法 URL 和跨源；公共 HTTP 写入缺省/false/true 均按 origin 执行，私有 runtime 继续由服务器派生。更新中英 API/Swagger、相关测试并重新构建本地预览。

用户已确认内网全部使用 HTTP，需要前一轮提出的“默认关闭、明确开启允许明文 HTTP”方案。继续当前独立工作树，不另建分支，不提交或合并已有改动。

## 设计

新建和编辑表单增加“允许明文 HTTP”复选框，新建默认关闭。HTTP origin 必须同时提交 `allow_insecure_http: true`；未开启时仍只接受 HTTPS。页面说明 HTTP 会明文发送凭据，保存时不再另加确认弹窗。HTTPS 地址开启此选项不会放宽其同源约束，也不会跳过 TLS 验证。

不增加数据库列：已确认的协议记录在现有 origin 中，且 origin 已参与密文 AAD。安全 View 的 `allow_insecure_http` 从持久化 origin 是否为 http 推导，编辑 HTTP 凭据预填已开启；这表示当前凭据实际使用 HTTP，不是跨协议许可。所有 HTTP 写请求（包括编辑留空保留密钥）必须显式提交 true。从 HTTPS 切 HTTP 或反向切换视为目标变化，必须提交新密钥且版本递增；伪改数据库 origin 仍无法解密。

私有 TargetAuth assignment 增加 `allow_insecure_http`，从已保存 origin 推导。HTTP runtime 必须明确为 true 才可执行。NormalizeTargetOrigin 和 ValidateTargetURLs 增加可选 allowHTTP 参数，省略仍 HTTPS-only；所有服务、Agent、runner、transport 校验显式传递可信许可。normalize 按 HTTP 80 / HTTPS 443 去除默认端口；其他端口保留。

传输仍严格同协议、同主机、同有效端口；即使开启选项也不允许 HTTPS→HTTP 或 HTTP→HTTPS 跨协议重定向携带密钥。Host 校验使用请求实际协议，禁用原有 raw/H2 绕过，响应脱敏和认证失败不生成成功报告保持。认证 Agent 能力版本升级为 infra-target-auth-v2，避免任务调度给不支持本规则的旧 Agent；需同步发布主服务与 Agent。

任务仍仅引用 ID/revision，使用手工同源完整 URL，拒绝导入清单和发现表达式。所有“仅 HTTPS”文案及中英 API/Swagger 更新为默认 HTTPS、显式 HTTP 和同源边界。

## 验证

RED→GREEN：HTTP 未确认拒绝，确认后创建/编辑/读取/解析/真实请求成功；默认端口归一化；错主机/端口/协议和跳转不得收到认证；TLS 验证未关闭；协议修改留空密钥拒绝，篡改 origin 密文不可解；HTTP assignment 许可丢失拒绝；前端 checkbox 默认关闭、阻止未确认提交、编辑预填、任务支持 HTTP 同源 URL。

运行受影响 Go 包/定向测试、前端 lint/typecheck/test/build、apidocs；现有全量 Go 基线失败使用上一轮记录，必要时复跑受影响包。更新本地 8089 预览，使用隔离 HTTP 靶场实测认证和脱敏后清理临时资源。数据库结构及既有 HTTPS 密文兼容。
