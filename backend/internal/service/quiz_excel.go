package service

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const (
	QuizExcelSheetName    = "Sheet1"
	QuizExcelHeaderRow    = 1
	QuizExcelDataStartRow = 2
	QuizExcelExpectedCols = 14

	QuizOptionSeparator = "{break}"
	QuizAnswerSeparator = "{break}"
	QuizCodeMinNumber   = 1000

	QuizGenerateCountMax = 20
)

var QuizExcelHeaders = []string{
	"题目编号", "题目标题", "题目描述（题面）", "代码ID", "代码提示",
	"题目类型", "题目选项", "正确答案", "难度", "访问类型",
	"是否VIP", "题目标签", "限定语言", "答案解析",
}

var breakSeparatorRE = regexp.MustCompile(`(?i)\s*\{break\}\s*`)
var optionLineRE = regexp.MustCompile(`^([A-Za-z])[.、)]\s*(.*)$`)

func ParseQuizRow(rowIndex int, cells [14]string) (domain.QuizProblem, error) {
	for i := range cells {
		cells[i] = normalizeQuizCell(cells[i])
	}

	code := cells[0]
	if code == "" {
		return domain.QuizProblem{}, fmt.Errorf("row %d: 题目编号不能为空", rowIndex)
	}
	codeType, err := quizTypeFromCode(code)
	if err != nil {
		return domain.QuizProblem{}, fmt.Errorf("row %d: %w", rowIndex, err)
	}
	typ, err := domain.QuizTypeFromExcel(cells[5])
	if err != nil {
		return domain.QuizProblem{}, fmt.Errorf("row %d: %w", rowIndex, err)
	}
	if typ != codeType {
		return domain.QuizProblem{}, fmt.Errorf("row %d: 题目编号前缀 %s 与题目类型 %s 不一致", rowIndex, typ.CodePrefix(), cells[5])
	}

	difficulty, err := domain.QuizDifficultyFromExcel(cells[8])
	if err != nil {
		return domain.QuizProblem{}, fmt.Errorf("row %d: %w", rowIndex, err)
	}
	visibility, err := domain.QuizVisibilityFromExcel(cells[9])
	if err != nil {
		return domain.QuizProblem{}, fmt.Errorf("row %d: %w", rowIndex, err)
	}
	isVIP, err := parseYesNo(cells[10])
	if err != nil {
		return domain.QuizProblem{}, fmt.Errorf("row %d: %w", rowIndex, err)
	}
	codeID, err := parseOptionalInt(cells[3])
	if err != nil {
		return domain.QuizProblem{}, fmt.Errorf("row %d: 代码ID无效: %w", rowIndex, err)
	}
	langs, err := parseIntList(cells[12])
	if err != nil {
		return domain.QuizProblem{}, fmt.Errorf("row %d: 限定语言无效: %w", rowIndex, err)
	}

	q := domain.QuizProblem{
		Code:        code,
		Title:       cells[1],
		Statement:   cells[2],
		Type:        typ,
		CodeID:      codeID,
		CodeHint:    cells[4],
		Answers:     SplitByBreak(cells[7]),
		Difficulty:  difficulty,
		Visibility:  visibility,
		IsVIP:       isVIP,
		Tags:        splitComma(cells[11]),
		Langs:       langs,
		Explanation: cells[13],
	}
	if q.Title == "" && q.Statement != "" {
		q.Title = truncateRunes(q.Statement, 255)
	}
	if q.Statement == "" {
		return domain.QuizProblem{}, fmt.Errorf("row %d: 题目描述（题面）不能为空", rowIndex)
	}
	if cells[6] != "" {
		options, err := parseOptions(cells[6])
		if err != nil {
			return domain.QuizProblem{}, fmt.Errorf("row %d: %w", rowIndex, err)
		}
		q.Options = options
	}
	if err := validateQuizRow(q, rowIndex); err != nil {
		return domain.QuizProblem{}, err
	}
	return q, nil
}

