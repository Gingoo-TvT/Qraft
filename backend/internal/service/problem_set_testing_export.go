package service

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
)

const (
	ProblemSetTestingGeneric = "generic"
	ProblemSetTestingHydro   = "hydro"
)

type testingProblemLoader interface {
	loadTestingProblem(context.Context, uuid.UUID) (*testingProblemAssets, error)
}

type testingSetManifest struct {
	Format     string           `json:"format"`
	Mode       string           `json:"mode"`
	Code       string           `json:"code"`
	Title      string           `json:"title"`
	TotalScore int              `json:"total_score"`
	Warning    string           `json:"warning"`
	Items      []testingSetItem `json:"items"`
}
type testingSetItem struct {
	problemSetExportItem
	SourceCode    string             `json:"source_code,omitempty"`
	ImportCode    string             `json:"import_code,omitempty"`
	DataDirectory string             `json:"data_directory,omitempty"`
	Rating        int                `json:"rating,omitempty"`
	Tags          []string           `json:"tags,omitempty"`
	HydroPID      string             `json:"hydro_pid,omitempty"`
	Directory     string             `json:"directory"`
	Validation    *testingValidation `json:"validation,omitempty"`
}
type testingValidation struct {
	Schema                   string `json:"schema"`
	SHA256                   string `json:"sha256"`
	TestCount                int    `json:"test_count"`
	DifferentialCheckedCount int    `json:"differential_checked_count,omitempty"`
}

// ExportTesting is explicitly separate from publication export. It snapshots
// all currently assembled items; planned quotas and prior-set reuse do not
// prevent testing. It never changes publication state or the export cooldown.
func (s *ProblemSetService) ExportTesting(ctx context.Context, id uuid.UUID, format string, options ...TestingExportOptions) (*ProblemSetExportResult, error) {
	if format != ProblemSetTestingGeneric && format != ProblemSetTestingHydro {
		return nil, fmt.Errorf("validation: testing format must be generic or hydro")
	}
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("problem set service is unavailable")
	}
	if err := s.requireSetAccess(ctx, id, false); err != nil {
		return nil, err
	}
	set, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if set.Generation.Active() {
		return nil, fmt.Errorf("%w: 题集仍在生成，请完成后下载测试包", ErrConflict)
	}
	if err := s.hydrateItems(ctx, set); err != nil {
		return nil, err
	}
	loader, _ := s.packageSvc.(testingProblemLoader)
	pkg, err := buildProblemSetTestingPackage(ctx, set, format, loader, options...)
	if err != nil {
		return nil, err
	}
	quality := &domain.ProblemSetQuality{
		Valid: true, ReadyForExport: false, ItemCount: len(set.Items), GeneratedAt: time.Now().UTC(),
		Warnings: []string{"仅供 OJ 测试，数据已核验；不代表题目已通过正式发布审核"},
	}
	entry := &domain.ProblemSetLedgerEntry{
		SetID: set.ID, EventType: "validated", SnapshotSHA256: problemSetSnapshotHash(set),
		KnowledgePointKeys: qualityKeys(set), ItemFingerprints: itemFingerprints(set),
		OverlapReport: map[string]interface{}{"operation": "testing_exported", "mode": "testing", "format": format, "quality": quality},
	}
	if err := s.repo.CreateLedgerEntry(ctx, entry); err != nil {
		return nil, err
	}
	return &ProblemSetExportResult{Package: pkg, Quality: quality, Ledger: entry}, nil
}

