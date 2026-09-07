# Skills ZIP 静态扫描设计

用户已确认首期使用单个 Skill ZIP 包，并批准独立任务类型、受治理模型、静态审计和专属工作台的方案。开发基线为 develop `ddb045545`。本设计细化已确认方案，不改变其他扫描类型。

## 业务与 API 合同

- 平台类型 `skills_scan`，引擎能力 `Skills-Scan`；使用现有平台任务、附件、调度、取消、报告服务。
- 创建请求严格限定 `params: {"model_id":"<可用治理模型 ID>"}`；恰好一个已上传 ready 的 ZIP 附件。`content` 必须为空；可选 `remark` 沿用 2,000 Unicode 码点和既有幂等规则。中文界面显式提交 `country_iso_code: "zh_CN"`。
- 无并发、端口、动态扫描、URL、仓库、自由审计提示词等首期选项；未知参数拒绝。
- 安全详情仅新增 Skills 的 `input_summary: {language, model_id, scan_mode: "static"}`。列表维持六个安全字段。名称、文件数、技能数不新增持久化与显示，不复用 AI 的 target_count。
- 类型创建、筛选、规范化、前端解析、详情匹配、报告归类均保留 Skills 身份。字符串类型扩展不新增迁移；历史 MCP 与报告不重新分类。

## ZIP 合同与执行目录

共享 Go 包 `internal/skillarchive` 提供 `Inspect(filename string) (Manifest, error)` 与 `Extract(filename, destination string) (string, error)`；后者返回规范化后的 Skill 根目录。Manifest 包含 Root、Name、FileCount，仅在内部使用。

- 压缩包最大 20 MiB，实际解压总量最大 100 MiB，单文件最大 5 MiB，原始 ZIP 条目和规范化后的文件/目录总数（含隐式目录）均最多 2,000。
- 必须只有一个 `SKILL.md`，位于 ZIP 根目录或一个顶层包裹目录；所有文件均在同一 Skill 根目录下。支持无脚本的说明型 Skill。
- SKILL.md 为有效 UTF-8 Markdown，YAML frontmatter 为对象，name、description 为非空字符串，name 最多 128 码点，description 最多 2,000 码点；拒绝重复键和无效 YAML。
- 拒绝绝对路径、盘符、反斜杠、空/点/父目录段、链接及其他特殊文件、重复/大小写冲突路径、文件与目录冲突、加密或不支持压缩方式、损坏 ZIP/CRC。目录项允许一个末尾斜杠。
- 读取实际解压流时限制字节数，不能只信 ZIP 元数据；异常时拒绝整包。创建前只检查附件，不把目标代码解压到 Web 服务目录。
- Agent 每任务使用独立临时目录，重新执行相同校验后解压；成功、失败、取消均清理。禁止执行包内代码、安装依赖、访问任意外部目标。

## 引擎约束

- 新 `SkillsTask` 执行器固定调用 mcp-scan 代码审计路径，显式传入 Skills 静态模式；复用三阶段流程、结果事件与取消回调。MCP 旧模式继续保持现有行为。
- Python 新增显式模式，确定性验证 Skill 根目录；Skill 入口只能配置受治理主模型，各子阶段不得回落到环境中的其他模型或服务。
- Agent 直接启动 Python，以便取消时终止实际进程。优先查找 mcp-scan/.venv，再查找 PATH；可用 `AIG_SKILLS_PYTHON_BIN` 显式指定已安装扫描依赖的解释器。API 的语言按空值/zh/zh_CN → zh、en → en 贯穿全部阶段，中文页面仍固定提交 zh_CN。
- 静态模式工具仅允许 finish、think、受根目录约束且有输出上限的读取/目录/搜索。分发时强制白名单，隐藏提示词不足以作为限制。禁用 Shell、文件写入和远程 MCP 调用。
- Skill 文本和附件都是待审计的不可信数据，不能改变任务规则、工具许可或模型配置。纯说明型 Skill 同样检查恶意指令和越权/外传行为。
- 凭据不进入命令行日志或浏览器 DTO；Skills 执行不打开 debug。若 Python/模型/结果解析失败，任务失败，不能伪造成功空报告。
- Skills 复核仅接受明确的无发现标记或完整有效的漏洞集合；损坏块、有效/无效块混合、无输出与格式重试耗尽均失败。Go SkillsTask 必须验证并收到唯一最终结果事件才能成功；错误事件或进程正常退出但没有结果也失败。保持 MCP 旧模式兼容行为。
- 结果沿用经验证的 score/results/level 格式，Skills 报告允许复用 MCP 风险和技术发现转换器，但快照 task_type 必须为 skills_scan。

## 页面与权限

路由为 `/tasks/skills`、`/tasks/skills/new`、`/tasks/skills/:taskId`。沿用 Fluent UI、主题令牌、四张真实范围指标卡、六列表和三段表单。提取共享工作台组件的业务文案与导航参数，不复制全部 AI 页面。Skills 表单单独维护；受治理模型必选，待上传/目录未确认时禁止提交。

普通用户读写自己的任务，审计员全局只读，管理员全局读写。URL 不能覆盖专属列表类型；详情类型不符隐藏数据与操作并停止轮询。幂等重试、有界轮询与取消不确定性使用既有实现。备注仅用于授权详情，不进入引擎、审计或报告。

## 验收

先写回归测试并确认失败，再实现：ZIP 正常与异常边界、附件-only 创建/重读/同键重试/隔离、模型校验、模式强制和工具禁用、Skills 与 MCP 报告区分、前端上传/模型/权限/状态隔离。运行前端 lint/typecheck/test/build，Go 相关包及 go test ./...（测试库串行），Python 对应入口冒烟。Go DB 测试使用独立 Compose 项目；不能与其他工作树共享会被清理的测试数据库。

浏览器检查浅/深主题与宽/窄屏；通过实际 HTTP、调度和执行器的集成测试验证链路，明确区分本地模型协议夹具与真实治理模型扫描。同步中英文 API、Swagger 三件套及架构指南；禁止默认 swag init。
