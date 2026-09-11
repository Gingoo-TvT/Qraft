# 题集自动编排

Qraft 可以按一段需求生成纯编程比赛，也可以生成包含编程、选择、填空、判断题的混合题集。入口：[创建题集](http://localhost:18180/problem-sets/new)。

1. 选择题集模式，设置各题型数量、每题分值和整体需求，总题数支持 1–1000。
2. 填写知识点、风格和难度要求，保存后开始生成；也可以先保存需求。
3. 在详情查看逐题进度、失败原因和已经保存的结果。离开页面不影响服务端继续执行。
4. 完成后检查题集覆盖、重复使用提示和导出状态，再下载题集包。

题型配额由表单固定，模型负责内容设计、覆盖范围、难度梯度和逐题任务。数量为 0 的题型跳过，模型不能自行增减题数。已有题目占用对应配额；修改需求不会重写这些题目，需要替换时先停止生成并移除它们。

## 生成与重试

整套计划先统筹，再按每批最多 12 个题位细化，最多同时执行 4 条子流程。编程题经过解法与数据验证后再入集。客观题增加独立解答步骤：验证模型只看到题干、选项和题型，不看生成答案或解析；有歧义或答案不一致时可以重写一次，仍失败则保留原因。

默认使用“模型配置”中保存的出题、验算和评审模型。只在没有独立覆盖时按配置继承，任务中保存的是密钥引用。

生成中锁定题集配置和手工编排。停止或部分失败后，重试只处理未完成题位；已生成但尚未入集的结果会尝试恢复。同一题位的入集与完成状态同时提交，重复启动不会重复添加题目。

## 创建请求

```json
{
  "title": "数据结构期末复习",
  "desired_item_count": 10,
  "start_generation": true,
  "generation_config": {
    "mode": "mixed",
    "requirements": "概念辨析与应用并重，前易后难，题目之间不泄露答案。",
    "distribution": [
      {"type": "choice", "count": 4, "score": 2},
      {"type": "fill_blank", "count": 2, "score": 5},
      {"type": "judge", "count": 2, "score": 1},
      {"type": "programming", "count": 2, "score": 40}
    ]
  }
}
```

提交到 `POST /api/v1/problem-sets`。纯编程比赛使用 `mode: "programming"`，其他题型配额必须为 0。配额之和必须等于 `desired_item_count`，每题分值支持 0–10000。省略 `start_generation` 时只保存题集，更新需求使用 `PUT /api/v1/problem-sets/:id`。

| 操作 | 接口 |
| --- | --- |
| 开始、继续或重试未完成题位 | `POST /api/v1/problem-sets/:id/generation` |
| 查询持久进度 | `GET /api/v1/problem-sets/:id/generation` |
| 请求停止 | `POST /api/v1/problem-sets/:id/generation/cancel` |
| 查看题数、覆盖与复用检查 | `GET /api/v1/problem-sets/:id/quality` |

创建保存成功但启动失败时，响应仍返回已创建的题集及 `generation_error`。保留这个 ID 并重试它的生成操作，避免再次创建题集。活动状态为 `queued`、`planning`、`generating`；终态为 `completed`、`partial`、`failed`、`cancelled`。停止是异步操作，应继续查询直到终态。

自动生成入口由 `ALGOFORGE_CUSTOM_GENERATION_API_MODE=jobs-v1` 启用；可通过 `GET /api/v1/integration/capabilities` 的 `problem_sets.generation.enabled` 检查服务实际支持情况。题集管理、[题库组卷](problem-set-assembly.md) 和导出是独立能力。

## 题集导出

`GET /api/v1/problem-sets/:id/export.zip` 下载 Qraft 题集包。它是用于保存和交换整套内容的格式，不能直接作为 Hydro 比赛导入包上传。

```text
my-set-qraft.zip
├── problem-set.json
├── README.txt
├── programming/
│   ├── 001-hydro.zip
│   └── 004-hydro.zip
└── quizzes.xlsx
```

- `problem-set.json` 使用格式标识 `algoforge.problem-set.v1`，保留标题、说明、用途、科目、标签、题目顺序、分区、备注和分值。
- `programming/<位置>-hydro.zip` 为经过资产检查的单题 Hydro 包。编号取整套题集中的位置，不是编程题的独立序号；没有编程题时不包含该目录。
- `quizzes.xlsx` 仅在有客观题时出现，采用 [客观题表格格式](quiz-import-format.md)。JSON 的 `items[].quiz` 同样保留客观题选项、答案与解析。
- JSON 清单只输出内容字段，不序列化模型配置、密钥、工作流或数据库内部 ID。Hydro 子包仍保留其自身的题目包元数据。

顺序、各题分值与总分以 JSON 为准。导入到 Hydro 时，先取出各单题 Hydro ZIP，再在接收系统中配置比赛和分值；Qraft 不会自动创建外部比赛。

导出前检查题数、题型配额和资产完整性，未完成题集或无效资产会阻止导出。近期复用导致冲突时，确认确实需要复用后可传 `?allow_reuse=true`；该参数不能跳过缺题、无效引用或编程题资产校验。

成功响应类型为 `application/zip`，包含 `X-AlgoForge-Export-Profile: algoforge.problem-set.v1` 与 `X-AlgoForge-Problem-Set-Quality`。导出会保存复用记录并更新题集状态，重复下载也会留下导出记录。

字段详情见 [JSON 结构参考](json-schema-reference.md#14-题集导出清单)，接口索引见 [API 文档](api-reference.md)。
