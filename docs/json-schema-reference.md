# Qraft JSON 结构参考

本文说明 Qraft 常用 API 对象和题集导出清单。API 对象包含工作区管理字段；导出清单只保留可交换的题目内容。

---

## 目录

1. [APIResponse 信封](#1-apiresponse-信封)
2. [Problem 对象](#2-problem-对象)
3. [Solution 对象](#3-solution-对象)
4. [TestCase 对象](#4-testcase-对象)
5. [TagCategory 对象](#5-tagcategory-对象)
6. [WorkflowState 对象](#6-workflowstate-对象)
7. [WorkflowStep 对象](#7-workflowstep-对象)
8. [TestDataConfig 对象](#8-testdataconfig-对象)
9. [ProblemGenParams 请求体](#9-problemgenparams-请求体)
10. [DashboardStats 对象](#10-dashboardstats-对象)
11. [ProblemSet 对象](#11-problemset-对象)
12. [ProblemSetQuality 对象](#12-problemsetquality-对象)
13. [题集编排对象](#13-题集编排对象)
14. [题集导出清单](#14-题集导出清单)

---

## 1. APIResponse 信封

所有 API 响应的统一封装格式。泛型参数 `T` 为实际的数据类型。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `success` | `boolean` | 是 | 请求是否成功 |
| `data` | `T \| null` | 否 | 成功时返回的数据，失败时为 `null` |
| `error` | `object \| null` | 否 | 失败时的错误信息 |
| `error.code` | `string` | — | 错误码，如 `"NOT_FOUND"` |
| `error.message` | `string` | — | 人类可读的错误描述 |
| `meta` | `object \| null` | 否 | 分页元信息（仅列表接口返回） |
| `meta.total` | `number` | — | 总记录数 |
| `meta.page` | `number` | — | 当前页码 |
| `meta.size` | `number` | — | 每页条数 |

### 示例 (成功，列表)

```json
{
  "success": true,
  "data": [
    { "id": "550e8400-...", "title": "两数之和" }
  ],
  "error": null,
  "meta": {
    "total": 150,
    "page": 1,
    "size": 20
  }
}
```

### 示例 (成功，单对象)

```json
{
  "success": true,
  "data": {
    "id": "550e8400-...",
    "title": "两数之和"
  },
  "error": null,
  "meta": null
}
```

### 示例 (失败)

```json
{
  "success": false,
  "data": null,
  "error": {
    "code": "NOT_FOUND",
    "message": "Problem not found"
  },
  "meta": null
}
```

---

## 2. Problem 对象

表示一道完整的算法/编程题目。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `id` | `string` (UUID) | 是 | 题目唯一标识 |
| `serial_number` | `string` | 是 | 顺序编号，如 `"AF-0001"` |
| `title` | `string` | 是 | 题目标题 |
| `statement` | `string` | 是 | 题面描述（Markdown 格式） |
| `level` | `string` | 是 | 题目层级：`"syntax"` 或 `"algorithm"` |
| `difficulty` | `number` | 是 | 难度值，范围 100-3500，步长 100 |
| `tags` | `string[]` | 是 | 标签名称列表 |
| `one_line_hint` | `string \| null` | 否 | 内部作者提示；不会写入选手题面或导出的竞赛包 |
| `detailed_solution` | `string \| null` | 否 | 详细题解（Markdown 格式） |
| `time_limit` | `number` | 是 | 时间限制（毫秒），如 `1000` |
| `memory_limit` | `number` | 是 | 内存限制（MB），如 `256` |
| `status` | `string` | 是 | 状态：`"draft"` / `"generating"` / `"review"` / `"published"` / `"rejected"` |
| `workflow_id` | `string \| null` | 否 | 关联的工作流 ID |
| `metadata_json` | `object \| null` | 否 | 附加元数据（JSON 对象） |
| `created_at` | `string` (ISO 8601) | 是 | 创建时间 |
| `updated_at` | `string` (ISO 8601) | 是 | 最后更新时间 |

### 示例（节选）

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "serial_number": "AF-0042",
  "title": "区间最大子段和",
  "statement": "## 题目描述\n\n给定一个长度为 $n$ 的整数序列 $a_1, a_2, \\ldots, a_n$，以及 $q$ 次查询...\n\n## 输入格式\n\n第一行两个整数 $n, q$...\n\n## 输出格式\n\n对于每次查询，输出一行一个整数...\n\n## 样例\n\n### 输入\n```\n5 3\n1 -2 3 4 -1\n1 5\n2 4\n3 3\n```\n\n### 输出\n```\n7\n7\n3\n```",
  "level": "algorithm",
  "difficulty": 1800,
  "tags": ["segment-tree", "dp"],
  "one_line_hint": "考虑线段树维护区间最大子段和",
  "detailed_solution": "## 解法\n\n本题是经典的线段树维护区间最大子段和问题...",
  "time_limit": 2000,
  "memory_limit": 256,
  "status": "published",
  "workflow_id": "770e8400-e29b-41d4-a716-446655440000",
  "metadata_json": {
    "constraint_analysis": {
      "n_range": [1, 100000],
      "q_range": [1, 100000]
    },
    "expected_complexity": "O((n + q) log n)"
  },
  "created_at": "2026-04-07T14:30:00Z",
  "updated_at": "2026-04-08T09:15:00Z"
}
```

---

## 3. Solution 对象

表示某道题目的一个解法（标程或其他语言解法）。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `id` | `string` (UUID) | 是 | 解法唯一标识 |
| `problem_id` | `string` (UUID) | 是 | 关联的题目 ID |
| `language` | `string` | 是 | 编程语言：`"cpp"` / `"c"` / `"java"` / `"python"` / `"rust"` / `"go"` |
| `code` | `string` | 是 | 源代码内容 |
| `is_model_solution` | `boolean` | 是 | 是否为标准解法 |
| `execution_time_ms` | `number \| null` | 否 | 标程运行时间（毫秒） |
| `memory_usage_kb` | `number \| null` | 否 | 标程内存占用（KB） |
| `created_at` | `string` (ISO 8601) | 是 | 创建时间 |

### 示例

```json
{
  "id": "660e8400-e29b-41d4-a716-446655440001",
  "problem_id": "550e8400-e29b-41d4-a716-446655440000",
  "language": "cpp",
  "code": "#include <bits/stdc++.h>\nusing namespace std;\n\nstruct Node {\n    long long sum, lmax, rmax, ans;\n};\n\nconst int MAXN = 100005;\nNode tree[MAXN * 4];\nint a[MAXN];\n\n// ... (完整代码)\n\nint main() {\n    int n, q;\n    scanf(\"%d%d\", &n, &q);\n    for (int i = 1; i <= n; i++) scanf(\"%d\", &a[i]);\n    build(1, 1, n);\n    while (q--) {\n        int l, r;\n        scanf(\"%d%d\", &l, &r);\n        Node res = query(1, 1, n, l, r);\n        printf(\"%lld\\n\", res.ans);\n    }\n    return 0;\n}",
  "is_model_solution": true,
  "execution_time_ms": 156,
  "memory_usage_kb": 12800,
  "created_at": "2026-04-07T14:35:00Z"
}
```

---

## 4. TestCase 对象

表示一个测试用例的元信息（不含实际数据内容，数据通过下载接口获取）。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `id` | `string` (UUID) | 是 | 测试用例唯一标识 |
| `problem_id` | `string` (UUID) | 是 | 关联的题目 ID |
| `group_index` | `number` | 是 | 所属测试组编号（从 0 开始） |
| `case_index` | `number` | 是 | 组内用例编号（从 0 开始） |
| `is_sample` | `boolean` | 是 | 是否为样例测试点 |
| `input_preview` | `string \| null` | 否 | 输入数据预览（前 200 字符） |
| `output_preview` | `string \| null` | 否 | 输出数据预览（前 200 字符） |
| `input_size_bytes` | `number` | 是 | 输入文件大小（字节） |
| `output_size_bytes` | `number` | 是 | 输出文件大小（字节） |
| `created_at` | `string` (ISO 8601) | 是 | 创建时间 |

### 示例

```json
{
  "id": "770e8400-e29b-41d4-a716-446655440010",
  "problem_id": "550e8400-e29b-41d4-a716-446655440000",
  "group_index": 0,
  "case_index": 0,
  "is_sample": true,
  "input_preview": "5 3\n1 -2 3 4 -1\n1 5\n2 4\n3 3\n",
  "output_preview": "7\n7\n3\n",
  "input_size_bytes": 35,
  "output_size_bytes": 6,
  "created_at": "2026-04-07T14:40:00Z"
}
```

---

## 5. TagCategory 对象

表示一个标签分类，包含该分类下的所有标签。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `id` | `string` (UUID) | 是 | 分类唯一标识 |
| `name` | `string` | 是 | 分类英文标识名（如 `"sorting"`） |
| `display_name` | `string` | 是 | 分类显示名（中文），如 `"排序"` |
| `level` | `string` | 是 | 所属层级：`"syntax"` 或 `"algorithm"` |
| `tags` | `Tag[]` | 是 | 分类下的标签列表 |

### Tag 子对象

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `id` | `string` (UUID) | 是 | 标签唯一标识 |
| `name` | `string` | 是 | 标签英文标识名 |
| `display_name` | `string` | 是 | 标签显示名（中文） |
| `category_id` | `string` (UUID) | 是 | 所属分类 ID |
| `problem_count` | `number \| undefined` | 否 | 使用该标签的题目数量 |

### 示例

```json
{
  "id": "880e8400-e29b-41d4-a716-446655440001",
  "name": "sorting",
  "display_name": "排序",
  "level": "algorithm",
  "tags": [
    {
      "id": "990e8400-e29b-41d4-a716-446655440001",
      "name": "sorting",
      "display_name": "排序",
      "category_id": "880e8400-e29b-41d4-a716-446655440001",
      "problem_count": 12
    }
  ]
}
```

---

## 6. WorkflowState 对象

表示一个题目生成工作流的完整状态。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `id` | `string` (UUID) | 是 | 工作流唯一标识 |
| `problem_id` | `string \| null` | 否 | 关联的题目 ID（生成后赋值） |
| `status` | `string` | 是 | 状态：`"pending"` / `"running"` / `"waiting_review"` / `"approved"` / `"rejected"` / `"failed"` / `"cancelled"` |
| `current_step` | `number` | 是 | 当前执行的步骤索引（从 0 开始） |
| `steps` | `WorkflowStep[]` | 是 | 步骤列表（生成链路当前为 12 步） |
| `error` | `string \| null` | 否 | 工作流级别的错误信息 |
| `created_at` | `string` (ISO 8601) | 是 | 创建时间 |
| `updated_at` | `string` (ISO 8601) | 是 | 最后更新时间 |

### 状态流转

```
pending -> running -> waiting_review -> approved
                                     -> rejected
                   -> failed         -> (retry) -> running
           -> cancelled
```

### 示例（节选）

```json
{
  "id": "770e8400-e29b-41d4-a716-446655440000",
  "problem_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "running",
  "current_step": 2,
  "steps": [
    {
      "name": "similarity_check",
      "description": "相似题目检测",
      "status": "completed",
      "result": {
        "success": true,
        "message": "相似题目检测通过",
        "data": {
          "report": {
            "schema_version": "algoforge.workflow.dedup.report.v1",
            "stage": "pre_generation",
            "model_version": "75c31ac5-10a2-5bc2-a4d0-c9ad245735bc",
            "kind": "statement",
            "content_hash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
            "top_k": 8,
            "threshold": 0.82,
            "decision": "pass"
          }
        },
        "artifacts": []
      },
      "started_at": "2026-04-08T10:00:05Z",
      "completed_at": "2026-04-08T10:00:45Z"
    },
    {
      "name": "generate_statement",
      "description": "生成题目描述",
      "status": "completed",
      "result": {
        "success": true,
        "message": "题目描述生成完成",
        "data": {},
        "artifacts": ["statement.md"]
      },
      "started_at": "2026-04-08T10:00:46Z",
      "completed_at": "2026-04-08T10:01:30Z"
    },
    {
      "name": "post_statement_similarity",
      "description": "题面相似度精筛",
      "status": "running",
      "result": null,
      "started_at": "2026-04-08T10:01:31Z",
      "completed_at": null
    },
    {
      "name": "generate_solution",
      "description": "生成标准解法",
      "status": "pending",
      "result": null,
      "started_at": null,
      "completed_at": null
    },
    {
      "name": "compile_check",
      "description": "编译检查",
      "status": "pending",
      "result": null,
      "started_at": null,
      "completed_at": null
    },
    {
      "name": "generate_testdata",
      "description": "生成测试数据",
      "status": "pending",
      "result": null,
      "started_at": null,
      "completed_at": null
    },
    {
      "name": "run_sandbox",
      "description": "沙箱执行",
      "status": "pending",
      "result": null,
      "started_at": null,
      "completed_at": null
    },
    {
      "name": "validate",
      "description": "验证输出",
      "status": "pending",
      "result": null,
      "started_at": null,
      "completed_at": null
    }
  ],
  "error": null,
  "created_at": "2026-04-08T10:00:00Z",
  "updated_at": "2026-04-08T10:01:31Z"
}
```

---

## 7. WorkflowStep 对象

表示工作流中的一个执行步骤。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `name` | `string` | 是 | 步骤标识名 |
| `description` | `string` | 是 | 步骤描述（中文） |
| `status` | `string` | 是 | 状态：`"pending"` / `"running"` / `"completed"` / `"failed"` / `"skipped"` |
| `result` | `StepResult \| null` | 否 | 步骤执行结果 |
| `started_at` | `string \| null` | 否 | 开始执行时间 (ISO 8601) |
| `completed_at` | `string \| null` | 否 | 完成时间 (ISO 8601) |

### StepResult 子对象

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `success` | `boolean` | 是 | 是否成功 |
| `message` | `string \| null` | 否 | 结果消息 |
| `data` | `object \| null` | 否 | 结果附加数据 |
| `artifacts` | `string[] \| null` | 否 | 产出的文件名列表 |

### 步骤名称列表

| 顺序 | name | description |
|------|------|-------------|
| 0 | `similarity_check` | 相似题目检测 |
| 1 | `generate_statement` | 生成题目描述 |
| 2 | `post_statement_similarity` | 题面相似度精筛 |
| 3 | `generate_solution` | 生成标准解法 |
| 4 | `compile_check` | 编译检查 |
| 5 | `generate_testdata` | 生成测试数据 |
| 6 | `run_sandbox` | 沙箱执行 |
| 7 | `validate` | 验证输出 |
| 8 | `assess_feasibility` | 可行性评估 |
| 9 | `llm_review` | LLM 审核 |
| 10 | `human_review` | 人工审核 |
| 11 | `store` | 存储题目 |

### 示例

```json
{
  "name": "generate_testdata",
  "description": "生成测试数据",
  "status": "completed",
  "result": {
    "success": true,
    "message": "已生成 20 个测试点 (含 2 个样例)",
    "data": {
      "total_cases": 20,
      "sample_cases": 2,
      "groups": 4,
      "max_input_size_bytes": 524288
    },
    "artifacts": [
      "testdata/01.in", "testdata/01.out",
      "testdata/02.in", "testdata/02.out"
    ]
  },
  "started_at": "2026-04-08T10:01:31Z",
  "completed_at": "2026-04-08T10:02:15Z"
}
```

---

## 8. TestDataConfig 对象

配置测试数据生成的参数，作为题目生成请求的一部分。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `groups` | `TestGroup[]` | 是 | 测试组定义列表 |
| `custom_cases` | `CustomTestCase[]` | 是 | 手动指定的自定义测试用例 |
| `total_count` | `number` | 是 | 总测试点数量；新生题流程固定传 `0`，由模型选择最终数量 |
| `adaptive_count` | `boolean \| undefined` | 否 | 前端配置标记；新配置为 `true` |
| `min_count` / `max_count` | `number \| undefined` | 否 | 前端显示的自适应范围，固定为 10 和 20 |
| `auto_case_count` | `boolean \| undefined` | 否 | 旧版兼容别名；新客户端不再使用 |
| `sample_count` | `number` | 是 | 样例测试点数量 |
| `time_limit` | `number` | 是 | 时间限制（毫秒） |
| `memory_limit` | `number` | 是 | 内存限制（MB） |
| `checker_type` | `string` | 是 | 比较器类型：`"exact"` / `"float_tolerance"` / `"special_judge"` |
| `float_tolerance` | `number \| undefined` | 否 | 浮点误差容忍度（仅当 checker_type 为 `"float_tolerance"` 时有效） |

页面保存配置使用 `total_count`、`min_count`、`max_count` 这组本地字段；提交题目生成 API 时，客户端应映射为
`num_test_cases`、`min_test_cases`、`max_test_cases`。新请求固定使用 `num_test_cases=0`，表示由模型在
10–20 个测试点中按 corner case 选择最终数量；旧 `auto_case_count` 仅用于兼容旧配置。

### TestGroup 子对象

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `name` | `string` | 是 | 组名，如 `"small"` |
| `points` | `number` | 是 | 该组分值 |
| `count` | `number` | 是 | 该组测试点数量 |
| `constraints` | `ConstraintRange[]` | 是 | 变量约束范围列表 |
| `boundary_config` | `BoundaryConfig \| undefined` | 否 | 边界数据配置 |

### ConstraintRange 子对象

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `min` | `number` | 是 | 最小值 |
| `max` | `number` | 是 | 最大值 |
| `variable` | `string` | 是 | 变量名，如 `"n"` |
| `description` | `string \| undefined` | 否 | 变量说明 |

### BoundaryConfig 子对象

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `include_min` | `boolean` | 是 | 是否包含最小边界数据 |
| `include_max` | `boolean` | 是 | 是否包含最大边界数据 |
| `include_zero` | `boolean` | 是 | 是否包含零值数据 |
| `include_negative` | `boolean` | 是 | 是否包含负数数据 |
| `custom_boundaries` | `number[]` | 是 | 自定义边界值 |

### CustomTestCase 子对象

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `input` | `string` | 是 | 输入数据 |
| `expected_output` | `string \| undefined` | 否 | 预期输出 |
| `description` | `string \| undefined` | 否 | 用例说明 |

### 示例

```json
{
  "groups": [
    {
      "name": "样例",
      "points": 0,
      "count": 0,
      "constraints": [
        { "min": 1, "max": 10, "variable": "n", "description": "序列长度" },
        { "min": 1, "max": 10, "variable": "q", "description": "查询次数" }
      ],
      "boundary_config": {
        "include_min": true,
        "include_max": false,
        "include_zero": false,
        "include_negative": true,
        "custom_boundaries": []
      }
    },
    {
      "name": "小数据",
      "points": 30,
      "count": 0,
      "constraints": [
        { "min": 1, "max": 1000, "variable": "n" },
        { "min": 1, "max": 1000, "variable": "q" }
      ]
    },
    {
      "name": "大数据",
      "points": 70,
      "count": 0,
      "constraints": [
        { "min": 1, "max": 100000, "variable": "n" },
        { "min": 1, "max": 100000, "variable": "q" }
      ]
    }
  ],
  "custom_cases": [
    {
      "input": "1 1\n42\n1 1\n",
      "expected_output": "42\n",
      "description": "单元素序列"
    }
  ],
  "adaptive_count": true,
  "min_count": 10,
  "max_count": 20,
  "total_count": 0,
  "sample_count": 2,
  "time_limit": 2000,
  "memory_limit": 256,
  "checker_type": "exact"
}
```

---

## 9. ProblemGenParams 请求体

POST /problems/generate 接口的请求体格式。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `level` | `string` | 是 | 题目层级：`"syntax"` 或 `"algorithm"` |
| `difficulty` | `number` | 是 | 难度值，范围 100-3500，步长 100 |
| `tags` | `string[]` | 是 | 标签名称列表（至少 1 个） |
| `contest_style` | `string \| undefined` | 否 | 赛制风格：`"icpc"` / `"ioi"` / `"codeforces"` / `"leetcode"` / `"noip"` / `"custom"` |
| `language` | `string \| undefined` | 否 | 标程语言：`"cpp"` / `"c"` / `"java"` / `"python"` / `"rust"` / `"go"`，默认 `"cpp"` |
| `custom_requirements` | `string \| undefined` | 否 | 自定义生成需求描述文本 |
| `test_data_config` | `Partial<TestDataConfig> \| undefined` | 否 | 测试数据配置（部分字段）；新请求使用 `num_test_cases=0`、`min_test_cases=10`、`max_test_cases=20`，未指定部分使用默认值 |
| `similar_problem_check` | `boolean \| undefined` | 否 | 是否启用相似题目检测，默认 `true` |
| `provider_config.statement.model` | `string \| undefined` | 否 | 题面生成模型 |
| `provider_config.statement.base_url` | `string \| undefined` | 否 | 题面生成 Anthropic Messages 兼容端点 |
| `provider_config.statement.provider` | `string \| undefined` | 否 | 题面生成 provider 身份；设置 `base_url` 时必填 |
| `provider_config.statement.api_key_ref` | `string \| undefined` | 否 | 题面生成 key 引用，支持 `env:NAME` 或 `runtime:<token>` |
| `provider_config.statement.api_key` | `string \| undefined` | 否 | 题面生成本次请求 raw key，API 会换成短期 `runtime:<token>` |
| `provider_config.verification.model` | `string \| undefined` | 否 | 验算/评审模型 |
| `provider_config.verification.base_url` | `string \| undefined` | 否 | 验算/评审 Anthropic Messages 兼容端点 |
| `provider_config.verification.provider` | `string \| undefined` | 否 | 验算/评审 provider 身份；设置 `base_url` 时必填 |
| `provider_config.verification.api_key_ref` | `string \| undefined` | 否 | 验算/评审 key 引用，支持 `env:NAME` 或 `runtime:<token>` |
| `provider_config.verification.api_key` | `string \| undefined` | 否 | 验算/评审本次请求 raw key，API 会换成短期 `runtime:<token>` |

### 示例 (最小请求)

```json
{
  "level": "algorithm",
  "difficulty": 1600,
  "tags": ["dp"]
}
```

### 示例 (完整请求)

```json
{
  "level": "algorithm",
  "difficulty": 2100,
  "tags": ["segment-tree", "dp"],
  "contest_style": "icpc",
  "language": "cpp",
  "custom_requirements": "要求题目包含线段树维护区间信息的思想，需要使用懒标记",
  "test_data_config": {
    "total_count": 0,
    "min_test_cases": 10,
    "max_test_cases": 20,
    "sample_count": 3,
    "time_limit": 2000,
    "memory_limit": 256,
    "checker_type": "exact",
    "groups": [
      {
        "name": "小数据",
        "points": 30,
        "count": 0,
        "constraints": [
          { "min": 1, "max": 1000, "variable": "n" }
        ]
      },
      {
        "name": "大数据",
        "points": 70,
        "count": 0,
        "constraints": [
          { "min": 1, "max": 200000, "variable": "n" }
        ]
      }
    ]
  },
  "similar_problem_check": true,
  "provider_config": {
    "statement": {
      "model": "statement-model-id",
      "base_url": "https://llm.example.com",
      "provider": "anthropic-compatible",
      "api_key_ref": "env:ALGOFORGE_STATEMENT_LLM_KEY"
    },
    "verification": {
      "model": "review-model-id",
      "base_url": "https://review.example.com/v1/messages",
      "provider": "review-provider",
      "api_key_ref": "env:ALGOFORGE_REVIEW_LLM_KEY"
    }
  }
}
```

---

## 10. DashboardStats 对象

仪表盘统计数据。

### 字段说明

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `total_problems` | `number` | 是 | 题目总数 |
| `published_problems` | `number` | 是 | 已发布题目数 |
| `draft_problems` | `number` | 是 | 草稿题目数 |
| `generating_problems` | `number` | 是 | 生成中题目数 |
| `review_problems` | `number` | 是 | 待审核题目数 |
| `rejected_problems` | `number` | 是 | 已拒绝题目数 |
| `total_workflows` | `number` | 是 | 工作流总数 |
| `active_workflows` | `number` | 是 | 活跃工作流数（running + pending） |
| `success_rate` | `number` | 是 | 工作流成功率（0.0 - 1.0） |
| `problems_by_level` | `object` | 是 | 按层级统计 |
| `problems_by_level.syntax` | `number` | 是 | 语法层级题目数 |
| `problems_by_level.algorithm` | `number` | 是 | 算法层级题目数 |
| `problems_by_difficulty` | `Record<string, number>` | 是 | 按难度段统计，键为难度阈值字符串 |
| `recent_activity` | `ActivityItem[]` | 是 | 近期活动列表 |

### ActivityItem 子对象

| 字段 | 类型 | 必有 | 说明 |
|------|------|------|------|
| `id` | `string` | 是 | 活动唯一标识 |
| `type` | `string` | 是 | 类型：`"problem_created"` / `"problem_published"` / `"workflow_completed"` / `"workflow_failed"` |
| `description` | `string` | 是 | 活动描述文本 |
| `timestamp` | `string` (ISO 8601) | 是 | 发生时间 |
| `reference_id` | `string \| undefined` | 否 | 关联资源 ID（题目或工作流 ID） |

### 示例

```json
{
  "total_problems": 150,
  "published_problems": 120,
  "draft_problems": 10,
  "generating_problems": 5,
  "review_problems": 8,
  "rejected_problems": 7,
  "total_workflows": 200,
  "active_workflows": 3,
  "success_rate": 0.85,
  "problems_by_level": {
    "syntax": 30,
    "algorithm": 120
  },
  "problems_by_difficulty": {
    "800": 15,
    "1200": 25,
    "1600": 30,
    "2100": 28,
    "2600": 15,
    "3500": 7
  },
  "recent_activity": [
    {
      "id": "act-001",
      "type": "problem_published",
      "description": "题目 AF-0120 「区间最大子段和」已发布",
      "timestamp": "2026-04-08T10:30:00Z",
      "reference_id": "550e8400-e29b-41d4-a716-446655440000"
    },
    {
      "id": "act-002",
      "type": "workflow_completed",
      "description": "工作流 #200 执行完成，等待审核",
      "timestamp": "2026-04-08T10:28:00Z",
      "reference_id": "770e8400-e29b-41d4-a716-446655440000"
    },
    {
      "id": "act-003",
      "type": "problem_created",
      "description": "新题目 AF-0151 开始生成",
      "timestamp": "2026-04-08T10:25:00Z",
      "reference_id": "550e8400-e29b-41d4-a716-446655440151"
    },
    {
      "id": "act-004",
      "type": "workflow_failed",
      "description": "工作流 #198 在步骤「验证解法正确性」失败",
      "timestamp": "2026-04-08T09:50:00Z",
      "reference_id": "770e8400-e29b-41d4-a716-446655440198"
    }
  ]
}
```

---

## 11. ProblemSet 对象

比赛、作业、课程或模拟赛题集的持久化对象。新记录的 `desired_item_count` 直接表示用户填写的题数，
允许范围为 1–1000；`min_item_count`/`max_item_count` 仅保留用于历史记录和兼容旧 API。

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | `string` (UUID) | 题集 ID |
| `code` | `string` | 稳定业务编号 |
| `title` / `description` | `string` | 标题和说明 |
| `kind` | `string` | `contest` / `homework` / `curriculum` / `mock_exam` |
| `visibility` | `string` | `public` 或 `private` |
| `subject` | `string` | 主题/科目 |
| `tags` | `string[]` | 去重后的标签 |
| `style_prompt` | `string` | 用户描述的题集风格 |
| `difficulty_prompt` | `string` | 用户描述的难度和梯度 |
| `generated_prompt` | `string` | LLM 生成、可审阅的组题提示词 |
| `desired_item_count` | `number` | 直接填写的题数（1–1000）；旧记录可为 0 |
| `min_item_count` / `max_item_count` | `number` | 兼容旧记录的题数范围；新精确题数会将二者归一为同一值 |
| `cooldown_sets` | `number` | 去重台账回看最近多少场题集 |
| `total_score` | `number` | 题集总分 |
| `status` | `string` | `draft` / `ready` / `exported` |
| `items` | `ProblemSetItem[]` | 题集题目，详情接口返回 |
| `generation_config` | `ProblemSetGenerationConfig?` | 题型配额及需求 |
| `generation` | `ProblemSetGenerationState?` | 可空持久生成状态，与题集 status 独立 |
| `generation_error` | `string?` | 创建已保存但启动失败，仅在该次创建响应返回 |
| `quality` | `ProblemSetQuality` | 质量评估，详情或 quality 接口返回 |

题集变更会清除旧的 `generated_prompt`；导出成功后追加不可变 ledger entry，避免把上一场比赛的
提示词或题目集合静默复用。

## 12. ProblemSetQuality 对象

| 字段 | 类型 | 说明 |
|------|------|------|
| `valid` | `boolean` | 结构与题目引用是否有效 |
| `ready_for_export` | `boolean` | 是否通过题数、覆盖和去重门禁 |
| `recommended_item_count` | `number` | 根据请求覆盖轴推荐的题数 |
| `item_count` | `number` | 当前题数 |
| `knowledge_point_count` | `number` | 覆盖的知识点数量 |
| `overlap_set_ids` | `string[]` | 与近期题集发生重叠的 ID |
| `reused_knowledge_points` | `string[]` | 重复知识点 |
| `reused_items` | `string[]` | 重复题目指纹 |
| `blocking_issues` | `string[]` | 阻止导出的原因 |
| `warnings` | `string[]` | 不阻止导出的提示 |

结构、题数或资产校验失败时，导出拒绝整个题集。若只是近期复用导致 `ready_for_export=false`，可在主动确认后使用 `allow_reuse=true`；该参数不绕过其他检查。


## 13. 题集编排对象

`ProblemSetGenerationConfig`：`mode` 为 programming/mixed；`requirements` 最多 12000 字；
`distribution` 为不重复的 `{type,count,score}[]`，type 为 programming/choice/fill_blank/judge，count 为 0–1000，score 为 0–10000，数量和等于题集目标；programming 模式的其他类型配额必须为 0。
可选 `assembly` 保存组卷筛选器，不表示自动重新抽题。

`ProblemSetGenerationState`：`id`（本次生成 ID）、`status`、`slots`、可选 `error`、`started_at/updated_at`。
活动状态 queued/planning/generating，终态 completed/partial/failed/cancelled。
每个 slot 的稳定展示字段为 position/type/score/title/status/error，状态包括 pending/ready/running/generated/succeeded/failed。
客户端仅统计 succeeded 为已入集；生成返回了内容并不等于已完成资产检查和入集。

`ProblemSetAssemblyFilter`：tags（至多 50 个，每个不超过 100 字，任一命中）、keyword（至多 200 字）、
min_difficulty/max_difficulty（800–3500、100 递增，省略默认全范围）、quiz_difficulty（空/easy/medium/hard）、
exclude_recent_sets（0–50；API 省略为 0，UI 默认 2）、seed（最多 128 字节）。

`ProblemSetAssemblyPreview`：filter、items、distribution、total_score、missing_count。items 每项含
id/type/updated_at/code/title/tags/score，及适用的 difficulty/quiz_difficulty。distribution 每项为
type/requested/available/selected/missing。保存时只提交 id/type/updated_at 引用，不发送题面或模型答案。

能力发现通过 `GET /api/v1/integration/capabilities` 返回 `release_version/problem_sets`。调用方应依赖公开的 enabled/routes 和状态字段；内部计划、指纹和子工作流 ID 不属于稳定客户端接口。

## 14. 题集导出清单

题集 ZIP 内的 `problem-set.json` 是独立交换结构，不是数据库中的 `ProblemSet` 原样序列化。格式标识保留 `algoforge.problem-set.v1`。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `format` | string | `algoforge.problem-set.v1` |
| `code` / `title` / `description` | string | 题集编号、名称、说明 |
| `kind` / `subject` | string | 用途、科目 |
| `tags` | string[] | 题集标签 |
| `total_score` | number | 各题分值之和 |
| `items` | object[] | 按题集顺序排列的题目 |

每个 item：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `position` | number | 从 1 开始的整套位置 |
| `score` | number | 本题分值 |
| `section` / `notes` | string? | 可选分区与备注 |
| `type` | string | programming/choice/fill_blank/judge |
| `title` / `statement` | string | 题目标题与 Markdown 题面 |
| `hydro_package` | string? | 编程题对应的 ZIP 相对路径 |
| `quiz` | object? | 客观题的 code、options、answers、explanation、difficulty、tags |

`quiz.options` 在有选项时出现，格式为 `{label,content}[]`；`answers` 为字符串数组。`difficulty` 使用 easy/medium/hard。`hydro_package` 与 `quiz` 按题型二选一。

```json
{
  "format": "algoforge.problem-set.v1",
  "code": "practice-set",
  "title": "两题练习",
  "description": "基础概念与编程应用",
  "kind": "homework",
  "subject": "程序设计",
  "tags": ["基础"],
  "total_score": 100,
  "items": [
    {
      "position": 1,
      "score": 10,
      "type": "judge",
      "title": "偶数判断",
      "statement": "0 是偶数。",
      "quiz": {
        "code": "P1000",
        "answers": ["对"],
        "explanation": "0 可以被 2 整除。",
        "difficulty": "easy",
        "tags": ["基础"]
      }
    },
    {
      "position": 2,
      "score": 90,
      "type": "programming",
      "title": "求和练习",
      "statement": "读取两个整数并输出它们的和。",
      "hydro_package": "programming/002-hydro.zip"
    }
  ]
}
```

该 JSON 不包含 API Key、模型配置、工作流或数据库内部 ID。子包仍遵循各自的格式；Hydro 子包可包含自己的题目元数据。客观题存在时，ZIP 另附 `quizzes.xlsx`；其表格格式见 [导入导出文档](quiz-import-format.md)。题集包使用方法见 [题集导出](problem-set-generation.md#题集导出)。
