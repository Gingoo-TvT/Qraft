package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/xuri/excelize/v2"
)

type fakeQuizBulkRepo struct {
	quizzes    []*domain.QuizProblem
	onConflict string
	err        error
}

func (f *fakeQuizBulkRepo) BulkCreate(ctx context.Context, qs []*domain.QuizProblem, onConflict string) (int, int, error) {
	f.quizzes = append([]*domain.QuizProblem(nil), qs...)
	f.onConflict = onConflict
	if f.err != nil {
		return 0, 0, f.err
	}
	return len(qs), 0, nil
}

func TestQuizImport_InvalidRowKeepsValidRows(t *testing.T) {
	valid := validChoiceImportRow()
	invalid := valid
	invalid[0] = "X1002"
	invalid[5] = "Choice"

	repo := &fakeQuizBulkRepo{}
	report, err := newQuizImportServiceWithRepo(repo).Import(context.Background(), ImportRequest{
		File:       workbookWithRows(t, QuizExcelHeaders, invalid[:], valid[:]),
		OnConflict: "skip",
		Subject:    domain.QuizSubjectCLanguage,
	})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if report.SuccessCount != 1 || report.FailedCount != 1 {
		t.Fatalf("report = %+v, want success=1 failed=1", report)
	}
	if repo.onConflict != "skip" {
		t.Fatalf("onConflict = %q, want skip", repo.onConflict)
	}
	if len(repo.quizzes) != 1 || repo.quizzes[0].Code != "X1001" {
		t.Fatalf("repo quizzes = %+v, want X1001 only", repo.quizzes)
	}
}

func TestQuizImport_HeaderMismatch(t *testing.T) {
	headers := append([]string(nil), QuizExcelHeaders...)
	headers[1] = "题目名称"
	row := validChoiceImportRow()
	_, err := newQuizImportServiceWithRepo(&fakeQuizBulkRepo{}).Import(context.Background(), ImportRequest{
		File:    workbookWithRows(t, headers, row[:]),
		Subject: domain.QuizSubjectCLanguage,
	})
	if err == nil {
		t.Fatalf("expected header mismatch error")
	}
}

func TestQuizImport_DuplicateCodeInFile(t *testing.T) {
	first := validChoiceImportRow()
	second := first
	second[2] = "重复编号的另一道题面"

	repo := &fakeQuizBulkRepo{}
	report, err := newQuizImportServiceWithRepo(repo).Import(context.Background(), ImportRequest{
		File:    workbookWithRows(t, QuizExcelHeaders, first[:], second[:]),
		Subject: domain.QuizSubjectCLanguage,
	})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if report.SuccessCount != 1 || report.FailedCount != 1 {
		t.Fatalf("report = %+v, want success=1 failed=1", report)
	}
	if report.FailedRows[0].Code != "X1001" {
		t.Fatalf("failed code = %q, want X1001", report.FailedRows[0].Code)
	}
}

func validChoiceImportRow() [14]string {
	return [14]string{
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
}

func workbookWithRows(t *testing.T, headers []string, rows ...[]string) *bytes.Reader {
	t.Helper()
	xlsx := excelize.NewFile()
	defer xlsx.Close()
	for col, header := range headers {
		cell, err := excelize.CoordinatesToCellName(col+1, 1)
		if err != nil {
			t.Fatalf("header cell: %v", err)
		}
		if err := xlsx.SetCellValue(QuizExcelSheetName, cell, header); err != nil {
			t.Fatalf("set header cell: %v", err)
		}
	}
	for rowIdx, row := range rows {
		for col, value := range row {
			cell, err := excelize.CoordinatesToCellName(col+1, rowIdx+2)
			if err != nil {
				t.Fatalf("row cell: %v", err)
			}
			if err := xlsx.SetCellValue(QuizExcelSheetName, cell, value); err != nil {
				t.Fatalf("set row cell: %v", err)
			}
		}
	}
	var buf bytes.Buffer
	if err := xlsx.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	return bytes.NewReader(buf.Bytes())
}
