# AI 安全治理平台功能清单

本清单面向产品、客户和管理人员，按用户可获得的结果盘点当前平台能力，并以运行入口和行为验证证据区分“已实现”“部分实现”“待修”和“待开发”。它与[产品需求文档](prd.md)、[项目状态](../project/status.md)和[API 参考](../api/reference.md)互相补充，不以路由声明、数据结构、配置项、页面占位或设计稿替代交付证据。

## 产品能力总览

平台把 AI 安全工作组织成一条可治理、可追溯的闭环：管理员先完成账号、角色、模型、品牌、规则和运行环境治理；用户在本人权限范围内选择扫描能力、提交目标和私有附件；受认证 Agent 获取任务并调用对应扫描组件；平台将事件与结果绑定到可信任务归属，在取消、断连、重试和恢复并发下收敛到唯一可信状态；可信成功结果随后固化为不可变报告快照，供在线详情、PDF、30 日趋势和审计追踪共同使用。

本清单是后端、API、CLI、Agent、Python 扫描组件、规则数据与部署能力的库存，不表示独立、可维护的企业控制台前端已经重建。当前仓库仍保留旧嵌入式 Web 产物；企业控制台重构在 `OPS-04` 中明确列为待开发。

## 角色能力总览

| 角色 | 可承担的职责 | 明确边界 |
| --- | --- | --- |
| 普通用户 | 拥有、扫描并查看本人受治理的任务、私有附件、私有模型和报告，可使用管理员授权的全局模型与只读知识数据 | 不查看或操作其他所有者的私有资源，不进入全局治理入口 |
| 安全审计员 | 全局只读查看任务、脱敏报告、趋势和审计记录，可在授权范围内导出不可变报告 | 一期目标禁止读取或下载任何原始附件；当前实现与目标的偏差由 `FILE-06` 标记为待修 |
| 系统管理员 | 治理用户与角色、模型、规则、品牌、任务恢复、报告补建和系统数据，可全局查看任务、报告与审计 | 跨所有者原始附件只允许用于目标治理场景，并须形成持久、脱敏审计；当前缺口同样由 `FILE-06` 记录 |
| 运维交付人员 | 执行迁移、部署、运行校验、镜像与发布交付，并配置内部 Agent 凭据和附件限额 | 不冒充业务所有者，不以运维身份绕过会话、角色或资源所有权边界 |

## 盘点口径

| 状态 | 判定标准 |
| --- | --- |
| 已实现 | 当前支持的入口可达，核心行为已有自动化测试或环境验收，且没有已知未关闭边界阻断该能力 |
| 部分实现 | 主路径可用，但仍有明确子能力、角色边界或验收项未完成 |
| 待修 | 运行行为存在，但有已确认的安全、权限、一致性或交付缺口，不能按完整能力承诺 |
| 待开发 | 只有需求、设计、声明或占位，尚无受支持的可用运行行为 |

“适用角色”记录能力的发起者、治理者和只读结果消费者，不等于所列角色都拥有写入或变更权；具体动作仍以角色标注、功能描述和服务端授权为准。

交付状态与兼容属性分开记录：“原生能力”由当前平台直接提供，“兼容保留”只为旧接口、旧数据或旧工作流提供受控过渡，“退役边界”不再是当前入口。主矩阵中的证据编号固定为 `E-<能力编号>`；已实现项必须同时具备受支持入口与行为验证，未注册能力不会因仓库中存在常量、脚本或夹具而被视为已交付。

交付入口中的 `Web/API` 表示受支持的服务端 HTTP 交付面，标注兼容属性时也包括相应兼容路径；它不证明独立、可维护的企业控制台已经重建。内部 Agent WebSocket、上传和下载路由单独标注为 `Agent` 或“内部 API”，不是浏览器 API，并继续要求独立内部认证。

## 功能矩阵

