package activities

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	statementInputHeadingPattern      = regexp.MustCompile(`(?mi)^#{1,6}\s*(输入格式|输入说明|Input(?:\s+Format)?)\s*$`)
	statementOutputHeadingPattern     = regexp.MustCompile(`(?mi)^#{1,6}\s*(输出格式|输出说明|Output(?:\s+Format)?)\s*$`)
	statementConstraintHeadingPattern = regexp.MustCompile(`(?mi)^#{1,6}\s*(约束|数据范围|限制|Constraints?)\s*$`)
	chineseIntegerFieldClaimPattern   = regexp.MustCompile(`包含\s*([一二两三四五六七八九十]|\d+)\s*个整数\s+([^，。；;\n]+)`)
	englishIntegerFieldClaimPattern   = regexp.MustCompile(`(?i)contains?\s+(one|two|three|four|five|six|seven|eight|nine|ten|\d+)\s+integers?\s+([^.;:\n]+)`)
	statementSectionLabelPattern      = regexp.MustCompile(`(?i)^(输入格式|输入说明|输入|输出格式|输出说明|输出|约束|数据范围|限制|input(?:\s+format)?|output(?:\s+format)?|constraints?)\s*[:：]?\s*(.*)$`)
)

const maxStatementMarkdownDiagnosticsV1 = 12

// StatementMarkdownDiagnosticV1 is a bounded, machine-readable reason why a
// generated statement cannot advance. The workflow sends these diagnostics,
// together with the exact candidate, to the targeted statement repair
// activity instead of blindly sampling a new problem.
type StatementMarkdownDiagnosticV1 struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
}

var simpleBareTeXCommands = map[string]string{
	"cdot":  "·",
	"dots":  "…",
	"ge":    "≥",
	"geq":   "≥",
	"ldots": "…",
	"le":    "≤",
	"leq":   "≤",
	"ne":    "≠",
	"neq":   "≠",
	"pm":    "±",
	"times": "×",
}

var statementTeXCommandsStartingWithN = map[string]bool{
	"nabla": true,
	"ne":    true,
	"neq":   true,
	"ni":    true,
	"not":   true,
	"notin": true,
	"nu":    true,
}

// statementMathCommandsInCode is deliberately a conservative allow-list. A
// backslash is perfectly normal in a code sample (for example, "\\n" or a
// Windows path), while these commands are unambiguously mathematical TeX.
// Keeping the list explicit avoids rejecting ordinary programming examples.
var statementMathCommandsInCode = map[string]bool{
	"alpha": true, "approx": true, "beta": true, "binom": true,
	"cdot": true, "choose": true, "circ": true, "cong": true,
	"cos": true, "cup": true, "ddots": true, "delta": true,
	"dots": true, "emptyset": true, "equiv": true, "epsilon": true,
	"exists": true, "forall": true, "frac": true, "gamma": true,
	"ge": true, "geq": true, "geqq": true, "gg": true, "in": true,
	"infty": true, "lambda": true, "ldots": true, "le": true,
	"leq": true, "leqq": true, "lim": true, "log": true, "mathbb": true,
	"mathbf": true, "mathrm": true, "mid": true, "min": true, "max": true,
	"mu": true, "ne": true, "neq": true, "nabla": true, "notin": true,
	"nu": true, "omega": true, "oplus": true, "otimes": true,
	"partial": true, "phi": true, "pi": true, "pm": true, "prod": true,
	"psi": true, "quad": true, "qquad": true, "rho": true, "right": true,
	"sigma": true, "sim": true, "sin": true, "sqrt": true, "sum": true,
	"tan": true, "tau": true, "text": true, "theta": true, "times": true,
	"to": true, "triangle": true, "underbrace": true, "vdots": true,
	"xi": true, "zeta": true,
}

