# AI-Infra-Guard 文档

本目录是 AI-Infra-Guard 人工维护文档的统一入口。以下链接按目标信息架构列出；目录迁移分阶段进行期间，部分目标文件会在后续任务中就位。

## 项目与产品

- [项目状态](project/status.md)：当前进度、里程碑证据、下一步和风险的唯一实时来源。
- [产品需求文档（PRD）](product/prd.md)：当前产品范围、角色边界和验收要求。
- [当前实施计划](project/plans/documentation.md)：文档信息架构整理实施计划。
- 企业控制台实施计划：待编写。

## 架构

- [架构演进](architecture/evolution.md)
- [企业控制台设计](architecture/enterprise-console.md)
- [文档治理设计](architecture/documentation.md)

## API

- [API 参考（中文）](api/reference.md)
- [API Reference (English)](api/reference.en.md)
- [API 数据同步](api/data-sync.md)

## 部署与归属

- [PostgreSQL 部署](deployment/postgres.md)
- [上游归属说明](deployment/attribution.md)

## 历史归档

- [2026-08 企业平台设计与计划归档](archive/2026-08-enterprise-platform/README.md)

归档文档保留历史上下文和当时的任务状态，不再更新，也不作为当前实施进度依据。当前进度只以[项目状态](project/status.md)为准。

## 维护边界

- `docs/` 只存放供人阅读和维护的文档。
- Swagger 运行时契约、注册代码和生成物位于 `internal/apidocs/`。
- 新的当前有效文档应归入产品、项目、架构、API 或部署分类；已被替代的设计与计划进入归档区。
