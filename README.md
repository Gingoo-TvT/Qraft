<div align="center">
  <img src="docs/assets/qraft-mark.svg" width="80" alt="Qraft">
  <h1>Qraft · 题构</h1>
  <p><strong>从一道好题，到一套好题。</strong></p>
  <p>AI 辅助出题 · 混合题集 · 题库组卷 · 可复现验证</p>
  <p>
    <a href="https://github.com/Gingoo-TvT/Qraft/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/Gingoo-TvT/Qraft?style=flat-square&color=2465ae"></a>
    <a href="https://github.com/Gingoo-TvT/Qraft/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Gingoo-TvT/Qraft/actions/workflows/ci.yml/badge.svg"></a>
    <a href="LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-6f8d76?style=flat-square"></a>
    <img alt="Windows x64" src="https://img.shields.io/badge/desktop-Windows%20x64-53647e?style=flat-square">
  </p>
  <p>
    <a href="https://github.com/Gingoo-TvT/Qraft/releases/latest">下载客户端</a> ·
    <a href="docs/windows-desktop.md">开始使用</a> ·
    <a href="docs/self-hosting.md">自行部署</a> ·
    <a href="docs/development.md">参与开发</a>
  </p>
</div>

---

Qraft 是一个开源的算法题创作工作区。用自然语言描述需求，生成编程题、混合题型题集或纯编程比赛；也可以从已有题库筛选、组卷、校验并导出。

Windows 原生窗口与 Web 共用业务界面。客户端负责交互，服务端负责模型编排、代码验证、数据生成和题库存储。你可以在自己的电脑上运行完整服务，也可以连接管理员提供的已有服务。

![Qraft 首次使用：选择自己的工作区](docs/assets/welcome.png)

## 选择适合你的使用方式

| | 连接已有服务 | 在本机自行部署 |
|---|---|---|
| 适合 | 已有可用工作区的使用者 | 希望独立保存题库的个人或小团队 |
| 安装内容 | Qraft 客户端、WebView2 | 左侧内容 + Docker Desktop + 后端镜像包 |
| 首次操作 | 填写服务根地址，保存并检测 | 导入后端包，启动服务，配置模型 |
| 数据保存 | 所连接服务的数据库与对象存储 | 自己电脑上的独立 Docker 数据卷 |
| 用户侧需要 WSL / Docker | 不需要 | Docker 使用 Linux 容器；WSL 2 可作为其后端 |

首次打开会显示使用方式选择页，**没有预设服务地址**。新安装不读取其他产品的配置、历史记录或题库；模型与去重服务由你配置。新建后端的业务数据为空，程序自带的知识点、标签和默认规则用于正常工作。

## 可以做什么

- **描述需求，开始出题** — 单道编程题、完整编程比赛，或编程、选择、填空、判断混合题集。题数、难度与分值可以明确指定。
- **让模型分工协作** — 生成、解答、测试数据和复核分别编排；任务状态持久化，失败可定位与重试，关闭客户端后后端任务继续运行。
- **一起检索两类题库** — 按题号、标题、标签和知识点匹配部分文字，再用题型、标签与原有难度筛选；网页和客户端共用搜索页。
- **把题库组织成题集** — 按知识点、难度、关键词与使用情况组卷，先预览缺口，再保存；组卷本身不需要调用模型。
- **在沙箱里验证** — 编译解法、执行测试、检查输入输出与资源限制；固定 C++ 数据生成框架支持保存种子后的重复生成与对拍。
- **管理自己的工作区** — 编程题与客观题题库、模型 API 与推理强度、语义去重、任务和质量记录集中管理。
- **按喜欢的方式工作** — 六套配色、浅色/深色/跟随系统、自定义主题、键盘搜索和原生文件保存。
- **在客户端内更新** — 手动检查正式版本，下载并校验后安装重启，保留原连接与资料目录；不影响运行中的后端。

