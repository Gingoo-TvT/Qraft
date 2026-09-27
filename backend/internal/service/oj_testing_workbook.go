package service

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/xuri/excelize/v2"
)

var ojProblemHeaders = append(append([]string{}, QuizExcelHeaders...), "模板版本", "时间限制", "内存限制", "文件IO名称", "评测详情")
var ojTestHeaders = []string{"题目编号", "子任务ID", "计分方式", "子任务分数", "测试点ID", "输入文件", "输出文件", "测试点分数", "子任务时间限制", "子任务内存限制", "测试点时间限制", "测试点内存限制"}

func ojProgrammingDifficulty(rating int) string {
	if rating < 800 || rating > 3500 {
		return ""
	}
	switch {
	case rating < 1000:
		return "入门"
	case rating < 1400:
		return "简单"
	case rating < 1800:
		return "进阶"
	case rating < 2200:
		return "困难"
	case rating < 2600:
		return "挑战"
	default:
		return "地狱"
	}
}

type ojTestingRow struct {
	Code     string
	Problem  *domain.Problem
	Quiz     *domain.QuizProblem
	Subtasks []hydroSubtask
	Metadata hydroMetadata
}
type ojExportNotice struct {
	Code     string `json:"code"`
	Field    string `json:"field"`
	Original string `json:"original"`
	Message  string `json:"message"`
}

