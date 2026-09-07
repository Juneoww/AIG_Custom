# Skills ZIP Scan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 支持单个 Skill ZIP 包通过独立 Skills 工作台创建、静态审计、取消和报告查看。

**Architecture:** 平台新增 skills_scan，Agent 新增 Skills-Scan；内部复用 mcp-scan 三阶段审计，并用强制工具许可限定静态模式。ZIP 解析器由创建服务与执行器共享。现有任务生命周期、附件事务和报告快照保持统一。

**Tech Stack:** Go 1.23.2、Python 3.12+、React/Fluent UI、TypeScript、PostgreSQL 16.4。

## 文件分工

- 根执行者：`internal/skillarchive/*`、`internal/platform/tasks/*`、`common/websocket/task_manager.go`、`internal/platform/reports/*`、`internal/apidocs/*`、`docs/*`。
- 引擎任务：`common/agent/skills_task.go`、`common/agent/types.go`、`cmd/agent/main.go`、`mcp-scan/*`；不得编辑平台或前端文件。
- 前端任务：`web/console/src/{app,features/tasks,features/reports,shared/api}/*`；不得编辑 Go、Python、文档。
- 固定跨层合同：params 只有必填 model_id；一个附件；content 为空；详情 scan_mode 为 static；根解析器 Inspect/Extract 接口见设计。接口一经确定，各区域按独立文件边界实施；集成测试由根执行者串行组织。

## Task 1: 基线与共享 ZIP 解析器

Files: create `internal/skillarchive/archive.go`, `internal/skillarchive/archive_test.go`.

- [x] 在独立工作树记录基线，准备独立 Docker 测试库和本地前端依赖。
- [x] 写表驱动 ZIP 测试：根/包装目录/纯文本 Skill、缺文件、多个 Skill、路径穿越、大小写冲突、链接、损坏、元数据与实际字节上限、错误 frontmatter。
- [x] 运行 `go test ./internal/skillarchive -count=1`，确认缺失实现失败。
- [x] 实现流式有界 Inspect、严格路径映射、元数据验证、共享 Extract；调用者只提供新临时目录。
- [x] 重跑解析器测试并检查提取文件内容与返回根目录。

## Task 2: 平台合同与报告

Files: `internal/platform/tasks/{service.go,dto.go,skills_test.go}`, `common/websocket/task_manager.go`, `internal/platform/reports/{risk.go,skills_test.go}`.

- [x] 先写 Skills 创建、身份过滤、未知参数/模型/附件/非空 content 拒绝、幂等重放、摘要白名单和报告归类测试，并确认失败。
- [x] 增加 skills_scan/Skills-Scan 规范化与调度映射；创建锁内使用附件服务读准备好的本地 ZIP 并调用 Inspect。
- [x] 接入已有治理引用校验、附件绑定事务、取消和状态协调；保持同键重试优先于 ready 检查。
- [x] 增加 Skills 安全摘要与报告转换类型；无需新增数据库列。
- [x] 运行相关 tasks/reports/websocket 测试，数据库测试只在独立库串行执行。

## Task 3: Skills 执行入口和静态工具

Files: `common/agent/{skills_task.go,skills_task_test.go,types.go}`, `cmd/agent/main.go`, `mcp-scan/main.py`, `mcp-scan/agent/agent.py`, `mcp-scan/tools/dispatcher.py`, new static read/search tool module and tests/prompts as needed.

- [x] 先写 Skills 执行参数/附件约束、取消清理和 Python 静态工具禁止执行测试，确认失败。
- [x] 实现 SkillsTask 使用共享 Extract、任务临时目录、受治理模型、固定模式和安全子进程配置。
- [x] Python 显式开启 Skill 静态模式，所有审计阶段使用治理主模型；增加有界读取/列目录/搜索，调用入口拒绝任何其他工具。
- [x] Skill 内容只能作为待分析材料；纯说明型 Skill 的审计覆盖指令恶意行为；保留既有 MCP 流程。
- [x] Python 严格校验最终复核输出：只有明确无发现或完整有效漏洞集合才允许发布结果；损坏、混合损坏与正常块、无输出及格式重试耗尽均失败。Go SkillsTask 校验最终事件，错误事件/坏结果/无结果/重复结果不得返回成功。分别增加 Python 和 Go 回归测试。
- [x] 运行 Python pytests、新入口 --help 冒烟和对应 Go 单测。缺失依赖先修测试环境，不能用假成功替代。

## Task 4: 前端专属工作台

Files: routes/navigation, task/report types and parsers, shared workbench components, new Skills form and tests.

- [x] 先增加类型解析、专属路由、上传未完成/缺模型、角色权限、错误详情类型、取消和重试测试，确认失败。
- [x] 在共享工作台组件参数化标题与路径；保留 AI 视觉与行为。添加 Skills 表单，一个 ZIP、20 MiB 上限、必选治理模型、独立备注。
- [x] 接通独立列表和详情；响应白名单仅允许静态摘要；报告列表识别 Skills。通用创建页在选择 Skills 时使用同一表单。
- [x] 执行 `pnpm --dir web/console lint`, `typecheck`, `test:run`, `build`，修复回归。

## Task 5: 合同文档与集成验证

Files: `docs/api/reference.md`, `docs/api/reference.en.md`, `internal/apidocs/{swagger.yaml,swagger.json,docs.go}`, `docs/architecture/scan-workbench-development.md`.

