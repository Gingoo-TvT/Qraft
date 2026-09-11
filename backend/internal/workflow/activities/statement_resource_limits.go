package activities

import (
	"fmt"
	"regexp"
	"strings"
)

// The statement generator receives provisional limits.  The workflow may
// later derive a different public limit from the standard solution benchmark.
// These expressions deliberately operate on human-facing prose only; fenced
// code and mathematical expressions are left untouched.
var (
	statementTimeLimitValuePatternV1        = regexp.MustCompile(`(?i)((?:时间限制|time\s+limit)[^0-9\n]{0,32})\d+(?:\.\d+)?\s*(?:毫秒|ms|milliseconds?|秒|s|seconds?)`)
	statementMemoryLimitValuePatternV1      = regexp.MustCompile(`(?i)((?:内存限制|memory\s+limit)[^0-9\n]{0,32})\d+(?:\.\d+)?\s*(?:字节|bytes?|mb|mib|gb|gib|兆字节|兆|gigabytes?)`)
	statementStandaloneTimeLimitPatternV1   = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:\*\*)?(?:时间限制|time\s+limit)(?:\*\*)?\s*[:：].*$`)
	statementStandaloneMemoryLimitPatternV1 = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:\*\*)?(?:内存限制|memory\s+limit)(?:\*\*)?\s*[:：].*$`)
	statementTimeLabelPatternV1             = regexp.MustCompile(`(?i)(时间限制|time\s+limit)`)
	statementMemoryLabelPatternV1           = regexp.MustCompile(`(?i)(内存限制|memory\s+limit)`)
)

// SynchronizeStatementResourceLimitsV1 makes the public statement agree with
// the limits that will be stored and enforced. It is intentionally
// deterministic and limited to resource-limit labels, so it cannot change the
// problem's algorithmic semantics. Existing inline/prose declarations are
// updated in place; duplicate standalone declarations are collapsed. Missing
// declarations are added at the end using the statement's apparent locale.
func SynchronizeStatementResourceLimitsV1(statement string, timeLimitMS, memoryLimitMB int) string {
	if strings.TrimSpace(statement) == "" || timeLimitMS <= 0 || memoryLimitMB <= 0 {
		return statement
	}

	statement = strings.ReplaceAll(statement, "\r\n", "\n")
	statement = strings.ReplaceAll(statement, "\r", "\n")
	lines := strings.Split(statement, "\n")
	inFence := false
	timeSeen := false
	memorySeen := false
	containsChinese := false
	for _, r := range statement {
		if (r >= '\u4e00' && r <= '\u9fff') || (r >= '\u3400' && r <= '\u4dbf') {
			containsChinese = true
			break
		}
	}

	updated := make([]string, 0, len(lines)+3)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			updated = append(updated, line)
			continue
		}
		if inFence {
			updated = append(updated, line)
			continue
		}

		if statementStandaloneTimeLimitPatternV1.MatchString(line) {
			if timeSeen {
				continue
			}
			timeSeen = true
			updated = append(updated, canonicalStatementTimeLimitLineV1(line, timeLimitMS, containsChinese))
			continue
		}
		if statementStandaloneMemoryLimitPatternV1.MatchString(line) {
			if memorySeen {
				continue
			}
			memorySeen = true
			updated = append(updated, canonicalStatementMemoryLimitLineV1(line, memoryLimitMB, containsChinese))
			continue
		}

		if statementTimeLimitValuePatternV1.MatchString(line) {
			line = statementTimeLimitValuePatternV1.ReplaceAllStringFunc(line, func(match string) string {
				parts := statementTimeLimitValuePatternV1.FindStringSubmatch(match)
				if len(parts) != 2 {
					return match
				}
				return parts[1] + fmt.Sprintf("%d ms", timeLimitMS)
			})
			timeSeen = true
		}
		if statementMemoryLimitValuePatternV1.MatchString(line) {
			line = statementMemoryLimitValuePatternV1.ReplaceAllStringFunc(line, func(match string) string {
				parts := statementMemoryLimitValuePatternV1.FindStringSubmatch(match)
				if len(parts) != 2 {
					return match
				}
				return parts[1] + fmt.Sprintf("%d MB", memoryLimitMB)
			})
			memorySeen = true
		}
		updated = append(updated, line)
	}

	result := strings.TrimSpace(strings.Join(updated, "\n"))
	if !timeSeen || !memorySeen {
		var missing strings.Builder
		if !timeSeen {
			if containsChinese {
				missing.WriteString(fmt.Sprintf("时间限制：%d ms", timeLimitMS))
			} else {
				missing.WriteString(fmt.Sprintf("Time limit: %d ms", timeLimitMS))
			}
		}
		if !memorySeen {
			if missing.Len() > 0 {
				missing.WriteByte('\n')
			}
			if containsChinese {
				missing.WriteString(fmt.Sprintf("内存限制：%d MB", memoryLimitMB))
			} else {
				missing.WriteString(fmt.Sprintf("Memory limit: %d MB", memoryLimitMB))
			}
		}
		missingText := missing.String()
		if marker := strings.Index(result, StatementSamplesPlaceholder); marker >= 0 {
			before := strings.TrimRight(result[:marker], " \t\n")
			after := strings.TrimLeft(result[marker:], " \t\n")
			parts := make([]string, 0, 3)
			if before != "" {
				parts = append(parts, before)
			}
			parts = append(parts, missingText, after)
			result = strings.Join(parts, "\n\n")
		} else {
			if result != "" {
				result += "\n\n"
			}
			result += missingText
		}
	}
	return strings.TrimSpace(result)
}

func canonicalStatementTimeLimitLineV1(original string, value int, chinese bool) string {
	if chinese || statementTimeLabelPatternV1.MatchString(original) && strings.ContainsAny(original, "时间") {
		return fmt.Sprintf("时间限制：%d ms", value)
	}
	return fmt.Sprintf("Time limit: %d ms", value)
}

func canonicalStatementMemoryLimitLineV1(original string, value int, chinese bool) string {
	if chinese || statementMemoryLabelPatternV1.MatchString(original) && strings.ContainsAny(original, "内存") {
		return fmt.Sprintf("内存限制：%d MB", value)
	}
	return fmt.Sprintf("Memory limit: %d MB", value)
}