| 编号 | 功能模块 | 功能能力 | 用户价值 | 适用角色 | 交付入口 | 交付状态 | 兼容属性 | 证据编号 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| ID-01 | 身份、会话与密码 | 本地账号登录、退出与服务端会话 | 由服务端确认用户身份并统一结束登录状态，避免由浏览器自行声明身份 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-ID-01 |
| ID-02 | 身份、会话与密码 | 用户改密与首次或重置后强制改密 | 帮助用户及时替换初始或临时密码，未完成改密前不能进入业务功能 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-ID-02 |
| ID-03 | 身份、会话与密码 | 本地管理员签发一次性重置令牌与用户确认 | 本地管理员通过 CLI 签发一次性密码重置令牌，用户通过 API 成功确认新密码后才撤销旧会话 | 普通用户、安全审计员、系统管理员 | CLI/API | 已实现 | 原生能力 | E-ID-03 |
| ID-04 | 身份、会话与密码 | 会话轮换、自动过期与安全 Cookie | 降低会话长期有效或被窃取后持续使用的风险，保护浏览器登录状态 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-ID-04 |
| RBAC-01 | 用户、角色与权限 | 管理员创建与查看用户 | 集中建立和盘点组织内的平台账号，避免开放式自助注册 | 系统管理员 | 管理 API | 已实现 | 原生能力 | E-RBAC-01 |
| RBAC-02 | 用户、角色与权限 | 用户启用、禁用与角色调整 | 及时收回离岗或异常账号访问，并按职责调整治理权限 | 系统管理员 | 管理 API | 已实现 | 原生能力 | E-RBAC-02 |
| RBAC-03 | 用户、角色与权限 | 所有者隔离、审计员全局只读与管理员治理 | 普通用户仅操作本人受治理资源，安全审计员跨用户只读审阅，系统管理员执行全局治理 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-RBAC-03 |
| RBAC-04 | 用户、角色与权限 | Cookie 身份认证、CSRF 校验与伪造身份头拒绝 | 所有权限判断以服务端会话为准，阻止跨站请求及伪造用户名或角色绕过授权 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-RBAC-04 |
| MODEL-01 | 模型与密钥治理 | 平台模型加密保存与 Token 掩码展示 | 集中管理扫描所需模型，同时避免明文凭据进入页面、响应或常规记录 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-MODEL-01 |
| MODEL-02 | 模型与密钥治理 | 按所有者和授权解析模型，支持禁用与撤销 | 任务只能使用当前用户获准且仍有效的模型，模型停用或撤销后不再被后续任务调用 | 普通用户、系统管理员 | API/Agent | 已实现 | 原生能力 | E-MODEL-02 |
| MODEL-03 | 模型与密钥治理 | 自动选择最新获授权的默认模型 | 未显式指定模型时优先使用当前用户最新可用配置，减少重复选择并避免越权回退 | 普通用户、系统管理员 | API/Agent | 已实现 | 原生能力 | E-MODEL-03 |
| MODEL-04 | 模型与密钥治理 | 只读 YAML 模型兼容与模型 ID 防遮蔽 | 保留既有只读模型配置的使用方式；平台模型不得使用或占用只读 YAML 模型 ID 以遮蔽配置，并确保兼容写入不回落到旧存储 | 普通用户、安全审计员、系统管理员 | API/Agent/配置 | 已实现 | 兼容保留 | E-MODEL-04 |
| SCAN-01 | 扫描类型与安全检测 | Web 与 AI 基础设施安全扫描 | 识别 AI 应用、服务组件、暴露面和已知安全风险，形成统一扫描结果 | 普通用户、系统管理员（发起）；安全审计员（只读审阅） | CLI/API/Agent | 已实现 | 原生能力 | E-SCAN-01 |
| SCAN-02 | 扫描类型与安全检测 | MCP Server 与工具安全扫描 | 检查 MCP 服务、工具定义及其代码或运行行为中的安全风险 | 普通用户、系统管理员（发起）；安全审计员（只读审阅） | API/Agent/Python 扫描组件 | 已实现 | 原生能力 | E-SCAN-02 |
| SCAN-03 | 扫描类型与安全检测 | Agent 工作流安全扫描 | 评估智能体工作流及其交互配置，发现越权调用、提示操纵等风险 | 普通用户、系统管理员（发起）；安全审计员（只读审阅） | API/Agent/Python 扫描组件 | 部分实现 | 原生能力 | E-SCAN-03 |
| SCAN-04 | 扫描类型与安全检测 | 模型红队与提示词安全评测 | 使用攻击场景和评测数据检验模型对提示词攻击与越狱行为的防护表现 | 普通用户、系统管理员（发起）；安全审计员（只读审阅） | API/Agent/Python 扫描组件 | 已实现 | 原生能力 | E-SCAN-04 |
| SCAN-05 | 扫描类型与安全检测 | 通用组件指纹与版本识别 | 通过多路径请求及 Header/Body/Icon/Hash 匹配识别组件，并按可用证据提取精确版本或模糊版本范围 | 普通用户、系统管理员（发起）；安全审计员（只读审阅） | CLI/API/Agent/规则引擎 | 已实现 | 原生能力 | E-SCAN-05 |
| SCAN-06 | 扫描类型与安全检测 | 反向代理后的后端应用识别 | 在部分透明或透传代理场景中，通用多路径请求仍可利用响应体、Header 和 Icon 信号识别后端应用；当前不识别代理拓扑，也不提供代理层与应用层分层输出 | 普通用户、系统管理员（发起）；安全审计员（只读审阅） | CLI/API/Agent | 部分实现 | 原生能力 | E-SCAN-06 |
| SCAN-07 | 扫描类型与安全检测 | 代理层与应用层分层展示 | 分别呈现 Nginx、Traefik 等代理组件和后端应用，避免把代理误报为业务应用 | 普通用户、系统管理员（发起）；安全审计员（只读审阅） | 扫描结果/报告 | 待开发 | 原生能力 | E-SCAN-07 |
| SCAN-08 | 扫描类型与安全检测 | Dify 专项证据聚合、置信度与版本范围判定 | 在已有基础 Dify 指纹和精确版本提取之上，补充专项证据聚合、置信度与版本范围判定，完善复杂部署下的识别结论 | 普通用户、系统管理员（发起）；安全审计员（只读审阅） | 规则引擎/扫描结果 | 待开发 | 原生能力 | E-SCAN-08 |
| TASK-01 | 任务管理 | 幂等创建、稳定平台任务 ID 与所有者绑定 | 重试同一创建请求时复用同一任务，任务身份与登录用户稳定绑定，避免重复扫描和冒名提交 | 普通用户、系统管理员 | Web/API | 已实现 | 原生能力 | E-TASK-01 |
| TASK-02 | 任务管理 | 按所有者或全局策略查看任务列表、详情、状态与结果 | 普通用户仅查看本人任务；安全审计员按全局只读策略审阅全部任务，系统管理员按全局查看策略查看全部任务 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-TASK-02 |
| TASK-03 | 任务管理 | 任务取消、并发状态收敛与受限交付 | 取消和完成并发发生时只保留一个有效终态；任务交付有总尝试次数限制，交付结果不确定时只检查可信状态以避免重复扫描 | 普通用户、系统管理员 | Web/API/Agent | 已实现 | 原生能力 | E-TASK-03 |
| TASK-04 | 任务管理 | 旧浏览器任务与 SSE 入口退役 | 明确阻止客户端继续使用旧任务创建、状态、结果和 SSE 路由，并以 410 响应引导迁移到平台任务 API | 普通用户、运维交付人员 | Web/API | 已实现 | 退役边界 | E-TASK-04 |
| AGENT-01 | Agent 接入与运行 | 内部 Token 与认证握手 | Agent 只能携带内部 Token 建立受认证连接；Token 未配置时服务拒绝启动，浏览器身份头不能替代内部认证 | 系统管理员、运维交付人员 | Agent/WebSocket/配置 | 已实现 | 原生能力 | E-AGENT-01 |
| AGENT-02 | Agent 接入与运行 | Agent ID 唯一接入与连接安全清理 | 拒绝重复 Agent ID 抢占活跃连接，并确保旧连接结束时不能移除后来建立的有效连接 | 系统管理员、运维交付人员 | Agent/WebSocket | 已实现 | 原生能力 | E-AGENT-02 |
| AGENT-03 | Agent 接入与运行 | 任务分配与事件、结果归属绑定 | 任务只分配给已认证 Agent，事件和结果必须同时匹配认证连接、任务会话及当前获分配的 Agent，防止跨任务或伪造回传 | 普通用户、系统管理员、运维交付人员 | Agent/WebSocket | 已实现 | 原生能力 | E-AGENT-03 |
| AGENT-04 | Agent 接入与运行 | 断连任务失败收敛与防重复下发 | Agent 断连时将其当前任务收敛为失败并持久化脱敏原因；已分配、运行中或已终止的任务不会被重复下发 | 普通用户、系统管理员、运维交付人员 | Agent/WebSocket | 已实现 | 原生能力 | E-AGENT-04 |
| FILE-01 | 附件管理 | 私有完整上传与不可推断的附件 ID | 上传内容以不可推断的附件 ID 对外引用，所有者来自登录身份且默认私有，避免暴露存储路径或由请求自行声明所有者 | 普通用户、系统管理员 | Web/API | 已实现 | 原生能力 | E-FILE-01 |
| FILE-02 | 附件管理 | 受限分片上传与完整性合并 | 对单个分片和累计内容分别限流，并在合并时校验分片完整性、声明大小和实际大小，降低超限或缺片文件进入任务的风险 | 普通用户、系统管理员 | Web/API | 已实现 | 原生能力 | E-FILE-02 |
| FILE-03 | 附件管理 | 私有下载与任务附件精确授权 | 普通用户下载仅限本人附件，特权角色的跨所有者偏差见 FILE-06；创建任务时只能绑定同一所有者已上传完成的附件 | 普通用户、系统管理员 | Web/API | 已实现 | 原生能力 | E-FILE-03 |
| FILE-04 | 附件管理 | Agent 内部附件上传与下载 | 内部上传和下载都必须通过 Agent Token；上传绑定可信平台任务及其引擎会话，下载还必须确认该附件列在所请求旧任务会话的附件清单中 | 系统管理员、运维交付人员 | Agent/内部 API | 已实现 | 原生能力 | E-FILE-04 |
| FILE-05 | 附件管理 | 旧公开图片交付下线与浏览器上传路由退役 | 旧公开图片 GET 已不再注册或公开交付；旧浏览器整文件、分片和合并上传端点仅在通过会话、改密和适用的 CSRF 保护链后返回 410 | 普通用户、运维交付人员 | Web/API | 部分实现 | 退役边界 | E-FILE-05 |
| FILE-06 | 附件管理 | 跨所有者原始附件治理边界 | 目标边界是第一阶段禁止安全审计员读取或下载原始附件，系统管理员仅可在治理场景跨所有者访问且必须留下持久、脱敏审计；当前实现尚未完整满足该边界 | 安全审计员、系统管理员 | Web/API | 待修 | 原生能力 | E-FILE-06 |
| KB-01 | 规则与知识库 | 组件指纹库受控读取与编辑 | 各角色可受控查看组件识别规则，系统管理员可经治理与审计新增、修改或删除指纹内容 | 普通用户、安全审计员、系统管理员 | Web/API/规则数据 | 已实现 | 原生能力 | E-KB-01 |
| KB-02 | 规则与知识库 | 中文漏洞库受控读写与英文漏洞数据保留 | 当前治理入口支持中文漏洞库读取和管理员编辑；英文漏洞数据随仓库和同步流程保留，但尚无已注册的分语言读取或编辑 API | 普通用户、安全审计员、系统管理员 | Web/API/规则数据 | 部分实现 | 原生能力 | E-KB-02 |
| KB-03 | 规则与知识库 | 评测规则与提示词集合受控读取与编辑 | 支持查看安全评测数据和提示词集合，并由系统管理员通过受治理写入口维护后续扫描使用的内容 | 普通用户、安全审计员、系统管理员 | Web/API/规则数据 | 已实现 | 原生能力 | E-KB-03 |
| KB-04 | 规则与知识库 | MCP 数据、Agent 配置与越狱规则受控管理 | MCP 数据和 Agent 配置可受控读取并由管理员维护；越狱规则当前仅提供受控读取，尚无专用编辑入口，也不据此扩展未注册扫描类型 | 普通用户、安全审计员、系统管理员 | Web/API/规则数据 | 部分实现 | 原生能力 | E-KB-04 |
| KB-05 | 规则与知识库 | 规则数据受控同步、状态与版本边界 | 提供管理员触发的受审计同步、同步状态查询和应用版本检查；尚未建立可持久追踪的规则数据版本标识，不能以应用版本代替规则版本 | 安全审计员、系统管理员 | Web/API/规则数据 | 部分实现 | 原生能力 | E-KB-05 |
| REPORT-01 | 风险报告、趋势与 PDF | 可信成功任务的唯一不可变快照 | 任务成功时自动固化报告，同一任务的重复通知、重试或并发处理只保留一份快照，避免历史结论漂移或重复生成 | 普通用户、安全审计员、系统管理员 | API/Agent/报告服务 | 已实现 | 原生能力 | E-REPORT-01 |
| REPORT-02 | 风险报告、趋势与 PDF | 四类真实扫描结果的白名单映射与有限脱敏 | 报告仅从白名单字段生成，对可识别的凭据模式和常见用户私有路径进行脱敏，并排除 Prompt 原文与完整会话内容 | 普通用户、安全审计员、系统管理员 | Web/API/报告服务 | 已实现 | 原生能力 | E-REPORT-02 |
| REPORT-03 | 风险报告、趋势与 PDF | 按所有者与角色隔离的安全报告列表和详情 | 普通用户仅查看本人报告，审计员和管理员可全局审阅；列表只返回安全摘要，列表和详情均不下发原始结果、Logo 字节或所有者内部标识 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-REPORT-03 |
| REPORT-04 | 风险报告、趋势与 PDF | 含当日的 30 日 UTC 风险趋势与服务端聚合 | 由服务端在权限范围内按 UTC 自然日聚合完整 30 日窗口，浏览器无需下载全部报告即可掌握风险变化 | 普通用户、安全审计员、系统管理员 | Web/API | 已实现 | 原生能力 | E-REPORT-04 |
| REPORT-05 | 风险报告、趋势与 PDF | 同一不可变展示快照生成中文多页 PDF | 在线详情与导出使用同一快照，PDF 嵌入中文字体，配置水印时在每页应用该水印；重复导出不重新读取引擎结果或当前品牌 | 普通用户、安全审计员、系统管理员 | Web/API/PDF | 已实现 | 原生能力 | E-REPORT-05 |
| REPORT-06 | 风险报告、趋势与 PDF | 管理员补建缺失报告与持久结果恢复 | 仅从可信成功任务已持久化的引擎结果补建报告，重复或并发补建收敛到同一快照，并为失败保留可恢复记录 | 系统管理员 | 管理 API/报告服务 | 已实现 | 原生能力 | E-REPORT-06 |
| BRAND-01 | 品牌配置 | 产品名称、主色、PNG/JPEG Logo 与水印配置 | 支持企业统一报告品牌，并对 Logo 字节大小、图片尺寸、总像素、实际格式及声明 MIME 进行校验 | 系统管理员 | 管理 API | 已实现 | 原生能力 | E-BRAND-01 |
| BRAND-02 | 品牌配置 | 历史报告品牌冻结与品牌变更审计 | 品牌更新只影响未来报告，历史报告继续使用生成时的品牌快照；管理员变更形成持久审计 | 安全审计员、系统管理员 | Web/管理 API/报告服务 | 已实现 | 原生能力 | E-BRAND-02 |
| AUDIT-01 | 审计、恢复与安全防护 | 关键变更全程留痕与可靠结果交付 | 关键治理操作在变更前留痕，完成结果持久保存并可靠投递；支持投递失败补偿与对账恢复，避免业务已成功却因审计短暂故障被误报失败 | 安全审计员、系统管理员 | Web/管理 API/审计服务 | 已实现 | 原生能力 | E-AUDIT-01 |
| AUDIT-02 | 审计、恢复与安全防护 | 敏感信息脱敏与最小化运行日志 | 结构化元数据按敏感字段脱敏，非结构化引擎失败文本按安全白名单替换且不回显私有路径；任务分发日志不记录完整任务 payload | 普通用户、安全审计员、系统管理员、运维交付人员 | API/Agent/运行日志 | 已实现 | 原生能力 | E-AUDIT-02 |
| AUDIT-03 | 审计、恢复与安全防护 | 有界任务完成与报告恢复 | 平台分批从持久结果恢复任务完成和缺失报告；单项超时不阻塞批次，后续轮次重试；并发恢复收敛为一个有效任务终态和唯一报告快照 | 系统管理员、运维交付人员 | 后台恢复/管理 API | 已实现 | 原生能力 | E-AUDIT-03 |
| AUDIT-04 | 审计、恢复与安全防护 | 服务端授权、审计员只读与最小权限运行 | 权限判断以服务端会话和角色为准，审计员对审计、任务和脱敏报告只有全局只读权限，原始附件缺口由 FILE-06 单独记录；运行账号无需结构变更权限，启动仅校验所需数据库对象 | 安全审计员、系统管理员、运维交付人员 | Web/API/服务启动 | 已实现 | 原生能力 | E-AUDIT-04 |
| OPS-01 | 数据迁移、部署与运维 | 显式版本化迁移与运行时只读校验 | 结构升级只由迁移命令执行且可重复运行；业务服务启动时只读校验结构，缺失时拒绝启动，不自动创建、修复或变更数据库对象 | 系统管理员、运维交付人员 | CLI/服务启动 | 已实现 | 原生能力 | E-OPS-01 |
| OPS-02 | 数据迁移、部署与运维 | PostgreSQL 全栈 Compose、Agent 内部凭据与附件限制 | 源码构建和预构建镜像 Compose 均交付 PostgreSQL、一次性迁移、Web 服务和 Agent；平台与 Agent 运行时共享独立内部 Token，缺失时失败关闭，浏览器仍使用会话与 CSRF；附件总量和分片上限可配置 | 系统管理员、运维交付人员 | 源码/镜像 Compose/CLI/Agent | 已实现 | 原生能力 | E-OPS-02 |
| OPS-03 | 数据迁移、部署与运维 | 多架构镜像、版本发布工作流与 PDF 依赖许可随包 | 自动构建服务器和 Agent 多架构镜像及版本归档；主服务镜像和发布包携带内嵌 PDF 字体归属与许可，以及固定 PDF 渲染器依赖集合的完整许可文本 | 运维交付人员 | 镜像/发布工作流 | 已实现 | 原生能力 | E-OPS-03 |
| OPS-04 | 数据迁移、部署与运维 | 企业控制台前端重构 | 交付可维护的独立品牌企业控制台，覆盖登录、治理、任务、报告与审计工作台 | 普通用户、安全审计员、系统管理员 | Web | 待开发 | 原生能力 | E-OPS-04 |
| OPS-05 | 数据迁移、部署与运维 | 完整 OpenAPI 单一生成源 | 在继续同步当前 YAML、JSON 和 Go 内嵌 Swagger 三件套的同时，建立覆盖全部平台路由的唯一规格源与可复现生成流程 | 系统管理员、运维交付人员 | API 规格/生成流程 | 待开发 | 原生能力 | E-OPS-05 |
| OPS-06 | 数据迁移、部署与运维 | 正式发布候选 | 完成全量端到端、安全回归、目标环境部署、容量基线和发行材料验收，形成可正式交付的发布候选 | 系统管理员、运维交付人员 | 发布包/部署验收 | 待开发 | 原生能力 | E-OPS-06 |