// Build from the public column contract, never from a private template file.
func buildOJTestingWorkbook(rows []ojTestingRow, options TestingExportOptions) ([]byte, []ojExportNotice, error) {
	book := excelize.NewFile()
	defer book.Close()
	const mainSheet = "题目列表"
	const caseSheet = "测试点配置"
	if err := book.SetSheetName("Sheet1", mainSheet); err != nil {
		return nil, nil, err
	}
	if _, err := book.NewSheet(caseSheet); err != nil {
		return nil, nil, err
	}
	if _, err := book.NewSheet("导出说明"); err != nil {
		return nil, nil, err
	}
	put := func(sheet string, row int, values []string) error {
		for column, value := range values {
			if value == "" {
				continue
			}
			// Excel silently truncates >32767 UTF-16 units. Reject instead of exporting
			// a different statement/answer. SetCellStr also prevents formula injection.
			if len(utf16.Encode([]rune(value))) > 32767 {
				return fmt.Errorf("validation: %s 第 %d 行第 %d 列超过 Excel 单元格上限，不能无损导出", sheet, row, column+1)
			}
			cell, _ := excelize.CoordinatesToCellName(column+1, row)
			numeric := row > 1 && value != "" && ((sheet == mainSheet && (column == 3 || column == 14)) || (sheet == caseSheet && (column == 1 || column == 3 || column == 4 || column == 7)))
			if numeric {
				number, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("validation: %s %s 必须为整数", sheet, cell)
				}
				if err := book.SetCellInt(sheet, cell, int64(number)); err != nil {
					return err
				}
				continue
			}
			if err := book.SetCellStr(sheet, cell, value); err != nil {
				return err
			}
		}
		return nil
	}
	if err := put(mainSheet, 1, ojProblemHeaders); err != nil {
		return nil, nil, err
	}
	if err := put(caseSheet, 1, ojTestHeaders); err != nil {
		return nil, nil, err
	}
	notices := []ojExportNotice{}
	caseRow := 2
	for index, row := range rows {
		cells := make([]string, len(ojProblemHeaders))
		var tags []string
		if row.Problem != nil {
			p := row.Problem
			cells[1], cells[2], cells[5] = p.Title, p.Statement, "编程题"
			cells[9], cells[10] = "私有", "否"
			cells[14], cells[15], cells[16], cells[17], cells[18] = "2", hydroTimeLimit(p.TimeLimit), hydroMemoryLimit(p.MemoryLimit), row.Metadata.Filename, row.Metadata.Detail
			if cells[18] == "" {
				cells[18] = "full"
			}
			tags = p.Tags
			cells[8] = ojProgrammingDifficulty(p.Difficulty)
			message := "按 Qraft 导出六档映射为：" + cells[8]
			if cells[8] == "" {
				message = "rating 缺失或不在 800–3500 范围，难度留空待确认"
			}
			notices = append(notices, ojExportNotice{row.Code, "难度", strconv.Itoa(p.Difficulty), message})
			testID := 0
			for _, subtask := range row.Subtasks {
				if subtask.Type != "sum" && subtask.Type != "min" {
					return nil, nil, fmt.Errorf("validation: 不支持子任务计分方式 %s", subtask.Type)
				}
				sum := 0
				for _, c := range subtask.Cases {
					sum += c.Score
				}
				if subtask.Type == "sum" && sum != subtask.Score {
					return nil, nil, fmt.Errorf("validation: 子任务 %d 测试点分数之和不一致", subtask.ID)
				}
				for _, c := range subtask.Cases {
					testID++
					score := ""
					if subtask.Type == "sum" {
						score = strconv.Itoa(c.Score)
					}
					data := []string{row.Code, strconv.Itoa(subtask.ID), subtask.Type, strconv.Itoa(subtask.Score), strconv.Itoa(testID), c.Input, c.Output, score, subtask.Time, subtask.Memory, c.Time, c.Memory}
					if err := put(caseSheet, caseRow, data); err != nil {
						return nil, nil, err
					}
					caseRow++
				}
			}
		} else if row.Quiz != nil {
			q := row.Quiz
			old := SerializeQuizRow(*q)
			copy(cells, old[:])
			tags = q.Tags
			if cells[8] == "中等" {
				cells[8] = "进阶"
				notices = append(notices, ojExportNotice{row.Code, "难度", "中等", "客观题中等档按导出规则映射为进阶"})
			}
			// Language IDs are instance-specific. Keep original configuration in the
			// sidecar instead of assigning a target compiler from a source-side ID.
			if q.CodeHint != "" || q.CodeID != nil || len(q.Langs) > 0 {
				notices = append(notices, ojExportNotice{row.Code, "语言配置", cells[3] + " / " + cells[12], "代码提示及语言配置保留在题目 JSON 中，模板字段待按目标 OJ 确认"})
				cells[3], cells[4], cells[12] = "", "", ""
			}
		} else {
			return nil, nil, fmt.Errorf("validation: 导出题目为空")
		}
		if strings.TrimSpace(cells[1]) == "" || strings.TrimSpace(cells[2]) == "" {
			return nil, nil, fmt.Errorf("validation: 第 %d 题标题或题面为空", index+1)
		}
		cells[0] = row.Code
		ids, unresolved := options.TagCatalog.resolve(tags)
		cells[11] = strings.Join(ids, ",")
		if len(unresolved) > 0 {
			notices = append(notices, ojExportNotice{row.Code, "题目标签", strings.Join(unresolved, ","), "标签未找到或存在同名歧义，未写入导入表；请按完整路径或 ID 确认"})
		}
		if err := put(mainSheet, index+2, cells); err != nil {
			return nil, nil, err
		}
	}
	notes := [][]string{
		{"项目", "说明", "原始值", "处理方式"},
		{"上传包", "上传整个 ZIP；根目录 problems.xlsx，数据位于 datas/题目编号/。"},
		{"题号", "按题集身份及题序生成稳定的 C/X/T/P 题号；来源题号与题集分值见 problem-set.json。"},
		{"计分", "每题内部计分保留验证数据的 sum/min 规则；题集分值请在目标 OJ 比赛中另行配置。样例保留原有零分规则。"},
		{"难度", "rating 800–999 入门；1000–1399 简单；1400–1799 进阶；1800–2199 困难；2200–2599 挑战；2600–3500 地狱。客观题中等映射为进阶。此规则仅用于导出分档，原始值见下方。"},
		{"标签", "仅使用本次提供的有效标签目录；同名或无法匹配的标签留待确认，不猜测 ID。"},
		{"语言", "未确认目标语言编号时不限制语言，不把参考解法作为代码提示。"},
		{"范围", "当前导出供 OJ 测试，不自动发布题目，也不自动创建比赛。"},
	}
	for _, n := range notices {
		notes = append(notes, []string{n.Code, n.Field, n.Original, n.Message})
	}
	for i, note := range notes {
		if err := put("导出说明", i+1, note); err != nil {
			return nil, nil, err
		}
	}
	header, err := book.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"23456A"}, Pattern: 1}, Alignment: &excelize.Alignment{Vertical: "center"}})
	if err != nil {
		return nil, nil, err
	}
	body, err := book.NewStyle(&excelize.Style{NumFmt: 49, Alignment: &excelize.Alignment{Vertical: "top", WrapText: true}})
	if err != nil {
		return nil, nil, err
	}
	for _, sheet := range []struct {
		name, end string
		rows      int
	}{{mainSheet, "S", len(rows) + 1}, {caseSheet, "L", caseRow - 1}, {"导出说明", "D", len(notes)}} {
		if err := book.SetColWidth(sheet.name, "A", sheet.end, 19); err != nil {
			return nil, nil, err
		}
		if err := book.SetCellStyle(sheet.name, "A1", sheet.end+"1", header); err != nil {
			return nil, nil, err
		}
		if sheet.rows > 1 {
			if err := book.SetCellStyle(sheet.name, "A2", fmt.Sprintf("%s%d", sheet.end, sheet.rows), body); err != nil {
				return nil, nil, err
			}
		}
		if err := book.SetPanes(sheet.name, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
			return nil, nil, err
		}
	}
	numericStyle, err := book.NewStyle(&excelize.Style{NumFmt: 0, Alignment: &excelize.Alignment{Vertical: "top", WrapText: true}})
	if err != nil {
		return nil, nil, err
	}
	for _, col := range []string{"D", "O"} {
		if err := book.SetCellStyle(mainSheet, col+"2", fmt.Sprintf("%s%d", col, len(rows)+1), numericStyle); err != nil {
			return nil, nil, err
		}
	}
	if caseRow > 2 {
		for _, col := range []string{"B", "D", "E", "H"} {
			if err := book.SetCellStyle(caseSheet, col+"2", fmt.Sprintf("%s%d", col, caseRow-1), numericStyle); err != nil {
				return nil, nil, err
			}
		}
	}
	for col, width := range map[string]float64{"B": 32, "C": 72, "E": 42, "G": 42, "H": 25, "L": 30, "N": 50} {
		if err := book.SetColWidth(mainSheet, col, col, width); err != nil {
			return nil, nil, err
		}
	}
	if err := book.SetColWidth("导出说明", "B", "D", 55); err != nil {
		return nil, nil, err
	}
	// Explicit row heights give readable previews without expanding a long
	// statement across a whole screen; Excel retains its entire cell value.
	for i := 2; i <= len(rows)+1; i++ {
		lines := 2
		for col, width := range map[string]int{"B": 28, "C": 64, "E": 36, "G": 36, "H": 21, "L": 26, "N": 44} {
			value, err := book.GetCellValue(mainSheet, fmt.Sprintf("%s%d", col, i))
			if err != nil {
				return nil, nil, err
			}
			count := 0
			for _, line := range strings.Split(value, "\n") {
				units := 0
				for _, r := range line {
					if r > 127 {
						units += 2
					} else {
						units++
					}
				}
				count += max(1, (units+width-1)/width)
			}
			if count > lines {
				lines = count
			}
		}
		height := min(409, float64(lines*16+8))
		if err := book.SetRowHeight(mainSheet, i, height); err != nil {
			return nil, nil, err
		}
	}
	for i := 2; i <= len(notes); i++ {
		lines := 1
		for _, value := range notes[i-1] {
			width := 0
			for _, r := range value {
				if r > 127 {
					width += 2
				} else {
					width++
				}
			}
			if count := (width + 44) / 45; count > lines {
				lines = count
			}
		}
		height := float64(lines*16 + 8)
		if height > 409 {
			height = 409
		}
		if err := book.SetRowHeight("导出说明", i, height); err != nil {
			return nil, nil, err
		}
	}
	for _, rule := range []struct {
		col    string
		values []string
	}{
		{"F", []string{"编程题", "选择题", "填空题", "判断题"}}, {"I", []string{"入门", "简单", "进阶", "困难", "挑战", "地狱"}},
		{"J", []string{"公开", "比赛", "私有"}}, {"K", []string{"是", "否"}}, {"S", []string{"full", "case", "none"}},
	} {
		validation := excelize.NewDataValidation(true)
		validation.Sqref = fmt.Sprintf("%s2:%s%d", rule.col, rule.col, len(rows)+1)
		if err := validation.SetDropList(rule.values); err != nil {
			return nil, nil, err
		}
		validation.SetError(excelize.DataValidationErrorStyleStop, "填写值无效", "请从下拉列表选择")
		if err := book.AddDataValidation(mainSheet, validation); err != nil {
			return nil, nil, err
		}
	}
	if caseRow > 2 {
		validation := excelize.NewDataValidation(true)
		validation.Sqref = fmt.Sprintf("C2:C%d", caseRow-1)
		if err := validation.SetDropList([]string{"sum", "min"}); err != nil {
			return nil, nil, err
		}
		if err := book.AddDataValidation(caseSheet, validation); err != nil {
			return nil, nil, err
		}
	}
	content, err := book.WriteToBuffer()
	if err != nil {
		return nil, nil, err
	}
	return content.Bytes(), notices, nil
}
