package activities

// Fixed-decimal formatting alone does not make precision handling a learning
// objective. Keep authoring, data generation, solving and review in agreement.
const numericScopeGuidance = `
NUMERIC DIFFICULTY ALIGNMENT:
- Match numerical demands to the intended knowledge points and learner level.
  Basic arithmetic, real-number input/output, and printing a fixed number of
  decimal places do not by themselves make exact decimal arithmetic or IEEE-754
  rounding behavior part of the problem. A higher rating alone does not either.
- For introductory arithmetic and formatting tasks, ordinary double arithmetic
  with the requested standard formatting should be sufficient on generated
  tests. Do not create a hidden requirement for decimal-string parsing,
  multiprecision, binary-representation analysis, or arbitrary epsilon tricks.
- Precision traps are appropriate only when numerical stability, exact decimal
  arithmetic, or exact tie-breaking is explicitly an intended task. In that
  case, the statement must make the rule clear and the difficulty must account
  for the required technique. Preserve such requirements in imported problems.
- Preserve the statement's output format, explicit tolerance/checker and source
  examples. Never silently change them to resolve a disagreement. Imported
  statements determine their own intended skills; provisional authoring labels
  and ratings do not authorize adding unrelated precision challenges.
`

const numericOutputContract = numericScopeGuidance + `
NUMERIC SOLUTION GUIDANCE:
- Prefer the simplest technique justified by the problem's intended skills.
  For elementary real arithmetic, use standard floating-point input, arithmetic
  and formatting rather than universally replacing them with scaled integers.
- Use exact fractions, scaled integers, or numerical-stability techniques when
  the actual task calls for them. An independent oracle may use exact arithmetic
  for verification without making that technique a contestant prerequisite.
- Main and independent solutions must obey the same stated output contract.
  Do not hardcode a failing case, add an arbitrary epsilon, or invent tolerance
  to make them agree. A precision-only counterexample outside the intended
  skills is a data/statement design issue, not a reason to teach a harder trick.
`

const numericTestDataContract = numericScopeGuidance + `
NUMERIC TEST-DATA GUIDANCE:
- In elementary fixed-decimal tasks, construct well-conditioned inputs with
  answers safely away from half-unit boundaries at the printed precision.
  Check the concrete samples and every seeded generator branch, including
  random branches: ordinary double evaluation and the intended mathematical
  rounding must agree. Use bounded construction instead of unbounded retries.
- Still cover decimal inputs, non-integer results, normal rounding up/down,
  carry, trailing zeroes, relevant size limits, and ordinary wrong solutions.
  Do not replace the suite with only integers, exact divisions or easy samples.
- Only for an explicit precision/tie-breaking task, include exact ties and
  nearby legal inputs, cancellation or other relevant numerical adversaries.
  Put small representatives in the selected differential subset. Otherwise
  choose another in-domain input instead of testing representation artifacts.
- Source examples and user-supplied cases are authoritative, not disposable
  random candidates. If they conflict with the intended skills or output rule,
  surface the conflict for review; never silently rewrite or discard them.
`

const numericReviewGuidance = numericScopeGuidance + `
NUMERIC REVIEW GUIDANCE:
- Check educational fairness as well as data strength: a normal implementation
  of the intended skills must pass. For basic real arithmetic, flag cases that
  distinguish only floating-point representation or rounding artifacts and
  request better-conditioned data. Producing WA alone does not make a good hack.
- Preserve explicit precision tasks. Report ambiguous numeric contracts instead
  of changing expected answers, loosening the checker, or forcing a harder trick.
`
