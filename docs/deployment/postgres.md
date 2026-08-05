# PostgreSQL 部署与迁移

AI-Infra-Guard 当前交付仅支持 PostgreSQL。数据库服务固定使用
`postgres:16.4-alpine`；平台镜像应使用明确的发布版本，例如
`zhuquelab/aig-server:<release-version>`。本仓库的 Compose 文件默认构建并使用
`aig-platform:local`，便于本地验证。

## 初始化

在 `deploy/compose` 目录或通过环境变量设置强密码，并单独提供使用同一凭据、且密码部分
已经 URL 编码的完整 `DB_DSN`，然后先启动数据库：

```bash
export POSTGRES_PASSWORD='example:p@ss' # 替换为实际密码，勿写入仓库或日志
export DB_DSN='postgres://aig:example%3Ap%40ss@postgres:5432/aig?sslmode=disable'
docker compose -f deploy/compose/docker-compose.postgres.yml up -d postgres
docker compose -f deploy/compose/docker-compose.postgres.yml run --rm migrate
```

`POSTGRES_USER` 和 `POSTGRES_DB` 默认都是 `aig`。该用户必须拥有目标数据库内
建表、建索引和写入 `schema_migrations` 的权限；无需超级用户权限。迁移容器会等待
PostgreSQL 健康检查成功后运行，并以成功完成作为后续 `platform`、`agent` 服务的
依赖条件。`POSTGRES_PASSWORD` 仅用于初始化 PostgreSQL；`migrate` 和平台服务均使用
相同的 `DB_DSN`。若密码中含 `:`, `@`, `/`, `?`, `#` 或 `%` 等字符，必须在 `DB_DSN`
中进行 URL 编码。

数据保存在命名卷 `aig-postgres-data`，不会与测试 Compose 使用的资源共享。生产
应在受控网络中运行 PostgreSQL，且不要将 `DB_DSN` 或密码写入日志或提交到仓库。

## 备份与恢复

在升级前创建逻辑备份：

```bash
docker compose -f deploy/compose/docker-compose.postgres.yml exec -T postgres \
  pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc > aig-before-migrate.dump
```

需要恢复时，先停止依赖数据库的服务，然后创建空库并执行：

```bash
pg_restore --clean --if-exists --no-owner -d "$DB_DSN" aig-before-migrate.dump
```

在实际灾难恢复中，请先在隔离环境演练恢复流程并验证 `schema_migrations` 与业务表。

## 迁移失败处置与回滚

迁移是版本化且可重复执行的：成功的版本会记录在 `schema_migrations`。如果迁移
失败，容器会以非零状态退出，后续服务不应启动。保留失败日志和数据库卷，修复
权限、连通性或版本问题后重新运行相同的 `migrate` 命令。

当前初始迁移没有自动 down 迁移。需要回滚到发布前状态时，应停止服务并从升级前
备份恢复；不要手动删除迁移版本记录或业务表。确认恢复完成后，再部署与该备份
兼容的平台镜像版本。
