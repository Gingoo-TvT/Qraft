package activities

// Compact workflow guidance adapted from:
// https://github.com/robinbg/algorithm-contest-Skills
//
// Keep these fragments concise. They are injected into every generation/review
// call, so large verbatim skill text would increase truncation risk.
const contestProblemsetterSkillGuidance = `

Algorithm Contest Problemsetter Skill:
- Start from the algorithm core before writing the story: key observation, target complexity, why it is not a bare template, and the exact step contestants must discover.
- Reverse-design common wrong solutions: brute force, one-optimization-short variants, false greedy rules, wrong DP state/transition, missed duplicates/zero/negative values, graph corner cases, overflow, and stale state across test cases.
- Treat constraints as part of the statement language: maximum sizes must fit the intended solution, reject too-easy constraints that allow inferior complexity, and use subtasks only when they form a meaningful learning path.
- Design tests as part of the problem, not afterthoughts: samples, basic cases, boundaries, randomized stress, structured extremes, and targeted hack cases for each plausible wrong solution.
- Make the statement zero-ambiguity: define every variable, index base, interval boundary, duplicate/negative/self-loop/multiedge policy, no-solution rule, and multi-answer rule when applicable.
- In difficulty justification, explicitly name the intended solution path, natural brute force, likely wrong solutions, data risks, and the tester-facing attack points.`

const contestTesterSkillGuidance = `

Algorithm Contest Tester Skill:
- Review from an attacker mindset. Do not trust the statement, proof, model solution, brute solution, generator, checker, or data until they survive explicit attacks.
- Attack statement ambiguity first: undefined variables, missing ranges, 0/1-indexing, closed/open intervals, empty input, duplicates, negative values, self-loops, multiedges, disconnected graphs, impossible cases, and multiple valid outputs.
- Attack the proof chain: greedy exchange arguments, DP state completeness, graph/model transformations, math edge cases, all-subtask complexity, recursion depth, memory, and integer overflow.
- Prefer small counterexamples before large stress. Use min/max size, all equal, monotone, alternating, repeated values, primes/composites, chains/stars/cliques/components, periodic strings, and multi-test state pollution.
- Judge data strength by whether plausible wrong solutions fail, not by whether the official solution passes. Require targeted hack groups when random data is unlikely to expose a bug.
- Mark blocking/high-risk issues when a correct solution could be rejected, a wrong solution could pass, the checker/validator is too narrow or too wide, or the data does not distinguish intended complexity.`

const contestTestDataSkillGuidance = `

Contest Test Data Skill:
- Every hidden group must have a purpose: baseline correctness, boundary behavior, maximum-size stress, structured adversarial shape, randomized coverage, or a targeted wrong-solution hack.
- For each group, state what it is meant to catch. Avoid relying only on uniform random data.
- Include deterministic seeds and explicit generator branches for min case, max case, degenerate structure, dense/sparse variants, duplicated/extreme values, and overflow-prone values.
- If the problem has multiple valid outputs, note checker requirements. If it has strict input constraints, note validator requirements.`

const contestSolutionSkillGuidance = `

Contest Solution Skill:
- Explain the key observation before code. The observation must be strong enough to justify the intended complexity.
- Include boundary reasoning for smallest input, largest input, duplicate values, empty/no-solution cases, overflow ranges, and graph/string/data-structure edge cases relevant to the statement.
- For brute force, intentionally choose the simplest independently correct method for small constraints so it is useful for stress testing and counterexample generation.`