## 典型使用流程

1. **管理员初始化与模型治理**：运维交付人员先运行显式迁移、配置服务和内部 Agent 凭据，并通过本地入口建立首个管理员；管理员完成强制改密、创建用户与角色、维护全局模型或授权私有模型，密钥只以受保护形式保存和展示。
2. **用户提交含私有附件的任务**：普通用户登录后选择本人获准模型，通过完整或分片上传取得不可推断的附件 ID，再以可重试的幂等请求提交扫描；平台从会话确定所有者，只允许绑定同一所有者且已完成上传的附件。
3. **受认证 Agent 执行与回传**：Agent 使用独立内部凭据建立连接，领取与自身分配关系匹配的任务，按任务类型调用 Go 或 Python 扫描组件；附件传输、事件和结果回传均需匹配可信任务或旧引擎会话，不能由浏览器身份头替代认证。
4. **可信结果固化与展示**：平台仅依据可信成功终态和持久结果生成唯一不可变快照；在线详情、PDF 和 30 日 UTC 趋势共同消费该快照，PDF 仅在已配置水印时应用水印，后续品牌变化不改写历史报告。
5. **审计员全局只读审阅**：安全审计员可全局查看任务、脱敏报告、趋势和审计记录，并按授权导出报告；一期目标不允许其查看或下载原始附件，当前实现偏差见 `FILE-06`。
6. **管理员恢复与补建**：任务完成、报告生成或审计投递因短暂故障未完全收敛时，后台按有界批次重试；管理员可从可信成功任务的持久结果补建缺失报告或处理需治理的恢复项，重复与并发执行仍收敛为单一终态和单一快照。