// normalizeGeneratedStatementMarkdown repairs transport-level formatting
// defects without changing problem semantics. In particular, some models
// double-escape JSON newlines and return the two characters "\\n" in the
// decoded Markdown. Simple TeX operators outside math delimiters are rendered
// as their Unicode equivalents; more complex bare TeX is rejected by the
// quality gate below.
func normalizeGeneratedStatementMarkdown(statement string) string {
	statement = strings.ReplaceAll(statement, "\r\n", "\n")
	statement = strings.ReplaceAll(statement, "\r", "\n")
	statement = decodeLiteralStatementNewlines(statement)
	statement = NormalizeStatementMathNotationV1(statement)
	statement = normalizeSimpleBareTeX(statement)
	statement = normalizeStatementSectionHeadings(statement)

	lines := strings.Split(statement, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func normalizeStatementSectionHeadings(statement string) string {
	lines := strings.Split(statement, "\n")
	normalized := make([]string, 0, len(lines)+6)
	inFence := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			normalized = append(normalized, line)
			continue
		}
		if inFence || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
			normalized = append(normalized, line)
			continue
		}

		match := statementSectionLabelPattern.FindStringSubmatch(trimmed)
		if len(match) != 3 {
			normalized = append(normalized, line)
			continue
		}
		label, ok := canonicalStatementSectionHeading(match[1])
		if !ok {
			normalized = append(normalized, line)
			continue
		}
		normalized = append(normalized, "## "+label)
		if remainder := strings.TrimSpace(match[2]); remainder != "" {
			normalized = append(normalized, "", remainder)
		}
	}
	return strings.Join(normalized, "\n")
}

func canonicalStatementSectionHeading(label string) (string, bool) {
	switch strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(label)), " ")) {
	case "输入", "输入格式", "输入说明":
		return "输入格式", true
	case "输出", "输出格式", "输出说明":
		return "输出格式", true
	case "约束", "数据范围", "限制":
		return "约束", true
	case "input", "input format":
		return "Input Format", true
	case "output", "output format":
		return "Output Format", true
	case "constraint", "constraints":
		return "Constraints", true
	default:
		return "", false
	}
}

func decodeLiteralStatementNewlines(statement string) string {
	var builder strings.Builder
	builder.Grow(len(statement))

	for index := 0; index < len(statement); {
		if statement[index] == '\\' && index+1 < len(statement) && statement[index+1] == 'n' &&
			literalNIsLayoutEscape(statement, index) {
			builder.WriteByte('\n')
			index += 2
			continue
		}
		if statement[index] == '\\' && index+3 < len(statement) && statement[index+1] == 'r' &&
			statement[index+2] == '\\' && statement[index+3] == 'n' {
			builder.WriteByte('\n')
			index += 4
			continue
		}
		builder.WriteByte(statement[index])
		index++
	}
	return builder.String()
}

func literalNIsLayoutEscape(statement string, slashIndex int) bool {
	afterN := slashIndex + 2
	if afterN >= len(statement) || !isASCIIAlpha(statement[afterN]) {
		return true
	}
	commandEnd := afterN
	for commandEnd < len(statement) && isASCIIAlpha(statement[commandEnd]) {
		commandEnd++
	}
	return !statementTeXCommandsStartingWithN[statement[slashIndex+1:commandEnd]]
}

func normalizeSimpleBareTeX(statement string) string {
	lines := strings.Split(statement, "\n")
	inFence := false
	mathDelimiter := byte(0)

	for lineIndex, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		var builder strings.Builder
		builder.Grow(len(line))
		inCode := false
		for index := 0; index < len(line); {
			if line[index] == '`' {
				inCode = !inCode
				builder.WriteByte(line[index])
				index++
				continue
			}
			if !inCode && line[index] == '$' {
				delimiter := byte(1)
				if index+1 < len(line) && line[index+1] == '$' {
					delimiter = 2
				}
				if mathDelimiter == 0 {
					mathDelimiter = delimiter
				} else if mathDelimiter == delimiter {
					mathDelimiter = 0
				}
				builder.WriteString(line[index : index+int(delimiter)])
				index += int(delimiter)
				continue
			}
			if !inCode && mathDelimiter == 0 && line[index] == '\\' {
				if index+1 < len(line) && line[index+1] == '_' {
					builder.WriteByte('_')
					index += 2
					continue
				}
				commandEnd := index + 1
				for commandEnd < len(line) && isASCIIAlpha(line[commandEnd]) {
					commandEnd++
				}
				if replacement, ok := simpleBareTeXCommands[line[index+1:commandEnd]]; ok {
					builder.WriteString(replacement)
					index = commandEnd
					continue
				}
			}
			builder.WriteByte(line[index])
			index++
		}
		lines[lineIndex] = builder.String()
	}
	return strings.Join(lines, "\n")
}

func validateGeneratedStatementMarkdown(statement string, expectStructuredSamples bool) error {
	diagnostics := diagnoseGeneratedStatementMarkdownV1(statement, expectStructuredSamples, false)
	if len(diagnostics) == 0 {
		return nil
	}
	return fmt.Errorf("%s", diagnostics[0].Message)
}

