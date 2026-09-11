# 客观题表格导入与导出

Qraft 使用自建的 `.xlsx` 表格保存选择、填空、判断题。先下载空白模板，再填入自己的题目；模板只有表头和排版，不含示例题、其他题库数据、语言编号表或外部链接。

| 操作 | 接口 |
| --- | --- |
| 下载空白模板 | `GET /api/v1/quizzes/template.xlsx` |
| 导入题目 | `POST /api/v1/quizzes/import` |
| 导出筛选后的客观题 | `GET /api/v1/quizzes/export` |

```bash
curl http://localhost:18180/api/v1/quizzes/template.xlsx -o qraft-quiz-template.xlsx
curl -X POST 'http://localhost:18180/api/v1/quizzes/import?on_conflict=error' \
  -F 'subject=程序设计' -F 'file=@my-quizzes.xlsx'
curl 'http://localhost:18180/api/v1/quizzes/export?type=choice' -o choices.xlsx
```

## 工作表与列

数据写在 `Sheet1`，第 1 行为表头，第 2 行起连续填写。中间不要插入整行空白：导入器遇到首个空行即停止读取。前 14 列的表头和顺序必须一致。

| 列 | 表头 | 说明 |
| --- | --- | --- |
| A | 题目编号 | `X`、`T` 或 `P` 加不小于 1000 的数字 |
| B | 题目标题 | 可为空；为空时从题面截取 |
| C | 题目描述（题面） | 必填，支持 Markdown |
| D | 代码ID | 客观题留空；为既有教学编程记录保留 |
| E | 代码提示 | 客观题留空 |
| F | 题目类型 | 选择题、填空题、判断题 |
| G | 题目选项 | 选择题使用；选项之间用 `{break}` 分隔 |
| H | 正确答案 | 必填；多个答案用 `{break}` 分隔 |
| I | 难度 | 简单、中等、困难，分别对应 `easy`、`medium`、`hard` |
| J | 访问类型 | 公开、私有；这是内容标记，不提供账户权限隔离 |
| K | 是否VIP | 是、否；保留的内容标记，不代表内置付费系统 |
| L | 题目标签 | 英文逗号分隔 |
| M | 限定语言 | 客观题留空 |
| N | 答案解析 | 可为空，支持 Markdown |

表头和单元格允许前后空白，但不要改变列名或顺序。题型前缀必须一致：`X1000` 为选择题，`T1000` 为填空题，`P1000` 为判断题；同一文件中不能重复编号。

## 选项与答案

`{break}` 是字面分隔符，解析时不区分大小写，并去除每段前后空白。不要在一个选项或一个答案内部使用该分隔符。

选择题选项：

```text
A. 2{break}B. 4{break}C. 6
```

单选答案写 `B`；多选答案可写 `A{break}C`。填空题按空位顺序填写，例如 `6{break}60`。判断题只能填 `对` 或 `错`。

下面仅是格式示意，不会自动装入任何工作区：

| 编号 | 题型 | 题面 | 选项 | 答案 |
| --- | --- | --- | --- | --- |
| X1000 | 选择题 | 2 + 2 的值是？ | `A. 2{break}B. 4{break}C. 6` | B |
| T1000 | 填空题 | 3 × 2 = ___ | 留空 | 6 |
| P1000 | 判断题 | 0 是偶数。 | 留空 | 对 |

## 导入参数与错误报告

`file` 是必填 multipart 文件字段。`subject` 必填，可放在 multipart 字段或查询参数中，前者优先。

`on_conflict` 查询参数决定已存在编号的处理：

| 值 | 行为 |
| --- | --- |
| `error` | 默认；编号冲突导致本批入库失败，原因放入报告 |
| `skip` | 跳过已有编号，插入新编号 |
| `update` | 更新已有编号，插入新编号 |

响应使用 `APIResponse` 信封；`data` 包含 `success_count`、`failed_count`、`inserted_ids` 和 `failed_rows`。每个失败项含 `row_index`、`code`、`reason`。检查这些字段确认结果，不要仅以 HTTP 200 判断全部导入成功。表头或文件格式错误会直接返回错误；普通行解析错误会记入报告，其余合法行仍会尝试入库。

导出可按 `type`、`subject`、`difficulty`、`tag`、`knowledge_point_id` 筛选。它导出全部命中项，而非当前列表的一页。

## 编程题与整套题集

为兼容既有教学表格，导入器仍认识 `C<number>` 和“编程题”，并保存为教学题库记录。这不会创建沙箱测试数据、执行验算，或将记录转成可评测编程题。普通表格导出遇到这种记录会拒绝导出；可使用 `type` 筛选客观题。

带标准解法和测试数据的编程题使用 [Hydro 包](hydro-service-integration.md)。混合题型题集使用 [Qraft 题集包](problem-set-generation.md#题集导出)：顺序和分值保存在 JSON 清单，客观题另附此格式的表格，编程题各自附 Hydro 包。