## 兼容与退役边界

- YAML 模型仅以只读方式兼容；平台模型不能遮蔽其 ID，也不能通过兼容写操作回落到旧明文存储。
- 旧模型 API 是受当前会话、角色、加密存储和审计约束的治理兼容门面，不代表旧的按用户名或明文模型工作流仍受支持。
- 旧浏览器任务创建、列表或详情、状态、结果、SSE、整文件上传、分片上传和合并入口均先经过会话、强制改密及适用的 CSRF 安全链，再按相应路由返回 `410 Gone`；客户端应迁移到平台任务与附件入口。
- 旧公开图片交付路径未在当前服务注册，不得作为附件公开访问或兼容下载入口。
- 历史日文 API 说明已归档在 `docs/archive/legacy-api/`，只提供历史上下文，不定义当前产品契约。
- Agent WebSocket 以及 Agent 内部附件上传、下载属于服务间协议，不是浏览器 API；三者都要求独立内部 Token，Cookie 或浏览器自报身份不能替代该凭据。

## 尚未交付

`SCAN-06` 的现有边界是“部分实现”：在部分透明或透传代理场景中，已有 Dify 基础规则和通用多路径、响应体、Header、Icon 等信号可能识别后端应用，但没有代理拓扑、代理层/应用层分层、专项置信度或复杂代理回归保证。以下项目在完成条件满足前均不得升级为完整交付：

| 编号 | 待交付能力 | 用户价值 | 完成条件 |
| --- | --- | --- | --- |
| `SCAN-03` | Agent 工作流注册路径的确定性冒烟 | 证明产品实际注册的 Go Agent 入口能够完整调用工作流扫描并返回安全扫描结果，而不只验证底层组件 | 使用受控夹具执行已注册 Go `AgentTask` 路径，确定性断言 Agent 安全扫描结果，并纳入自动化回归 |
| `SCAN-06` | 反向代理透传识别回归 | 明确哪些代理透传场景能够可靠识别后端应用，避免把偶然可见信号当作普遍保证 | 使用代表性反向代理夹具覆盖受支持透传场景，以自动化回归断言通用信号能够识别后端应用；代理/应用分层和 Dify 专项置信度仍分别属于 `SCAN-07`、`SCAN-08` |
| `SCAN-07` | 代理层与应用层分层结果 | 区分边缘代理与真实业务组件，减少混淆和误报 | 建立代理拓扑与分层结果模型，覆盖代表性代理组合的回归夹具，并在扫描结果和报告中稳定展示两层证据 |
| `SCAN-08` | Dify 证据聚合、置信度与版本范围增强 | 在复杂部署下给出可解释、可比较的 Dify 识别结论 | 实现 Dify 专项多信号聚合、置信度和精确版本或范围规则，覆盖透传、隐藏 Header、静态资源变化等回归场景并接入报告 |
| `FILE-05` | 旧公开图片路径 HTTP 回归 | 证明历史公开路径不会绕过附件治理重新交付服务器中的文件字节 | 在生产路由测试中准备受控存储字节，请求原旧公开图片路径并断言响应不能返回这些字节 |
| `FILE-06` | 附件权限纠正 | 防止只读审计角色接触原始输入，并让管理员跨所有者治理可追责 | 服务端拒绝审计员读取全部原始附件；管理员仅在治理场景访问且成功、失败均写入持久脱敏审计；补齐角色矩阵越权回归和契约文档 |
| `KB-02` | 英文漏洞分语言治理 | 让英文漏洞数据通过明确、受权和可审计的产品入口供不同角色使用，而不只停留在仓库与同步目录 | 注册受治理的英文漏洞分语言读取与编辑路径，并以角色矩阵和持久审计测试验证只读消费、管理员变更及拒绝行为 |
| `KB-04` | 越狱规则受治理变更 | 让越狱检测规则在受控校验和追责边界内更新，避免人工直接修改数据文件 | 提供受治理的越狱规则变更入口，通过内容校验、持久审计和角色矩阵测试覆盖成功与拒绝路径 |
| `KB-05` | 规则数据版本身份 | 让同步结果、问题排查和回滚能够指向唯一规则数据版本，而不是借用应用版本 | 建立持久、不可变的规则数据版本身份，在同步与状态结果中暴露该身份，并补齐升级和重试测试 |
| `OPS-04` | 企业控制台前端重构 | 让三类平台角色通过可维护、角色化的企业工作台使用现有平台能力 | 形成经评审实施计划，交付可维护前端源码并覆盖登录、治理、任务、报告和审计主流程，通过角色、安全与端到端验收后替换旧嵌入式产物 |
| `OPS-05` | 完整 OpenAPI 单一生成源 | 让客户端、测试与人读契约从同一权威规格稳定生成 | 单一规格覆盖全部受支持平台路由，能可复现生成并校验 YAML、JSON 和 Go 内嵌产物，迁移期间不破坏现有运行时三件套 |
| `OPS-06` | 正式发布候选 | 给目标环境提供可签收的质量、容量和发行依据 | 完成全量端到端与安全回归、目标环境部署、容量基线、离线或升级验证及发行材料签收 |

## 证据索引

