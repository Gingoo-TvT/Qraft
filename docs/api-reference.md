# Qraft API 接口文档

本文说明 Qraft 工作区的主要 HTTP 接口。客户端可连接自行部署或已有的兼容服务。

- **默认 Base URL：** `http://localhost:18180/api/v1`。连接远程服务时替换为管理员提供的地址。
- **访问方式：** 当前服务为共享工作区，没有内置用户登录或租户隔离；若管理员配置了认证网关，按网关要求附加凭据。接口中的内容可见性标记不替代访问控制。
- **响应格式：** 普通 JSON 接口使用 `APIResponse`；能力发现接口直接返回 JSON，文件下载和事件流使用各自内容类型。

本文保留 `algoforge.*` schema、`ALGOFORGE_*` 配置名与兼容响应头，便于已有客户端继续调用。示例中的地址、模型名和题目仅用于说明，请使用自己的工作区和模型设置。

## 通用响应格式

普通 JSON 接口返回统一的信封：

```json
{
  "success": true,
  "data": {},
  "error": null,
  "meta": {
    "total": 100,
    "page": 1,
    "size": 20
  }
}
```

错误响应：

```json
{
  "success": false,
  "data": null,
  "error": {
    "code": "NOT_FOUND",
    "message": "Problem not found"
  }
}
```

### 通用错误码

| 错误码 | HTTP 状态码 | 说明 |
|--------|-----------|------|
| `UNAUTHORIZED` | 401 | 所配置的认证层或主体校验拒绝请求 |
| `FORBIDDEN` | 403 | 无权限访问 |
| `NOT_FOUND` | 404 | 资源不存在 |
| `VALIDATION_ERROR` | 400 | 请求参数校验失败 |
| `CONFLICT` | 409 | 资源冲突（如重复创建） |
| `INTERNAL_ERROR` | 500 | 服务器内部错误 |
| `RATE_LIMITED` | 429 | 请求过于频繁 |

---

## 客户端调用约定

| 项 | 约定 |
|----|------|
| 异步生成 | 调用方优先使用稳定资源 `POST /generation/jobs`，再读取其 status/result/events/cancel 链接；旧 `POST /problems/generate` 继续返回 `workflow_id/run_id`，作为兼容入口保留。 |
| 三槽多样性生成 | 需要一次生成三道结构不同的题时使用 `POST /generation/micro-batches`；请求内恰好放三个 Job API v1 slot，每个仍固定 `candidate_count=1`。 |
| 异步验算 | `POST /problems/:id/validate` 只启动验算工作流，返回 `workflow_id/run_id`；调用方应轮询工作流详情。 |
| 幂等性 | `POST /generation/jobs` 与 `POST /generation/micro-batches` 必须携带 `Idempotency-Key`；同一主体、同一 key 与 canonical payload 返回同一资源，不同 payload 返回 `idempotency_conflict`。旧 `POST /problems/generate` 与 `POST /problems/:id/validate` 每次仍会启动新工作流。 |
| 客户端超时 | 生成/验算通过工作流轮询处理；Hydro ZIP 下载和上传预检建议客户端 HTTP timeout 不低于 120 秒。 |
| 请求大小 | Hydro 预检 ZIP 最大 128 MiB；`problem.yaml`、题面 Markdown 和 `testdata/config.yaml` 单个文本文件最大 2 MiB。 |
| 集成示例 | `examples/hydro_cleanroom_client.py` 提供零第三方依赖 client；`scripts/dev/hydro-chain-smoke.ps1` 提供“生成/已有题 -> 验算 -> Hydro 包 -> 预检”链路脚本。 |

---

## 统一题目搜索

### GET /questions/search

同时检索编程题库与客观题库，返回摘要、总数及统一分页。无需调用模型，也不需要额外搜索服务或数据库迁移。

| 参数 | 说明 |
|---|---|
| `q` | 题号/code、UUID、标题、标签名称/标识、知识点名称/编码的部分文字；忽略大小写，`%`、`_` 按普通字符匹配 |
| `type` | 空为全部；`programming`、`choice`、`fill_blank`、`judge` |
| `tag` | 标签标识或显示名的部分文字 |
| `knowledge_point` | 知识点名称或编码的部分文字；编程题使用已有标签分类名称/标识 |
| `min_difficulty` / `max_difficulty` | 编程题库原有整数评分，范围 800–3500，可只传一端；下限不得高于上限 |
| `quiz_difficulty` | 客观题库原有 `easy`、`medium`、`hard`；不能与数值评分同时传入 |
| `page` / `size` | 默认 1 / 20；页码 1–1000000，每页 1–100 |

非空条件之间取交集。三个文本条件各最多 200 个字符。选择数值难度仅搜索编程题库，选择枚举难度仅搜索客观题库；不将两种尺度互相换算。无匹配返回空数组及 `meta.total=0`，超出末页仍返回准确总数。

示例：`GET /api/v1/questions/search?q=图&type=choice&quiz_difficulty=medium&page=1&size=20`

成功响应的 `data` 为摘要数组，每项含 `id`、`source`（`problem` / `quiz`）、`type`、`code`、`title`、`tags`、`knowledge_points`、`updated_at`，以及来源适用的 `difficulty` 或 `quiz_difficulty`（可含 `level`、`status`）。不返回题面、选项、答案、解法或实例配置。结果按更新时间降序、来源和 ID 排序，避免分页顺序不稳定。

详情链接必须按 `source` 决定：`problem` 对应 `/problems/:id`，`quiz` 对应 `/quizzes/:id`。客观题库中兼容保留的 `type=programming` 记录仍归 `quiz`；两库相同 UUID 也是不同记录。

搜索延续共享工作区题库的可见范围，排除隔离、拒绝和已删除的编程题；不增加用户权限或租户隔离。错误参数返回 HTTP 400。旧服务没有本接口时，客户端显示升级提示和原题库入口，不将单库结果当作完整搜索结果。

---

## 1. 题目管理

### 1.1 POST /problems/generate

**生成新题目。** 启动一个异步工作流来生成题目描述、标准解法、测试数据等。

**请求体 (JSON):**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `level` | string | 是 | 题目层级：`syntax` 或 `algorithm` |
| `difficulty` | number | 是 | 难度值：语法题 `800-1200`，算法题 `1200-3500`，步长 100 |
| `tags` | string[] | 否 | 标签名称列表 |
| `contest_style` | string | 否 | 赛制风格：`icpc`/`ioi`/`codeforces`/`leetcode`/`noip`/`custom` |
| `languages` | string[] | 是 | 解法语言，如 `["cpp"]` |
| `custom_prompt` | string | 否 | 自定义生成需求描述 |
| `test_data_config` | object | 是 | 新题流程传 `num_test_cases: 0`，LLM 按 corner case 在 `min_test_cases=10` 到 `max_test_cases=20` 中选择最终测试点数量；另含 `num_samples` 等 |
| `similar_limit` | number | 否 | embedding 向量去重近邻上限；`0` 关闭，推荐 `3` |
| `provider_config.statement.model` | string | 否 | 题面生成使用的模型；未提供且没有已保存的 statement 配置时拒绝请求 |
| `provider_config.statement.base_url` | string | 否 | 题面生成服务根地址；系统按协议补充具体路径 |
| `provider_config.statement.provider` | string | 否 | 题面生成 provider 身份；设置 `base_url` 时必填，用于 provenance/审计 |
| `provider_config.statement.protocol` | string | 否 | `auto`、`gemini-native`、`openai-responses`、`anthropic-messages` 或 `openai-chat`；默认 `auto` |
| `provider_config.statement.api_key_ref` | string | 否 | 题面生成 key 引用，支持 `env:NAME` 或 API 下发的 `runtime:<token>` |
| `provider_config.statement.api_key` | string | 否 | 题面生成本次请求 API key；API 服务会换成短期 `runtime:<token>`，不回显、不入库 |
| `provider_config.verification.model` | string | 否 | LLM 验算使用的模型；为空时继承已保存的有效 V 配置 |
| `provider_config.verification.base_url` | string | 否 | LLM 验算服务根地址；系统按协议补充具体路径 |
| `provider_config.verification.provider` | string | 否 | LLM 验算 provider 身份；设置 `base_url` 时必填，用于 provenance/审计 |
| `provider_config.verification.protocol` | string | 否 | 与 statement 相同；`auto` 按模型名和 provider 自动识别协议 |
| `provider_config.verification.api_key_ref` | string | 否 | LLM 验算 key 引用，支持 `env:NAME` 或 API 下发的 `runtime:<token>` |
| `provider_config.verification.api_key` | string | 否 | LLM 验算本次请求 API key；API 服务会换成短期 `runtime:<token>`，不回显、不入库 |
| `provider_config.review.*` | 同 verification | 否 | R 独立覆盖；未提供时按有效 V 配置运行 |

