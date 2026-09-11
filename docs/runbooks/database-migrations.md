# 数据库迁移与恢复

本文用于自己的 Qraft 源码部署，命令在项目根目录执行，并使用该实例原有的 Compose 参数和 .env。

## 首次安装与旧系统边界

Qraft 的公开源码以全新空库为基线。迁移 014、019 已去除旧集成实例相关的初始化内容，因此它与旧私有项目的迁移文件不再逐字节相同。不要把 Qraft 迁移直接指向旧项目数据库，也不要修改旧库的迁移摘要来绕过检查。

新用户按 [自行部署](../self-hosting.md) 启动即可。需要保留旧系统内容时，保留原实例，通过通用题目导入导出选择性转移自己的数据；当前不提供跨项目数据库原地升级。

## 升级现有 Qraft 实例

1. 记录当前提交与镜像版本，保留旧代码和配置。
2. 等待活动任务结束，停止该实例的 worker、API 以及其他写入者。
3. 备份 PostgreSQL、Temporal 数据库、对象存储及实例 .env。模型密钥的解密依赖原有加密配置，不能只保留数据库。
4. 构建与新提交匹配的迁移镜像，检查已应用版本，再运行增量迁移。
5. 在新数据副本上完成恢复与功能验证后，再更新自己的服务。

常用命令：

```bash
docker compose build migrations
docker compose run --rm migrations status
docker compose run --rm migrations up
docker compose run --rm migrations status
```

迁移器检查版本、文件名和摘要，并通过数据库锁避免并发迁移。每个 SQL 文件与对应的迁移记录在同一事务中提交。已在 Qraft 实例应用的迁移应保持不变，后续修改新增迁移文件。

如果 status 报告缺文件、版本或摘要不一致，先检查是否用了错误项目或版本，不要直接编辑 schema_migrations。Qraft 没有通用 migrate down；不可逆结构变更应从备份恢复。

## 应用数据库备份示例

先暂停所有写入者。以下示例只备份应用 PostgreSQL；Temporal 数据库、MinIO 数据和 .env 仍需分别保留。

```bash
QRAFT_BACKUP_DIR=/path/outside/repository/qraft-backup
mkdir -p "$QRAFT_BACKUP_DIR"
chmod 700 "$QRAFT_BACKUP_DIR"
docker compose exec -T postgresql sh -euc \
  'pg_dump --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" --format=custom' \
  > "$QRAFT_BACKUP_DIR/qraft.dump"
docker compose exec -T postgresql pg_restore --list \
  < "$QRAFT_BACKUP_DIR/qraft.dump" > "$QRAFT_BACKUP_DIR/qraft.contents"
sha256sum "$QRAFT_BACKUP_DIR/qraft.dump"
```

将示例路径替换为自己受控的备份目录。备份可能包含题目、答案和加密配置记录，应限制访问并保存在仓库外；不要把数据库或 .env 上传到公共 issue 或 CI。

## 恢复检查

先恢复到新数据库或新的隔离实例，保留原数据不动。使用与备份相容的 PostgreSQL/pgvector 版本；重建大型向量索引时，根据可用内存调整恢复会话的 maintenance_work_mem，而不是修改所有会话的全局配置。

使用同一 PostgreSQL 服务中新建的独立数据库进行恢复检查时，可以参考：

```bash
docker compose exec -T -e QRAFT_RESTORE_DB=qraft_restore postgresql sh -euc \
  'createdb --username="$POSTGRES_USER" "$QRAFT_RESTORE_DB"'
docker compose exec -T -e QRAFT_RESTORE_DB=qraft_restore postgresql sh -euc \
  'pg_restore --exit-on-error --no-owner --username="$POSTGRES_USER" --dbname="$QRAFT_RESTORE_DB"' \
  < "$QRAFT_BACKUP_DIR/qraft.dump"
```

先确认恢复数据库名尚未存在，且不会被运行中的服务使用。此示例不修改服务连接配置，不会自动切换工作区。

至少检查迁移记录、关键表数量、约束与索引，并用相应对象存储副本核对题目文件、测试数据和模型配置读取。恢复成功不等于备份期间没有丢失写入；若业务不能暂停写入，需要另行配置并验证连续备份/PITR。

需要回退时，使用经过验证的整套备份和匹配代码恢复；不要仅退回应用镜像后继续写入不兼容结构。
