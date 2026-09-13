# 开发与打包

## 开发环境

- Node.js 22.12+ 与 npm；使用 `package-lock.json`。
- Go 1.24.4+；各模块的 `go.mod` 固定自己的最低版本和依赖。
- Python 3.12；生成桌面 Compose 时需要 `PyYAML==6.0.2`。
- Docker 用于集成验证与后端镜像；NSIS 用于 Windows 安装包。

```bash
git clone https://github.com/Gingoo-TvT/Qraft.git
cd Qraft
(cd frontend && npm ci)
python3 -m pip install PyYAML==6.0.2
```

## 常用检查

```bash
go -C backend test ./... -count=1
go -C desktop test -race ./... -count=1
go -C sandbox test ./... -count=1
(cd frontend && npm run test:search && npm run test:desktop && npm run lint && npm run build && npm run build:desktop)
python3 desktop/scripts/prepare-runtime.py --check
python3 scripts/check-repository.py
```

修改 Windows 专属逻辑时，还需在 Windows 上运行桌面测试与检查；Linux 上可先用 `GOOS=windows GOARCH=amd64 go -C desktop vet ./...` 检查平台代码，交叉编译不能替代原生窗口验证。

前端开发运行 `cd frontend && npm run dev`；请求默认同源。若独立使用开发服务器，应显式配置 `NEXT_PUBLIC_API_URL` 指向自己的测试后端，见 `.env.example`。

桌面界面构建后，可在 Linux 前台预览：

```bash
go -C desktop run ./cmd/qraft --serve-ui --data-dir /tmp/qraft-preview --output /tmp/qraft-preview.json
```

进程输出和文件中提供随机回环地址，Ctrl C 退出。仅浏览器预览不能替代真实 Windows 窗口、安装、文件保存与后端实测。

## 生成安装包

在已提交且干净的仓库中执行：

```bash
python3 desktop/scripts/package.py --out /absolute/path/qraft-artifacts \
  --makensis /path/to/makensis \
  --webview-bootstrapper /path/to/MicrosoftEdgeWebview2Setup.exe
```

提前在 Windows 确认微软引导程序的签名。脚本构建与当前提交绑定的后端镜像、桌面工作台、EXE、安装版和便携 ZIP，并生成摘要清单。产物必须放在仓库外。

发布前至少验证：空首启不请求旧服务、两种连接方式、原生 WebView2 窗口、主题和表单输入、安装与卸载、纯净数据库初始化、题集保存和导出、沙箱执行。只上传明确列出的发行产物，不上传临时数据目录或本机测试日志。

GitHub CI 检查主分支及 PR。Release 附件必须与通过检查的提交一致；代码构建通过不意味着外部模型题目质量或公开部署已经验证。

发行说明保存在 `docs/releases/`，当前为 [V2.2.0](releases/v2.2.0.md)。发布时同步版本声明、README 下载入口、Windows 使用指南与 GitHub Release 正文，列出升级步骤及客户端/服务端兼容要求。不要把客户端更新描述成后端更新；也不要把编译通过写成已完成安装或业务实测。

## 保持仓库整洁

源码、可合成测试夹具、文档和锁文件进入 Git；构建目录、EXE、运行配置、数据库、题库、调试截图和机器日志留在忽略目录或仓库外。文档展示图片只能来自确认可公开的空工作区或专门制作的合成样例。

修改数据库迁移时区分新项目初始化与已发布数据库升级。已应用的迁移不要回写；新增版本以追加迁移演进。第三方代码及许可证不要做品牌替换。