`api_key` 和 `api_key_ref` 不能同时传。外部集成推荐优先使用 `env:NAME`；需要由平台用户临时输入 key 时，传 `api_key`，API 服务会在启动工作流前存入 Redis 短期密钥槽并把工作流参数改写为 `runtime:<token>`，避免原始密钥进入 Temporal history、题目元数据和响应体。

API 自动注入 `/settings/llm` 的有效配置：statement 必须先保存完整的 Base URL、模型和 API Key；V 无独立覆盖时跟随 G，R 无独立覆盖时跟随 V。请求只覆盖 `model` 时可沿用该角色的有效密钥；请求一旦改变 `base_url`、`provider` 或 `protocol`，必须同时提供自己的 `api_key` 或 `api_key_ref`，避免把永久密钥发送到调用方指定的新端点。没有用户保存配置时不会回退到部署模型，而是明确返回配置错误。

`auto` 对 `gemini-*` 使用 Gemini Native，对 `gpt-*`/`o1*`/`o3*`/`o4*` 使用 OpenAI Responses，对 `claude-*` 使用 Anthropic Messages；其他兼容服务按配置使用 OpenAI Chat Completions。Gemini 和 Claude 的 Base URL 填根地址，OpenAI 路径也可填根地址，Qraft 会避免重复追加 `/v1`。

**响应:** `APIResponse<{ workflow_id: string; run_id: string }>`

**示例:**

```bash
curl -X POST http://localhost:18180/api/v1/problems/generate \
  -H "Content-Type: application/json" \
  -d '{
    "level": "algorithm",
    "difficulty": 1600,
    "tags": ["dp", "greedy"],
    "contest_style": "icpc",
    "time_limit": 2000,
    "memory_limit": 256,
    "test_data_config": {
      "num_test_cases": 0,
      "min_test_cases": 10,
      "max_test_cases": 20,
      "num_samples": 2
    },
    "languages": ["cpp"],
    "similar_limit": 3,
    "locale": "zh",
    "provider_config": {
      "statement": {
        "model": "gemini-example",
        "base_url": "https://llm.example.com",
        "provider": "custom",
        "protocol": "auto",
        "api_key": "sk-..."
      },
      "verification": {
        "model": "gpt-5.6-sol",
        "base_url": "https://llm.example.com",
        "provider": "custom",
        "protocol": "auto",
        "api_key_ref": "env:ALGOFORGE_REVIEW_LLM_KEY"
      }
    }
  }'
```

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `INVALID_LEVEL` | level 值不合法 |
| `INVALID_DIFFICULTY` | difficulty 超出范围或步长不对 |
| `INVALID_TAGS` | tags 列表为空或包含无效标签 |
| `WORKFLOW_LIMIT_REACHED` | 同时运行的工作流数达到上限 |

### 1.1A POST /generation/jobs

**创建稳定的单候选生成 job。** 当前 v1 的 `candidate_count` 必须为 `1`；大于 `1` 会以
`unsupported_constraint` fail-closed。请求必须携带 `Idempotency-Key`，key 绑定调用主体和规范化请求。默认共享工作区没有登录主体，客户端应使用不冲突的 key；不能依靠此机制实现用户隔离。

`ALGOFORGE_CUSTOM_GENERATION_API_MODE` 默认是 `jobs-v1`。设置为 `legacy-only` 或
`contract-preview` 会关闭这五个 jobs 路由，但保留旧 `/problems/generate`；未知 mode 会阻止 API 启动。
该开关不改变自动发布策略。

**请求头：**

| Header | 必填 | 说明 |
|---|---|---|
| `Idempotency-Key` | 是 | 最长 256 字节，不得包含控制字符；相同主体和 key 重试同一 canonical 请求会复用 job |
| `Content-Type: application/json` | 是 | 请求最大 64 KiB，拒绝未知字段和尾随 JSON 文档 |

**请求体示例：**

```json
{
  "schema_version": "algoforge.product.custom-generation.request.v1",
  "domain": {
    "name": "competitive_programming",
    "level": "algorithm",
    "knowledge_points": ["dp", "prefix-sum"],
    "combination": {"mode": "set", "max_concepts": 3}
  },
  "difficulty": {
    "rating": 1800,
    "tolerance": 0,
    "calibration_profile": "default"
  },
  "problem_type": "standard",
  "constraints": {
    "time_limit_ms": 2000,
    "memory_limit_mb": 256,
    "test_case_count": 0,
    "test_case_count_min": 10,
    "test_case_count_max": 20,
    "sample_count": 2,
    "max_input_bytes": 0,
    "max_output_bytes": 0
  },
  "quality": {
    "strategy": "baseline",
    "dedup_mode": "reject",
    "similar_limit": 3,
    "budget": {
      "max_llm_calls": 0,
      "max_tokens": 0,
      "max_wall_time_seconds": 3600,
      "max_regenerations": 0
    },
    "audit_profiles": []
  },
  "candidate_count": 1,
  "output": {
    "evidence_level": "minimal",
    "formats": ["algoforge"],
    "include_editorial": true,
    "include_solutions": true,
    "include_test_data": true
  },
  "locale": "zh",
  "languages": ["cpp"],
  "contest_style": "icpc",
  "custom_requirements": "Prefer a controllable two-concept interaction.",
  "runtime": {
    "statement_profile": "default-statement",
    "verification_profile": "default-verification"
  }
}
```

v1 只接受 `standard` 题型、单一解法语言、`baseline` + `reject`、服务端默认 statement/verification
profile，以及 `minimal`、`standard`、`audit` 三种 evidence level。三种 level 都进入同一 S3 九门质量流程；
`standard` 和 `audit` 不再调用旧 publication/review receipt workflow。旧 standard receipt 只为历史 job 保留读取
兼容。`ALGOFORGE_QUALITY_MODE=legacy-only` 时 minimal 仍可生成，而新的 standard/audit 会在启动 Temporal 前以
HTTP 422 拒绝。尚未贯通的随机种子、多候选、audit profile、调用/token/重生成预算和每请求字节上限会明确拒绝，
不会静默忽略。`max_wall_time_seconds` 已作为 Temporal execution/run timeout 贯通，允许范围为 60..86400 秒，
超时投影为 `budget_exhausted`。

新题流程将 `constraints.test_case_count` 设为 `0`，并使用
`test_case_count_min=10`、`test_case_count_max=20`。LLM 先枚举题目实际需要区分的
corner case，再选择覆盖所需的最小充分集合；自定义用例计入最终总数，服务端会校验最终数量必须在
10–20（含）之间，不能用重复数据凑数。旧的 `auto_case_count=true` 和固定正数请求仍作为
旧兼容输入仍可接受，但新客户端不应再依赖旧上限语义。

`knowledge_points` 使用 `GET /api/v1/tags` 返回的稳定小写 `tag_name` slug。jobs v1 支持四种
`combination.mode`，并以 `algoforge.knowledge-point-combination.v1` 贯通到生成、解法、测试数据、
review 与 Store：

- `single`：恰好一个主知识点；
- `set`：无序集合，所有列出的知识点都必须是必要条件；
- `sequence`：数组顺序就是预期求解阶段顺序，换序会改变 canonical payload；
- `mixed`：第一个知识点是主/建模概念，其余知识点是无序辅助集合，至少需要两个知识点。

`max_concepts=0` 会规范为本次列出的知识点数量；正值不得小于该数量。当前 v1 不自动补充额外概念，
模型返回的 `tags` 必须与请求精确一致：set 会排序，sequence 保序，mixed 固定首项并排序尾部。漂移、
重复、缺失或额外 tag 会进入 `quality_not_met` / `content`，不会写入题库。通过 Store 的题目 metadata
含确定性的 `knowledge_point_conformance` 记录；旧 `/problems/generate` 和旧 Temporal history 不带该
可选契约，保持原标签语义。

**响应：** 新建返回 HTTP 201；幂等重放返回 HTTP 200。两者均为
`APIResponse<JobAccepted>`：

```json
{
  "success": true,
  "data": {
    "contract_version": "algoforge.generation-job.v1",
    "job_id": "generation-job-v1-...",
    "status": "accepted",
    "idempotent_replay": false,
    "links": {
      "status": "/api/v1/generation/jobs/generation-job-v1-...",
      "result": "/api/v1/generation/jobs/generation-job-v1-.../result",
      "cancel": "/api/v1/generation/jobs/generation-job-v1-...",
      "events": "/api/v1/generation/jobs/generation-job-v1-.../events"
    }
  }
}
```

### 1.1B GET /generation/jobs/:id

**读取稳定 job 状态。** `status` 只使用
`accepted/queued/running/succeeded/quarantined/failed/cancelled/cancellation_requested`；`phase` 只使用
`queued/generating/validating/reviewing/storing/completed`。