// ValidateGeneratedStatementMarkdownStrictV1 applies the current, stricter
// public-statement contract.  The original V1 validator is intentionally kept
// unchanged for replaying older workflow histories; new histories opt into
// this validator through a workflow version marker.
func ValidateGeneratedStatementMarkdownStrictV1(statement string, expectStructuredSamples bool) error {
	diagnostics := diagnoseGeneratedStatementMarkdownStrictV1(statement, expectStructuredSamples, false)
	if len(diagnostics) == 0 {
		return nil
	}
	return fmt.Errorf("%s", diagnostics[0].Message)
}

// DiagnoseGeneratedStatementMarkdownV1 exposes the deterministic statement
// quality gate to the workflow.  Keeping the diagnostic pass pure and bounded
// lets the workflow re-check the exact candidate returned by a cleaning or
// repair activity immediately before any test-data activity is scheduled.
func DiagnoseGeneratedStatementMarkdownV1(
	statement string,
	expectStructuredSamples bool,
) []StatementMarkdownDiagnosticV1 {
	return diagnoseGeneratedStatementMarkdownV1(statement, expectStructuredSamples, true)
}

// DiagnoseGeneratedStatementMarkdownStrictV1 is the bounded diagnostic pass
// used by newly started generation workflows.  In addition to the original
// transport/section/field checks it rejects duplicate canonical headings and
// TeX commands hidden inside inline code spans.  Those two defects are easy to
// miss in a rendered page but make the public statement misleading.
func DiagnoseGeneratedStatementMarkdownStrictV1(
	statement string,
	expectStructuredSamples bool,
) []StatementMarkdownDiagnosticV1 {
	return diagnoseGeneratedStatementMarkdownStrictV1(statement, expectStructuredSamples, true)
}

func diagnoseGeneratedStatementMarkdownStrictV1(
	statement string,
	expectStructuredSamples bool,
	includeReasoningTrace bool,
) []StatementMarkdownDiagnosticV1 {
	// Put the new, actionable defects first so they cannot be crowded out by a
	// long list of legacy diagnostics before the bounded repair context is sent
	// to the model.
	diagnostics := make([]StatementMarkdownDiagnosticV1, 0, 10)
	appendDiagnostic := func(code, message string, line int) {
		if len(diagnostics) >= maxStatementMarkdownDiagnosticsV1 {
			return
		}
		for _, existing := range diagnostics {
			if existing.Code == code && existing.Message == message && existing.Line == line {
				return
			}
		}
		diagnostics = append(diagnostics, StatementMarkdownDiagnosticV1{
			Code:    code,
			Message: message,
			Line:    line,
		})
	}

	if heading, line := firstDuplicateStatementHeading(statement); heading != "" {
		appendDiagnostic(
			"duplicate_section_heading",
			fmt.Sprintf("line %d repeats the canonical %q section heading; keep exactly one standalone heading for this section", line, heading),
			line,
		)
	}
	if command, line := firstBareStatementTeXInCodeSpan(statement); command != "" {
		appendDiagnostic(
			"bare_tex_in_code_span",
			fmt.Sprintf("line %d contains TeX command \\%s inside an inline code span; use plain text or put the complete expression inside $...$", line, command),
			line,
		)
	}

	for _, diagnostic := range diagnoseGeneratedStatementMarkdownV1(statement, expectStructuredSamples, includeReasoningTrace) {
		appendDiagnostic(diagnostic.Code, diagnostic.Message, diagnostic.Line)
	}
	return diagnostics
}

