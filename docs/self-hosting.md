# 自行部署 Qraft

Windows 用户优先使用[客户端本机部署](windows-desktop.md)：不需要克隆源码，界面会管理匹配后端包与实例凭据。

准备向互联网用户提供服务时，请使用[公网部署指南](cloud-deployment.md)，支持公网 IPv4 或域名及管理员邀请注册；本页命令用于回环地址的本机部署。

本文用于 Linux / WSL 的源码部署。需要 Docker Engine 与 Compose v2、Python 3、Git；服务运行 Linux x64 镜像。构建时需要网络下载锁定依赖和基础镜像。

## 启动空工作区

```bash
git clone https://github.com/Gingoo-TvT/Qraft.git
cd Qraft
make backend-up
```

`scripts/configure.py` 从 `.env.example` 生成本机 `.env`，为数据库、对象存储与加密配置独立随机值。文件已存在时保留，不覆盖旧凭据。脚本不填写模型服务地址、模型名称或 API Key。首次启动暂不启用 embedding，因此 API 可以在模型配置为空时提供设置页面。

`make backend-up` 构建镜像、绑定沙箱身份，再启动数据库、迁移、API、Web 与沙箱。打开 **http://localhost:18180**。此时生成 worker 尚未启动，先按[账号与权限指南](shared-service-auth.md)使用当前实例的一次性令牌初始化管理员，再完成模型配置。源码默认启用账号认证；本机 HTTP 使用非 Secure Cookie，公网前必须切换 HTTPS 设置。

## 启用生成

1. 在模型配置中保存生成、解答与审查模型。
2. 在去重服务中填写你自己的 embedding API 地址、模型和密钥，测试并保存版本。默认允许使用公网 API；服务仍仅监听本机回环地址。
3. 执行：

```bash
make worker-up
```

共享账号模式下，该命令会在终端询问管理员邮箱和密码，建立临时会话并在读取后退出；密码不会写入文件。该命令读取自己工作区已保存的 embedding 身份，将 embedding 设为启用，并同时重建 API 和启动 worker，让两个进程使用相同配置。API Key 留在后端加密设置里，不会由脚本导出。重新打开去重服务页面，按提示完成回填与启用。更换去重模型时先处理旧任务，再沿用此流程；不要手工伪造 active pointer。

如果改过 HTTP_PORT，Makefile 会读取 `.env` 中的端口。也可以显式执行：

```bash
python3 scripts/configure.py --saved-embedding http://localhost:18180
docker compose up -d --wait --wait-timeout 360 api worker
```

同一 API 地址和模型名称注册了多个版本时，从去重服务页面复制目标版本 ID，明确选择（将下列 `YOUR_MODEL_VERSION_UUID` 替换为复制的值）：

```bash
make worker-up MODEL_VERSION_ID=YOUR_MODEL_VERSION_UUID
# 或直接指定脚本参数：
python3 scripts/configure.py --saved-embedding http://localhost:18180 --model-version-id YOUR_MODEL_VERSION_UUID
docker compose up -d --wait --wait-timeout 360 api worker
```

未配置、版本不明确或身份不完整时，脚本不会启用 embedding，也不会修改现有密钥。只需要内网 embedding 的管理员可以将 `ALGOFORGE_EMBEDDING_UI_ALLOW_PUBLIC=false` 后重建 API，限制页面填写的服务地址。

## 数据与端口

源码部署默认 Compose 项目为 `qraft`，客户端管理的实例为 `qraft-desktop`，两者各自保存数据。不要把两个管理方式指向同一组卷或复制别人的 `.env`。

所有默认主机端口绑定回环地址，主要入口为 18180。服务间通过内部网络通信，沙箱不公开端口。题库存于 PostgreSQL，任务历史存于 Temporal，题目文件存于 MinIO；删除数据卷会删除相应数据。

当前源码提供管理员邀请注册的共享工作区，成员共享已发布题库，只管理自己的任务。公网部署先运行 `python3 scripts/configure.py --shared`，配置 HTTPS 反向代理，再显式重建并重启匹配版本服务。代理同时转发 Web 与 `/api/*`，客户端填写 HTTPS 根地址。不要公开开发模式或数据库、Temporal、MinIO 管理端口；完整流程见[账号与权限](shared-service-auth.md)。V2.2.0 后端发行包没有该模块，不能只改环境变量就获得认证。

## 停止与升级

```bash
docker compose stop worker
docker compose stop
```

worker 允许活动任务收尾，最长停止宽限期为 40 分钟。从旧源码升级时 `.env` 保持原样，不自动关闭开发模式；共享部署应明确执行 `--shared` 并完成首次管理员初始化。历史无归属任务只向管理员开放。升级前等待任务结束并备份数据库、对象存储及 `.env` 中的加密密钥，再执行 `make backend-up` 基于新提交重建匹配镜像，最后执行 `make worker-up`。构建前脚本只刷新 `SOURCE_REVISION` 和 `SANDBOX_REVISION`，保留已有实例凭据与模型配置；普通 `make setup` 不改已有文件。恢复应使用新的空实例验证，不覆盖唯一的原数据副本。

Qraft 是独立项目，不会自动迁移其他系统的数据库或客户端资料。保留原实例作为备份，通过明确的通用导入导出流程转移你有权使用的题目。

排错时先查看 `docker compose ps` 和对应服务日志。不要把 `.env`、模型密钥或非公开题目贴到公共 issue。
