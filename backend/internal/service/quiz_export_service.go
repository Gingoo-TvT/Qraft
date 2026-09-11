package service

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/xuri/excelize/v2"
)

type quizLister interface {
	List(context.Context, repository.QuizListFilter, int, int) ([]*domain.QuizProblem, int, error)
}
type QuizExportService struct{ repo quizLister }
type ExportRequest struct {
	Filter repository.QuizListFilter
	Writer io.Writer
}

func NewQuizExportService(repo *repository.QuizRepository) *QuizExportService {
	return &QuizExportService{repo: repo}
}
func newQuizExportServiceWithRepo(repo quizLister) *QuizExportService {
	return &QuizExportService{repo: repo}
}

// Export writes only the requested workspace records to an AlgoForge workbook.
func (s *QuizExportService) Export(ctx context.Context, req ExportRequest) error {
	if req.Writer == nil || s == nil || s.repo == nil {
		return fmt.Errorf("quiz export source and writer are required")
	}
	rows := make([]domain.QuizProblem, 0)
	for page := 1; ; page++ {
		items, total, err := s.repo.List(ctx, req.Filter, page, 500)
		if err != nil {
			return fmt.Errorf("listing quizzes for export: %w", err)
		}
		for _, q := range items {
			if q == nil {
				return fmt.Errorf("quiz export returned a nil row")
			}
			rows = append(rows, *q)
		}
		if len(items) == 0 || len(rows) >= total {
			break
		}
	}
	workbook, err := BuildQuizWorkbook(rows)
	if err != nil {
		return err
	}
	_, err = io.Copy(req.Writer, bytes.NewReader(workbook))
	return err
}

// BuildQuizWorkbook creates a new workbook entirely from public format rules.
// An empty input is the import template: no examples, hidden data, language
// catalogs, external links, or inherited third-party workbook bytes are bundled.
func BuildQuizWorkbook(rows []domain.QuizProblem) ([]byte, error) {
	if len(rows) > 1048575 {
		return nil, fmt.Errorf("too many workbook rows")
	}
	book := excelize.NewFile()
	defer book.Close()
	header := make([]interface{}, len(QuizExcelHeaders))
	for i, text := range QuizExcelHeaders {
		header[i] = text
	}
	if err := book.SetSheetRow(QuizExcelSheetName, "A1", &header); err != nil {
		return nil, err
	}
	for i, q := range rows {
		if q.Type == domain.QuizTypeProgramming {
			return nil, fmt.Errorf("row %d: programming questions require a verified Hydro package", i+2)
		}
		if err := validateQuizRow(q, i+2); err != nil {
			return nil, err
		}
		cells := SerializeQuizRow(q)
		values := make([]interface{}, len(cells))
		for j := range cells {
			values[j] = cells[j]
		}
		if err := book.SetSheetRow(QuizExcelSheetName, fmt.Sprintf("A%d", i+2), &values); err != nil {
			return nil, err
		}
	}
	style, err := book.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"2563EB"}, Pattern: 1}})
	if err != nil {
		return nil, err
	}
	if err := book.SetCellStyle(QuizExcelSheetName, "A1", "N1", style); err != nil {
		return nil, err
	}
	if err := book.SetColWidth(QuizExcelSheetName, "A", "N", 20); err != nil {
		return nil, err
	}
	if err := book.SetColWidth(QuizExcelSheetName, "C", "C", 60); err != nil {
		return nil, err
	}
	if err := book.SetPanes(QuizExcelSheetName, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return nil, err
	}
	result, err := book.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}