func diagnoseGeneratedStatementMarkdownV1(
	statement string,
	expectStructuredSamples bool,
	includeReasoningTrace bool,
) []StatementMarkdownDiagnosticV1 {
	diagnostics := make([]StatementMarkdownDiagnosticV1, 0, 8)
	appendDiagnostic := func(code, message string, line int) {
		if len(diagnostics) >= maxStatementMarkdownDiagnosticsV1 {
			return
		}
		diagnostics = append(diagnostics, StatementMarkdownDiagnosticV1{
			Code:    code,
			Message: message,
			Line:    line,
		})
	}

	if strings.TrimSpace(statement) == "" {
		appendDiagnostic("empty_statement", "statement is empty", 0)
		return diagnostics
	}
	if index := firstLiteralStatementNewline(statement); index >= 0 {
		appendDiagnostic(
			"literal_newline_escape",
			fmt.Sprintf("statement contains a literal \\n layout escape at byte %d instead of a real newline", index),
			statementLineAtByte(statement, index),
		)
	}
	if command, line := firstBareStatementTeXCommand(statement); command != "" {
		appendDiagnostic(
			"bare_tex_command",
			fmt.Sprintf("line %d contains bare TeX command \\%s outside $...$ or $$...$$", line, command),
			line,
		)
	}
	for _, diagnostic := range statementIntegerFieldDiagnosticsV1(statement) {
		appendDiagnostic(diagnostic.Code, diagnostic.Message, diagnostic.Line)
	}
	if !statementInputHeadingPattern.MatchString(statement) {
		appendDiagnostic("missing_input_heading", "statement is missing a standalone input-format heading", 0)
	}
	if !statementOutputHeadingPattern.MatchString(statement) {
		appendDiagnostic("missing_output_heading", "statement is missing a standalone output-format heading", 0)
	}
	if !statementConstraintHeadingPattern.MatchString(statement) {
		appendDiagnostic("missing_constraints_heading", "statement is missing a standalone constraints heading", 0)
	}
	placeholderCount := strings.Count(statement, StatementSamplesPlaceholder)
	if expectStructuredSamples && placeholderCount != 1 {
		appendDiagnostic(
			"structured_sample_placeholder_count",
			fmt.Sprintf("statement must contain exactly one structured sample placeholder; found %d", placeholderCount),
			0,
		)
	}
	if !expectStructuredSamples && placeholderCount != 0 {
		appendDiagnostic(
			"unexpected_structured_sample_placeholder",
			fmt.Sprintf("final statement still contains %d structured sample placeholder(s)", placeholderCount),
			0,
		)
	}
	if includeReasoningTrace && cotPatterns.MatchString(statement) {
		appendDiagnostic(
			"reasoning_trace",
			"statement contains chain-of-thought, self-correction, or thinking-out-loud text",
			0,
		)
	}
	return diagnostics
}

func statementLineAtByte(statement string, index int) int {
	if index < 0 || index > len(statement) {
		return 0
	}
	return strings.Count(statement[:index], "\n") + 1
}

func validateStatementMarkdownSurface(statement string) error {
	if index := firstLiteralStatementNewline(statement); index >= 0 {
		return fmt.Errorf("statement contains a literal \\n layout escape at byte %d instead of a real newline", index)
	}
	if command, line := firstBareStatementTeXCommand(statement); command != "" {
		return fmt.Errorf("line %d contains bare TeX command \\%s outside $...$ or $$...$$", line, command)
	}
	return validateStatementIntegerFieldClaims(statement)
}

func firstLiteralStatementNewline(statement string) int {
	for index := 0; index+1 < len(statement); index++ {
		if statement[index] == '\\' && statement[index+1] == 'n' && literalNIsLayoutEscape(statement, index) {
			return index
		}
	}
	return -1
}

func firstBareStatementTeXCommand(statement string) (string, int) {
	lines := strings.Split(statement, "\n")
	inFence := false
	mathDelimiter := byte(0)
	for lineIndex, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		inCode := false
		for index := 0; index < len(line); index++ {
			if line[index] == '`' {
				inCode = !inCode
				continue
			}
			if inCode {
				continue
			}
			if line[index] == '$' {
				delimiter := byte(1)
				if index+1 < len(line) && line[index+1] == '$' {
					delimiter = 2
					index++
				}
				if mathDelimiter == 0 {
					mathDelimiter = delimiter
				} else if mathDelimiter == delimiter {
					mathDelimiter = 0
				}
				continue
			}
			if mathDelimiter != 0 || line[index] != '\\' || index+1 >= len(line) || !isASCIIAlpha(line[index+1]) {
				continue
			}
			commandEnd := index + 1
			for commandEnd < len(line) && isASCIIAlpha(line[commandEnd]) {
				commandEnd++
			}
			return line[index+1 : commandEnd], lineIndex + 1
		}
	}
	return "", 0
}

// firstDuplicateStatementHeading returns the second occurrence of one of the
// canonical public sections. It only considers standalone Markdown headings
// outside fenced code, so a quoted example or a heading in source code cannot
// accidentally invalidate an otherwise valid statement.
func firstDuplicateStatementHeading(statement string) (string, int) {
	seen := make(map[string]bool, 3)
	inFence := false
	for lineIndex, line := range strings.Split(statement, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Strip the Markdown heading marker and require that the remainder is
		// exactly a known section label. A heading with prose on the same line is
		// not canonical and is handled by the existing missing-section check.
		hashEnd := 0
		for hashEnd < len(trimmed) && trimmed[hashEnd] == '#' {
			hashEnd++
		}
		if hashEnd == 0 || hashEnd >= len(trimmed) {
			continue
		}
		labelText := strings.TrimSpace(trimmed[hashEnd:])
		labelText = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(labelText, ":"), "："))
		labelText = strings.TrimSpace(strings.TrimSuffix(labelText, "#"))
		label, ok := canonicalStatementSectionHeading(labelText)
		if !ok {
			continue
		}
		key := strings.ToLower(label)
		switch key {
		case "输入格式", "input format":
			key = "input"
		case "输出格式", "output format":
			key = "output"
		case "约束", "constraints":
			key = "constraints"
		}
		if seen[key] {
			return label, lineIndex + 1
		}
		seen[key] = true
	}
	return "", 0
}

