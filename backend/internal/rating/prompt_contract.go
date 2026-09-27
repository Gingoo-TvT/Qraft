package rating

// This is an output contract for the existing analysis, not a new scoring rule.
// Keep every field aligned with Analysis/KC/Path/AnchorComparison; execution and
// human evidence are server-authored and deliberately excluded from the model.
const analysisOutputContract = `

输出契约（必须同时满足以下类型、枚举和引用约束）：
1. 只返回一个完整、裸 JSON 对象；不使用 Markdown 围栏、额外说明或额外字段。所有下列字段都要提供，空列表用 []，不要用 null、空字符串、对象或逗号分隔文本代替数组。模板中的说明文字须替换为本题分析，不可当成事实或照抄。
2. 顶层恰含 summary:string、kcs:KC[]、paths:Path[]、comparisons:Comparison[]、disagreements:string[]、limitations:string[]。summary 为单个字符串；分歧和局限各列表元素为完整文字，disagreements 最多 20 项。
3. 每个 KC 恰含 id:string、name:string、definition:string、conditions:string、related_tags:string[]、status:string。id 非空且在 kcs 中唯一；definition 非空；conditions 是一段文字，不是数组或对象；status 只能是 "candidate"。
4. 每个 Path 恰含 id:string、name:string、kind:string、summary:string、proof:string、complexity:string、language:string、code:string、kc_ids:string[]、bypasses:string[]、constraint_scope:string、semantic_review:string、semantic_review_reason:string、counterexamples:Counterexample[]。id、name 非空，路径 id 唯一。
5. kind 只能是 "intended"、"alternative"、"misleading"。blind_a、blind_b 是两路盲解路径的 id，不是 kind；禁止 kind="blind" 或 kind="blind_a"/"blind_b"。路径是否与已知原解一致由证据决定，不能由角色名猜测；只因来自盲解不可宣称是作者预设路径。
6. constraint_scope 只能是 "full"、"restricted"、"uncertain"。full 必须有非空 proof 和 complexity；证据不足时如实用 uncertain。semantic_review 只能是 "candidate"、"equivalent_dependency"、"needs_review"。language 为 "cpp"；两路盲解 code 原样保留，不改写、不替换、不把代码包成对象。
7. kc_ids 和 bypasses 每个元素都是本次 kcs 中已有的 id；不要填名称、角色名、对象或新造的引用。没有依赖/绕过证据时用 []。已列明的 blind_a、blind_b 两条路径都必须保留；最多 16 个 KC、5 条路径。
8. 每条路径 counterexamples 最多 3 项；每项恰含 input:string、legality_argument:string、failure_reason:string。input 最多 16 KiB，必须论证合法性；无反例时用 []。每条 code 最多 128 KiB。反例是待执行候选，不得声明已实测。
9. 每个 Comparison 恰含 anchor_id:string、anchor_rating:integer、relation:string、reason:string。anchor_id 必须逐字引用 reviewed_anchors 中实际给定的 UUID，anchor_rating 必须是该锚点原有整数。relation 只能是 "easier"、"similar"、"harder"、"incomparable"，reason 非空。最多 6 项；reviewed_anchors 为空时 comparisons 必须为 []，禁止编造或使用格式模板中的锚点。
10. 不输出 evidence、validation、human_observations、estimate、rating、rule_version 等服务端字段。不要把模型推断写成测试通过、正式评分或人类观察事实。格式自检只修正类型/引用，不得为满足格式而编造算法、证明、反例或共识。

以下模板演示完整字段形状；两路盲解的真实内容必须从输入对应结果复制或分析，额外路径按同一形状添加，比较项按第 9 条填入实际锚点：
`

const analysisOutputTemplate = `{
  "summary": "本题路径、关键观察与未解决争议的简短分析",
  "kcs": [{"id":"kc1","name":"知识组件名称","definition":"可检查的关键观察或推理步骤","conditions":"该知识组件适用条件的文字说明","related_tags":[],"status":"candidate"}],
  "paths": [
    {"id":"blind_a","name":"第一路盲解路径名称","kind":"alternative","summary":"路径摘要","proof":"完整约束下可检查的论证","complexity":"时间与空间复杂度","language":"cpp","code":"原样保留输入 blind_a 的源码","kc_ids":["kc1"],"bypasses":[],"constraint_scope":"full","semantic_review":"needs_review","semantic_review_reason":"待人工复核的具体原因","counterexamples":[]},
    {"id":"blind_b","name":"第二路盲解路径名称","kind":"alternative","summary":"路径摘要","proof":"完整约束下可检查的论证","complexity":"时间与空间复杂度","language":"cpp","code":"原样保留输入 blind_b 的源码","kc_ids":["kc1"],"bypasses":[],"constraint_scope":"full","semantic_review":"needs_review","semantic_review_reason":"待人工复核的具体原因","counterexamples":[]}
  ],
  "comparisons": [],
  "disagreements": [],
  "limitations": []
}`