func buildProblemSetTestingPackage(ctx context.Context, set *domain.ProblemSet, format string, loader testingProblemLoader, options ...TestingExportOptions) (*ProblemSetPackage, error) {
	if format != ProblemSetTestingGeneric && format != ProblemSetTestingHydro {
		return nil, fmt.Errorf("validation: unsupported testing format")
	}
	if set == nil || len(set.Items) == 0 {
		return nil, fmt.Errorf("validation: 题集为空，无法导出测试包")
	}
	if format == ProblemSetTestingHydro {
		for i, item := range set.Items {
			if item.Quiz != nil {
				return nil, fmt.Errorf("validation: 第 %d 题为客观题；当前 Hydro 测试包仅支持纯编程题集，请下载通用 ZIP 保留所有题型", i+1)
			}
		}
	}
	manifest := testingSetManifest{
		Format: "qraft.problem-set.testing." + format + ".v1", Mode: "testing",
		Code: set.Code, Title: set.Title, TotalScore: sumItemScores(set.Items),
		Warning: "仅供 OJ 测试，不代表正式发布审核通过。题目分值与顺序见本清单；导入题库后仍须在目标 OJ 创建比赛并设置分值。",
		Items:   make([]testingSetItem, 0, len(set.Items)),
	}
	var exportOptions TestingExportOptions
	if len(options) > 0 {
		exportOptions = options[0]
	}
	if format == ProblemSetTestingGeneric {
		manifest.Format = "qraft.problem-set.testing.generic.v2"
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	defer zw.Close()
	workbookRows := []ojTestingRow{}
	for index, item := range set.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := testingSetItem{problemSetExportItem: problemSetExportItem{Position: index + 1, Score: item.Score, Section: item.Section, Notes: item.Notes}}
		entry.Directory = fmt.Sprintf("problems/%03d", index+1)
		if format == ProblemSetTestingHydro {
			entry.Directory = fmt.Sprintf("%03d", index+1)
		}
		prefix := entry.Directory + "/"
		switch {
		case item.Problem != nil && item.Quiz == nil:
			if loader == nil {
				return nil, fmt.Errorf("programming testing export is unavailable")
			}
			assets, err := loader.loadTestingProblem(ctx, item.Problem.ID)
			if err != nil {
				return nil, fmt.Errorf("validation: 第 %d 题（%s）: %w", index+1, item.Problem.Title, err)
			}
			entry.Type, entry.Title, entry.Statement = domain.QuizTypeProgramming, assets.request.Problem.Title, assets.request.Problem.Statement
			entry.Validation = &assets.validation
			entry.Rating, entry.Tags = assets.request.Problem.Difficulty, assets.request.Problem.Tags
			entry.SourceCode = assets.request.Problem.SerialNumber
			if format == ProblemSetTestingHydro {
				req := assets.request
				// Stable, distinct Hydro IDs preserve set positions even when
				// the same source problem occurs twice in the set.
				entry.HydroPID = fmt.Sprintf("Q%sP%03d", sha256Hex([]byte(set.ID.String() + ":" + set.Code))[:8], index+1)
				problemCopy := *req.Problem
				var metadata map[string]interface{}
				if err := json.Unmarshal(problemCopy.MetadataJSON, &metadata); err != nil {
					return nil, err
				}
				metadata["hydro_pid"] = entry.HydroPID
				if nested, ok := metadata["hydro"].(map[string]interface{}); ok {
					nested["pid"] = entry.HydroPID
				}
				problemCopy.MetadataJSON, err = json.Marshal(metadata)
				if err != nil {
					return nil, err
				}
				req.Problem = &problemCopy
				req.Prefix = prefix
				if err := writeHydroProblemEntries(ctx, zw, req); err != nil {
					return nil, fmt.Errorf("validation: 第 %d 题: %w", index+1, err)
				}
				if err := writeTestingJSON(zw, prefix+"additional_file/qraft_testing.json", map[string]interface{}{
					"mode": "testing", "position": index + 1, "score": item.Score, "validation": entry.Validation,
				}); err != nil {
					return nil, err
				}
			} else {
				entry.ImportCode = testingOJCode(set, index, domain.QuizTypeProgramming)
				entry.DataDirectory = "datas/" + entry.ImportCode
				subtasks, _, buildErr := buildHydroSubtasksForTestManifestV2(assets.request.Problem.ID, assets.cases, assets.request.TestManifestV2)
				if buildErr != nil {
					return nil, buildErr
				}
				_, metadata, metaErr := hydroIdentity(assets.request.Problem)
				if metaErr != nil {
					return nil, metaErr
				}
				workbookRows = append(workbookRows, ojTestingRow{Code: entry.ImportCode, Problem: assets.request.Problem, Subtasks: subtasks, Metadata: metadata})
				if err := writeGenericTestingProblem(zw, prefix, entry.DataDirectory, assets); err != nil {
					return nil, fmt.Errorf("validation: 第 %d 题: %w", index+1, err)
				}
			}
		case item.Quiz != nil && item.Problem == nil:
			q := *item.Quiz
			if q.Type == domain.QuizTypeProgramming {
				return nil, fmt.Errorf("validation: 第 %d 题缺少已验证的编程题数据", index+1)
			}
			if err := validateQuizRow(q, index+1); err != nil {
				return nil, fmt.Errorf("validation: %w", err)
			}
			q.KnowledgePointIDs = nil
			entry.ImportCode = testingOJCode(set, index, q.Type)
			entry.Tags = q.Tags
			entry.SourceCode = q.Code
			workbookRows = append(workbookRows, ojTestingRow{Code: entry.ImportCode, Quiz: &q})
			entry.Type, entry.Title, entry.Statement = q.Type, q.Title, q.Statement
			entry.Quiz = &problemSetExportQuiz{Code: q.Code, Options: q.Options, Answers: q.Answers, Explanation: q.Explanation, Difficulty: q.Difficulty, Tags: q.Tags}
			if err := writeZipFile(zw, prefix+"statement.md", []byte(q.Statement)); err != nil {
				return nil, err
			}
			if err := writeTestingJSON(zw, prefix+"question.json", map[string]interface{}{"item": entry, "code_id": q.CodeID, "code_hint": q.CodeHint, "langs": q.Langs}); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("validation: 第 %d 题必须且只能关联一个完整题目", index+1)
		}
		manifest.Items = append(manifest.Items, entry)
	}
	var notices []ojExportNotice
	if format == ProblemSetTestingGeneric {
		var workbook []byte
		var err error
		workbook, notices, err = buildOJTestingWorkbook(workbookRows, exportOptions)
		if err != nil {
			return nil, err
		}
		if err := writeZipFile(zw, "problems.xlsx", workbook); err != nil {
			return nil, err
		}
	}
	if format == ProblemSetTestingGeneric {
		// Notes contain only the set's values and matches, never the supplied catalog.
		if err := writeTestingJSON(zw, "export-notes.json", notices); err != nil {
			return nil, err
		}
	}
	if err := writeTestingJSON(zw, "problem-set.json", manifest); err != nil {
		return nil, err
	}
	readme := "Qraft 题集测试包\n仅供测试，不表示正式发布审核通过。题号按目录 001、002 的题集顺序排列，分值、章节、客观题答案和验证摘要见 problem-set.json。\n"
	if format == ProblemSetTestingHydro {
		readme += "在 Hydro 题库的导入入口选择 Hydro 格式并上传本 ZIP。每个根目录是一道编程题，含 problem.yaml、problem_zh.md、testdata/config.yaml 和输入输出。题集分值须在创建比赛时按清单另行设置；此包不自动创建比赛。\n"
	} else {
		readme += "通用包按 Excel 模板版本 2 组织：根目录 problems.xlsx 的题目列表包含全部题型，测试点配置保留 sum/min 计分；编程题输入输出位于 datas/题目编号/。请上传整个 ZIP。附加题面与验证摘要保留在 problems/题序/；judge.json 的测试点路径相对 ZIP 根目录。标签仅写入本次目录可唯一确认的有效 ID；未匹配项和原始难度见导出说明及 export-notes.json。导入后仍需在目标 OJ 核对题目、判题和比赛分值。\n"
	}
	readme += "仅支持普通文本输出比较；不支持特殊判题、交互题或输出答案题。勿将含答案和测试数据的整包发给参赛者。\n"
	if err := writeZipFile(zw, "README.txt", []byte(readme)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return &ProblemSetPackage{Profile: manifest.Format, FileName: safeSetFilename(set.Code) + "-" + format + "-testing.zip", Content: buf.Bytes()}, nil
}

func writeTestingJSON(zw *zip.Writer, name string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeZipFile(zw, name, append(data, '\n'))
}

func writeGenericTestingProblem(zw *zip.Writer, prefix, dataDirectory string, assets *testingProblemAssets) error {
	p := assets.request.Problem
	cases := assets.cases
	subtasks, _, err := buildHydroSubtasksForTestManifestV2(p.ID, cases, assets.request.TestManifestV2)
	if err != nil {
		return err
	}
	_, meta, err := hydroIdentity(p)
	if err != nil {
		return err
	}
	if err := validateHydroMetadata(meta); err != nil {
		return err
	}
	if err := writeZipFile(zw, prefix+"statement.md", []byte(p.Statement)); err != nil {
		return err
	}
	entries := buildHydroManifest(p, "", time.Time{}, p.Statement, assets.validation.SHA256, cases).TestCases
	for i := range entries {
		entries[i].InputFile = dataDirectory + "/" + cases[i].inputFile
		entries[i].OutputFile = dataDirectory + "/" + cases[i].outputFile
	}
	if err := writeTestingJSON(zw, prefix+"judge.json", map[string]interface{}{
		"type": "standard", "checker": "default", "time_limit_ms": p.TimeLimit, "memory_limit_mb": p.MemoryLimit,
		"filename": meta.Filename, "test_cases": entries, "subtasks": subtasks, "validation": assets.validation,
		"statement_sha256": sha256Hex([]byte(p.Statement)),
	}); err != nil {
		return err
	}
	for _, c := range cases {
		if err := writeZipFile(zw, dataDirectory+"/"+c.inputFile, c.inputData); err != nil {
			return err
		}
		if err := writeZipFile(zw, dataDirectory+"/"+c.outputFile, c.outputData); err != nil {
			return err
		}
	}
	return nil
}

// Stable per-set position codes avoid collisions when a set contains repeated
// source questions. The type prefix matches the target template contract.
func testingOJCode(set *domain.ProblemSet, index int, kind domain.QuizType) string {
	value, _ := strconv.ParseUint(sha256Hex([]byte(set.ID.String() + ":" + set.Code))[:8], 16, 32)
	return fmt.Sprintf("%s%d%04d", kind.CodePrefix(), value+1000, index+1)
}