// firstBareStatementTeXInCodeSpan finds mathematical TeX commands in inline
// code. Code spans are otherwise ignored by the legacy surface checker because
// programming examples legitimately contain backslashes. Only the explicit
// mathematical command allow-list above is rejected here.
func firstBareStatementTeXInCodeSpan(statement string) (string, int) {
	inFence := false
	for lineIndex, line := range strings.Split(statement, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		inCode := false
		for index := 0; index < len(line); index++ {
			if line[index] == '`' {
				// A doubled backtick is still treated as two delimiters by the
				// Markdown dialect used by the editor; toggling here matches the
				// existing bare-TeX scanner's deliberately simple behavior.
				inCode = !inCode
				continue
			}
			if !inCode || line[index] != '\\' || index+1 >= len(line) || !isASCIIAlpha(line[index+1]) {
				continue
			}
			commandEnd := index + 1
			for commandEnd < len(line) && isASCIIAlpha(line[commandEnd]) {
				commandEnd++
			}
			command := strings.ToLower(line[index+1 : commandEnd])
			if statementMathCommandsInCode[command] {
				return command, lineIndex + 1
			}
			index = commandEnd - 1
		}
	}
	return "", 0
}

func validateStatementIntegerFieldClaims(statement string) error {
	diagnostics := statementIntegerFieldDiagnosticsV1(statement)
	if len(diagnostics) > 0 {
		return fmt.Errorf("%s", diagnostics[0].Message)
	}
	return nil
}

func statementIntegerFieldDiagnosticsV1(statement string) []StatementMarkdownDiagnosticV1 {
	diagnostics := make([]StatementMarkdownDiagnosticV1, 0, 2)
	for lineIndex, line := range strings.Split(statement, "\n") {
		for _, match := range chineseIntegerFieldClaimPattern.FindAllStringSubmatch(line, -1) {
			claimed, ok := parseStatementIntegerCount(match[1])
			listed := countStatementFieldList(match[2])
			if ok && listed > 0 && claimed != listed {
				diagnostics = append(diagnostics, StatementMarkdownDiagnosticV1{
					Code:    "integer_field_count_mismatch",
					Message: fmt.Sprintf("line %d claims %d integers but lists %d fields: %s", lineIndex+1, claimed, listed, strings.TrimSpace(match[2])),
					Line:    lineIndex + 1,
				})
			}
		}
		for _, match := range englishIntegerFieldClaimPattern.FindAllStringSubmatch(line, -1) {
			claimed, ok := parseStatementIntegerCount(strings.ToLower(match[1]))
			listed := countStatementFieldList(match[2])
			if ok && listed > 0 && claimed != listed {
				diagnostics = append(diagnostics, StatementMarkdownDiagnosticV1{
					Code:    "integer_field_count_mismatch",
					Message: fmt.Sprintf("line %d claims %d integers but lists %d fields: %s", lineIndex+1, claimed, listed, strings.TrimSpace(match[2])),
					Line:    lineIndex + 1,
				})
			}
		}
	}
	return diagnostics
}

func countStatementFieldList(fields string) int {
	normalized := strings.NewReplacer(
		"，", ",",
		"、", ",",
		" 和 ", ",",
		" 与 ", ",",
		" and ", ",",
	).Replace(strings.TrimSpace(fields))
	if !strings.Contains(normalized, ",") {
		return 0
	}
	count := 0
	for _, field := range strings.Split(normalized, ",") {
		field = strings.Trim(strings.TrimSpace(field), "$`(){}[]")
		if field != "" {
			count++
		}
	}
	return count
}

func parseStatementIntegerCount(value string) (int, bool) {
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed, true
	}
	counts := map[string]int{
		"一": 1, "两": 2, "二": 2, "三": 3, "四": 4, "五": 5,
		"六": 6, "七": 7, "八": 8, "九": 9, "十": 10,
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}
	count, ok := counts[value]
	return count, ok
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