- [x] 手工同步 Swagger 枚举、参数/摘要描述和 API 文档，运行 `go test ./internal/apidocs`；不运行默认 swag init。
- [x] 独立测试库下构建 CLI，提供 AIG_TEST_CLI_BINARY，串行跑 tasks/reports/database/apidocs 和涉及的 websocket/agent 测试。
- [x] 执行 `go test -p 1 ./...` 与 Python 回归，记录基线失败和新增失败的区别。
- [x] 使用本地模型协议夹具跑真实进程和结果事件；若可用再用治理模型完成一次真实扫描。确认失败路径不产生成功报告。
- [x] 浏览器检查浅/深主题，1440/768/320px；上传/创建/状态/取消/报告。复用已有应用壳，记录其既有限制。
- [x] 独立审查规格符合性和代码质量，修复关键问题；`git diff --check`；提交本次文件，保留功能分支供审阅，不擅自合并/推送。

## 执行记录

- 2026-09-05：已确认用户方案；新工作树 `.worktrees/skills-scan`，基于 ddb045545。宿主无 Go，使用缓存 Go 1.23.2 Docker 镜像；Docker 操作需要沙箱提权。宿主 Node 24.13.0/pnpm 10.30.3，固定版本容器用于必要的最终校验。

### 最终验证（2026-09-05）

| 范围 | 结果 |
| --- | --- |
| 前端固定工具链 | Node 22.18.0 / pnpm 10.15.0：lint、typecheck、生产构建通过；最终完整 39 文件、559 项测试通过。 |
| Python | `python -m pytest pytests -q --tb=short`：46 项通过；`python main.py --mode skills --help` 退出 0。包含实际入口、zh/en、长对话压缩、有效/损坏/混合/空输出。 |
| Go 核心 | `internal/skillarchive`、`internal/platform/tasks`、`internal/platform/reports`、`pkg/database`、`internal/apidocs` 全包通过；Skills Agent、WebSocket 映射、接收帧凭据日志定向测试通过。CLI 与 Agent 编译通过。 |
| 浏览器完整执行链 | 最终 Playwright 3/3 通过：上传 ZIP→治理模型→Go Agent→Python→成功报告；损坏结果失败且无报告；模型调用期间取消且无后续调用/报告。 |
| 视觉与权限 | 创建、列表、详情在 320/390/768/1440px、浅/深主题下检查；修复长 ID 覆盖、长模型和详情裁切、侧栏和按钮溢出。审计员只读，无法直接访问创建页。 |
| 接收日志 | 最终 Agent 实际接收了任务，启动后的日志不含已知模型夹具令牌。 |
| 打包配置 | Skills E2E Compose 合并配置校验通过；最小 Docker COPY 构建确认依赖清单、Skills 与 compact 提示词进入构建上下文。 |
| 审查 | 独立审查发现的问题全部关闭，最终 Approved；`git diff --check` 通过。 |

最终 E2E 使用 `skills_model_fixture.py` 提供的固定 OpenAI SSE 协议响应，正常场景为明确标注的 High 样例（60 分）。取消测试先读取 slow fixture 请求计数，等待 Python 确实发起请求才取消；跨过 20 秒响应延迟后再次确认状态、请求数和报告。测试没有使用真实付费模型，不能作为模型安全检出率或真实 Skill 风险结论。

Go 全量命令已执行完成：

```bash
docker compose -p aig-skills-check -f deploy/compose/docker-compose.postgres-test.yml run --rm database-test sh -ec 'go build -o /tmp/aig ./cmd/cli/main.go && AIG_TEST_CLI_BINARY=/tmp/aig go test -p 1 ./... -count=1 -timeout=3m'
```

全量结果不是全绿：以下 6 个包的相同失败已在 `ddb045545` 原始源码、另一独立数据库中复现；另一个未修改的 `common/runner` 包达到 3 分钟测试时间预算。未为本功能改写这些旧用例。

| 包 | 基线失败原因 |
| --- | --- |
| `common/agent` | `TestLargeDataSend` 创建 Agent 时未提供连接令牌。 |
| `common/fingerprints/preload` | `TestRunner_RunFpReqs` 使用错误的相对规则路径。 |
| `common/utils` | `TestFaviconHash` 缺少预设本地文件，随后空指针。 |
| `common/utils/chromium` | Go 测试镜像没有 Chrome/Chromium，旧截图测试随后空指针。 |
| `common/websocket` | 5 个旧 API 用例的中文错误文案/模型字段先后顺序断言与当前实际响应不符。 |
| `internal/mcp/utils` | 5 个工具用例依赖不存在的 `/mcp-server/...` 固定目录。 |

本轮运行日志与截图保存在未跟踪的 `.worktrees/validation/`、`web/console/test-results/`，不进入产品或 Git 提交。可复现 E2E 命令见架构指南第 11 节。本轮复用缓存 Python 运行镜像并加载本分支源文件和编译产物，没有重新构建全部发布镜像。前端仍有既有 bundle 大小提示，320px 下既有 TopBar 的“关于”文字仍可能裁切。

本功能不变更数据库结构或规则库，无需新增迁移或执行规则 YAML 校验。上述验证完成时，功能提交为 `6fd22538f`。用户随后授权合并到 `develop` 并推送 `origin/develop`；合并时业务代码与该验证版本一致。
