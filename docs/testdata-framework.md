# 可复用数据生成框架

Qraft 的服务端和命令工具共用这套框架。新题由模型设计测试计划和少量专用 C++，通用造数由固定库完成；保存方案后可以直接调用工具重新生成和验证，无需模型参与。生成器、校验器和解法始终在现有独立沙箱执行。

## 接入现有出题流程

实际入口为 `GenerateTestDataActivity`，S3 的语义规格、测试意图和错误模式继续传入该入口。注册提示词和活动提示词共享同一份框架 API 说明。

模型输出：
- `test_cases`：完整的测试点计划，空 `input` 表示由生成器执行产生；内联样例仍占原始下标。
- `generator_recipe`：`{"version":"algoforge.testdata.v1","code":"..."}`。
- `code` 只需定义 `void generate(long long test_index, long long group_id, af::Random& rng, std::ostream& out)`。服务端嵌入库和入口，无需模型重复生成随机数、树图、格式输出等公共代码。
- 每个测试点可声明 `output_limit_bytes`，默认 1 MiB，最大 8 MiB。8 MiB 为传输预算，不代表绕过“只有一个最大规模点、其余不超过 1 MiB”的现有题目生成策略。
- 原有自定义用例、10–20 自适应数量或旧固定数量、分组、种子、测试输入资产和后续质量验证继续生效。

旧的 `generator_code` 自包含程序仍可使用，读取 `test_index group_id seed`。两种表示不得同时提供；未知方案版本或非法输出预算会报错。没有声明预算的旧生成器维持每批一个测试点。

生成活动已有的模型响应资产保存原始方案；完整执行源代码（含固定库）参与 generator SHA 和种子计算。工具的返回值同时提供可保存的 recipe、完整 source、source SHA、library SHA，以及实际沙箱的工具链/镜像身份。源代码哈希标识生成器内容。

## 固定库提供什么

库位于 `backend/internal/testdatagen/generator.hpp`，由本项目实现，使用 C++20 与标准库，不依赖运行时安装 CaseCraft 或 testlib。

| API | 语义 |
| --- | --- |
| `rng.integer(lo,hi)` | 闭区间 64 位有符号整数，包含极值范围 |
| `rng.array(n,lo,hi)` / `distinct` | 普通或不重复整数数组；不重复采样的内存随输出规模增长 |
| `rng.permutation(n)` / `shuffle(v)` | 1..n 排列 / 原地打乱 |
| `rng.text(n,alphabet)` / `palindrome` | 指定字节字符集的字符串 / 回文 |
| `rng.tree(n,shape)` | random、chain、star、binary；根为 1，binary 对根同样限制最多两个孩子 |
| `rng.degree_tree(n,max_degree)` | 最大度数受限的连通树，使用数组候选集合，线性构造 |
| `rng.graph(n,m,connected)` | 无自环、无重边的无向图，可要求连通 |
| `rng.dag(n,m)` | 无重边的有向无环图 |
| `rng.relabel(n,edges)` | 随机重编号及边序打乱；根的编号会改变 |
| `af::line(out,v)` / `print_edges` | 序列或边表的固定格式输出 |

辅助函数的 n/m/数组长度上限为 2,000,000。整数数组、树构造等为线性或期望线性开销；图采用无放回采样，避免稠密图反复撞边，无连通要求时约 O(m log n)，要求连通时约 O(n log n + m log² n)。随机树、连通图不承诺在所有合法结构中均匀分布。

有状态操作、权值关系、总和约束、特殊几何条件和针对错误算法的构造，仍由题目专用代码负责。通用库不能代替题意理解。所有模型代码均在沙箱执行；worker 不本地编译运行生成器。

## AI / 脚本调用

命令是一次请求一次退出的 JSON CLI，不新增常驻服务。先在调用环境中设置能访问已有沙箱的 `SANDBOX_URL`；Docker 服务名只适用于同一网络内。