```json
{
  "success": true,
  "data": {
    "contract_version": "algoforge.generation-job.v1",
    "job_id": "generation-job-v1-...",
    "status": "running",
    "phase": "validating",
    "progress": 60,
    "result_available": false,
    "links": {
      "status": "/api/v1/generation/jobs/generation-job-v1-...",
      "result": "/api/v1/generation/jobs/generation-job-v1-.../result",
      "cancel": "/api/v1/generation/jobs/generation-job-v1-...",
      "events": "/api/v1/generation/jobs/generation-job-v1-.../events"
    }
  }
}
```

终态失败时 `data.error` 为 `{code,message,retryable,outcome_category}`。`outcome_category`
稳定取 `technical/content/review/publication_eligibility` 之一，用于分开技术、内容、审核与发布资格
分母；它不把 reviewer verdict 当作内容 gold。调用方不得从响应中推断 Temporal
workflow/activity/step 名称。

### 1.1C GET /generation/jobs/:id/result

**读取稳定结果。** 非终态返回 HTTP 409 `job_not_ready`；终态返回
`APIResponse<JobResult>`。当前新建任务只在 S3 九门全部 PASS 且质量草稿已完成物化后返回
`succeeded`；这里的成功表示“可编辑、可按证据导出”，题目仍是 `draft`，不等于 `published`。

```json
{
  "success": true,
  "data": {
    "contract_version": "algoforge.generation-job.v1",
    "job_id": "generation-job-v1-...",
    "status": "succeeded",
    "evidence_level": "audit",
    "candidates": [{
      "candidate_id": "generation-job-v1-...-c1",
      "problem_id": "550e8400-e29b-41d4-a716-446655440000",
      "status": "succeeded",
      "problem_url": "/api/v1/problems/550e8400-e29b-41d4-a716-446655440000",
      "quality_report_uri": "/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/quality"
    }],
    "evidence": [
      {
        "kind": "quality_audit",
        "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "uri": "/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/quality/audit"
      },
      {
        "kind": "test_manifest",
        "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        "uri": "/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/test-manifest"
      }
    ],
    "evidence_identity": {
      "schema_version": "algoforge.generation-evidence-bundle.v0",
      "profile": "algoforge.review-evidence.v0",
      "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    }
  }
}
```

`quality_audit` 绑定 Store 保存的 canonical 九门 QualityAudit，`test_manifest` 绑定同一任务的 canonical
TestManifest v2；两者与问题 metadata 中完整 ArtifactRef 互相校验。任何一门未通过时 workflow 返回
非重试 `QualityNotMet`，job 以 `quality_not_met` 失败且不会创建题目。旧 workflow history 仍可投影
`quarantined`、`review_result` 或 `publication_decision`，但这只是兼容读取路径。`evidence_identity.sha256`
继续对稳定排序的 `{kind,sha256}` 计算，URI 只是 locator，不进入内容身份。

历史上以旧 `ProblemGenerationStandardEvidenceWorkflowV1` 创建的 standard job，仍会在最终
review/publication gate 和可选自动批准之后返回：

```json
{
  "kind": "standard_evidence",
  "sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
  "uri": "/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/standard-evidence"
}
```

该兼容收据只绑定旧 jobs contract/profile descriptor、A arm、请求的 `include_*`、最终状态/outcome，以及排序后的
TestManifest + review result 或 publication decision `{kind,sha256}`。它不包含对象存储路径、源 artifact
位置、模型正文、review 全文或 Temporal workflow/run/activity 身份。standard job 完成时的 status、隔离原因
与 outcome evidence SHA 会随收据引用一起独立固化；题目后来获批或改变生命周期不会改写该 job 的结果。
当前 create 路径不会把 S3 draft 静默降级成这种旧收据。

### 1.1D GET /generation/jobs/:id/events

**订阅版本化 SSE。** 事件名为 `job_updated` 或 `job_terminal`，每个 `data` 都是稳定 `JobEvent`：

```text
id: 1
event: job_updated
data: {"contract_version":"algoforge.generation-job.v1","event_id":"1","type":"job_updated","job_id":"generation-job-v1-...","status":"running","phase":"generating","progress":20,"occurred_at":"2026-08-21T01:02:03Z"}
```

### 1.1E DELETE /generation/jobs/:id

**幂等请求取消。** 运行中 job 返回 HTTP 202 和 `cancellation_requested`；已经终止的 job 返回 HTTP 200
及其现有终态。取消不会删除 Temporal 历史。

```json
{
  "success": true,
  "data": {
    "contract_version": "algoforge.generation-job.v1",
    "job_id": "generation-job-v1-...",
    "status": "cancellation_requested",
    "links": {
      "status": "/api/v1/generation/jobs/generation-job-v1-...",
      "result": "/api/v1/generation/jobs/generation-job-v1-.../result",
      "cancel": "/api/v1/generation/jobs/generation-job-v1-...",
      "events": "/api/v1/generation/jobs/generation-job-v1-.../events"
    }
  }
}
```

### 1.1F GET /problems/:id/test-manifest

**读取不可变测试证据。** 新版题目存在 TestManifest 时返回 HTTP 200、`application/json`，
响应体是 Store 时写入对象存储的 canonical JSON 原始字节；其 SHA-256 与 jobs result
`evidence[kind=test_manifest].sha256` 相同。legacy 题目没有 manifest 时返回 HTTP 404；
元数据引用或对象字节与 SHA 不一致时返回 HTTP 500。

当前质量任务只物化 canonical `algoforge.test-manifest.v2`：每个 case 固定连续 `test_index`、purpose、
constraint region、boundary refs、seed，以及输入/输出 CAS ArtifactRef、SHA-256 和错误程序覆盖信息；
manifest 还绑定 semantic spec、authoring plan、oracle promotion、sanitizer 与 boundary coverage 收据。
旧 v1 仅供已有历史读取。该资源不暴露 Temporal step。

### 1.1G GET /problems/:id/standard-evidence

**读取历史 standard 级生成收据。** 仅旧 standard workflow 且 Store v5 完整结束的题目存在该资源。
返回 HTTP 200、`application/json` 和对象存储中的 canonical JSON 原始字节；响应体 SHA-256 必须等于 jobs
result 的 `evidence[kind=standard_evidence].sha256`。minimal/legacy 题目返回 HTTP 404；请求合同、schema、
固定路径、独立数据库绑定 SHA、下载正文任一不一致均返回 HTTP 500，不会降级为 minimal。收据绑定存于
`problem_generation_standard_evidence`，不会在最终 publication gate 后改写参与题面身份的 metadata。

### 1.1H GET /problems/:id/quality

**读取 V1 完整质量投影。** 服务会重新读取问题、测试用例、AuthoringBundle、StatementDraft、QualityAudit、
TestManifest v2 和逐测试资产，逐项重算 SHA 与祖先绑定后返回 canonical
`algoforge.problem-quality-report.v1` 原始 JSON 字节。响应头包含：

- `X-AlgoForge-Quality-Report-SHA256`
- `X-AlgoForge-Quality-Audit-SHA256`
- `X-AlgoForge-Test-Manifest-SHA256`
- `X-AlgoForge-External-OJ-Import-Verified: false`

报告的九门状态固定按 canonical 顺序排列；`standard`、`verified`、`publication_ready` 三层不会把 draft
伪装成已发布。概念签名和难度目标来自不可变 AuthoringBundle CAS；数据库 tags/difficulty 与 CAS 漂移时返回
HTTP 409。难度明确是 `calibrated=false` 的生成目标提示，cost 在无法从持久证据复算时明确为
`unavailable`，外部 OJ 导入状态恒为 false。报告 SHA 只是可复算响应身份，不是新的 EvidenceRef。

### 1.1I GET /problems/:id/quality/audit

**读取 canonical 九门审计原始字节。** 返回对象存储中的
`algoforge.s3-quality-audit.v1`，但只有在问题、测试用例、全部 CAS 引用与 SHA 重新校验后才会输出。
响应头 `X-AlgoForge-Quality-Audit-SHA256` 必须与正文 SHA-256 一致。

### 1.1J POST /problems/quality/batch-manifest

**为 1..500 道题生成确定性质量清单。** 请求最大 64 KiB，拒绝未知字段、重复 JSON key、尾随文档、
重复或非严格升序 UUID：

```json
{
  "schema_version": "algoforge.problem-quality-batch-request.v1",
  "problem_ids": [
    "11111111-1111-1111-1111-111111111111",
    "22222222-2222-2222-2222-222222222222"
  ]
}
```

返回 canonical `algoforge.problem-quality-batch-manifest.v1`，每项绑定 quality report、QualityAudit、
TestManifest 的 SHA 与稳定 URI；响应头
`X-AlgoForge-Quality-Batch-Manifest-SHA256` 绑定正文。任一题为 legacy、缺证据或发生篡改/漂移时整批
HTTP 409，不产生部分清单。

