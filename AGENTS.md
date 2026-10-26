# llm-refund 工作约定

- 这是独立退款工作台，不是 space-merge 或 space-user 的线上发布目录。
- 沿用 Vue 3 / TypeScript / Vite / Tailwind、Go net/http 和前端嵌入结构，数据库为 PostgreSQL。
- 正式业务数据在 `.env` 指向的 PostgreSQL；本地独立实例在 `.local/postgres`，端口 18742；应用 18740。不得修改已有 5432 实例和 Space 项目容器。
- `.env`、`.local`、管理员密码、数据库密码、加密主密钥、退款凭据、完整 API key / 兑换码不能提交到 Git。
- 退款执行队列已接入、默认关闭。维护前阅读 `docs/REFUND-INTEGRATION.md`，不得模拟上游成功。客户仅允许通用进度；用户于 2026-10-26 授权申请全监控按 admin.refunds.read 权限显示单字阶段、时间、耗时和通用状态。该只读投影不得暴露协议、上游接口、请求详情、凭据、资源身份或原始诊断，也不得触发实际操作。
- 管理权限必须后端检查；个人资源始终按用户 ID 隔离；兑换、冻结、释放与结算须在数据库事务内完成。
- 代码修改后执行相应检查。完整验证入口 `scripts/test-local.ps1`；浏览器脚本仅使用独立 QA 数据库与 18743 端口。

## 正式部署（2026-10-26）

- 先读 `docs/DEPLOYMENT.md` 和父目录 `_codex-memory/huixu-production-runbook.md`。
- GitHub `https://github.com/yigerende/huixuteam.git`（用户明确要求公开）；私密配置、凭据和运行数据绝不提交。
- 正式目录 `/opt/1panel/apps/huixuteam`，Compose `compose.production.yml`，项目 `huixuteam`。
- 应用 `huixuteam-app`，公网端口 7825；数据库 `huixuteam-postgres`，独立卷 `huixuteam_postgres_data`，不映射宿主机数据库端口。
- 2026-10-26 已完整迁移原本机数据和 MASTER_KEY；本机应用 18740 已停止，防止双队列。不要自动重启旧本机服务当成正式环境。
- 更新只操作此项目，保留 `.env`、主密钥、数据库与数据卷，不重启其他容器。先构建、备份，再仅切换 app。
- 用户于 2026-10-26 明确要求：以后正式环境重建前必须先检查是否有正在执行的申请。核对 processing/verifying 状态及未释放的有效执行租约；有任务在跑时等待完成，不强行中断、不改申请状态。镜像可先构建，实际重建前必须再次确认空闲，不能只用构建前的旧检查。持续有新任务时先安排暂停领取并保留原设置，确认执行任务结束后切换，完成后恢复原设置；排队申请、积分和进度必须保留。
