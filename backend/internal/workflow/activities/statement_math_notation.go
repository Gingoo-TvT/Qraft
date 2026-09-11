package activities

import (
	"fmt"
	"regexp"
	"strings"
)

// statementMathOperatorPatternV1 keeps the small set of operators that are
// commonly emitted as Unicode in an otherwise LaTeX expression.  Converting
// them here gives the public renderer one representation to handle.
var statementMathOperatorPatternV1 = strings.NewReplacer(
	"≤", `\le `,
	"≥", `\ge `,
	"≠", `\ne `,
	"×", `\times `,
	"·", `\cdot `,
)

var statementMathOperatorSpacingPatternV1 = regexp.MustCompile(`\\(le|ge|ne|times|cdot)[ \t]*`)

// NormalizeStatementMathNotationV1 applies presentation-only formatting to
// generated Markdown.  It never changes a numeric value: large integers that
// are exact multiples of a power of ten are written as scientific notation,
// for example 200000 -> 2 \\times 10^5 and 100000000000000 -> 10^{14}.
//
// The scanner deliberately skips fenced and inline code.  It also accepts the
// standard \(...\) and \[...\] delimiters and canonicalizes them to the
// dollar delimiters understood by every AlgoForge Markdown consumer.
func NormalizeStatementMathNotationV1(statement string) string {
	statement = strings.ReplaceAll(statement, "\r\n", "\n")
	statement = strings.ReplaceAll(statement, "\r", "\n")
	if strings.TrimSpace(statement) == "" {
		return statement
	}

	var result strings.Builder
	result.Grow(len(statement) + 32)
	inFence := false
	fenceChar := byte(0)
	inCode := false
	mathDelimiter := ""
	var mathBody strings.Builder

	lines := strings.SplitAfter(statement, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if mathDelimiter == "" && !inCode && isStatementFenceLineV1(trimmed) {
			result.WriteString(line)
			if !inFence {
				inFence = true
				fenceChar = trimmed[0]
			} else if trimmed[0] == fenceChar {
				inFence = false
				fenceChar = 0
			}
			continue
		}
		if inFence {
			result.WriteString(line)
			continue
		}

		for index := 0; index < len(line); {
			if mathDelimiter != "" {
				if hasStatementMathCloseV1(line, index, mathDelimiter) {
					result.WriteString(formatStatementMathBodyV1(mathBody.String()))
					if mathDelimiter == "\\)" {
						result.WriteByte('$')
					} else if mathDelimiter == "\\]" {
						result.WriteString("$$")
					} else {
						result.WriteString(mathDelimiter)
					}
					index += len(mathDelimiter)
					mathDelimiter = ""
					mathBody.Reset()
					continue
				}
				mathBody.WriteByte(line[index])
				index++
				continue
			}

			if line[index] == '`' {
				inCode = !inCode
				result.WriteByte(line[index])
				index++
				continue
			}
			if inCode {
				result.WriteByte(line[index])
				index++
				continue
			}
			if line[index] == '\\' && index+1 < len(line) {
				switch line[index+1] {
				case '(':
					result.WriteByte('$')
					mathDelimiter = `\)`
					mathBody.Reset()
					index += 2
					continue
				case '[':
					result.WriteString("$$")
					mathDelimiter = `\]`
					mathBody.Reset()
					index += 2
					continue
				}
			}
			if line[index] == '$' && line[index] != '\\' {
				if index+1 < len(line) && line[index+1] == '$' {
					result.WriteString("$$")
					mathDelimiter = "$$"
					mathBody.Reset()
					index += 2
				} else {
					result.WriteByte('$')
					mathDelimiter = "$"
					mathBody.Reset()
					index++
				}
				continue
			}
			result.WriteByte(line[index])
			index++
		}
		// A code span cannot safely carry over a Markdown line break.  Resetting
		// this state avoids treating the next paragraph as source code after a
		// malformed single-backtick span.
		if strings.HasSuffix(line, "\n") {
			inCode = false
		}
	}

	// If a model returned an unclosed delimiter, preserve the original body
	// rather than silently dropping it.  The quality gate will report it.
	if mathDelimiter != "" {
		result.WriteString(formatStatementMathBodyV1(mathBody.String()))
	}
	return strings.TrimSpace(result.String())
}

func isStatementFenceLineV1(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

func hasStatementMathCloseV1(line string, index int, delimiter string) bool {
	if index+len(delimiter) > len(line) || line[index:index+len(delimiter)] != delimiter {
		return false
	}
	if index > 0 && line[index-1] == '\\' {
		return false
	}
	return true
}

func formatStatementMathBodyV1(body string) string {
	body = statementMathOperatorPatternV1.Replace(body)
	body = statementMathOperatorSpacingPatternV1.ReplaceAllString(body, `\$1 `)
	var result strings.Builder
	result.Grow(len(body) + 16)
	for index := 0; index < len(body); {
		if body[index] < '0' || body[index] > '9' {
			result.WriteByte(body[index])
			index++
			continue
		}
		start := index
		for index < len(body) && body[index] >= '0' && body[index] <= '9' {
			index++
		}
		value := body[start:index]
		if statementIntegerCanUseScientificNotationV1(body, start, index) {
			result.WriteString(scientificStatementIntegerV1(value))
		} else {
			result.WriteString(value)
		}
	}
	return result.String()
}

func statementIntegerCanUseScientificNotationV1(body string, start, end int) bool {
	if end-start < 4 || body[start] == '0' {
		return false
	}
	if start > 0 && body[start-1] == '.' || end < len(body) && body[end] == '.' {
		return false
	}
	// A word immediately adjacent to the number means this is an identifier
	// or a command argument, not a standalone bound. Whitespace separates a
	// normal operator/variable from the bound and is therefore intentional.
	if start > 0 && isStatementMathWordByteV1(body[start-1]) {
		return false
	}
	if start > 0 && body[start-1] == '^' {
		return false
	}
	if start > 0 && body[start-1] == '{' {
		beforeBrace := start - 2
		for beforeBrace >= 0 && (body[beforeBrace] == ' ' || body[beforeBrace] == '\t' || body[beforeBrace] == 10) {
			beforeBrace--
		}
		if beforeBrace >= 0 && body[beforeBrace] == '^' {
			return false
		}
	}
	if end < len(body) && isStatementMathWordByteV1(body[end]) {
		return false
	}
	trimmed := strings.TrimRight(body[start:end], "0")
	return len(trimmed) < end-start
}

func isStatementMathWordByteV1(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value == '_'
}

func scientificStatementIntegerV1(value string) string {
	if len(value) < 4 || value[0] == '0' {
		return value
	}
	trimmed := strings.TrimRight(value, "0")
	zeros := len(value) - len(trimmed)
	if zeros < 3 {
		return value
	}
	exponent := fmt.Sprintf("10^%d", zeros)
	if zeros >= 10 {
		exponent = fmt.Sprintf("10^{%d}", zeros)
	}
	if trimmed == "1" {
		return exponent
	}
	return trimmed + ` \times ` + exponent
}