- **E-ID-01**：运行入口——平台认证 API 的登录、退出与当前服务端会话；验证来源——`internal/platform/identity/service_test.go` 和 `internal/platform/identity/middleware_test.go` 的认证、会话与认证路由行为测试。
- **E-ID-02**：运行入口——平台认证 API 的改密与强制改密安全链；验证来源——`internal/platform/identity/service_test.go` 的首次或重置后改密测试及 `internal/platform/identity/middleware_test.go` 的业务入口阻断测试。
- **E-ID-03**：运行入口——本地密码重置 CLI 与认证 API 的重置确认；验证来源——`cmd/cli/main_test.go` 的一次性令牌交付测试和 `internal/platform/identity/middleware_test.go` 的重置、会话撤销与不泄露测试。
- **E-ID-04**：运行入口——所有受保护 Web/API 的服务端会话 Cookie；验证来源——`internal/platform/identity/service_test.go` 的哈希存储、轮换和撤销测试，以及 `internal/platform/identity/middleware_test.go` 的过期、HTTPS 与 Cookie 策略测试。
- **E-RBAC-01**：运行入口——平台管理员用户管理 API；验证来源——`internal/platform/admin/handler_test.go` 的用户生命周期、角色守卫与审计一致性测试。
- **E-RBAC-02**：运行入口——平台管理员用户启停和角色调整 API；验证来源——`internal/platform/admin/handler_test.go` 的管理员变更测试与 `internal/platform/identity/service_concurrency_test.go` 的并发身份更新测试。
- **E-RBAC-03**：运行入口——平台任务、报告、模型、知识库和审计 API；验证来源——`common/websocket/resource_authorization_integration_test.go`、`internal/platform/reports/service_export_test.go` 和 `internal/platform/audit/service_test.go` 的所有者及全局只读角色测试。
- **E-RBAC-04**：运行入口——受保护的 Cookie Web/API；验证来源——`internal/platform/identity/middleware_test.go` 的伪造身份头、角色、CSRF 和安全 Cookie 测试，以及 `common/websocket/route_security_test.go` 的生产安全链测试。
- **E-MODEL-01**：运行入口——平台模型治理 API；验证来源——`internal/platform/models/service_test.go` 的认证加密与密钥轮换测试，以及 `common/websocket/platform_governance_integration_test.go` 的响应掩码和治理集成测试。
- **E-MODEL-02**：运行入口——平台模型 API 与 Agent 任务模型解析器；验证来源——`internal/platform/models/resolver_test.go` 的所有者、授权、禁用和撤销测试，以及 `common/websocket/resource_authorization_integration_test.go` 的模型角色矩阵测试。
- **E-MODEL-03**：运行入口——未显式指定模型的任务提交与 Agent 解析；验证来源——`internal/platform/models/resolver_test.go` 的最新获授权默认模型选择测试。
- **E-MODEL-04**：运行入口——平台模型目录、只读 YAML 配置与旧模型 API 兼容门面；验证来源——`common/websocket/legacy_model_compatibility_test.go` 的加密存储、只读和 ID 冲突测试，以及 `cmd/cli/migrate_legacy_models_test.go` 的原子迁移测试。
- **E-SCAN-01**：运行入口——Go 扫描 CLI、平台任务 API 和已注册 AI 基础设施 Agent 任务；验证来源——`common/runner/runner_test.go` 的扫描执行测试和 `common/agent/tasks_test.go` 的 AI 基础设施任务行为测试。
- **E-SCAN-02**：运行入口——平台任务 API、已注册 MCP Agent 任务与 `mcp-scan` 生产入口；验证来源——`common/agent/tasks_test.go` 的 URL/代码两类 MCP 执行测试、`internal/mcp/plugins_test.go` 的插件注册测试，以及 `mcp-scan/pytests/test_parse.py`、`mcp-scan/pytests/test_read_file.py` 的解析与文件边界测试。
- **E-SCAN-03**：运行入口——平台任务 API、已注册 Go `AgentTask` 与 `agent-scan` 生产入口；验证来源——`agent-scan/test_websocket_provider.py` 和 `agent-scan/test_llm_error_handling.py` 当前覆盖提供方路由、终止信号和失败重试组件；现状限制——缺少执行已注册 Go AgentTask 路径并断言 Agent 安全扫描结果的确定性冒烟，因此状态为部分实现。
- **E-SCAN-04**：运行入口——平台任务 API、已注册模型红队 Agent 任务与 `AIG-PromptSecurity` 生产入口；验证来源——`common/agent/tasks_test.go` 的模型红队报告执行测试及 `cmd/agent/main.go` 的生产任务注册核对。
- **E-SCAN-05**：运行入口——CLI/API/Agent 通用 Web 扫描和指纹规则引擎；验证来源——`common/fingerprints/preload/preload_test.go`、`common/fingerprints/preload/version_detection_test.go`、`common/fingerprints/preload/version_range_test.go` 与 `common/runner/runner_test.go` 的多路径匹配和精确或模糊版本行为测试。
- **E-SCAN-06**：运行入口——通用 Web 扫描的多路径、响应体、Header、Icon 和现有 Dify 基础规则；验证来源——`common/fingerprints/preload/preload_test.go`、`common/fingerprints/preload/version_detection_test.go`、`common/fingerprints/preload/version_range_test.go` 的通用匹配及版本测试与 `data/fingerprints/dify.yaml` 的当前规则核对；现状限制——没有代理专项回归、代理拓扑识别、分层结果或统一置信度保证。
- **E-SCAN-07**：运行入口——无（尚未交付）；验证来源——扫描结果模型、报告映射与现有回归夹具的缺口核对，以及 `docs/product/prd.md` 的分层目标；现状限制——尚无代理层和应用层拓扑模型、代表性回归或报告展示。
- **E-SCAN-08**：运行入口——无（尚未交付）；验证来源——当前 Dify 基础规则、通用版本测试与 `docs/product/prd.md` 的专项增强目标对照；现状限制——尚无 Dify 专项证据聚合、置信度、版本范围策略和复杂代理场景回归。
- **E-TASK-01**：运行入口——平台任务创建 API；验证来源——`internal/platform/tasks/service_test.go` 的并发幂等、稳定所有者和唯一分发测试，以及 `internal/platform/tasks/handler_test.go` 的会话主体与幂等键测试。
- **E-TASK-02**：运行入口——平台任务列表、详情、状态与结果 API；验证来源——`internal/platform/tasks/service_test.go` 和 `internal/platform/tasks/handler_test.go` 的普通用户所有者过滤、审计员全局只读与纯读行为测试。
- **E-TASK-03**：运行入口——平台任务取消 API、后台交付与 Agent 事件链；验证来源——`internal/platform/tasks/service_test.go` 和 `common/websocket/platform_task_adapter_test.go` 的终态竞争、持久交付预算、未知确认与可信状态收敛测试。
- **E-TASK-04**：运行入口——旧浏览器任务、状态、结果、SSE 与上传兼容路径；验证来源——`common/websocket/route_security_test.go` 的认证、强制改密、CSRF 和 `410 Gone` 行为测试，以及 `common/websocket/server_rbac_test.go` 的生产注册合同测试。
- **E-AGENT-01**：运行入口——内部 Agent WebSocket 握手；验证来源——`common/websocket/agent_security_test.go` 和 `common/agent/agent_test.go` 的缺失凭据失败关闭、独立 Token 握手及不泄露测试。
- **E-AGENT-02**：运行入口——Agent 注册与连接管理；验证来源——`common/websocket/agent_security_test.go` 的顺序和并发重复 Agent ID 拒绝测试，以及 `common/websocket/platform_task_adapter_test.go` 的陈旧连接安全清理测试。
- **E-AGENT-03**：运行入口——Agent 任务分配、事件和结果回传协议；验证来源——`common/websocket/platform_task_adapter_test.go` 的认证连接、引擎会话和当前分配三重归属测试。
- **E-AGENT-04**：运行入口——Agent 断连清理与平台任务恢复链；验证来源——`common/websocket/platform_task_adapter_test.go` 的断连失败、脱敏原因、单向终态和防重复下发测试。
- **E-FILE-01**：运行入口——平台任务附件完整上传 API；验证来源——`internal/platform/tasks/service_test.go` 的私有、有界、所有者派生测试和 `internal/platform/tasks/handler_test.go` 的不可推断元数据及存储路径不外泄测试。
- **E-FILE-02**：运行入口——平台任务附件分片开始、分片上传与合并 API；验证来源——`internal/platform/tasks/service_test.go` 的单片、累计大小、缺片、声明大小和合并清理测试。
- **E-FILE-03**：运行入口——平台附件下载与任务创建附件绑定；验证来源——`internal/platform/tasks/handler_test.go` 的普通用户跨所有者下载拒绝测试及 `internal/platform/tasks/service_test.go` 的同所有者完成态附件绑定测试；现状边界——这些证据证明普通用户所有者路径，特权角色偏差见 `FILE-06`。
- **E-FILE-04**：运行入口——Agent 内部制品上传与任务附件下载；验证来源——`common/websocket/route_security_test.go` 的独立 Token 和精确绑定测试；现状边界——上传绑定可信且运行中的平台任务及引擎会话，下载绑定所请求旧任务会话的附件清单，两者不是同一种绑定模型。
- **E-FILE-05**：运行入口——通过安全链保留的旧浏览器任务与上传兼容路径；验证来源——`common/websocket/route_security_test.go` 已验证认证、强制改密、适用的 CSRF 和 `410 Gone` 行为，`common/websocket/server.go` 的路由清单仅佐证旧公开图片路径未注册；现状限制——缺少 HTTP 回归，用于请求原旧公开图片路径并证明其不能返回受控存储字节，因此状态为部分实现。
- **E-FILE-06**：运行入口——现有平台附件读取与下载入口；验证来源——`internal/platform/tasks/service.go` 的当前特权角色判断与 `docs/product/prd.md` 的一期权限合同对照；现状限制——审计员仍可跨所有者读取，管理员跨所有者访问也未具备目标要求的专用持久脱敏审计，因此状态为待修。
- **E-KB-01**：运行入口——受保护的指纹知识 API 与 `data/fingerprints` 规则数据；验证来源——`internal/platform/knowledge/service_test.go`、`common/websocket/knowledge_governance_test.go` 和 `common/websocket/server_rbac_test.go` 的只读角色、管理员治理与审计测试。
- **E-KB-02**：运行入口——中文漏洞知识 API 及中英文漏洞数据同步清单；验证来源——`internal/platform/knowledge/service_test.go` 的受审计治理测试与 `common/websocket/update_api_test.go` 的选择性数据同步测试；现状限制——英文漏洞数据存在并可同步，但没有已注册的分语言读取或编辑 API。
- **E-KB-03**：运行入口——评测规则与提示词集合知识 API；验证来源——`internal/platform/knowledge/handler_test.go` 的受治理写入、异步完成和恢复测试，以及 `common/websocket/server_rbac_test.go` 的角色保护合同测试。
- **E-KB-04**：运行入口——MCP 数据、Agent 配置和越狱规则知识 API；验证来源——`internal/platform/knowledge/service_test.go` 与 `common/websocket/server_rbac_test.go` 的管理员写入和全角色受控读取测试；现状限制——越狱规则仅有读取入口，没有专用编辑入口，也不扩展未注册扫描类型。
- **E-KB-05**：运行入口——系统数据同步、同步状态与应用版本检查 API；验证来源——`common/websocket/update_api_test.go`、`common/websocket/version_api_test.go` 和 `internal/platform/knowledge/handler_test.go` 的同步选择、版本比较及异步治理恢复测试；现状限制——尚无可持久追踪的规则数据版本标识，应用版本不能替代规则版本。
- **E-REPORT-01**：运行入口——可信任务成功处理与平台报告服务；验证来源——`internal/platform/tasks/report_snapshot_test.go`、`internal/platform/tasks/report_snapshot_postgres_test.go` 和 `internal/platform/reports/repository_test.go` 的事务一致、唯一和不可变快照测试。
- **E-REPORT-02**：运行入口——可信成功结果到安全报告快照的映射；验证来源——`internal/platform/reports/risk_test.go` 和 `internal/platform/reports/technical_findings_test.go` 的四类生产结构白名单、凭据模式和私有路径脱敏测试；适用边界——只覆盖白名单字段、已识别模式及常见用户私有路径，不代表任意输入都能绝对脱敏。
- **E-REPORT-03**：运行入口——平台报告列表与详情 API；验证来源——`internal/platform/reports/service_export_test.go` 和 `internal/platform/reports/handler_test.go` 的所有者先过滤、全局只读角色、安全摘要及敏感字段不出线测试。
- **E-REPORT-04**：运行入口——平台报告趋势 API；验证来源——`internal/platform/reports/snapshot_test.go` 和 `internal/platform/reports/repository_test.go` 的所有者过滤、含当日 30 日 UTC 自然日窗口与服务端安全列投影测试。
- **E-REPORT-05**：运行入口——平台报告详情与 PDF 导出 API；验证来源——`internal/platform/reports/pdf_test.go` 和 `internal/platform/reports/service_export_test.go` 的嵌入中文字体、多页排版、同快照重试及导出审计测试；适用边界——水印仅在快照品牌已配置水印时应用。
- **E-REPORT-06**：运行入口——管理员报告补建 API 与后台完成结果恢复；验证来源——`internal/platform/reports/backfill_test.go`、`internal/platform/tasks/completed_task_source_test.go` 和 `common/websocket/platform_task_recovery_test.go` 的可信来源、幂等与并发恢复测试。
- **E-BRAND-01**：运行入口——平台品牌读取与管理员更新 API；验证来源——`internal/platform/brand/service_test.go`、`internal/platform/brand/handler_test.go` 和 `internal/platform/brand/governed_test.go` 的产品名、主色、Logo 实际格式、尺寸、像素、MIME、水印与角色治理测试。
- **E-BRAND-02**：运行入口——品牌治理 API 与报告快照生成；验证来源——`internal/platform/tasks/report_snapshot_test.go` 和 `internal/platform/reports/snapshot_test.go` 的历史品牌冻结测试，以及 `internal/platform/brand/governed_test.go` 的持久审计测试。
- **E-AUDIT-01**：运行入口——用户、模型、知识、品牌、任务、报告等治理服务的审计链；验证来源——`internal/platform/audit/service_test.go` 和 `internal/platform/audit/ready_queue_test.go` 的 pending、completion、outbox、失败补偿与幂等对账测试。
- **E-AUDIT-02**：运行入口——平台 API、Agent 任务分发与运行日志；验证来源——`internal/platform/reports/technical_findings_test.go`、`common/websocket/task_dispatch_log_test.go` 和 `common/agent/attachment_log_test.go` 的结构化凭据、自由文本、私有路径及任务载荷最小化测试。
- **E-AUDIT-03**：运行入口——后台任务完成恢复与管理员报告补建；验证来源——`internal/platform/tasks/service_test.go`、`common/websocket/server_recovery_test.go` 和 `common/websocket/platform_task_recovery_test.go` 的有界批次、单项超时、后续重试及并发收敛测试。
- **E-AUDIT-04**：运行入口——受保护平台 API 与主服务启动校验；验证来源——`internal/platform/identity/middleware_test.go`、`internal/platform/audit/service_test.go` 和 `pkg/database/runtime_schema_test.go` 的服务端授权、审计员只读及运行期无 DDL 测试；现状边界——原始附件的已知权限缺口单独记录在 `FILE-06`。
- **E-OPS-01**：运行入口——数据库迁移 CLI 与主服务启动；验证来源——`pkg/database/migrate_cli_test.go`、`pkg/database/migrate_test.go` 和 `pkg/database/runtime_schema_test.go` 的版本化幂等迁移、并发串行及运行时只读拒绝测试。
- **E-OPS-02**：运行入口——根目录源码构建与预构建镜像 Compose、迁移 CLI、Web 服务和 Agent；验证来源——`docker-compose.yml` 和 `docker-compose.images.yml` 的 PostgreSQL、一次性迁移、主服务和 Agent 全栈定义，以及 `common/websocket/agent_security_test.go` 和 `internal/platform/tasks/service_test.go` 的内部凭据与附件限额测试；适用边界——`deploy/compose/docker-compose.postgres.yml` 只是最小迁移基线，不作为全栈交付入口。
- **E-OPS-03**：运行入口——主服务多架构镜像与版本发布工作流；验证来源——`.github/workflows/docker-publish.yml`、`.github/workflows/create-release.yml` 和 `internal/platform/reports/license_packaging_test.go` 的双架构、发布归档、字体许可和固定 PDF 渲染依赖 notices 随包合同测试；适用边界——许可随包范围是主服务镜像与发布包，不扩展为 Agent 镜像的 PDF 能力声明。
- **E-OPS-04**：运行入口——无（尚未交付）；验证来源——`docs/project/status.md` 与 `docs/architecture/enterprise-console.md` 对“设计已完成、实施计划和实现待开发”的一致记录；现状限制——现有旧嵌入式产物不能证明企业控制台已重建。
- **E-OPS-05**：运行入口——无（尚未交付）；验证来源——`docs/project/status.md` 的缺口记录与 `internal/apidocs/swagger_sync_test.go` 对现有 YAML、JSON、Go 内嵌三件套同步范围的合同检查；现状限制——当前尚无覆盖全部平台路由的单一权威生成源。
- **E-OPS-06**：运行入口——无（尚未交付）；验证来源——`docs/project/status.md` 和 `docs/product/prd.md` 对端到端、安全回归、目标环境、容量和发行验收尚未完成的状态记录；现状限制——现有镜像与发布工作流不等同于正式发布候选签收。

