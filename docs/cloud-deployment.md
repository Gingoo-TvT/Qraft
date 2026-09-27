# 公网部署 Qraft

将一套 Qraft 服务部署到 Linux 云服务器，用户通过浏览器，或在配套 Windows 客户端中填写 HTTPS 根地址后登录。适用于腾讯云 CVM 等有稳定公网 IPv4 的服务器，也支持已经解析到服务器的域名。

> 本文对应**尚未发布的共享服务源码**，需要包含本页、`docker-compose.cloud.yml`、`deploy/caddy/Caddyfile.cloud` 及认证模块的完整版本。旧 **V2.2.1 客户端 / V2.2.0 后端包**不具备这里的账号能力，不能通过修改环境变量补齐。本文是部署操作说明，不代表某个实例已经上线或通过验收。

## 准备服务器和入口

- 使用 Linux x64，安装 Docker Engine、**Docker Compose >= 2.24.4**、Python 3、Git。云端覆盖文件使用 Compose 的 `!reset`、`!override` 语法。
- 取得经过验证的配套源码，并进入仓库根目录。先核对部署提交；不要混用不同提交的 API、worker、Web 与客户端。源码构建需要能下载锁定依赖和容器镜像。
- 为本实例保留独立数据卷和 `.env`。默认 Compose 项目名为 `qraft`；同机有另一套实例时，先规划独立项目名与入口，不要接管其卷。
- 应用入口只开放 **TCP 80、443**；腾讯云安全组与系统防火墙都需允许它们。SSH 等运维入口仅向管理员开放。80 用于跳转及证书验证，443 提供 HTTPS；不要将数据库、Redis、Temporal、MinIO、API、前端或沙箱端口额外公开。
- 使用公网 IPv4 时，直接填写该地址。使用域名时，先将 A 记录指向服务器；错误或不可达的 AAAA 记录也需处理。当前配置脚本接受公网 IPv4 或域名，不接受裸 IPv6、内网地址、端口和完整 URL。
- 确认 80/443 未被其他服务占用，且服务器能访问 ACME、模型与 embedding API。公网地址应保持稳定。