### 1.1K GET /integration/capabilities

**读取当前服务实际支持的能力。** 直接返回 JSON，没有 `success/data` 信封；响应带 `Cache-Control: no-store`。

| 字段 | 含义 |
| --- | --- |
| `schema_version` | 兼容格式 `algoforge.integration-capabilities.v1` |
| `release_version` | 当前服务版本 |
| `generation_jobs` | 单候选任务的 `enabled/routes` 和相关运行开关 |
| `generation_micro_batches` | 三槽多样性生成的 `enabled/routes` |
| `quality_layer` | 扩展质量接口是否可用 |
| `generation_evidence_levels` | 服务接受的证据级别 |
| `exports.hydro_routes_enabled` | Hydro 单题、批量导出和预检是否可用 |
| `exports.portable_sets_enabled` | Qraft 题集包是否可用 |
| `problem_sets` | 支持的题型、模式、题数范围以及管理/生成/组卷/导出能力 |

`problem_sets` 支持 `programming/choice/fill_blank/judge`、`programming/mixed` 两种模式和 1–1000 题。其 `generation.enabled` 表示自动生成是否开启；组卷无需模型。`problem_sets.export.format` 为 `algoforge.problem-set.v1`，`scores_included=true` 表示分值写入题集清单。

客户端应先检查目标操作的 `enabled` 与 `routes`。生成或质量开关调整需要重启对应服务；这些能力字段不代表第三方 OJ 已接收过题目。

### 1.1L /generation/micro-batches

**S5 三槽多样性生成资源。** `POST /generation/micro-batches` 的请求 schema 为
`algoforge.generation-micro-batch.v1`，`slots` 必须恰好包含三个完整的 Job API v1 请求；每槽
`candidate_count` 仍须为 `1`。请求沿用 Job API 的严格 JSON、认证主体、provider runtime key 和
wall-time 约束，并必须携带 `Idempotency-Key`。父级 ID 与三个 child ID 均由服务端按主体和 key 派生，
响应不暴露 Temporal workflow/run/activity 身份。

请求顶层只有 `schema_version` 与 `slots` 两个字段；`slots[0..2]` 各自使用第 1.1A 节的完整请求对象，
不得使用简写、未知字段或尾随 JSON 文档。

资源接口为：

- `POST /generation/micro-batches`：新建返回 HTTP 201；同 payload 幂等重放返回 HTTP 200；
- `GET /generation/micro-batches/:id`：返回父级 phase/progress、三个 slot 状态，以及可用时的
  `corpus_revision`、`reservation_sha256`、`manifest_sha256`；
- `GET /generation/micro-batches/:id/result`：成功后返回三道 draft 的 problem URL，并绑定每槽
  `concept_id`、`pool_sha256`、`structural_signature_sha256`；其中后者是 reserved concept 的结构签名，只绑定预留 winner，不是成题后的最终题结构验证；同时返回统一 corpus revision、reservation/
  manifest SHA 与 12 条真实有序去重 observation；成功空查询显式为 `neighbor_count=0`；
- `DELETE /generation/micro-batches/:id`：幂等请求取消父级；父级取消会按 Temporal 父子关系停止未完成子流程。

父流程固定顺序为“6 次创意调用全部结束 → 3 次低温规范化全部结束 → 一次 12 概念去重 → 确定性
winner/runner-up → 不可变预留 manifest → 3 条既有九门质量流程”。结构近邻 `warn` 只是选择信息，
不是第十条硬门；embedding、vector query、corpus revision 或 CAS 不可用会在子流程启动前失败。
这类失败在内部 observation 中固定为 `check_failed`，不会返回成功结果或伪装成 `neighbor_count=0`。
`ALGOFORGE_S5_DIVERSITY_MODE=diversity-v1` 默认注册四条路由；`legacy-only` 只撤回这些路由，旧 Job API
v1、Hydro 与质量接口保持不变。未知或显式空 mode 会阻止 API 启动。

**稳定错误码：**

| 错误码 | 位置/HTTP | 说明 |
|---|---|---|
| `budget_exhausted` | 终态 `data.error` | job 超过执行 wall-time budget |
| `quality_not_met` | 终态 `data.error` | 候选未满足质量链；结合 `outcome_category` 区分 content/review/technical，保留旧 error code 兼容 |
| `unsupported_constraint` | 创建时 HTTP 422 | v1 不支持该约束，例如 `candidate_count > 1` |
| `unsatisfiable_spec` | 创建时 HTTP 400/422 | JSON 或字段组合无法形成可执行规格 |
| `idempotency_conflict` | HTTP 409 | 同一主体与 `Idempotency-Key` 已绑定不同 canonical payload |
| `job_not_ready` | HTTP 409 | result 尚未到终态 |
| `not_found` | HTTP 404 | job 不存在或不属于当前主体 |

jobs v1 的 JSON DTO 不包含 `run_id`、`workflow_id`、`current_step`、`steps`、activity type 或原始 provider
错误文本。客户端只能依赖本文列出的 contract version、status、phase、result、event 与 error code。

`outcome_category` 的稳定含义：

| 值 | 含义 |
|---|---|
| `technical` | timeout、provider/存储/历史/内部执行或请求执行契约失败 |
| `content` | 去重、题面、样例、解法、编译、差分或验证质量门失败 |
| `review` | 自动/人工 review gate 拒绝；不等于独立内容 gold |
| `publication_eligibility` | 内容链完成，但 provenance/rights/publication policy 不允许进入发布面 |

---

### 1.2 GET /problems

**获取题目列表。** 支持筛选、排序和分页。未显式传 `status` 时默认隐藏 `quarantined` 与 `rejected`；需要运维排查时可显式按状态查询。

**查询参数:**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `level` | string | 否 | 筛选层级：`syntax` 或 `algorithm` |
| `status` | string | 否 | 筛选状态：`draft`/`generating`/`review`/`published`/`rejected`/`quarantined` |
| `difficulty_min` | number | 否 | 最低难度 |
| `difficulty_max` | number | 否 | 最高难度 |
| `tags` | string[] | 否 | 标签筛选（多选，同一参数名多次传递） |
| `search` | string | 否 | 标题/题面全文搜索 |
| `sort_by` | string | 否 | 排序字段：`created_at`/`difficulty`/`title`/`serial_number` |
| `sort_order` | string | 否 | 排序方向：`asc`/`desc`，默认 `desc` |
| `page` | number | 否 | 页码，默认 1 |
| `size` | number | 否 | 每页条数，默认 20，最大 100 |

**响应:** `APIResponse<Problem[]>`，`meta` 包含分页信息。

**示例:**

```bash
curl "http://localhost:18180/api/v1/problems?level=algorithm&difficulty_min=1200&difficulty_max=2000&tags=dp&tags=greedy&page=1&size=20"
```

---

### 1.3 GET /problems/:id

**获取单个题目的完整信息。**

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**响应:** `APIResponse<Problem>`

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000
```

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `NOT_FOUND` | 题目不存在 |

---

### 1.4 PUT /problems/:id

**更新题目信息。** 支持部分更新。该接口采用乐观锁，必须携带调用方读取题目时得到的 `updated_at`；`status`、`source`、`workflow_id` 等系统字段不可由客户端直接修改。已发布题目被编辑后会自动回到 `draft`，并把题面向量、标程和测试数据标记为 stale，等待重新嵌入、验算和发布 gate。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**请求体 (JSON):**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `expected_updated_at` | string (RFC3339) | 是 | 题目当前 `updated_at`，并发编辑冲突时返回 409 |
| `title` | string | 否 | 题目标题 |
| `statement` | string | 否 | 题面（Markdown） |
| `level` | string | 否 | `syntax` 或 `algorithm` |
| `difficulty` | number | 否 | 难度值 |
| `tags` | string[] | 否 | 标签列表 |
| `one_line_hint` | string | 否 | 内部作者提示；不属于选手可见题面，也不会随 Hydro 竞赛包导出 |
| `detailed_solution` | string | 否 | 详细题解 |
| `time_limit` | number | 否 | 时间限制 (ms) |
| `memory_limit` | number | 否 | 内存限制 (MB) |
| `metadata_json` | object | 否 | 题目元数据 |

请求体拒绝未知字段；至少要包含一个可变字段。

**响应:** `APIResponse<Problem>`

**示例:**

```bash
curl -X PUT http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000 \
  -H "Content-Type: application/json" \
  -d '{
    "expected_updated_at": "2026-08-18T08:30:00Z",
    "title": "新标题",
    "difficulty": 1800,
    "tags": ["dp", "dp-knapsack"]
  }'
