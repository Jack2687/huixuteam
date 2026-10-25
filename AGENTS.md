# llm-refund 工作约定

- 这是独立退款工作台，不是 space-merge 或 space-user 的线上发布目录。
- 沿用 Vue 3 / TypeScript / Vite / Tailwind、Go net/http 和前端嵌入结构，数据库为 PostgreSQL。
- 正式业务数据在 `.env` 指向的 PostgreSQL；本地独立实例在 `.local/postgres`，端口 18742；应用 18740。不得修改已有 5432 实例和 Space 项目容器。
- `.env`、`.local`、管理员密码、数据库密码、加密主密钥、退款凭据、完整 API key / 兑换码不能提交到 Git。
- 退款执行队列已接入、默认关闭。维护前阅读 `docs/REFUND-INTEGRATION.md`，不得模拟上游成功。所有网站角色只允许通用进度，内部协议/步骤禁止通过页面、API、静态资源或文档泄露。
- 管理权限必须后端检查；个人资源始终按用户 ID 隔离；兑换、冻结、释放与结算须在数据库事务内完成。
- 代码修改后执行相应检查。完整验证入口 `scripts/test-local.ps1`；浏览器脚本仅使用独立 QA 数据库与 18743 端口。
