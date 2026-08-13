# 项目文档信息架构整理设计

## 1. 背景与目标

当前 `docs/` 同时包含产品需求、架构历史、专项 API 说明、部署文档、设计稿、实施计划，以及由 Swagger 生成的 Go 源码、JSON/YAML 规格和测试。人读文档与运行时代码混放，使目录职责不清，当前文档和历史文档也难以区分。

本次整理的目标是：

- `docs/` 只存放需要人阅读和维护的项目文档；
- Go 源码、测试和机器生成的 Swagger 产物全部移出 `docs/`；
- 当前有效文档按产品、项目、架构、API、部署分类；
- 旧设计和旧实施计划保留但明确归档；
- 新增唯一实时项目状态页，统一记录总计划、已完成、正在进行、待开发和风险；
- 所有代码引用、生成流程、仓库链接和测试在迁移后继续有效。

本设计只整理信息架构，不修改产品功能、API 语义、扫描器、数据库或页面行为。

## 2. 已确认原则

- 采用按文档职责分层的目录结构。
- 历史文档保留并归档，不删除。
- 新增 `docs/project/status.md` 作为唯一实时项目进度入口。
- `docs/` 不允许放置 `.go` 文件、Go 测试、Swagger JSON/YAML 生成物或其他运行时代码。
- Swagger 四件套整体迁移到 `internal/apidocs/`，成为明确的运行时代码/生成物模块。
- 人工维护的 API 指南进入 `docs/api/`。
- 根目录 `README.md`、`CHANGELOG.md`、`NOTICE`、`LICENSE`、`AGENTS.md` 保持原位。

## 3. 目标目录结构

```text
docs/
├─ README.md
├─ product/
│  └─ prd.md
├─ project/
│  ├─ status.md
│  └─ plans/
│     ├─ documentation.md
│     └─ enterprise-console.md
├─ architecture/
│  ├─ evolution.md
│  ├─ enterprise-console.md
│  └─ documentation.md
├─ api/
│  ├─ reference.md
│  ├─ reference.en.md
│  └─ data-sync.md
├─ deployment/
│  ├─ postgres.md
│  └─ attribution.md
└─ archive/
   └─ 2026-08-enterprise-platform/
      ├─ README.md
      ├─ design.md
      └─ implementation-plan.md

internal/apidocs/
├─ docs.go
├─ swagger.json
├─ swagger.yaml
└─ swagger_sync_test.go
```

`docs/project/plans/enterprise-console.md` 是后续经批准生成的企业控制台实施计划目标路径。本次整理不得创建空计划或把尚未生成的计划标成已存在；`docs/README.md` 和状态页在计划不存在时明确写“待编写”。

本次文档整理实施计划迁入 `docs/project/plans/documentation.md`，作为当前有效且正在执行的计划；完成整理后保留并在状态页标为已完成证据。

## 4. 文件迁移映射

| 当前路径 | 目标路径 | 处理方式 |
| --- | --- | --- |
| `docs/prd.md` | `docs/product/prd.md` | 当前有效 PRD；只调整相对链接 |
| `docs/architecture_evolution.md` | `docs/architecture/evolution.md` | 当前架构历史；只移动和更新引用 |
| `docs/superpowers/specs/2026-08-13-enterprise-console-rebuild-design.md` | `docs/architecture/enterprise-console.md` | 当前有效控制台设计 |
| `docs/superpowers/specs/2026-08-13-documentation-information-architecture-design.md` | `docs/architecture/documentation.md` | 当前有效文档治理设计；完成本次迁移后长期保留 |
| `docs/superpowers/plans/2026-08-13-documentation-information-architecture.md` | `docs/project/plans/documentation.md` | 当前文档整理实施计划；迁移后长期保留 |
| `docs/api_data_update.md` | `docs/api/data-sync.md` | 当前专项 API 指南 |
| 根 `api_zh.md` | `docs/api/reference.md` | 中文 API 参考；更新仓库入口引用 |
| 根 `api.md` | `docs/api/reference.en.md` | 英文 API 参考；为现有双语维护约束保留 |
| `docs/deployment/postgres.md` | 原位 | 已分类正确 |
| `docs/deployment/attribution.md` | 原位 | 已分类正确 |
| `docs/superpowers/specs/2026-08-04-enterprise-platform-redesign-design.md` | `docs/archive/2026-08-enterprise-platform/design.md` | 原文归档，顶部加非当前依据提示 |
| `docs/superpowers/plans/2026-08-04-enterprise-platform-redesign.md` | `docs/archive/2026-08-enterprise-platform/implementation-plan.md` | 原文归档，保留历史勾选状态 |
| `docs/docs.go` | `internal/apidocs/docs.go` | 生成的 Go 注册源码；包名改为 `apidocs` |
| `docs/swagger.json` | `internal/apidocs/swagger.json` | 机器生成 API 规格 |
| `docs/swagger.yaml` | `internal/apidocs/swagger.yaml` | 机器生成 API 规格 |
| `docs/swagger_sync_test.go` | `internal/apidocs/swagger_sync_test.go` | 同步和契约测试；包名改为 `apidocs` |