```

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `INVALID_BODY` / `INVALID_PARAMS` | 请求体缺字段、未知字段或字段值不合法 |
| `CONFLICT` | 题目正在生成/评审、不可编辑，或 `expected_updated_at` 已过期 |
| `NOT_FOUND` | 题目不存在 |

---

### 1.4.1 POST /problems/:id/edit-refresh

**完成编辑后刷新 gate。** 当调用方已经为 stale 题面写入新的 active embedding，并完成重新验算/评审报告后，调用该接口清除 release-critical stale 标记并重新进入发布 gate。接口使用 `workflow_operations` 幂等账本；相同 `operation_key` 重试会返回同一份 gate 报告。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**请求体 (JSON):**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `validation_report_sha256` | string | 是 | 重新验算/评审报告的 SHA256 |
| `operation_key` | string | 否 | 幂等键；不传时服务端按题目 ID 和报告 hash 生成 |
| `actor` | string | 否 | 操作人；不传时使用 `X-Actor`，再默认 `api` |

**响应:** `APIResponse<ProblemEditRefreshReport>`

**示例:**

```bash
curl -X POST http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/edit-refresh \
  -H "Content-Type: application/json" \
  -d '{
    "validation_report_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  }'
```

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `INVALID_BODY` / `INVALID_PARAMS` | 请求体缺字段、未知字段或字段值不合法 |
| `CONFLICT` | stale 资产尚未刷新、题目状态不满足 gate，或发布 gate 冲突 |
| `NOT_FOUND` | 题目不存在 |

---

### 1.5 DELETE /problems/:id

**删除题目。** 同时删除关联的解法、测试数据和工作流。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**响应:** `APIResponse<void>`

**示例:**

```bash
curl -X DELETE http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000
```

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `NOT_FOUND` | 题目不存在 |
| `CONFLICT` | 题目关联的工作流正在运行，无法删除 |

---

### 1.6 GET /problems/:id/testcases

**获取题目的所有测试用例元信息。**

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**响应:** `APIResponse<TestCase[]>`

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/testcases
```

---

### 1.7 GET /problems/:id/testcases/:tid/input

**下载单个测试用例的输入文件。**

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |
| `tid` | string (UUID) | 测试用例 ID |

**响应:** `text/plain` — 原始输入数据。

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/testcases/660e8400-0001/input \
  -o input.txt
```

---

### 1.8 GET /problems/:id/testcases/:tid/output

**下载单个测试用例的输出文件。**

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |
| `tid` | string (UUID) | 测试用例 ID |

**响应:** `text/plain` — 原始输出数据。

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/testcases/660e8400-0001/output \
  -o output.txt
```

---

### 1.9 GET /problems/:id/testdata.zip

**下载题目的裸测试数据 ZIP。** 仅包含 `001.in`、`001.out` 等输入输出文件，供调试使用；不是 Hydro 上传包。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**响应:** `application/zip`

**集成约束:** 该接口是可重试 `GET`。建议外部 client 对 ZIP 下载设置至少 120 秒超时；下载失败后可重试同一 URL。

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/testdata.zip \
  -o testdata.zip
