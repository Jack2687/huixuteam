# 回序正式部署

- 仓库：`https://github.com/yigerende/huixuteam.git`，按用户要求公开；仅部署密钥、业务凭据与运行数据保持私密。
- 服务目录：`/opt/1panel/apps/huixuteam`。
- Compose 项目：`huixuteam`，配置：`compose.production.yml`。
- 应用容器：`huixuteam-app`，对外 `7825`，容器内 `18740`。
- 数据库容器：`huixuteam-postgres`，PostgreSQL 18，数据库和用户 `huixu`，不映射宿主机数据库端口。
- 网络 `huixuteam_default`；数据卷 `huixuteam_postgres_data`。
- 环境变量在服务器 `.env`，权限 600；私钥、凭据、备份和运行数据不进仓库。

## 首次部署

先检查目录、7825 端口、容器名、网络名和卷名不存在冲突，记录其他容器的 ID、启动时间和重启次数。

```sh
git clone https://github.com/yigerende/huixuteam.git /opt/1panel/apps/huixuteam
cd /opt/1panel/apps/huixuteam
cp .env.production.example .env
chmod 600 .env
# 填写数据库随机密码、32 字节 Base64 主密钥和初始化管理员凭据。
# HUIXU_VERSION 填本次 git rev-parse HEAD；所有密码仅在服务器配置中保存。
docker compose -p huixuteam -f compose.production.yml build app
docker compose -p huixuteam -f compose.production.yml up -d postgres
# 如需迁移，先导入原数据库并保留原 MASTER_KEY，再启动应用。
docker compose -p huixuteam -f compose.production.yml up -d --no-deps --no-build app
```

直接 HTTP 使用 COOKIE_SECURE=false。配置 HTTPS 入口后可设为 true。迁移时须同时保存数据库与原 MASTER_KEY，否则原加密凭据无法读取；不能同时运行两套自动办理同一业务数据。

## 后续更新

1. 本地完成必要测试并推送 GitHub；服务器仅在此目录 `git pull --ff-only`。
2. 原服务运行时先构建新版本镜像，保存旧镜像标签。数据库和 `.env` 备份放受保护目录。
3. **重建前检查业务是否空闲**：查询 processing/verifying 申请和未释放且未过期的执行租约。有正在办理的任务时等待其完成，不强制重启或取消申请。构建完成后、实际切换前再次检查；若持续有新任务，安排暂停领取新申请并记录原开关，确认在途任务结束后再切换，验证后恢复原设置。排队记录和预留积分保留，不清空队列。
4. 只在本项目 Compose 中更新 app：

```sh
docker compose -p huixuteam -f compose.production.yml up -d --no-deps --no-build --force-recreate app
```

5. 核验 `http://127.0.0.1:7825/health/live`、`/health/ready`、版本标签、页面资源和日志，并比对其他容器仍未变化。
6. 不执行全局 Docker 清理、全局重启、删除数据卷或修改其他项目 Compose。失败时恢复旧应用镜像，不用旧数据库覆盖新业务数据。

异常恢复计划保存在数据库，等待重试不代表正在执行上游请求；发布检查应同时查看状态、`next_attempt_at` 与有效租约，不能丢弃等待中的申请。持有调度锁 `718374023` 可阻止新领取，等待有效在途任务结束后备份及切换，完成健康检查再释放锁。不要把关闭新申请队列当成关闭既有任务的恢复开关。备份统一放 `/opt/1panel/apps/codex-release-backups/huixu`，成功验证后仅保留最近 3 份完整备份；清理前检查挂载、Compose 与进程引用。

不对真实客户执行测试退款。代理、母号校验仅通过明确操作发起只读请求；自动办理保持用户设置。
