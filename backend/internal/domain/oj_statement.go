package domain

import (
	"fmt"
	"regexp"
	"strings"
)

const OJStatementVersion = 1

type OJSample struct {
	Input       string `json:"input"`
	Output      string `json:"output"`
	Origin      string `json:"origin"` // original or generated; both require execution verification
	Explanation string `json:"explanation,omitempty"`
}

// OJStatement stores content, not model-written heading/fence syntax. The
// renderer is shared by new imports and explicitly requested legacy repairs.
type OJStatement struct {
	Version     int        `json:"version"`
	Description string     `json:"description"`
	Input       string     `json:"input"`
	Output      string     `json:"output"`
	Constraints string     `json:"constraints"`
	Samples     []OJSample `json:"samples"`
	Notes       string     `json:"notes,omitempty"`
	Reason      string     `json:"reason"`
}

var sourceFigure = regexp.MustCompile(`!\[[^\]\n]*\]\((?:<([^>\n]+)>|([^\s)]+))\)`)

var sectionHeading = regexp.MustCompile(`(?m)^\s*\\?#{1,6}\s+`)

func (s OJStatement) Validate(source string) error {
	if s.Version != OJStatementVersion {
		return fmt.Errorf("unsupported OJ statement version")
	}
	for name, value := range map[string]string{"description": s.Description, "input": s.Input, "output": s.Output, "constraints": s.Constraints} {
		if strings.TrimSpace(value) == "" || len(value) > 64*1024 {
			return fmt.Errorf("%s is required and must be bounded", name)
		}
		if sectionHeading.MatchString(value) {
			return fmt.Errorf("%s must contain body text only; headings are rendered by Qraft", name)
		}
	}
	if len(s.Notes) > 16*1024 || sectionHeading.MatchString(s.Notes) || strings.TrimSpace(s.Reason) == "" || len(s.Reason) > 4000 {
		return fmt.Errorf("normalization notes/reason invalid")
	}
	if len(s.Samples) < 1 || len(s.Samples) > 20 {
		return fmt.Errorf("at least one complete input/output example is required (maximum 20)")
	}
	canonical := func(v string) string { return strings.TrimRight(strings.ReplaceAll(v, "\r\n", "\n"), "\n") }
	source = strings.ReplaceAll(source, "\r\n", "\n")
	for i, sample := range s.Samples {
		if sample.Output == "" || len(sample.Input)+len(sample.Output) > 32*1024 || len(sample.Explanation) > 4000 || sectionHeading.MatchString(sample.Explanation) {
			return fmt.Errorf("example %d is incomplete or too large", i+1)
		}
		switch sample.Origin {
		case "original":
			if !strings.Contains(source, canonical(sample.Input)) || !strings.Contains(source, canonical(sample.Output)) {
				return fmt.Errorf("example %d must preserve source input/output exactly", i+1)
			}
		case "generated":
			if len(sample.Input) > 4096 || len(sample.Output) > 4096 {
				return fmt.Errorf("new examples must be small enough for independent checking")
			}
		default:
			return fmt.Errorf("example %d origin must be original or generated", i+1)
		}
	}
	for _, original := range ExtractStatementSamples(source) {
		found := false
		for _, sample := range s.Samples {
			if sample.Origin == "original" && canonical(sample.Input) == canonical(original.Input) && canonical(sample.Output) == canonical(original.Output) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("an original source example was omitted or changed; retain all original examples")
		}
	}
	for _, match := range sourceFigure.FindAllStringSubmatch(source, -1) {
		target := match[1]
		if target == "" {
			target = match[2]
		}
		retained := false
		for _, figure := range sourceFigure.FindAllStringSubmatch(s.Markdown(), -1) {
			if figure[1] == target || figure[2] == target {
				retained = true
				break
			}
		}
		if !retained {
			return fmt.Errorf("an original figure or formula image was omitted; retain this exact Markdown image in its corresponding description, constraints or notes: %s", match[0])
		}
	}
	if len(s.Markdown()) > 128*1024 {
		return fmt.Errorf("formatted statement exceeds 128 KiB")
	}
	return nil
}

func (s OJStatement) Markdown() string {
	var b strings.Builder
	section := func(name, body string) { fmt.Fprintf(&b, "#### %s\n\n%s\n\n", name, strings.TrimSpace(body)) }
	section("题目描述", s.Description)
	section("输入格式", s.Input)
	section("输出格式", s.Output)
	for i, sample := range s.Samples {
		fmt.Fprintf(&b, "#### 输入输出样例 #%d\n\n", i+1)
		for j, body := range []string{sample.Input, sample.Output} {
			label := "输入"
			if j == 1 {
				label = "输出"
			}
			fence := "```"
			for strings.Contains(body, fence) {
				fence += "`"
			}
			fmt.Fprintf(&b, "#### %s #%d\n\n%s\n%s", label, i+1, fence, strings.ReplaceAll(body, "\r\n", "\n"))
			if !strings.HasSuffix(body, "\n") {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "%s\n\n", fence)
		}
		if strings.TrimSpace(sample.Explanation) != "" {
			fmt.Fprintf(&b, "样例说明：%s\n\n", strings.TrimSpace(sample.Explanation))
		}
	}
	constraints := s.Constraints
	if strings.TrimSpace(s.Notes) != "" {
		constraints += "\n\n" + s.Notes
	}
	section("数据规模与约定", constraints)
	return strings.TrimSpace(b.String()) + "\n"
}

// Recognize explicitly labelled fenced samples. Never guess that an input
// grammar or code excerpt is a sample merely because it has a fence.
func ExtractStatementSamples(text string) []OJSample {
	var out []OJSample
	label, fence, body, input := "", "", "", ""
	haveInput := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if fence != "" {
			if t == fence {
				if label == "input" {
					input = strings.TrimSuffix(body, "\n")
					haveInput = true
				}
				if label == "output" && haveInput {
					out = append(out, OJSample{Input: input, Output: strings.TrimSuffix(body, "\n"), Origin: "original"})
					haveInput = false
				}
				fence = ""
				body = ""
				label = ""
			} else {
				body += line + "\n"
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			c := t[:1]
			n := 0
			for n < len(t) && t[n:n+1] == c {
				n++
			}
			fence = t[:n]
			continue
		}
		if strings.HasPrefix(t, "#") {
			title := strings.ToLower(strings.Trim(strings.TrimLeft(t, "# "), "*：: "))
			label = ""
			if strings.Contains(title, "输入样例") || strings.Contains(title, "样例输入") || strings.HasPrefix(title, "sample input") || strings.HasPrefix(title, "输入 #") || title == "输入" {
				label = "input"
			}
			if strings.Contains(title, "输出样例") || strings.Contains(title, "样例输出") || strings.HasPrefix(title, "sample output") || strings.HasPrefix(title, "输出 #") || title == "输出" {
				label = "output"
			}
		}
	}
	return out
}
