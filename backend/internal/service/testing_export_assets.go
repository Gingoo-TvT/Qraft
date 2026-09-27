package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
)

type testingProblemAssets struct {
	request    HydroPackageRequest
	cases      []hydroExportCase
	validation testingValidation
}

// Each object is read once; integrity verification and ZIP writing see the
// same bytes even when underlying object storage changes during a download.
type testingObjectSnapshot struct {
	source HydroObjectReader
	data   map[string][]byte
}

func (s *testingObjectSnapshot) DownloadFile(ctx context.Context, name string) ([]byte, error) {
	if data, ok := s.data[name]; ok {
		return append([]byte(nil), data...), nil
	}
	data, err := s.source.DownloadFile(ctx, name)
	if err != nil {
		return nil, err
	}
	s.data[name] = append([]byte(nil), data...)
	return append([]byte(nil), data...), nil
}

func (s *HydroExportService) loadTestingProblem(ctx context.Context, id uuid.UUID) (*testingProblemAssets, error) {
	if s == nil || s.problems == nil || s.objects == nil {
		return nil, fmt.Errorf("testing export is unavailable")
	}
	p, err := s.problems.GetProblem(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil || p.ID != id || id == uuid.Nil {
		return nil, fmt.Errorf("%w: 题目身份不完整", ErrConflict)
	}
	if p.Status != domain.ProblemStatusDraft && p.Status != domain.ProblemStatusReview && p.Status != domain.ProblemStatusPublished {
		return nil, fmt.Errorf("%w: 当前题目状态 %s 不可导出测试数据", ErrConflict, p.Status)
	}
	if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Statement) == "" || p.TimeLimit <= 0 || p.MemoryLimit <= 0 {
		return nil, fmt.Errorf("%w: 题面或时间/内存限制不完整", ErrConflict)
	}
	metadata, err := hydroExportGateMetadata(p.MetadataJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: 题目验证信息损坏", ErrConflict)
	}
	if hydroMetadataBool(metadata, "stale") || hydroMetadataBool(metadata, "publication_quarantined") || hydroMetadataString(metadata, "publication_quarantine_reason") != "" {
		return nil, fmt.Errorf("%w: 题目修改后尚未重新验证，或仍在隔离中", ErrConflict)
	}
	var importMetadata struct {
		ImportSource *struct {
			FinalSHA256 string `json:"final_sha256"`
		} `json:"import_source"`
	}
	if err := json.Unmarshal(p.MetadataJSON, &importMetadata); err != nil {
		return nil, fmt.Errorf("%w: 题目元数据无效", ErrConflict)
	}
	if importMetadata.ImportSource != nil && importMetadata.ImportSource.FinalSHA256 != sha256Hex([]byte(p.Statement)) {
		return nil, fmt.Errorf("%w: 导入题面已变化，须重新验证测试数据", ErrConflict)
	}
	if err := validateHydroProductMetadataV2(p.MetadataJSON); err != nil {
		return nil, err
	}
	var stored struct {
		PayloadVersion int                         `json:"activity_payload_version"`
		Manifest       *s3ExportManifestMetadataV1 `json:"test_manifest"`
	}
	if err := json.Unmarshal(p.MetadataJSON, &stored); err != nil || stored.Manifest == nil {
		return nil, fmt.Errorf("%w: 缺少测试数据验证清单，请先生成数据并完成沙箱验证", ErrConflict)
	}
	tests, err := s.problems.GetTestCases(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("%w: 缺少测试数据，请先生成并验证", ErrConflict)
	}
	snapshot := &testingObjectSnapshot{source: s.objects, data: map[string][]byte{}}
	assets := &testingProblemAssets{request: HydroPackageRequest{Problem: p, TestCases: tests, ObjectReader: snapshot, GeneratedAt: resolveHydroGeneratedAt(p, p.UpdatedAt)}}
	fail := func(message string) (*testingProblemAssets, error) {
		return nil, fmt.Errorf("%w: 测试数据校验失败：%s；请重新生成或验证数据", ErrConflict, message)
	}
	switch stored.Manifest.SchemaVersion {
	case activities.TestManifestSchemaVersion:
		ref := stored.Manifest
		if stored.PayloadVersion < activities.StoreProblemTestManifestPayloadVersion || ref.Path != fmt.Sprintf("problems/%s/test_manifest.v1.json", id) || !isS3ExportSHA256V1(ref.SHA256) {
			return fail("验证清单身份不匹配")
		}
		data, err := snapshot.DownloadFile(ctx, ref.Path)
		if err != nil {
			return fail("无法读取验证清单")
		}
		if sha256Hex(data) != ref.SHA256 {
			return fail("验证清单 SHA-256 不匹配")
		}
		manifest, err := activities.ParseTestManifestJSON(data)
		if err != nil {
			return fail("验证清单格式无效")
		}
		if manifest.TestCount != len(tests) || manifest.DifferentialCheckedCount < 1 {
			return fail("数据数量或差分验证证据不完整")
		}
		assets.cases, err = prepareHydroCases(ctx, tests, snapshot)
		if err != nil {
			return fail("输入输出文件缺失或不可读取")
		}
		hasNonSample := false
		for i, c := range assets.cases {
			mc := manifest.Cases[i]
			if c.tc.ProblemID != id || c.tc.TestIndex != i || c.tc.GroupID != mc.GroupID || c.tc.IsSample != mc.IsSample || c.inputSHA256 != mc.InputSHA256 || c.outputSHA256 != mc.ExpectedOutputSHA256 {
				return fail(fmt.Sprintf("第 %d 组数据与沙箱验证结果不一致", i+1))
			}
			if !c.tc.IsSample {
				hasNonSample = true
			}
		}
		if !hasNonSample {
			return fail("只有样例，缺少正式测试点")
		}
		assets.validation = testingValidation{Schema: manifest.SchemaVersion, SHA256: ref.SHA256, TestCount: manifest.TestCount, DifferentialCheckedCount: manifest.DifferentialCheckedCount}
	case activities.TestManifestSchemaVersionV2:
		binding, err := loadS3ExportBindingV1(ctx, p, tests, snapshot)
		if err != nil {
			return nil, err
		}
		assets.request.TestManifestV2 = binding.TestManifestV2
		assets.cases, err = prepareHydroCases(ctx, tests, snapshot)
		if err != nil {
			return fail("输入输出文件缺失或不可读取")
		}
		if _, _, err := buildHydroSubtasksForTestManifestV2(id, assets.cases, binding.TestManifestV2); err != nil {
			return nil, err
		}
		assets.validation = testingValidation{Schema: activities.TestManifestSchemaVersionV2, SHA256: stored.Manifest.SHA256, TestCount: len(tests)}
	default:
		return fail("不支持的验证清单版本")
	}
	return assets, nil
}
