package service

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

type quizBulkCreator interface {
	BulkCreate(ctx context.Context, qs []*domain.QuizProblem, onConflict string) (inserted, skipped int, err error)
}

type QuizImportService struct {
	repo   quizBulkCreator
	kpRepo *repository.KnowledgePointRepository
}

type ImportRequest struct {
	File       io.Reader
	OnConflict string
	Subject    string
}

type ImportReport struct {
	SuccessCount int              `json:"success_count"`
	FailedCount  int              `json:"failed_count"`
	InsertedIDs  []uuid.UUID      `json:"inserted_ids"`
	FailedRows   []ImportRowError `json:"failed_rows"`
}

type ImportRowError struct {
	RowIndex int    `json:"row_index"`
	Code     string `json:"code"`
	Reason   string `json:"reason"`
}

func NewQuizImportService(repo *repository.QuizRepository, kpRepo *repository.KnowledgePointRepository) *QuizImportService {
	return &QuizImportService{repo: repo, kpRepo: kpRepo}
}

func newQuizImportServiceWithRepo(repo quizBulkCreator) *QuizImportService {
	return &QuizImportService{repo: repo}
}

func (s *QuizImportService) Import(ctx context.Context, req ImportRequest) (*ImportReport, error) {
	if req.File == nil {
		return nil, fmt.Errorf("file is required")
	}
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		return nil, fmt.Errorf("subject is required")
	}
	onConflict := strings.TrimSpace(req.OnConflict)
	if onConflict == "" {
		onConflict = "error"
	}
	if onConflict != "error" && onConflict != "skip" && onConflict != "update" {
		return nil, fmt.Errorf("unsupported on_conflict: %s", onConflict)
	}

	xlsx, err := excelize.OpenReader(req.File)
	if err != nil {
		return nil, fmt.Errorf("opening quiz workbook: %w", err)
	}
	defer xlsx.Close()

	rows, err := xlsx.GetRows(QuizExcelSheetName)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", QuizExcelSheetName, err)
	}
	if len(rows) < QuizExcelHeaderRow {
		return nil, fmt.Errorf("quiz workbook missing header row")
	}
	if err := validateQuizExcelHeader(rows[QuizExcelHeaderRow-1]); err != nil {
		return nil, err
	}

	report := &ImportReport{}
	seenCodes := make(map[string]int)
	batch := make([]*domain.QuizProblem, 0, len(rows)-1)

	for idx := QuizExcelDataStartRow - 1; idx < len(rows); idx++ {
		rowIndex := idx + 1
		cells := fixedQuizCells(rows[idx])
		if isQuizRowEmpty(cells) {
			break
		}
		code := normalizeQuizCell(cells[0])
		if strings.HasPrefix(code, "说明：") {
			continue
		}
		if firstRow, exists := seenCodes[code]; exists {
			report.FailedRows = append(report.FailedRows, ImportRowError{
				RowIndex: rowIndex,
				Code:     code,
				Reason:   fmt.Sprintf("题目编号重复，首次出现在第 %d 行", firstRow),
			})
			continue
		}

		q, err := ParseQuizRow(rowIndex, cells)
		if err != nil {
			report.FailedRows = append(report.FailedRows, ImportRowError{
				RowIndex: rowIndex,
				Code:     code,
				Reason:   err.Error(),
			})
			continue
		}
		seenCodes[q.Code] = rowIndex
		q.Subject = subject
		batch = append(batch, &q)
	}

	if len(batch) > 0 {
		if s.repo == nil {
			return nil, fmt.Errorf("quiz repository is required")
		}
		inserted, skipped, err := s.repo.BulkCreate(ctx, batch, onConflict)
		if err != nil {
			report.FailedRows = append(report.FailedRows, ImportRowError{
				RowIndex: rowIndexForQuizCode(rows, batch[0].Code),
				Code:     batch[0].Code,
				Reason:   err.Error(),
			})
			report.FailedCount = len(report.FailedRows)
			return report, nil
		}
		report.SuccessCount = inserted
		if skipped == 0 && onConflict == "error" {
			report.InsertedIDs = make([]uuid.UUID, 0, len(batch))
			for _, q := range batch {
				report.InsertedIDs = append(report.InsertedIDs, q.ID)
			}
		}
	}

	report.FailedCount = len(report.FailedRows)
	return report, nil
}

func validateQuizExcelHeader(row []string) error {
	if len(row) < QuizExcelExpectedCols {
		return fmt.Errorf("quiz workbook header has %d columns, want %d", len(row), QuizExcelExpectedCols)
	}
	for i, want := range QuizExcelHeaders {
		got := normalizeQuizCell(row[i])
		if got != want {
			return fmt.Errorf("quiz workbook header column %d = %q, want %q", i+1, got, want)
		}
	}
	return nil
}

func fixedQuizCells(row []string) [14]string {
	var cells [14]string
	for i := 0; i < len(cells) && i < len(row); i++ {
		cells[i] = normalizeQuizCell(row[i])
	}
	return cells
}

func isQuizRowEmpty(cells [14]string) bool {
	for _, cell := range cells {
		if normalizeQuizCell(cell) != "" {
			return false
		}
	}
	return true
}

func rowIndexForQuizCode(rows [][]string, code string) int {
	for i := QuizExcelDataStartRow - 1; i < len(rows); i++ {
		cells := fixedQuizCells(rows[i])
		if normalizeQuizCell(cells[0]) == code {
			return i + 1
		}
	}
	return 0
}