```

---

### 1.10 GET /problems/:id/hydro.zip

**下载单题 Hydro 原生上传包。** ZIP 根目录直接包含 `problem.yaml`、`problem_zh.md`、`testdata/config.yaml`、`testdata/*.in/out`，并在 `additional_file/algoforge_manifest.json` 保留工程审计元数据。

当前导出覆盖 Hydro 阶段一能力：普通编程题、标准 IO、可选 `filename` 文件 IO、
`checker_type: default`、`cases`、`subtasks[].type: sum/min` 与三级 time/memory。TestManifest v2 的
sample/tiny/random/boundary/extreme/complexity/metamorphic purpose 会确定性映射，总分固定 100，
存在压力档时其合计至少 30。SPJ、交互、通信、提交答案、客观题、语言倍率、额外文件执行等字段
只登记为阶段二保留能力，不在当前包中伪装支持。

S3/TestManifest v2 产品路径要求题目 time/memory 均为正，并把同一数值写入 config、每个 subtask 和
每个 case，避免三层出现不同语义。`metadata_json.hydro` 在单题和批量共享写包路径严格解析；未知键、
`interactive/type/subtasks/strict` 等阶段二字段、非规范 pid/filename 或非法 detail 均返回
`INVALID_PARAMS`，不会先清洗再继续导出。

导出受 S3 证据绑定保护：题目必须是九门 PASS 后物化的 v6 `draft`（或其后保持同一证据的
`published`），`s3_quality_v1`、canonical QualityAudit、TestManifest v2 与逐测试资产的 problem/index/
sample/SHA/size 必须全部一致，且不得标记 `stale: true`。仅有旧 `published` 字样而没有这条证据链
同样返回 `409 CONFLICT`。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**响应:** `application/zip`

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `CONFLICT` | 九门 PASS、ArtifactRef、QualityAudit、TestManifest、测试资产绑定或 fresh 状态任一不成立 |
| `INVALID_PARAMS` | Hydro 阶段一不支持当前题目配置或测试数据不完整 |
| `NOT_FOUND` | 题目不存在 |

**集成约束:** 该接口是可重试 `GET`。建议外部 client 对 ZIP 下载设置至少 120 秒超时；下载失败后可重试同一 URL。

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/hydro.zip \
  -o C1000-hydro.zip
```

---

### 1.11 GET /problems/hydro.zip

**下载批量 Hydro 上传包。** 查询参数 `ids` 可重复或使用逗号分隔；ZIP 根目录包含多个题目目录，每个目录直接包含 `problem.yaml`，不使用 ZIP 套 ZIP。

批量导出逐题执行同一 S3 证据绑定；任一题目不满足时整个请求返回 `409 CONFLICT`，不生成部分成功包。

**查询参数:**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `ids` | string / string[] | 是 | 题目 UUID，可重复传递或逗号分隔，最多 100 个 |

**响应:** `application/zip`

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `INVALID_PARAMS` | `ids` 为空、包含非 UUID，或超过 100 个题目 |
| `CONFLICT` | 任一题目的九门/manifest/测试资产/fresh 绑定不成立 |
| `NOT_FOUND` | 任一题目不存在 |

**集成约束:** 该接口是可重试 `GET`。批量请求任一题失败时整体失败，不返回部分 ZIP。

**示例:**

```bash
curl "http://localhost:18180/api/v1/problems/hydro.zip?ids=550e8400-e29b-41d4-a716-446655440000&ids=660e8400-e29b-41d4-a716-446655440000" \
  -o qraft-hydro-batch.zip
```

---

### 1.12 POST /problems/hydro/validate

**校验 Hydro 单题或批量 ZIP。** 该接口只做格式和阶段一兼容性预检，不落库导入；返回每个题目目录的 `problem.yaml`、题面、测试数据、`testdata/config.yaml` 解析结果，以及明确的错误、警告和阶段二保留项。

**请求:** `multipart/form-data`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `file` | file | 是 | Hydro 单题或批量 `.zip` |

**大小限制:** ZIP 最大 128 MiB；`problem.yaml`、题面 Markdown、`testdata/config.yaml` 单个文本文件最大 2 MiB。

**响应:** `APIResponse<HydroValidationReport>`

**示例:**

```bash
curl -X POST http://localhost:18180/api/v1/problems/hydro/validate \
  -F "file=@C1000-hydro.zip"
```

报告字段：

| 字段 | 说明 |
|------|------|
| `valid` | 整个 ZIP 是否可按当前阶段一能力接收 |
| `mode` | `single` / `batch` / `unknown` |
| `problems[].config_mode` | `cases` / `subtasks` / `auto_detect` |
| `problems[].unsupported` | 已识别但当前阶段不执行的 Hydro 字段，例如 SPJ、交互、通信、`if`、`max`；存在 unsupported 时该题 `valid=false` |

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `MISSING_FILE` | multipart 中缺少 `file` 字段 |
| `INVALID_FILE` | 上传文件无法打开 |
| `HYDRO_VALIDATE_FAILED` | ZIP 无法读取、超过大小限制，或请求体不是合法 ZIP |

**集成约束:** 该接口无落库副作用，可用同一 ZIP 重试。建议外部 client 对上传预检设置至少 120 秒超时。

---

### 1.12A 题集生成、组卷与管理

题集用于组织比赛、作业、课程或模拟练习，保存需求、题目顺序、分区与分值。既可自动出题，也可从已有题库组卷。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/problem-sets/assembly-preview` | 按 config/filter 返回选题与缺口；只读 |
| `POST` | `/problem-sets/assemble` | 按预览引用保存；过期返回 409 |
| `POST` | `/problem-sets/:id/generation` | 开始或补齐未完成题位，202 |
| `GET` | `/problem-sets/:id/generation` | 持久进度；未开始可为 data:null |
| `POST` | `/problem-sets/:id/generation/cancel` | 请求停止，202；需轮询到终态 |
| `POST` | `/problem-sets` | 创建题集；`desired_item_count` 为 1–1000 |
| `GET` | `/problem-sets` | 分页查询；支持 search、kind、status |
| `GET`/`PUT`/`DELETE` | `/problem-sets/:id` | 读取、更新或删除 |
| `POST` | `/problem-sets/:id/items` | 添加一个 problem_id 或 quiz_id |
| `PUT` | `/problem-sets/:id/items/reorder` | 用完整 item_ids 列表重排 |
| `DELETE` | `/problem-sets/:id/items/:item_id` | 移除题目 |
| `GET` | `/problem-sets/:id/quality` | 查看题数、覆盖、近期复用与可导出状态 |
| `POST` | `/problem-sets/:id/generate-prompt` | 根据题集需求生成可编辑提示词 |
| `GET` | `/problem-sets/:id/export.zip` | 下载 Qraft 题集包 |

创建示例：

```json
{
  "code": "practice-set",
  "title": "数据结构综合练习",
  "kind": "homework",
  "visibility": "private",
  "desired_item_count": 3,
  "start_generation": true,
  "generation_config": {
    "mode": "mixed",
    "requirements": "先检验基本概念，再安排综合应用。",
    "distribution": [
      {"type": "choice", "count": 2, "score": 10},
      {"type": "programming", "count": 1, "score": 80}
    ]
  }
}
```

省略 `start_generation` 时只保存题集。配额之和必须等于题数，每题分值为 0–10000。创建 201 但启动失败时，`data.generation_error` 说明原因；保留已创建 ID，重试它的生成操作。

自动编排状态包括 queued、planning、generating、completed、partial、failed、cancelled。生成中手工修改返回 409。停止后可继续补齐未完成题位，已入集题目保留。详见 [题集生成](problem-set-generation.md) 与 [组卷筛选和保存](problem-set-assembly.md)。

**导出：** 响应为 `application/zip`，包内包含 `problem-set.json`、`README.txt`，有编程题时附 `programming/<位置>-hydro.zip`，有客观题时附 `quizzes.xlsx`。JSON 保留整套顺序、分区、备注、分值与内容；格式详见 [题集导出清单](json-schema-reference.md#14-题集导出清单)。

响应头 `X-AlgoForge-Export-Profile` 为 `algoforge.problem-set.v1`，`X-AlgoForge-Problem-Set-Quality` 表示当前质量状态。成功导出会保存复用记录并将题集标记为 exported。需要有意复用近期题目或知识点时可传 `allow_reuse=true`；它不跳过题数、题型配额或编程资产检查。整个题集包不能直接交给 Hydro 导入器，应取出各单题 Hydro 子包分别导入。

### 1.12B 客观题表格

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/quizzes/template.xlsx` | 下载实时生成的空白表格，只有表头和排版 |
| `POST` | `/quizzes/import` | multipart 字段 file、subject；查询参数 on_conflict=error/skip/update |
| `GET` | `/quizzes/export` | 导出筛选后的全部客观题为 XLSX |

导出支持 type、subject、difficulty、tag、knowledge_point_id 筛选；编程教学记录不能从此入口导出为可评测题。导入响应的 data 包含 success_count、failed_count、inserted_ids、failed_rows，失败项包含 row_index/code/reason。应检查报告，不仅判断 HTTP 状态。

列定义、答案分隔方式、冲突策略及编程题边界见 [表格导入导出格式](quiz-import-format.md)。

---

### 1.13 GET /problems/:id/metadata

**获取题目的元数据 (JSON)。** 包含 LLM 生成时的中间数据、约束分析等。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**响应:** `APIResponse<Record<string, unknown>>`

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/metadata
```

---

### 1.14 GET /problems/:id/editorial

**获取题目的题解/解题思路。**

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**响应:** `APIResponse<{ editorial: string }>`。`editorial` 始终是已解包的 Markdown 文本，不包含模型返回的 JSON 外壳。

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/editorial
```

---

### 1.15 GET /problems/:id/solutions

**获取题目关联的可执行解法源码。** 返回结果按 `main`（标准解法）、`brute`（暴力/参考解法）及其他角色排序。题解 Markdown 请使用上面的 `/editorial` 接口。

**响应:** `APIResponse<Solution[]>`

其中每项包含 `id`、`problem_id`、`solution_type`、`language`、`source_code`、`compile_status` 和 `created_at`。

**示例:**

```bash
curl http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/solutions
```

---

### 1.16 POST /problems/:id/validate

**验证题目完整性。** 检查题面、标程、测试数据的一致性。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 题目 ID |

**响应:** `APIResponse<{ valid: boolean; issues: string[] }>`

**示例:**

```bash
curl -X POST http://localhost:18180/api/v1/problems/550e8400-e29b-41d4-a716-446655440000/validate
```

---

### 1.12 GET /problems/similar

**相似题目搜索。** 基于已入库题目的向量嵌入做语义近邻搜索。

**查询参数:**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `problem_id` | string(UUID) | 是 | 已有题目的 ID |
| `limit` | number | 否 | 返回数量上限，默认 10 |

**响应:** `APIResponse<Array<{ problem: Problem; similarity: number }>>`

**示例:**

```bash
curl "http://localhost:18180/api/v1/problems/similar?problem_id=550e8400-e29b-41d4-a716-446655440000&limit=5"
```

---

## 2. 标签

### 2.1 GET /tags

**获取所有标签分类及其下属标签。**

**查询参数:**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `level` | string | 否 | 按层级筛选：`syntax` 或 `algorithm` |

**响应:** `APIResponse<TagCategory[]>`

**示例:**

```bash
curl http://localhost:18180/api/v1/tags

# 按层级筛选
curl "http://localhost:18180/api/v1/tags?level=algorithm"
```

---

## 3. 工作流

### 3.1 GET /workflows

**获取工作流列表。** 支持分页和状态筛选。

**查询参数:**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `page` | number | 否 | 页码，默认 1 |
| `size` | number | 否 | 每页条数，默认 20 |
| `status` | string | 否 | 状态筛选：`pending`/`running`/`waiting_review`/`approved`/`rejected`/`failed`/`cancelled` |

**响应:** `APIResponse<WorkflowState[]>`

**示例:**

```bash
curl "http://localhost:18180/api/v1/workflows?status=running&page=1&size=20"
```

---

### 3.2 GET /workflows/:id

**获取单个工作流的详细状态。**

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 工作流 ID |

**响应:** `APIResponse<WorkflowState>`

生成链路会在 `similarity_check` 与 `post_statement_similarity` 步骤输出 `report` 字段；题目成功落库后，同一批报告会写入 `Problem.metadata_json.dedup_reports`，用于审计本次去重使用的向量空间和决策。

**DedupReport 字段:**

| 字段 | 类型 | 说明 |
|------|------|------|
| `schema_version` | string | 固定为 `algoforge.workflow.dedup.report.v1` |
| `stage` | string | `pre_generation` 或 `post_statement` |
| `model_version` | string(UUID) | 实际检索使用的 active statement model version；provider 不可用导致跳过时可为空 |
| `kind` | string | 向量空间类型，目前为 `statement` |
| `content_hash` | string | 本次检索文本的 SHA-256；post 阶段与最终写入 embedding 的 canonical text 公式一致 |
| `top_k` | number | 检索的 Top-K 数量 |
| `threshold` | number | 检索阈值；post 阶段为 warning threshold |
| `reject_threshold` | number | post 阶段硬拒绝阈值 |
| `decision` | string | `pass`/`warn`/`rejected`/`provider_unavailable` |
| `neighbor_count` | number | 返回近邻数量 |
| `max_similarity` | number | 最高相似度 |
| `similar_limit` | number | pre 阶段触发拒绝的近邻上限 |
| `reason` | string | 可读原因，如 `duplicate_title` 或 provider 错误 |

当 `decision=rejected` 时，生成链路不会进入默认发布面：pre 阶段会以 `TooSimilarError` 终止，post 阶段会重生成题面并在达到最大尝试次数后以 `StatementTooSimilar` 终止；自动评审拒绝的候选仍按既有路径进入 `rejected_quarantined`/`quarantined`。

**示例:**

```bash
curl http://localhost:18180/api/v1/workflows/770e8400-e29b-41d4-a716-446655440000
```

---

### 3.3 GET /workflows/:id/events (SSE)

**订阅工作流实时事件流。** 使用 Server-Sent Events (SSE) 协议。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 工作流 ID |

**查询参数:**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `token` | string | 否 | JWT token（SSE 不支持自定义头，通过 query 传递） |

**响应:** `text/event-stream`

事件类型：

| 事件 type | 说明 | 数据字段 |
|-----------|------|---------|
| `step_started` | 步骤开始执行 | `step_index`, `step_name`, `timestamp` |
| `step_completed` | 步骤执行完成 | `step_index`, `step_name`, `data`, `timestamp` |
| `step_failed` | 步骤执行失败 | `step_index`, `step_name`, `message`, `timestamp` |
| `workflow_completed` | 工作流全部完成 | `timestamp` |
| `workflow_failed` | 工作流失败终止 | `message`, `timestamp` |
| `log` | 日志信息 | `message`, `timestamp` |

**示例:**

```bash
curl -N "http://localhost:18180/api/v1/workflows/770e8400-e29b-41d4-a716-446655440000/events?token=$TOKEN"
```

**JavaScript 使用:**

```javascript
const es = new EventSource(
  `http://localhost:18180/api/v1/workflows/${id}/events?token=${token}`
);
es.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log(data.type, data);
};
```

---

### 3.4 POST /workflows/:id/approve

**审核通过工作流。** 将工作流状态标记为 `approved`，题目状态变为 `published`。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 工作流 ID |

**响应:** `APIResponse<WorkflowState>`

**示例:**

```bash
curl -X POST http://localhost:18180/api/v1/workflows/770e8400-e29b-41d4-a716-446655440000/approve
```

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `INVALID_STATE` | 工作流不在 `waiting_review` 状态 |

---

### 3.5 POST /workflows/:id/reject

**驳回工作流。** 将工作流状态标记为 `rejected`。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 工作流 ID |

**请求体 (JSON):**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `reason` | string | 否 | 驳回原因 |

**响应:** `APIResponse<WorkflowState>`

**示例:**

```bash
curl -X POST http://localhost:18180/api/v1/workflows/770e8400-e29b-41d4-a716-446655440000/reject \
  -H "Content-Type: application/json" \
  -d '{"reason": "题面描述不够清晰"}'
```

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `INVALID_STATE` | 工作流不在 `waiting_review` 状态 |

---

### 3.6 POST /workflows/:id/retry

**重试失败的工作流。** 从失败的步骤重新开始执行。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 工作流 ID |

**响应:** `APIResponse<WorkflowState>`

**示例:**

```bash
curl -X POST http://localhost:18180/api/v1/workflows/770e8400-e29b-41d4-a716-446655440000/retry
```

**特定错误码:**

| 错误码 | 说明 |
|--------|------|
| `INVALID_STATE` | 工作流不在 `failed` 状态 |

---

### 3.7 DELETE /workflows/:id

**取消/删除工作流。** 如果工作流正在运行则先取消，然后删除记录。

**路径参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string (UUID) | 工作流 ID |

**响应:** `APIResponse<WorkflowState>`

**示例:**

```bash
curl -X DELETE http://localhost:18180/api/v1/workflows/770e8400-e29b-41d4-a716-446655440000
```

---

## 4. 统计与运行状态

### 4.1 GET /stats

**获取仪表盘统计数据。**

**响应:** `APIResponse<DashboardStats>`

**示例:**

```bash
curl http://localhost:18180/api/v1/stats
```

**响应示例:**

```json
{
  "success": true,
  "data": {
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
        "description": "题目 AF-0120 已发布",
        "timestamp": "2026-04-08T10:30:00Z",
        "reference_id": "550e8400-e29b-41d4-a716-446655440000"
      }
    ]
  }
}
```

---

### 4.2 GET /embedding/status

**获取当前 active embedding 向量空间。** 返回每个 embedding kind 的 active pointer 与不可变 model version 元数据。该接口只读，用于前端状态展示、部署 preflight 和外部服务集成；切换 active model 仍必须使用受控运维命令。

**响应:** `APIResponse<ActiveEmbeddingModelStatus[]>`

**响应字段:**

| 字段 | 类型 | 说明 |
|------|------|------|
| `embedding_kind` | string | 向量空间类型：`statement` 或 `solution` |
| `expected_dimensions` | number | active pointer 期望维度 |
| `updated_by` | string | 最近一次更新 active pointer 的操作者 |
| `reason` | string | 最近一次更新原因 |
| `updated_at` | string | active pointer 更新时间 |
| `model_version` | object | active model version 元数据 |
| `model_version.id` | string(UUID) | active model version ID；仅在自动解析出现多个匹配版本时可作为高级消歧 pin 参考，active 本身不决定运行身份 |
| `model_version.provider` | string | provider 身份，不含密钥 |
| `model_version.model_id` | string | 模型 ID |
| `model_version.revision` | string | 模型 revision/tag/commit |
| `model_version.weights_hash` | string | 权重或服务 profile SHA-256 |
| `model_version.dimensions` | number | 向量维度 |
| `model_version.normalization` | string | 归一化/投影策略 |
| `model_version.quantization` | string | 量化策略 |
| `model_version.status` | string | `shadow`、`active` 或 `retired` |

**示例:**

```bash
curl http://localhost:18180/api/v1/embedding/status
```

**响应示例:**

```json
{
  "success": true,
  "data": [
    {
      "embedding_kind": "statement",
      "expected_dimensions": 1536,
      "updated_by": "migration-019",
      "reason": "seed legacy statement active pointer",
      "updated_at": "2026-08-18T08:00:00Z",
      "model_version": {
        "id": "11111111-1111-1111-1111-111111111111",
        "provider": "openai-compatible",
        "model_id": "legacy-openai-compatible-1536",
        "revision": "pre-t02-legacy",
        "weights_hash": "20b8b24a0f7d07f2e63bfb99022b90751e13e811b2f6680e97d07bafdcae876e",
        "dimensions": 1536,
        "normalization": "provider-default",
        "quantization": "float32",
        "instruction_template": "title\\n\\nstatement\\n\\none_line_hint",
        "index_params": {
          "index": "ivfflat",
          "lists": 100,
          "distance": "cosine"
        },
        "status": "active",
        "created_at": "2026-08-18T08:00:00Z",
        "updated_at": "2026-08-18T08:00:00Z"
      }
    }
  ]
}
```

### 4.3 GET /embedding/models

**列出已注册的 embedding 模型版本。** 同时返回 `shadow`、`active` 和 `retired` 版本，供页面刷新后继续回填或启用；不包含 API Key。

**响应:** `APIResponse<EmbeddingModelVersion[]>`

### 4.4 GET /embedding/runtime-settings

**读取当前有效的 embedding 配置身份。** 普通配置输入只有 Base URL、模型和 API Key。响应返回从规范化
Base URL 派生的 credential-free `provider_id`、默认维度 `1536`、自动解析的
`statement_model_version_id`、超时和 `api_key_configured`，不返回明文或密文 Key。身份字段来自 API
启动配置，`source=deployment`；已保存记录只有在 base/model/dimensions 匹配时才提供 Key，不能覆盖身份，
也不能作为 worker/live runtime 证明。

**响应:** `APIResponse<EmbeddingRuntimeSettings>`

### 4.5 POST /embedding/local/test

**测试 OpenAI-compatible embedding endpoint。** 管理页只要求填写 `base_url`、`model`、`api_key`；维度固定
使用默认 `1536`，超时和探测文本由系统给默认值。该接口只发起一次 `/v1/embeddings` 探测，不注册模型、
不写入 active pointer，也不持久化新 `api_key`。当请求省略 Key 且 endpoint 身份与已保存配置一致时，
会使用加密保存的 Key。生产默认只接受本机或私网地址；显式开启 Web 公网 endpoint 后也可测试公网 provider。

**请求体 (JSON):**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `base_url` | string | 是 | 本地或私网 OpenAI-compatible base URL，例如 `http://127.0.0.1:8000/v1` |
| `model` | string | 是 | endpoint 暴露的 embedding 模型名 |
| `api_key` | string | 首次是 | 本次探测使用；不回显。已有同身份加密 Key 时可省略 |
| `dimensions` | number | 否 | API 兼容字段；普通管理页不展示，固定使用默认 `1536` |
| `timeout_sec` | number | 否 | API 高级兼容字段；普通管理页不展示，默认 `30` |
| `sample_text` | string | 否 | API 高级兼容字段；普通管理页使用系统探测文本 |

**响应:** `APIResponse<LocalEmbeddingTestResult>`

### 4.6 POST /embedding/local/deploy

**注册去重模型版本并保存运行凭据。** 后端先测试 endpoint，从规范化 Base URL 派生 `provider_id`，再以
provider/model/默认 `dimensions=1536` 写入或复用 `embedding_model_versions` 的 shadow 版本，同时加密
保存 Key。一个非 retired 匹配版本会被自动唯一解析；管理员不需要手填或复制版本 UUID。同一身份存在
多个候选时，可在部署配置中使用 `expected_statement_model_version_id` 作高级消歧 pin。默认不切 active
pointer；`activate=true` 仅在目标与解析后的启动身份一致时提交。

**请求体 (JSON):**

Web 管理页只提交 `endpoint.base_url`、`endpoint.model`、`endpoint.api_key`，并按按钮自动设置
`activate`/`dry_run`；下列其余字段保留为 API 兼容或高级运维选项。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `endpoint` | object | 是 | 同 `/embedding/local/test` 请求体 |
| `provider` | string | 否 | 兼容字段；非空时必须等于测试结果 `provider_id`，不能覆盖 |
| `model_id` | string | 否 | 兼容字段；非空时必须等于 endpoint `model`，不能覆盖 |
| `revision` | string | 否 | 默认 `local-runtime` |
| `weights_hash` | string | 否 | 64 位 SHA-256；为空时按 endpoint/model/revision/dimensions 生成 |
| `normalization` | string | 否 | 默认 `mrl-1536+l2` |
| `quantization` | string | 否 | 默认 `float32` |
| `instruction_template` | string | 否 | 默认 `title\n\nstatement\n\none_line_hint` |
| `index_params` | object | 否 | 默认 `{"index":"ivfflat","lists":100,"distance":"cosine"}` |
| `embedding_kinds` | string[] | 否 | `statement`、`solution`；默认 `statement` |
| `actor` | string | 否 | 默认 `embedding-web-admin` |
| `reason` | string | 否 | 默认 `local embedding model deployment` |
| `dataset_report_sha256` | string | 否 | active pointer 审计证据 hash；为空时后端生成 |
| `activate` | boolean | 否 | 是否在部署响应内尝试启用；Web 页面提供“部署并启用”按钮 |
| `dry_run` | boolean | 否 | `activate=true` 时是否只预演 |
| `backfill_plan_limit` | number | 否 | 回填计划候选上限，最大 `100` |

**响应:** `APIResponse<LocalEmbeddingDeployResult>`。`runtime_settings_saved=true` 表示 Key 已加密保存；`env`
字段包含 endpoint 身份变量但不包含密钥。普通管理员无需复制 `model_version_id` 或 `env` 才能继续页面流程。

### 4.7 POST /embedding/local/backfill

**按注册模型版本回填去重向量。** 目标注册记录的 provider/model/dimensions 必须与请求 endpoint
配置一致，否则在计划或写入前拒绝。`dry_run=true` 只做身份校验和回填计划，不调用 embedding 服务。
这是 Web 管理页使用的有界同步接口；单次 `limit` 最大 `100`，适合本地运维逐批推进。

**请求体 (JSON):**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `endpoint` | object | 是 | 同 `/embedding/local/test` 请求体；Key 可留空并使用匹配的已保存 Key |
| `model_version_id` | string(UUID) | API 是 | `/embedding/local/deploy` 返回的 model version ID；Web 页面自动使用当前部署/选择的版本，不要求手填 |
| `embedding_kind` | string | 否 | `statement` 或 `solution`，默认 `statement` |
| `limit` | number | 否 | 本批候选上限，最大 `100` |
| `all_stale` | boolean | 否 | 是否包含已有但非当前内容的向量 |
| `dry_run` | boolean | 否 | 只生成计划，不写入向量 |

**响应:** `APIResponse<LocalEmbeddingBackfillResult>`

### 4.8 POST /embedding/local/activate

**预演或提交 active pointer 切换。** Web 页面自动使用当前部署/选择的版本 ID；管理员不需要额外填写。
API 会读取目标注册记录，并要求其 ID、provider、model 和 dimensions 与自动解析后的启动身份完全一致。
正式提交不允许 mismatch 绕过；不一致时返回 `activation_blocked_reason` 且不切 pointer。active pointer
仅是运维/发布治理状态，不参与生成、查询或 Store 写入的版本选择。

**请求体 (JSON):**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model_version_id` | string(UUID) | API 是 | 目标 model version ID；Web 页面自动提供，不要求管理员手填 |
| `embedding_kinds` | string[] | 否 | `statement`、`solution`；默认 `statement` |
| `actor` | string | 否 | 操作者 |
| `reason` | string | 否 | 切换原因 |
| `dataset_report_sha256` | string | 否 | 审计证据 hash |
| `dry_run` | boolean | 否 | true 时只预演事务与 preflight |
| `endpoint_base_url` | string | 否 | 兼容字段；正式启用身份以启动配置和目标注册记录为准 |
| `endpoint_model` | string | 否 | 兼容字段；正式启用身份以启动配置和目标注册记录为准 |
| `endpoint_dimensions` | number | 否 | 兼容字段；正式启用身份以启动配置和目标注册记录为准 |
| `allow_runtime_mismatch` | boolean | 否 | 已弃用兼容字段；不能绕过正式启用校验 |

**响应:** `APIResponse<LocalEmbeddingActivateResult>`

---

## 5. 永久模型配置

### 5.1 GET /settings/llm

**读取 G（生成）、V（验算）和 R（评审）的无凭据有效配置。** G 未保存时返回 `source=unconfigured` 的空身份，不使用部署默认模型；V 没有独立覆盖时跟随 G，R 没有独立覆盖时跟随 V。`override_configured` 区分独立覆盖与继承，`inherited_from` 给出直接父角色。响应只包含 `api_key_configured` 与 `api_key_source`，绝不回传 API Key 明文或密文。

**响应:** `APIResponse<{ statement: LLMProviderSetting; verification: LLMProviderSetting; review: LLMProviderSetting }>`

```json
{
  "success": true,
  "data": {
    "statement": {
      "purpose": "statement",
      "model": "gemini-example",
      "base_url": "https://llm.example.com",
      "provider": "llm-endpoint:https://llm.example.com",
      "protocol": "gemini-native",
      "reasoning_effort": "",
      "api_key_source": "stored",
      "api_key_configured": true,
      "source": "saved",
      "override_configured": true
    },
    "verification": {
      "purpose": "verification",
      "model": "gemini-example",
      "base_url": "https://llm.example.com",
      "provider": "llm-endpoint:https://llm.example.com",
      "protocol": "gemini-native",
      "api_key_source": "stored",
      "api_key_configured": true,
      "source": "inherited",
      "override_configured": false,
      "inherited_from": "statement"
    },
    "review": {
      "purpose": "review",
      "model": "gemini-example",
      "base_url": "https://llm.example.com",
      "provider": "llm-endpoint:https://llm.example.com",
      "protocol": "gemini-native",
      "api_key_source": "stored",
      "api_key_configured": true,
      "source": "inherited",
      "override_configured": false,
      "inherited_from": "verification"
    }
  }
}
```

### 5.2 PUT /settings/llm/:purpose

**永久保存一个角色的独立覆盖。** `purpose` 可以是 `statement`、`verification` 或 `review`。普通页面提交 Base URL、模型名、推理强度与 API Key；服务端规范化端点、派生 credential-free provider 身份和实际协议，并在持久化前做连接测试。旧客户端仍可显式传 `provider`/`protocol`。API Key 经 AES-256-GCM 加密后写入 PostgreSQL；仅当规范化 Base URL 未变化时，已有覆盖的空 `api_key` 才保留当前密钥。改变端点必须提供新 `api_key` 或显式设置 `use_environment_key=true`，后者会切回部署环境中的 `ANTHROPIC_API_KEY`。推理强度留空时不向 provider 发送字段，不使用应用层默认值；OpenAI Responses 使用 `reasoning.effort`，OpenAI Chat 使用 `reasoning_effort`。

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 模型名称 |
| `base_url` | string | 否 | 服务根地址；系统规范化并自动补充接口路径 |
| `provider` | string | 否 | 旧客户端高级覆盖；省略时从规范化端点派生稳定身份 |
| `protocol` | string | 否 | 旧客户端高级覆盖；省略或 `auto` 时由模型和端点自动解析实际协议 |
| `reasoning_effort` | string | 否 | 显式推理强度：`none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`；留空表示不发送该字段。仅 OpenAI Responses/Chat 协议使用 |
| `api_key` | string | 否 | 新密钥；只写，不回显 |
| `use_environment_key` | boolean | 否 | true 时清除已存密文并使用部署环境密钥；不能与 `api_key` 同传 |

```bash
curl -X PUT http://localhost:18180/api/v1/settings/llm/statement \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-example",
    "base_url": "https://llm.example.com",
    "api_key": "sk-..."
  }'
```

**响应:** `APIResponse<LLMProviderSetting>`，其中不含任何 API Key 内容。

### 5.3 DELETE /settings/llm/:purpose

**清除 V 或 R 的独立覆盖并恢复继承。** `purpose` 只能是 `verification` 或 `review`；重复调用是幂等的，不能删除 G。删除 V 不会级联删除 R 的独立覆盖。响应为清除后重新解析的无凭据有效配置，`override_configured=false` 并带 `inherited_from`。