```sh
go -C backend run ./cmd/testdata-tool -describe
go -C backend run ./cmd/testdata-tool < backend/internal/testdatagen/examples/array-sum.json
```

标准输入为一个 JSON 对象，标准输出为 JSON 结果；请求/编译/服务错误写入标准错误并以非零退出。生成结果中的输入可保存为文件，仍需按正常质量与导出流程产出可用题包。

正式 API / worker 镜像也包含 `/app/testdata-tool`。可沿用当前 Compose 项目参数执行 `docker compose exec -T worker /app/testdata-tool -describe`，或移除 `-describe` 并重定向 JSON 请求；使用 worker 的既有 sandbox 配置。

三个工具：
1. `build_generator`：`{"tool":"build_generator","recipe":{...}}`。在沙箱中编译并返回诊断与身份。直接生成时可省略这个预检查。
2. `generate_cases`：`{"tool":"generate_cases","recipe":{...},"cases":[{"index":0,"group_id":0,"seed":42,"purpose":"最小边界"}]}`。省略 seed 时由源代码、原始下标和组别派生；显式 seed 支持复现或扩充数据。一次最多 32 点，最终输入总计不超过 32 MiB。
3. `verify_cases`：`{"tool":"verify_cases","verify":{"inputs":[...],"validator":"C++源码","reference":"C++源码","brute":"可选C++源码","brute_indices":[0,1],"wrong_programs":[{"id":"漏掉末项","source":"C++源码"}],"comparison":"exact"}}`。

校验器从标准输入读一份完整输入，合法时退出 0，非法时非零退出。`brute_indices` 为输入数组中的零基位置；省略时验证全部点，可只指定小规模点避免暴力在大数据上超时。对拍比较支持 exact 和 tokens；多解题、自定义 checker 继续使用原有质量流程，工具不擅自近似比较。

`verify_cases` 返回：
- `passed`：本次输入/参考解/所请求对拍检查是否通过。
- `differential_checked` 和 `differential_case_count`：是否实际对拍以及覆盖多少点；未提供暴力解不能声称已经对拍。
- `findings`：非法输入、运行失败、对拍差异的测试位置与限长诊断；输出不一致明确标为 WA，并附 expected / actual。
- `killed_wrong_ids` / `surviving_wrong_ids`：错误解法检出情况。存活意味着当前数据还未检出该程序；不自动证明程序正确。
- `input_sha256` / `audits`：实际输入和沙箱运行身份。

这里的 passed 是工具检查结果，不能取代题目入库、质量门禁或外部 OJ 验收。

## 时间效率和可复现性

默认 1 MiB 输出预算时，每批最多 8 点；单批最坏原始输出预算不超过 8 MiB，为 JSON 转义及元数据预留空间。相邻点预算相同时合批；单点 8 MiB 时单独执行。沙箱在每个请求中编译一次，目前不提供跨请求的二进制缓存。

同一固定库、题目代码、参数和种子可以重复执行；实际工具链/镜像身份也要保存，专用 C++ 仍须避免未定义行为、时间和外部状态。改变库或专用代码会改变源代码身份和默认派生种子。

现有多 Agent 分工继续适用：设计角色根据语义规格和错误模式制定计划，独立 oracle / 审核角色检查题意与覆盖，执行工具提供真实反例。进一步补数据时优先改变结构和参数，发现缺陷后再修订方案，避免重复让模型输出通用代码。

## 验证

```sh
go -C backend test ./internal/testdatagen ./cmd/testdata-tool ./internal/workflow/activities ./internal/llm/prompts
go -C backend build ./...
```

有 g++ 的环境会实际编译 C++ 性质测试并启用 UBSan，检查 100 个种子、64 位极值、稠密图、二叉树根节点、20 万节点构造以及非法参数。没有 g++ 时相关 C++ 测试会显示 skip，应在具备编译器的环境补跑。

在自己的隔离环境中验证生成、同种子重放、输入合法性和参考解对拍，并保留工具返回的诊断。工具验证通过只说明本次请求通过了所执行的检查。
