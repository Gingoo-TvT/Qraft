# Hydro 格式支持范围

Qraft 导出普通编程题的 Hydro ZIP，并提供上传前的格式预检。使用步骤见 [导出到 Hydro](hydro-service-integration.md)。本页描述当前实现支持的字段，不代表目标 Hydro 实例已经接受过某个包。

## 接口

| 接口 | 用途 |
| --- | --- |
| `GET /api/v1/problems/:id/hydro.zip` | 单题 Hydro ZIP |
| `GET /api/v1/problems/hydro.zip?ids=<uuid>&ids=<uuid>` | 最多 100 题的 Hydro 批量 ZIP |
| `POST /api/v1/problems/hydro/validate` | multipart 字段 file；只预检，不导入 |
| `GET /api/v1/problems/:id/testdata.zip` | 原始输入输出 ZIP，不是 Hydro 题包 |

## 文件结构

单题 ZIP 根目录直接包含以下文件：

```text
problem.yaml
problem_zh.md
testdata/config.yaml
testdata/1.in
testdata/1.out
additional_file/algoforge_manifest.json
```

`problem.yaml` 包含 pid、title 和可选 tag。pid 优先取 `metadata_json.hydro.pid`，其次为兼容字段 `metadata_json.hydro_pid`，否则由题目编号生成。非法显式配置会被拒绝。

Hydro 批量 ZIP 每题一个直接子目录，各目录内直接放单题文件。它与 [Qraft 题集包](problem-set-generation.md#题集导出) 不同：后者使用 JSON 清单与各编程题的 Hydro 子 ZIP，不可整体作为 Hydro 批量包上传。

## 支持矩阵

| 内容 | 当前行为 |
| --- | --- |
| 普通编程题、标准 IO | 支持 |
| 文件 IO | 支持 `metadata_json.hydro.filename` 指定的合法名称 |
| 默认比较器 | 支持；生成 `checker_type: default` |
| cases 与 sum/min 子任务 | 支持；具体结构由生成数据计划决定 |
| 时间、内存 | 写入配置；质量绑定路径使用相同的正数限制 |
| 样例分值 | 0；计分测试点总和为 100 |
| 省略 config.yaml 的预检 | 可自动识别支持的测试点文件配对 |
| SPJ、自定义比较器 | 不支持 |
| 交互、通信、提交答案、客观题评测、远程评测 | 不支持 |
| max 子任务、条件依赖、语言倍率 | 不支持 |
| 额外执行文件、自定义编译运行脚本、多次执行 | 不支持 |

单题和批量导出使用相同的配置解析。未知字段、当前不支持的配置、无效时间内存或资产不一致会返回错误，不能通过批量入口绕过。样例、随机与边界等分组来源于 TestManifest；接收平台中的比赛分值需另行设置。

## 预检行为

预检识别根目录有 `problem.yaml` 的单题包，以及直接子目录分别有 `problem.yaml` 的批量包。重复 pid、不安全 ZIP 路径、缺标题、缺题面、缺测试点和不支持的配置都会报告错误。

`data.valid` 表示当前预检是否通过，`problems[].unsupported` 列出识别到但不支持的字段。预检不会保存题目、上传对象存储、调用外部 Hydro 或执行它的评测器。

- 上传 ZIP 最大 128 MiB。
- 题面、problem.yaml、config.yaml 单个文本文件最大 2 MiB。
- 测试点自动识别支持 .in/.out、.in/.ans 及 inputN.txt/outputN.txt 等成对命名；使用显式配置可避免歧义。

## 包元数据

`additional_file/algoforge_manifest.json` 是题目包元数据，不是评测配置。它可包含题目/工作流身份、测试点组别、样例标记、分值和输入输出摘要，用于对照来源与文件完整性。该兼容文件名保留不变。

公开或转发自己生成的题包前，应按用途检查题面、答案及这些元数据。Qraft 的空白安装不会携带其他实例的题包或用户数据。