本方案使用 Caddy 的显式 ACME issuer 和 `shortlived` 证书配置。Let’s Encrypt 已支持 IP 地址证书；IP 证书有效期为 160 小时，必须自动续期。Caddy 负责申请和续期，需持续保留公网可达性与 `caddy_data` 卷；关闭服务器数天后不能假定证书仍然有效。参见 [Let’s Encrypt 官方说明](https://letsencrypt.org/2026/01/15/6day-and-ip-general-availability/)。

不要为了跳过证书问题使用浏览器忽略警告、`curl -k` 或关闭 Secure Cookie。证书未就绪时先排查入口和 Caddy 日志。

## 1. 配置并启动设置页面

以下命令使用 Bash，在仓库根目录执行。将示例域名替换为自己的公网 IPv4 或域名，不带 `https://`：

~~~bash
QRAFT_DEPLOY_HOST='qraft.example.com'
python3 scripts/configure.py --cloud-host "$QRAFT_DEPLOY_HOST"
python3 scripts/configure.py --refresh-revision

qraft() {
  docker compose -f docker-compose.yml -f docker-compose.cloud.yml "$@"
}

docker compose version
qraft config --quiet
qraft build
python3 scripts/configure.py --bind-sandbox
qraft up -d --wait --wait-timeout 360 caddy sandbox
qraft ps
~~~

`--cloud-host` 创建或保留当前实例凭据，配置共享账号、Secure Cookie 和公网入口，不启动服务。不要把 `.env` 或渲染后的完整 Compose 配置公开；后者也含密钥。

构建完成后必须执行 `--bind-sandbox`：它校验镜像的源码版本，并将实际沙箱镜像、工具链与策略摘要写入本实例配置。每次重建沙箱后重新绑定，不手填摘要。

上述启动命令通过依赖关系启动 API、Web、数据库、Redis、MinIO、Temporal 及迁移，另外启动内部沙箱；**暂不启动 worker**。新实例尚未保存模型与 embedding，先完成下面的初始化。云端覆盖文件移除其他服务的主机端口，只有 Caddy 发布 80/443；Temporal 管理界面默认不启动，公网 `/temporal` 返回 404。

后续所有 Compose 命令都必须同时加载这两个文件。重新进入终端后重新定义 `qraft` 函数；不要使用只加载本机配置的 `make backend-up`、`make worker-up` 或裸 `docker compose up` 替代。

等待证书申请完成，检查：

~~~bash
qraft logs --tail=100 caddy api migrations
curl -fsS "https://$QRAFT_DEPLOY_HOST/health"
~~~

`up --wait` 与 `/health` 只证明服务启动和基础健康，不代表模型生成或证书续期已完成验证。初次拉镜像、数据库初始化和证书申请可能耗时；遇到超时先读日志，不删除数据卷重试。

## 2. 初始化管理员，保存模型配置

在服务器私密终端查看本实例的一次性令牌：

~~~bash
python3 scripts/configure.py --show-bootstrap-token
~~~

用浏览器打开 `https://你的公网IPv4或域名`。首次登录界面会提示初始化，填写令牌、管理员邮箱、显示名称和密码。页面使用 `POST /api/v1/auth/bootstrap`；令牌不放入 URL。已有账号的数据库不能再次初始化管理员。

完成后：

1. 在**模型配置**保存生成、解答与审查等所需模型，并检查连接。
2. 在**去重服务**填写 embedding API、模型、密钥，测试并保存模型版本。当前运行时要求 **1536 维**；保存身份不等于已经启用运行时。
3. 在服务器终端应用已保存的 embedding 身份：

~~~bash
python3 scripts/configure.py --saved-embedding "https://$QRAFT_DEPLOY_HOST"
qraft up -d --wait --wait-timeout 360 api worker
qraft ps
~~~

脚本询问管理员邮箱和密码，密码输入不回显；通过临时 Cookie 会话读取身份后退出。模型密钥留在服务端的加密配置中，不由脚本导出。URL 使用服务**根地址**，不要追加 `/api/v1`。

同一地址与模型有多个已注册版本时，从去重页面复制版本 ID，再执行：

~~~bash
python3 scripts/configure.py --saved-embedding "https://$QRAFT_DEPLOY_HOST" --model-version-id YOUR_MODEL_VERSION_UUID
qraft up -d --wait --wait-timeout 360 api worker
~~~

将 `YOUR_MODEL_VERSION_UUID` 替换为页面中的真实版本 ID。脚本拒绝不明确或不完整的身份；不要凭空生成版本 ID。重新打开去重服务页面，按提示完成回填与启用。API 和 worker 必须一起应用相同运行时配置。

初始化完成后可以从 `.env` 删除 `QRAFT_AUTH_BOOTSTRAP_TOKEN` 的值，在下一次正常重建 API 时生效；保留数据库与设置加密密钥。数据库已有账号时，初始化接口本身已经关闭。

## 3. 邀请用户并验收完整流程

1. 管理员打开**成员管理**，为用户邮箱创建注册邀请，复制链接通过自己的渠道发送。
2. 用户打开邀请链接，设置密码完成注册，再登录 Web；Windows 客户端需使用包含认证模块的配套构建，保存相同 HTTPS **根地址**后登录。
3. 管理员确认生成 worker 与去重配置就绪，再让成员提交一道合成测试题，完成生成、查看任务、验算与导出。
4. 用两个不同成员账号检查各自任务隔离；对方的任务 ID、事件和未共享结果不可访问。再验证邀请不可重复消费、退出、密码重置及停用账号。

首期是**管理员邀请注册**，没有任何人都能注册的公开入口，也不发送邮件。邀请绑定邮箱、限时、单次使用；管理员核实接收人并传递链接。密码要求、角色权限及遗忘密码处理见[账号与权限](shared-service-auth.md)。

验收时应同时确认：未登录的业务接口返回 401，成员访问管理接口返回 403，HTTPS 证书可信，下载与长任务状态更新正常。通过基础健康检查并不能替代这些检查。日志与排障截图须移除凭据、邀请链接和题目私有内容。

## 备份、更新与恢复

备份至少覆盖：`.env`、部署源码提交及构建身份、业务数据库 `pgdata`、Temporal 数据库 `temporal_pgdata`、对象文件 `miniodata`。完整实例恢复还应保留 `redisdata`、`sandbox_audit`、`caddy_data` 与 `caddy_config`。这些是 Compose 的逻辑卷名，实际名称带项目名前缀。

简单可靠的首期方式是安排维护窗口：等待任务结束，停止 worker，再停止本实例，给承载全部数据卷的云盘做一致性快照，并将 `.env` 和版本记录加密备份到服务器之外。不要在数据库持续写入时把其数据目录直接复制当作可靠备份。停止命令不删除卷：

~~~bash
qraft stop worker
qraft stop
~~~

worker 最长允许 40 分钟收尾，不要为了加速维护直接强杀。备份后用本页相同双文件配置重新启动原版本；生成已配置的实例可启动 `caddy sandbox worker`。

升级前先完成上述备份，取得经过验证的完整新版本，保留本实例 `.env` 和数据卷，再执行：

~~~bash
python3 scripts/configure.py --refresh-revision
qraft build
python3 scripts/configure.py --bind-sandbox
qraft up -d --wait --wait-timeout 360 caddy sandbox
python3 scripts/configure.py --saved-embedding "https://$QRAFT_DEPLOY_HOST"
qraft up -d --wait --wait-timeout 360 api worker
~~~

迁移会先于 API 执行。恢复应先在隔离的空实例验证，使用备份对应的源码、镜像、卷及原设置加密密钥，再切换入口。丢失 `ALGOFORGE_SETTINGS_ENCRYPTION_KEY` 会导致已保存模型密钥无法解密；仅退回旧镜像不能可靠撤销数据库迁移。不要覆盖唯一备份，也不要执行 `down -v` 删除数据卷。

## 当前边界与排障

这是单机团队共享服务，提供基础 HTTPS 入口和账号权限；没有多租户、自动邮件、用量配额、跨节点高可用或自动异地备份。管理员需要监控磁盘、备份与证书续期，按服务器能力调整任务并发。模型调用与沙箱资源会影响实际生成能力，首次部署应通过完整测试题验证后再邀请更多用户。

常用排查命令：

~~~bash
qraft ps
qraft logs --tail=100 api worker sandbox
qraft logs --tail=100 caddy migrations
~~~

- 页面打不开或证书未签发：检查 80/443、域名解析、公网 IPv4、ACME 可达性与 Caddy 日志。
- 能登录但不能生成：检查保存的模型、embedding 身份与启用状态，以及 worker、沙箱健康和绑定摘要。
- 注册失败：检查邀请类型、邮箱、有效期及是否已消费；不把评题链接当注册邀请。
- 服务可达但旧客户端无法登录：更新为包含此认证模块的配套客户端，或先使用 Web。

运行配置与数据属于部署实例，不应提交到公开仓库。
