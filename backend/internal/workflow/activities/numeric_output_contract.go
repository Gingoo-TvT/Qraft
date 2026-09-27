package activities

const numericOutputContract = `
NUMERIC OUTPUT CONTRACT:
- Read the required decimal precision and tie-breaking rule from the statement.
  Fixed decimal formatting is not a proof of decimal rounding correctness.
  For finite decimal inputs and rational arithmetic, parse decimal strings into
  scaled integers (or exact fractions) and round using quotient/remainder;
  do not rely on binary float division followed by setprecision/printf/round.
- Follow the specified rule at exact half-unit ties, including negative values
  when legal. Do not use an arbitrary epsilon to force a preferred answer.
- Independently test exact ties, the nearest legal inputs on both sides,
  carry into the integer part, and trailing zeroes. A brute oracle must obey
  the same numeric contract as the main solution; independence does not permit
  a different rounding rule. Preserve an explicitly stated tolerance/checker.
`