## 维护与验收

- 能力编号一经分配永不复用；能力拆分时保留原编号作为历史或退役编号，并为新能力分配新编号。证据编号始终为 `E-<能力编号>`。
- 代码、测试、PRD、项目状态或 API 文档冲突时采用更保守状态：已实现项失去入口、行为验证或出现未关闭边界时必须降级；只有修正权威文档并取得新的运行与验证证据后才能升级。
- 每次变更必须保持 57 个现有能力编号与证据一一对应、12 个模块仍在正式矩阵中，并检查无重复能力、重复证据或孤儿证据。
- 常规检查是编辑时可选的快速子集；发布验收只需运行一次完整目录合同，完整目录合同已经包含常规检查，无需重复运行 Docker 检查。涉及 API、数据、Python 或部署实现的后续改动还须按仓库 `AGENTS.md` 增加对应范围测试。
- 完整目录合同接受 `AIG_DOCS_BASE_REF` 作为差异基线覆盖值；该值应是现有命名 Git ref（例如 `origin/main`、`main` 或完整 `refs/...`）。未设置时依次选择现有 `origin/main`、本地 `main`，均不可用或没有共同历史时明确失败。

编辑时需要快速反馈，可在仓库根目录按需单独运行以下常规检查：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1
docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test go test ./internal/apidocs -count=1
$mount = '{0}:/input' -f (Get-Location).Path
docker run --rm -v $mount -w /input lycheeverse/lychee:0.24.2 --offline --no-progress 'docs/**/*.md' 'README.md' 'readme/*.md'
```

<details>
<summary>维护者附录：完整 Windows PowerShell 5.1 目录验收程序（可直接运行）</summary>

以下程序已经包含上面的常规检查，以及精确结构、差异范围和敏感信息校验；发布验收请从仓库根目录整体执行一次。

```powershell
$ErrorActionPreference = 'Stop'
if (Get-Variable PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

$text = Get-Content -LiteralPath docs/product/features.md -Raw -Encoding UTF8
$expectedRanges = [ordered]@{
    'ID'     = 1..4
    'RBAC'   = 1..4
    'MODEL'  = 1..4
    'SCAN'   = 1..8
    'TASK'   = 1..4
    'AGENT'  = 1..4
    'FILE'   = 1..6
    'KB'     = 1..5
    'REPORT' = 1..6
    'BRAND'  = 1..2
    'AUDIT'  = 1..4
    'OPS'    = 1..6
}
$expectedPrefixModules = [ordered]@{
    'ID'     = '身份、会话与密码'
    'RBAC'   = '用户、角色与权限'
    'MODEL'  = '模型与密钥治理'
    'SCAN'   = '扫描类型与安全检测'
    'TASK'   = '任务管理'
    'AGENT'  = 'Agent 接入与运行'
    'FILE'   = '附件管理'
    'KB'     = '规则与知识库'
    'REPORT' = '风险报告、趋势与 PDF'
    'BRAND'  = '品牌配置'
    'AUDIT'  = '审计、恢复与安全防护'
    'OPS'    = '数据迁移、部署与运维'
}
$expectedIDs = @(
    foreach ($entry in $expectedRanges.GetEnumerator()) {
        foreach ($number in $entry.Value) {
            '{0}-{1:D2}' -f $entry.Key, $number
        }
    }
)
$expectedModules = @($expectedPrefixModules.Values)
$expectedEvidenceIDs = @($expectedIDs | ForEach-Object { "E-$_" })
$expectedModuleCounts = [ordered]@{}
foreach ($entry in $expectedRanges.GetEnumerator()) {
    $expectedModuleCounts[$expectedPrefixModules[$entry.Key]] = @($entry.Value).Count
}
$expectedStatusCounts = [ordered]@{
    '已实现' = 45
    '部分实现' = 6
    '待修' = 1
    '待开发' = 5
}
$expectedCompatibility = @('原生能力', '兼容保留', '退役边界')

function Assert-ExactSet {
    param(
        [Parameter(Mandatory)] [string] $Name,
        [Parameter(Mandatory)] [string[]] $Expected,
        [Parameter(Mandatory)] [string[]] $Actual
    )

    $expectedSet = @($Expected | Sort-Object -Unique)
    $actualSet = @($Actual | Sort-Object -Unique)
    if ($expectedSet.Count -ne $Expected.Count) {
        throw "$Name 的预期集合自身存在重复"
    }
    if ($actualSet.Count -ne $Actual.Count) {
        $duplicates = @($Actual | Group-Object | Where-Object Count -gt 1 | ForEach-Object Name)
        throw "$Name 存在重复项: $($duplicates -join ', ')"
    }
    $delta = @(Compare-Object -ReferenceObject $expectedSet -DifferenceObject $actualSet)
    if ($delta.Count -gt 0) {
        $summary = @($delta | ForEach-Object { "$($_.SideIndicator)$($_.InputObject)" })
        throw "$Name 与预期集合不一致: $($summary -join ', ')"
    }
}

function Assert-ExactCounts {
    param(
        [Parameter(Mandatory)] [string] $Name,
        [Parameter(Mandatory)] [System.Collections.IDictionary] $Expected,
        [Parameter(Mandatory)] [string[]] $Actual
    )

    $actualGroups = @($Actual | Group-Object)
    Assert-ExactSet -Name "$Name 枚举" -Expected @($Expected.Keys) -Actual @($actualGroups | ForEach-Object Name)
    foreach ($entry in $Expected.GetEnumerator()) {
        $group = @($actualGroups | Where-Object Name -EQ $entry.Key)
        $actualCount = if ($group.Count -eq 1) { $group[0].Count } else { 0 }
        if ($actualCount -ne $entry.Value) {
            throw ('{0} [{1}] 数量应为 {2}，实际为 {3}' -f $Name, $entry.Key, $entry.Value, $actualCount)
        }
    }
}

$parts = [regex]::Split($text, '(?m)^## 证据索引\s*$')
if ($parts.Count -ne 2) {
    throw '证据索引章节缺失或重复'
}
$matrixText = $parts[0]
$evidenceText = $parts[1]
$matrixLines = @([regex]::Split($matrixText, '\r?\n') | Where-Object { $_ -match '^\|\s*[A-Z]+-[0-9]+\s*\|' })
$matrixRows = @(
    foreach ($line in $matrixLines) {
        $trimmedLine = $line.Trim()
        if (-not $trimmedLine.EndsWith('|')) {
            throw "功能矩阵行缺少结尾分隔符: $trimmedLine"
        }
        $cells = @($trimmedLine.Substring(1, $trimmedLine.Length - 2).Split('|') | ForEach-Object { $_.Trim() })
        if ($cells.Count -ne 9) {
            throw "功能矩阵行必须包含 9 列，实际为 $($cells.Count) 列: $trimmedLine"
        }
        [pscustomobject] [ordered]@{
            ID            = $cells[0]
            Module        = $cells[1]
            Capability    = $cells[2]
            UserValue     = $cells[3]
            Roles         = $cells[4]
            Entry         = $cells[5]
            Status        = $cells[6]
            Compatibility = $cells[7]
            EvidenceID    = $cells[8]
        }
    }
)
$actualIDs = @($matrixRows | ForEach-Object ID)
$actualModules = @($matrixRows | ForEach-Object Module | Sort-Object -Unique)
$actualEvidenceColumnIDs = @($matrixRows | ForEach-Object EvidenceID)
$evidenceMatches = @([regex]::Matches($evidenceText, '(?m)^- \*\*E-([A-Z]+-[0-9]+)\*\*：[^\r\n]*运行入口——[^\r\n]*验证来源——[^\r\n]*\r?$'))
$actualEvidenceIDs = @($evidenceMatches | ForEach-Object { $_.Groups[1].Value })

if ($matrixRows.Count -ne 57) {
    throw "功能矩阵应包含 57 项能力，实际为 $($matrixRows.Count) 项"
}
if ($evidenceMatches.Count -ne 57) {
    throw "证据索引应包含 57 项证据，实际为 $($evidenceMatches.Count) 项"
}

foreach ($row in $matrixRows) {
    $id = $row.ID
    $prefix = $id.Split('-')[0]
    $module = $row.Module
    if (-not $expectedPrefixModules.Contains($prefix)) {
        throw "$id 使用了未知编号前缀 $prefix"
    }
    $expectedModule = $expectedPrefixModules[$prefix]
    if ($module -ne $expectedModule) {
        throw ('{0} 的模块应为 [{1}]，实际为 [{2}]' -f $id, $expectedModule, $module)
    }
    if (-not $expectedStatusCounts.Contains($row.Status)) {
        throw ('{0} 使用了非法交付状态 [{1}]' -f $id, $row.Status)
    }
    if ($expectedCompatibility -notcontains $row.Compatibility) {
        throw ('{0} 使用了非法兼容属性 [{1}]' -f $id, $row.Compatibility)
    }
    $expectedEvidenceID = "E-$id"
    if ($row.EvidenceID -ne $expectedEvidenceID) {
        throw ('{0} 的证据编号应为 [{1}]，实际为 [{2}]' -f $id, $expectedEvidenceID, $row.EvidenceID)
    }
}

Assert-ExactSet -Name '功能编号' -Expected $expectedIDs -Actual $actualIDs
Assert-ExactSet -Name '证据编号' -Expected $expectedIDs -Actual $actualEvidenceIDs
Assert-ExactSet -Name '功能矩阵证据编号' -Expected $expectedEvidenceIDs -Actual $actualEvidenceColumnIDs
Assert-ExactSet -Name '功能模块' -Expected $expectedModules -Actual $actualModules
Assert-ExactSet -Name '功能矩阵证据与证据索引' -Expected $actualEvidenceColumnIDs -Actual @($actualEvidenceIDs | ForEach-Object { "E-$_" })
Assert-ExactCounts -Name '功能模块' -Expected $expectedModuleCounts -Actual @($matrixRows | ForEach-Object Module)
Assert-ExactCounts -Name '交付状态' -Expected $expectedStatusCounts -Actual @($matrixRows | ForEach-Object Status)

& powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1
if ($LASTEXITCODE -ne 0) { throw '文档布局检查失败' }
& docker compose -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test go test ./internal/apidocs -count=1
if ($LASTEXITCODE -ne 0) { throw 'internal/apidocs 测试失败' }
$mount = '{0}:/input' -f (Get-Location).Path
& docker run --rm -v $mount -w /input lycheeverse/lychee:0.24.2 --offline --no-progress 'docs/**/*.md' 'README.md' 'readme/*.md'
if ($LASTEXITCODE -ne 0) { throw 'Markdown 链接检查失败' }

& git diff --check
if ($LASTEXITCODE -ne 0) { throw '工作区差异格式检查失败' }

function Test-GitReference {
    param([Parameter(Mandatory)] [string] $Reference)

    & git show-ref --verify --quiet $Reference
    $showRefExit = $LASTEXITCODE
    if ($showRefExit -eq 0) { return $true }
    if ($showRefExit -eq 1) { return $false }
    throw ('检查 Git ref [{0}] 失败（exit {1}）' -f $Reference, $showRefExit)
}

function Resolve-GitReference {
    param([Parameter(Mandatory)] [string] $Reference)

    if ($Reference.StartsWith('refs/')) {
        $candidates = @($Reference)
    }
    else {
        $candidates = @(
            'refs/remotes/{0}' -f $Reference
            'refs/heads/{0}' -f $Reference
            'refs/tags/{0}' -f $Reference
        )
    }
    foreach ($candidate in $candidates) {
        if (Test-GitReference -Reference $candidate) { return $candidate }
    }
    return $null
}

$requestedBaseRef = [Environment]::GetEnvironmentVariable('AIG_DOCS_BASE_REF')
if ([string]::IsNullOrWhiteSpace($requestedBaseRef)) {
    if (Test-GitReference -Reference 'refs/remotes/origin/main') {
        $baseRef = 'refs/remotes/origin/main'
    }
    elseif (Test-GitReference -Reference 'refs/heads/main') {
        $baseRef = 'refs/heads/main'
    }
    else {
        throw '未找到可用的差异基线；请获取 origin/main、创建本地 main，或设置 AIG_DOCS_BASE_REF'
    }
}
else {
    $requestedBaseRef = $requestedBaseRef.Trim()
    $baseRef = Resolve-GitReference -Reference $requestedBaseRef
    if ([string]::IsNullOrWhiteSpace($baseRef)) {
        throw ('AIG_DOCS_BASE_REF [{0}] 不是现有命名 Git ref' -f $requestedBaseRef)
    }
}

$mergeBaseOutput = @(& git merge-base $baseRef HEAD 2>&1)
$mergeBaseExit = $LASTEXITCODE
if ($mergeBaseExit -ne 0 -or $mergeBaseOutput.Count -eq 0) {
    throw ('无法计算基线 [{0}] 与 HEAD 的 merge-base（exit {1}）；请确认两者具有共同历史' -f $baseRef, $mergeBaseExit)
}
$base = ([string] $mergeBaseOutput[-1]).Trim()
if ([string]::IsNullOrWhiteSpace($base)) {
    throw ('基线 [{0}] 的 merge-base 结果为空' -f $baseRef)
}
& git diff --check "$base..HEAD"
if ($LASTEXITCODE -ne 0) { throw '提交范围差异格式检查失败' }

$sensitivePattern = 'sk-[A-Za-z0-9_-]{12,}|Bearer\s+[A-Za-z0-9._-]{16,}|AKIA[0-9A-Z]{16}|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|[A-Za-z]:[\\/]|/(home|Users|root)/'
$scanOutput = @(rg -n $sensitivePattern docs/product/features.md 2>&1)
$scanExit = $LASTEXITCODE
if ($scanExit -eq 0) {
    throw "功能目录包含疑似敏感信息或绝对本地路径:`n$($scanOutput -join [Environment]::NewLine)"
}
if ($scanExit -gt 1) {
    throw "功能目录敏感信息扫描执行失败（exit $scanExit）:`n$($scanOutput -join [Environment]::NewLine)"
}
```

</details>