导出支持 **Hydro 编程题包**与 **Qraft 通用题集包**。题集主清单保存题序和分值，编程题使用 Hydro 子包，客观题使用通用工作簿；不依赖任何指定 OJ 的私有模板。查看[题集与组卷](docs/problem-set-assembly.md)及[导入导出格式](docs/quiz-import-format.md)。

## 三步开始

1. **下载** [最新 Release](https://github.com/Gingoo-TvT/Qraft/releases/latest) 中的 Windows 安装包或便携 ZIP。便携版解压后运行 `Qraft.exe`。
2. **选择使用方式**：连接已有服务时填写根地址；自行部署时按界面导入同版本后端包并启动。
3. **配置并创作**：独立部署者先设置模型和去重服务，之后在工作台写下出题需求。

连接已有服务时不需要下载后端镜像包。模型通过你配置的 API 调用，不要求本地 GPU。完整步骤、WebView2 安装、数据位置与升级方式见 [Windows 使用指南](docs/windows-desktop.md)。

<details>
<summary><strong>下载文件分别是什么？</strong></summary>

| 文件 | 用途 |
|---|---|
| `Qraft-*-windows-x64-setup.exe` | 安装版，带卸载程序 |
| `Qraft-*-windows-x64-portable.zip` | 免安装客户端、许可和使用说明 |
| `Qraft-*-windows-x64.exe` | 独立 EXE，需已安装 WebView2 |
| `Qraft-*-backend-linux-x64.tar.gz` | 自行部署所需的完整后端镜像包 |
| `SHA256SUMS.txt` / `runtime-manifest.json` | 下载文件及后端镜像的身份校验 |

</details>

## 自行部署与开发

Windows 用户可以直接使用客户端的本机部署流程。Linux / WSL 开发者可以从源码运行：

```bash
git clone https://github.com/Gingoo-TvT/Qraft.git
cd Qraft
make backend-up
```

这会生成仅属于本机的凭据、构建服务并启动配置工作区。打开 `http://localhost:18180`，保存模型与去重配置后运行 `make worker-up`。详细前置条件、端口、数据与停止方式见[自部署指南](docs/self-hosting.md)。

| 目录 | 内容 |
|---|---|
| `frontend/` | 共享 Web 页面、桌面界面和主题 |
| `desktop/` | Windows 原生窗口、服务连接、本机部署与打包 |
| `backend/` | API、模型编排、题库、组卷与数据生成工具 |
| `sandbox/` | 独立代码执行与资源限制 |
| `docs/` | 使用、开发、API 与格式文档 |
| `scripts/` | 小型配置与仓库检查工具 |

构建输出、客户端资料和实例数据不进入 Git。项目采用一个主仓库持续开发；贡献方式见 [CONTRIBUTING](CONTRIBUTING.md)。

## 文档导航

[使用客户端](docs/windows-desktop.md) · [自部署](docs/self-hosting.md) · [主题扩展](docs/desktop-themes.md) · [题集生成](docs/problem-set-generation.md) · [组卷](docs/problem-set-assembly.md) · [数据生成框架](docs/testdata-framework.md) · [API 参考](docs/api-reference.md) · [架构](docs/architecture.md) · [开发](docs/development.md)

## 当前边界

Qraft 当前面向个人或可信团队的共享工作区，尚未提供完整的账号登录、多租户权限隔离和公网访问网关。同一服务的连接者共享题库与模型配置；默认自部署仅监听本机回环地址。需要不同数据边界时使用独立实例。

模型生成与自动验证可以辅助创作，但不保证每道题目的正确性或比赛适用性。正式使用前应人工审阅题面、解法、数据和难度。独立实例之间不会自动合并题库或进行全局去重。

## 开源许可

Qraft 的项目代码采用 [MIT License](LICENSE)。第三方组件保留各自许可，见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES.md)。欢迎提交问题、改进文档与贡献代码。
