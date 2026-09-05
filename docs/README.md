# AI-Infra-Guard 文档

本目录是 AI-Infra-Guard 人工维护文档的统一入口，当前有效文档按产品、项目、架构、API、部署与归档分类。

## 项目与产品

- [项目状态](project/status.md)：当前进度、里程碑证据、下一步和风险的唯一实时来源。
- [产品需求文档（PRD）](product/prd.md)：当前产品范围、角色边界和验收要求。
- [平台功能清单](product/features.md)：当前平台能力、入口与实现状态的盘点。
- [当前实施计划目录](project/plans/README.md)：当前有效计划及其完成状态。
- [企业控制台实施计划](project/plans/enterprise-console.md)：已完成实施与验收，证据见[项目状态](project/status.md)。

## 架构

- [架构演进](architecture/evolution.md)
- [企业控制台设计](architecture/enterprise-console.md)
- [AI 基础设施扫描开发说明与四类扫描接入规范](architecture/scan-workbench-development.md)：以 AI 基础设施扫描为参照，约定 MCP 扫描、Skills 扫描、Agent 工作流扫描专属工作台的开发与验收基线，区分已有合同与待补齐能力。
- [文档治理设计](architecture/documentation.md)

## API

- [API 参考（中文）](api/reference.md)
- [API Reference (English)](api/reference.en.md)
- [API 数据同步](api/data-sync.md)

Swagger 运行时三件套是 `internal/apidocs/swagger.yaml`、`internal/apidocs/swagger.json` 和 `internal/apidocs/docs.go`。完整 OpenAPI 单一源建立前，必须通过现有同步测试共同维护三者，不得直接运行默认 `swag init` 覆盖；修改后运行 `go test ./internal/apidocs`。

## 部署与归属

- [PostgreSQL 部署](deployment/postgres.md)
- [上游归属说明](deployment/attribution.md)

## 历史归档

- [2026-08 企业平台设计与计划归档](archive/2026-08-enterprise-platform/README.md)
- [历史日文 API 归档](archive/legacy-api/README.md)

归档文档保留历史上下文和当时的任务状态，不再更新，也不作为当前实施进度依据。当前进度只以[项目状态](project/status.md)为准。

## 维护边界

- `docs/` 只存放供人阅读和维护的文档。
- Swagger 运行时契约、注册代码和生成物位于 `internal/apidocs/`，按上述三件套规则共同维护。
- 新的当前有效文档应归入产品、项目、架构、API 或部署分类；已被替代的设计与计划进入归档区。
