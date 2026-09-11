package service

import (
	"reflect"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestQuizExcel_SplitByBreak(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "single", in: "A", want: []string{"A"}},
		{name: "multiple with newline", in: "A{break}\nB{break}\nC", want: []string{"A", "B", "C"}},
		{name: "spaces and case", in: " A {BREAK} \n B {break} \nC ", want: []string{"A", "B", "C"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitByBreak(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SplitByBreak(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestQuizExcel_ParseOptionLine(t *testing.T) {
	label, content, err := ParseOptionLine("A. 选项内容")
	if err != nil {
		t.Fatalf("ParseOptionLine returned error: %v", err)
	}
	if label != "A" || content != "选项内容" {
		t.Fatalf("got (%q, %q), want (A, 选项内容)", label, content)
	}
}

func TestQuizExcel_ParseQuizRowChoice(t *testing.T) {
	row := [14]string{
		"X1001",
		"选择题标题",
		"以下关于指针的说法正确的是？",
		"",
		"",
		"选择题",
		"A. 指针变量保存地址{break}\nB. 指针变量只能保存整数{break}\nC. 指针不能参与比较",
		"A",
		"中等",
		"公开",
		"否",
		"pointer,C语言",
		"",
		"指针变量用于保存对象地址。",
	}
	q, err := ParseQuizRow(2, row)
	if err != nil {
		t.Fatalf("ParseQuizRow returned error: %v", err)
	}
	if q.Type != domain.QuizTypeChoice || len(q.Options) != 3 || q.Options[0].Label != "A" || q.Options[0].Content != "指针变量保存地址" {
		t.Fatalf("unexpected parsed quiz: %+v", q)
	}
	serialized := SerializeQuizRow(q)
	if serialized[0] != "X1001" || serialized[5] != "选择题" || serialized[7] != "A" {
		t.Fatalf("unexpected serialized row: %#v", serialized)
	}
}

func TestQuizExcel_ParseQuizRowPrefixMismatch(t *testing.T) {
	row := [14]string{
		"X1001", "标题", "题面", "", "", "编程题", "", "", "简单", "公开", "否", "", "", "",
	}
	if _, err := ParseQuizRow(2, row); err == nil {
		t.Fatalf("expected prefix/type mismatch error")
	}
}

func TestQuizExcel_ParseQuizRowInvalidJudgeAnswer(t *testing.T) {
	row := [14]string{
		"P1003", "判断题", "C 语言数组下标从 0 开始。", "", "", "判断题", "", "正确", "简单", "公开", "否", "", "", "",
	}
	if _, err := ParseQuizRow(2, row); err == nil {
		t.Fatalf("expected invalid judge answer error")
	}
}

func TestQuizExcel_SerializeRoundtrip(t *testing.T) {
	codeID := 75
	q := domain.QuizProblem{
		Code:       "C1000",
		Title:      "两数之和",
		Statement:  "读取两个整数并输出和。",
		Type:       domain.QuizTypeProgramming,
		CodeID:     &codeID,
		CodeHint:   "#include <stdio.h>",
		Difficulty: domain.QuizDifficultyEasy,
		Visibility: domain.QuizVisibilityPublic,
		IsVIP:      false,
		Tags:       []string{"C语言", "基础"},
		Langs:      []int{75, 76},
	}
	row := SerializeQuizRow(q)
	got, err := ParseQuizRow(2, row)
	if err != nil {
		t.Fatalf("ParseQuizRow returned error: %v", err)
	}
	if got.Code != q.Code || got.Type != q.Type || !reflect.DeepEqual(got.Langs, q.Langs) {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", got, q)
	}
}

// Synthetic cases exercise every objective type without shipping an imported
// workbook or historical question data.
func TestQuizExcel_SyntheticRoundtrip(t *testing.T) {
	rows := [][14]string{
		{"X9001", "Addition", "What is 1 + 1?", "", "", "选择题", "A.2{break}\nB.3", "A", "简单", "私有", "否", "arithmetic", "", "1 + 1 = 2."},
		{"T9001", "Fill", "Write the decimal representation of two.", "", "", "填空题", "", "2", "中等", "公开", "否", "", "", "The answer is 2."},
		{"P9001", "Judge", "The integer 2 is even.", "", "", "判断题", "", "对", "困难", "私有", "否", "", "", "2 is divisible by 2."},
	}
	for _, cells := range rows {
		q, err := ParseQuizRow(2, cells)
		if err != nil {
			t.Fatal(err)
		}
		got := SerializeQuizRow(q)
		if got != cells {
			t.Fatalf("roundtrip drift:\ngot=%#v\nwant=%#v", got, cells)
		}
	}
}
