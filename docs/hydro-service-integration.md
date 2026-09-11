# 导出到 Hydro

Qraft 可以导出普通编程题的 Hydro 包。你可以从题目详情下载，也可以通过 API 获取、预检后上传到自己的 Hydro 服务。

默认服务入口为 `http://localhost:18180/api/v1`。使用远程部署时替换地址，并按管理员配置的网关要求附加凭据。Qraft 本身是共享工作区，不提供内置用户登录或租户隔离。

## 单题流程

1. 用 `POST /generation/jobs` 和独立的 `Idempotency-Key` 提交出题需求。
2. 查询 `GET /generation/jobs/:job_id`，等待任务完成。
3. 从 `GET /generation/jobs/:job_id/result` 取得 `problem_id`，检查质量结果。
4. 下载 `GET /problems/:problem_id/hydro.zip`。
5. 用 `POST /problems/hydro/validate` 预检该 ZIP，检查 `data.valid` 及错误列表。
6. 将预检通过的包上传到目标 Hydro 服务，并确认目标服务实际接受。

已有题目可以从第 4 步开始。生成任务请求与响应见 [API 文档](api-reference.md)，包字段支持范围见 [Hydro 兼容性说明](hydro-compatibility.md)。

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/hydro.zip \
  -o problem-hydro.zip

curl -X POST http://localhost:18180/api/v1/problems/hydro/validate \
  -F 'file=@problem-hydro.zip'
```

预检只检查包格式和本服务支持的 Hydro 字段，不会向 Hydro 上传、创建题目或执行目标平台的评测。

## 导出条件

编程题需要与其质量记录、TestManifest 和已保存测试资产一致。正常生成路径会检查九项质量结果和各测试点的输入输出摘要、大小与归属；被标记为过期的数据不能直接导出。仅把题目状态改成 published，不能替代这些检查。

数据或证据不一致通常返回 `409 CONFLICT`。先查看题目质量与任务结果，再重新生成或完成对应验证；不要通过更改状态绕过失败原因。Hydro 导出模式使用兼容配置名 `ALGOFORGE_QG15_EXPORT_MODE`，其默认值为 `qg15-v1`；不要把旧兼容模式当作修复无效资产的方法。

## 单题包结构

```text
problem.yaml
problem_zh.md
testdata/config.yaml
testdata/1.in
testdata/1.out
additional_file/algoforge_manifest.json
```

元数据文件名保留兼容协议名称。支持普通编程题、标准 IO、可选文件 IO、默认比较器、cases 及 sum/min 子任务。时间与内存限制随数据配置写入；样例不计分，生成数据组的计分总和为 100。题集中的“本题分值”需要在 Hydro 比赛配置中单独设置。

当前不支持 SPJ/自定义 checker、交互、通信、提交答案、客观题评测、语言倍率和多次执行。遇到未支持的配置会返回错误，不会悄悄丢弃后继续导出。

## 批量与整套题集

`GET /problems/hydro.zip` 接受最多 100 个题目 ID，参数 `ids` 可重复或使用逗号分隔：

```bash
curl 'http://localhost:18180/api/v1/problems/hydro.zip?ids=550e8400-e29b-41d4-a716-446655440000&ids=660e8400-e29b-41d4-a716-446655440000' \
  -o hydro-batch.zip
```

这个 Hydro 批量包按题目分目录，目录内直接放单题文件；任一题目校验失败则整个下载失败。

[Qraft 题集包](problem-set-generation.md#题集导出) 使用另一种结构：`problem-set.json` 记录整套顺序和分值，`programming/*.zip` 是单题 Hydro 子包，`quizzes.xlsx` 是客观题表格。不要把整个 Qraft 题集包或客观题表格直接交给 Hydro 导入器。先取出编程题子包上传，再根据 JSON 在接收系统中配置比赛；混合题型内容的接收需要目标系统另外支持。

## 请求大小与重试

| 操作 | 约束与建议 |
| --- | --- |
| Hydro 预检 ZIP | 最大 128 MiB |
| 题面、problem.yaml、config.yaml | 每个文本文件最大 2 MiB |
| 单题、批量下载 | 可重试；建议至少 120 秒客户端超时 |
| 预检上传 | 无落库副作用，可用相同 ZIP 重试 |
| 生成 Job | 同一共享主体、Idempotency-Key 和规范化请求复用同一任务 |
| 重新验算 | `POST /problems/:id/validate` 每次启动新工作流，不是幂等调用 |

生成和验算为异步操作，应该轮询任务状态，而不是等待一次 HTTP 请求完成所有模型调用。新客户端可先读取 `GET /integration/capabilities`，确认 `exports.hydro_routes_enabled` 与可用路由。