func SerializeQuizRow(q domain.QuizProblem) [14]string {
	var cells [14]string
	cells[0] = q.Code
	cells[1] = q.Title
	cells[2] = q.Statement
	if q.CodeID != nil {
		cells[3] = strconv.Itoa(*q.CodeID)
	}
	cells[4] = q.CodeHint
	cells[5] = q.Type.ToExcel()
	cells[6] = serializeOptions(q.Options)
	cells[7] = JoinByBreak(q.Answers)
	cells[8] = q.Difficulty.ToExcel()
	cells[9] = q.Visibility.ToExcel()
	if q.IsVIP {
		cells[10] = "是"
	} else {
		cells[10] = "否"
	}
	cells[11] = strings.Join(q.Tags, ",")
	cells[12] = joinInts(q.Langs)
	cells[13] = q.Explanation
	return cells
}

func SplitByBreak(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	raw := breakSeparatorRE.Split(s, -1)
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func JoinByBreak(parts []string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, QuizAnswerSeparator+"\n")
}

func ParseOptionLine(line string) (label, content string, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", fmt.Errorf("选项不能为空")
	}
	matches := optionLineRE.FindStringSubmatch(line)
	if len(matches) != 3 {
		return "", "", fmt.Errorf("选项格式无效: %q", line)
	}
	label = strings.ToUpper(matches[1])
	content = strings.TrimSpace(matches[2])
	if content == "" {
		return "", "", fmt.Errorf("选项 %s 内容不能为空", label)
	}
	return label, content, nil
}

func normalizeQuizCell(s string) string {
	s = strings.ReplaceAll(s, "_x000D_", "\n")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

func quizTypeFromCode(code string) (domain.QuizType, error) {
	code = strings.TrimSpace(code)
	if len(code) < 2 {
		return "", fmt.Errorf("题目编号无效: %q", code)
	}
	typ, err := domain.QuizTypeFromCodePrefix(code[:1])
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(code[1:])
	if err != nil || n < QuizCodeMinNumber {
		return "", fmt.Errorf("题目编号数字部分必须 >= %d: %q", QuizCodeMinNumber, code)
	}
	return typ, nil
}

func parseYesNo(s string) (bool, error) {
	switch strings.TrimSpace(s) {
	case "是":
		return true, nil
	case "否":
		return false, nil
	default:
		return false, fmt.Errorf("是否VIP未识别: %q", s)
	}
}

func parseOptionalInt(s string) (*int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func parseIntList(s string) ([]int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := splitComma(s)
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func splitComma(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseOptions(s string) ([]domain.QuizOption, error) {
	lines := SplitByBreak(s)
	out := make([]domain.QuizOption, 0, len(lines))
	for _, line := range lines {
		label, content, err := ParseOptionLine(line)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.QuizOption{Label: label, Content: content})
	}
	return out, nil
}

func validateQuizRow(q domain.QuizProblem, rowIndex int) error {
	switch q.Type {
	case domain.QuizTypeChoice:
		if len(q.Options) == 0 {
			return fmt.Errorf("row %d: 选择题必须有题目选项", rowIndex)
		}
		if len(q.Answers) == 0 {
			return fmt.Errorf("row %d: 选择题必须有正确答案", rowIndex)
		}
	case domain.QuizTypeJudge:
		if len(q.Answers) != 1 || (q.Answers[0] != "对" && q.Answers[0] != "错") {
			return fmt.Errorf("row %d: 判断题答案只能是 对 或 错", rowIndex)
		}
	case domain.QuizTypeFillBlank:
		if len(q.Answers) == 0 {
			return fmt.Errorf("row %d: 填空题至少需要一个答案", rowIndex)
		}
	case domain.QuizTypeProgramming:
		if q.Type.CodePrefix() != "C" {
			return fmt.Errorf("row %d: 编程题编号必须以 C 开头", rowIndex)
		}
	default:
		return fmt.Errorf("row %d: 题目类型无效", rowIndex)
	}
	return nil
}

func serializeOptions(options []domain.QuizOption) string {
	parts := make([]string, 0, len(options))
	for _, opt := range options {
		label := strings.TrimSpace(opt.Label)
		content := strings.TrimSpace(opt.Content)
		if label == "" || content == "" {
			continue
		}
		parts = append(parts, strings.ToUpper(label)+"."+content)
	}
	return strings.Join(parts, QuizOptionSeparator+"\n")
}

func joinInts(nums []int) string {
	parts := make([]string, 0, len(nums))
	for _, n := range nums {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ",")
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