迁移完成并更新所有引用后，删除已为空的 `docs/superpowers/specs/`、`docs/superpowers/plans/` 和 `docs/superpowers/`。不删除任何非空目录或未纳入映射的用户文档。

## 5. 文档入口与维护职责

### 5.1 `docs/README.md`

作为文档总入口，包含：

- 当前有效文档导航；
- 项目状态入口；
- 产品、架构、API、部署分类说明；
- 历史归档入口和“归档文档不再更新”的提示；
- 文档维护规则；
- Swagger 运行时规格位于 `internal/apidocs/` 的说明。

### 5.2 当前文档与归档

- `docs/product/`、`docs/project/`、`docs/architecture/`、`docs/api/` 和 `docs/deployment/` 是当前有效文档区。
- `docs/archive/` 只保存历史决策和旧计划，不能作为当前实施状态来源。
- 归档目录的 `README.md` 记录归档时间、原因以及替代文档链接。
- 旧文档正文尽量保持不变；历史命令中的旧路径允许保留，但归档提示必须防止读者误用。

## 6. 项目状态页

新增 `docs/project/status.md`，作为项目进度的唯一实时入口。

固定结构：

1. 文档元信息：最后更新时间、当前阶段、当前分支；
2. 当前结论：一句话说明平台可用程度和当前边界；
3. 里程碑总览：里程碑、状态、交付内容和证据；
4. 已完成：只列有提交、测试或验收证据的工作；
5. 正在进行：只列实际正在执行的阶段和当前动作；
6. 下一步：按顺序列最近 3–5 项；
7. 后续计划：尚未进入开发的里程碑；
8. 阻塞与风险：外部依赖、已知测试基线、产品决策和发布门槛；
9. 文档导航：链接 PRD、当前设计、API、部署和历史归档。

状态只使用以下枚举：

- `已完成`
- `正在进行`
- `待开发`
- `阻塞`

不使用无法验证的主观百分比。“已完成”必须附提交、测试或验收证据；详细步骤不复制到状态页，只链接正式设计或计划。完成里程碑、出现阻塞或范围变化时必须同步更新。

首次创建状态页时，依据当前分支的提交和验证证据记录：数据库/迁移、身份权限、治理审计模型、受控任务与附件、不可变报告与品牌等后端阶段已经完成；企业控制台设计已完成并提交；企业控制台实施计划与前端实现尚未开始。不得把尚未重新验证的历史计划勾选直接当作完成证据。

## 7. Swagger 运行时模块迁移

Swagger 四件套与 Go 代码直接绑定，因此整体迁入 `internal/apidocs/`：

- `docs.go` 的包声明从 `package docs` 改为 `package apidocs`；
- `swagger_sync_test.go` 同步改为 `package apidocs`；
- `common/websocket/server.go` 的匿名导入从 `github.com/Juneoww/AIG_Custom/docs` 改为 `github.com/Juneoww/AIG_Custom/internal/apidocs`；
- 测试继续在同目录读取 `swagger.json` 和 `swagger.yaml`；
- `AGENTS.md` 中 API/任务变更校验路径更新为人工指南 `docs/api/reference*.md` 和运行时规格 `internal/apidocs/swagger.yaml`。

