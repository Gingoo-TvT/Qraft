package service

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

// Numbering is scoped to one download. It never changes source identifiers.
type TestingExportNumbering struct {
	Prefix string `json:"prefix"`
	Start  int    `json:"start"`
}

var testingPrefixRE = regexp.MustCompile(`^[A-Za-z0-9_-]{0,24}$`)

func ParseTestingExportNumbering(prefix, start string) (*TestingExportNumbering, error) {
	if prefix == "" && start == "" {
		return nil, nil
	}
	if start == "" {
		start = "1"
	}
	number, err := strconv.Atoi(start)
	if err != nil {
		return nil, fmt.Errorf("validation: 起始编号必须为整数")
	}
	result := &TestingExportNumbering{Prefix: prefix, Start: number}
	if err := result.validate(1); err != nil {
		return nil, err
	}
	return result, nil
}

func (n *TestingExportNumbering) validate(count int) error {
	if n == nil {
		return nil
	}
	if !testingPrefixRE.MatchString(n.Prefix) {
		return fmt.Errorf("validation: 编号前缀最多 24 位，仅支持英文字母、数字、下划线和连字符")
	}
	if n.Start < 1 || n.Start > 999999 || count > 999999-n.Start+1 {
		return fmt.Errorf("validation: 导出编号必须在 1 到 999999 之间")
	}
	return nil
}

func (n *TestingExportNumbering) code(index int, kind domain.QuizType) string {
	letter := kind.CodePrefix()
	if kind == domain.QuizTypeProgramming {
		letter = "P"
	}
	return fmt.Sprintf("%s%s%03d", n.Prefix, letter, n.Start+index)
}
