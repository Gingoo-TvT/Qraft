package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

const ProblemSetPackageFormat = "algoforge.problem-set.v1"

type ProblemSetPackage struct {
	Profile  string
	FileName string
	Content  []byte
}
type problemSetExportManifest struct {
	Format      string                 `json:"format"`
	Code        string                 `json:"code"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Kind        domain.ProblemSetKind  `json:"kind"`
	Subject     string                 `json:"subject"`
	Tags        []string               `json:"tags"`
	TotalScore  int                    `json:"total_score"`
	Items       []problemSetExportItem `json:"items"`
}
type problemSetExportItem struct {
	Position     int                   `json:"position"`
	Score        int                   `json:"score"`
	Section      string                `json:"section,omitempty"`
	Notes        string                `json:"notes,omitempty"`
	Type         domain.QuizType       `json:"type"`
	Title        string                `json:"title"`
	Statement    string                `json:"statement"`
	HydroPackage string                `json:"hydro_package,omitempty"`
	Quiz         *problemSetExportQuiz `json:"quiz,omitempty"`
}
type problemSetExportQuiz struct {
	Code        string                `json:"code"`
	Options     []domain.QuizOption   `json:"options,omitempty"`
	Answers     []string              `json:"answers"`
	Explanation string                `json:"explanation"`
	Difficulty  domain.QuizDifficulty `json:"difficulty"`
	Tags        []string              `json:"tags"`
}

// buildProblemSetPackage deliberately projects content fields rather than
// serializing domain records: runtime IDs, provider configuration, audit state,
// and any unrelated workspace data cannot enter the portable manifest.
func buildProblemSetPackage(ctx context.Context, set *domain.ProblemSet, programming problemSetPackageBuilder) (*ProblemSetPackage, error) {
	if set == nil || len(set.Items) == 0 {
		return nil, fmt.Errorf("validation: problem set must contain at least one item")
	}
	manifest := problemSetExportManifest{Format: ProblemSetPackageFormat, Code: set.Code, Title: set.Title, Description: set.Description, Kind: set.Kind, Subject: set.Subject, Tags: set.Tags, TotalScore: sumItemScores(set.Items), Items: make([]problemSetExportItem, 0, len(set.Items))}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	write := func(name string, data []byte) error {
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	}
	quizzes := make([]domain.QuizProblem, 0)
	for index, item := range set.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := problemSetExportItem{Position: index + 1, Score: item.Score, Section: item.Section, Notes: item.Notes}
		switch {
		case item.Problem != nil && item.Quiz == nil:
			if programming == nil {
				return nil, fmt.Errorf("programming asset verification is unavailable")
			}
			pkg, err := programming.BuildProblemPackage(ctx, item.Problem.ID)
			if err != nil {
				return nil, fmt.Errorf("verifying programming item %d: %w", index+1, err)
			}
			if pkg == nil || len(pkg.Content) == 0 {
				return nil, fmt.Errorf("programming item %d returned an empty package", index+1)
			}
			entry.Type = domain.QuizTypeProgramming
			entry.Title = item.Problem.Title
			entry.Statement = item.Problem.Statement
			entry.HydroPackage = fmt.Sprintf("programming/%03d-hydro.zip", index+1)
			if err := write(entry.HydroPackage, pkg.Content); err != nil {
				return nil, err
			}
		case item.Quiz != nil && item.Problem == nil:
			q := *item.Quiz
			if q.Type == domain.QuizTypeProgramming {
				return nil, fmt.Errorf("validation: programming rows must reference a verified programming problem")
			}
			if err := validateQuizRow(q, index+1); err != nil {
				return nil, fmt.Errorf("validation: %w", err)
			}
			q.KnowledgePointIDs = nil
			quizzes = append(quizzes, q)
			entry.Type = q.Type
			entry.Title = q.Title
			entry.Statement = q.Statement
			entry.Quiz = &problemSetExportQuiz{Code: q.Code, Options: q.Options, Answers: q.Answers, Explanation: q.Explanation, Difficulty: q.Difficulty, Tags: q.Tags}
		default:
			return nil, fmt.Errorf("validation: item %d must have exactly one resolved source", index+1)
		}
		manifest.Items = append(manifest.Items, entry)
	}
	if len(quizzes) > 0 {
		workbook, err := BuildQuizWorkbook(quizzes)
		if err != nil {
			return nil, err
		}
		if err := write("quizzes.xlsx", workbook); err != nil {
			return nil, err
		}
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := write("problem-set.json", encoded); err != nil {
		return nil, err
	}
	readme := "Qraft problem-set package v1\n\nproblem-set.json preserves item order, sections, scores, statements and objective answers.\nprogramming/*.zip are individually verified Hydro programming packages.\nquizzes.xlsx is the AlgoForge objective-question workbook when objective items exist.\nThis is an AlgoForge interchange format; import the programming packages into a compatible Hydro service individually.\n"
	if err := write("README.txt", []byte(readme)); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return &ProblemSetPackage{Profile: ProblemSetPackageFormat, FileName: safeSetFilename(set.Code) + "-qraft.zip", Content: buffer.Bytes()}, nil
}