当前仓库不是完整的注解驱动生成链：Go 源码只有 6 条旧 `/app/taskapi` `@Router` 注解，而现有 Swagger 规格包含约 37 条平台路由。直接运行 `swag init` 会丢失企业平台接口。因此本次整理只做字节等价迁移、包名/import 修正和既有三件套同步测试，不新增会破坏规格的伪生成脚本，也不从不完整注解重新生成。

迁移不得改变 Swagger 路由、注册名称、API 内容或运行时页面行为。`docs/README.md` 和 `AGENTS.md` 明确记录：在完整平台路由注解或独立 OpenAPI 源尚未建立前，`internal/apidocs/swagger.yaml`、`swagger.json` 和 `docs.go` 仍按现有同步测试共同维护；不得运行默认 `swag init` 覆盖它们。

“重建单一、可复现的 OpenAPI 源与生成脚本”作为 `docs/project/status.md` 的后续工程项，需另写规格和实施计划，至少覆盖全部现有平台路由后才能替换当前维护方式。该工程不属于本次目录整理范围。

## 8. 链接与引用迁移

必须扫描并更新：

- 根 `README.md`；
- `readme/` 下各语言 README；
- `AGENTS.md`；
- PRD 的关联文档；
- API、部署和架构文档内的相对链接；
- Go import；
- CI、脚本、生成命令和测试中的硬编码路径；
- 非归档源文件中的旧 `docs/superpowers/**`、`docs/prd.md`、`docs/architecture_evolution.md`、`docs/api_data_update.md` 和 `docs/swagger.*` 引用。

归档正文内用于历史还原的旧路径不强制改写，但归档头部的替代链接必须使用新路径。

## 9. 实施顺序

1. 建立目标目录和 `docs/README.md`、`docs/project/status.md`、归档说明；
2. 使用 Git 可追踪移动迁移人工文档；
3. 移动 Swagger 四件套并修正包名和 Go import；
4. 记录当前 Swagger 非完整注解生成链的约束，禁止默认命令覆盖现有规格；
5. 更新所有当前链接和路径引用；
6. 运行文档、Swagger、Go 构建和静态扫描验证；
7. 检查最终目录，不保留空目录或重复当前文档。

## 10. 验证与验收

必须满足：

- `docs/` 下不存在 `.go`、Go 测试、`swagger.json`、`swagger.yaml` 或其他机器生成运行时文件；
- `internal/apidocs/` 四件套齐全，包名和导入路径正确；
- `go test ./internal/apidocs` 通过；
- Web 服务相关 Go 包能够编译，Swagger 注册仍生效；
- `docs.go`、`swagger.json` 和 `swagger.yaml` 同步测试通过；
- 迁移前后的 `swagger.yaml`、`swagger.json` 内容一致，`docs.go` 除包名外的注册内容一致；
- 仓库内不存在会默认向 `docs/` 输出 Swagger 的生成脚本或说明；
- 非归档源文件对旧文档路径的扫描结果为零；
- 根 README 和各语言 README 的架构链接有效；
- PRD、状态页、当前控制台设计、API 和部署文档均可从 `docs/README.md` 到达；
- 归档目录明确标注“非当前实施依据”；
- `docs/project/status.md` 的已完成项都有提交、测试或验收证据；
- Markdown 相对链接检查通过；
- `git diff --check` 通过；
- 未移动或删除 `NOTICE`、`LICENSE`、规则数据及无关用户文件。

## 11. 非目标

- 不修改 API 语义或重新生成不同内容的 Swagger；
- 不实现企业控制台或其他产品功能；
- 不重写 PRD、架构演进和部署正文；
- 不把所有文档合成一个巨型文件；
- 不删除历史设计和旧实施计划；
- 不把代码或生成物重新放回 `docs/`；
- 不以本次整理替代后续企业控制台实施计划。
