package activities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"go.temporal.io/sdk/activity"
)

const autoApprovalQuarantineReason = "public_release denied by current provenance policy"

// StoreProblemActivity persists the fully validated problem, its solutions,
// test data, and metadata. It performs the following steps:
//  1. Creates the Problem record in the database
//  2. Creates Solution records for main and brute-force solutions
//  3. Uploads test data (inputs and outputs) to MinIO
//  4. Creates TestCase records in the database referencing the MinIO paths
//  5. Generates and stores metadata/solution files in MinIO
//  6. Generates an embedding for the problem and stores it in the vector store
//  7. Applies the publication gate, publishing or quarantining the problem
func (a *Activities) StoreProblemActivity(ctx context.Context, input StoreInput) (result *StoreResult, retErr error) {
	if err := validateStoreProblemPayloadVersion(input.PayloadVersion); err != nil {
		return nil, err
	}
	if err := validateKnowledgePointStorePayload(input); err != nil {
		return nil, err
	}
	if err := validateGenerationEvidenceStorePayload(input); err != nil {
		return nil, err
	}
	if err := a.validateS3QualityPassDraftStoreInputV1(ctx, input); err != nil {
		return nil, err
	}
	canonicalTags, err := conformStatementKnowledgePoints(input.Params, input.Statement.Tags)
	if err != nil {
		return nil, err
	}
	input.Statement.Tags = canonicalTags
	if err := validateActivityPayloadVersion(input.SandboxOutput.PayloadVersion); err != nil {
		return nil, fmt.Errorf("sandbox output: %w", err)
	}
	testManifestJSON, testManifestSHA256, err := prepareTestManifestForStore(input)
	if err != nil {
		return nil, err
	}
	if err := a.validateTestManifestStoreInput(ctx, input); err != nil {
		return nil, err
	}
	reviewJSON, sourceAncestryJSON, err := validateReviewQuarantineEvidence(input)
	if err != nil {
		return nil, err
	}

	logger := activity.GetLogger(ctx)
	logger.Info("storing problem",
		"title", input.Statement.Title,
		"test_count", len(input.TestCases),
	)

	now := time.Now()
	problemID := uuid.New()
	isVersioned := isVersionedStorePayload(input.PayloadVersion)
	var inputHash string
	operationStarted := false
	if isVersioned {
		if input.IdempotencyKey == "" {
			return nil, fmt.Errorf("idempotency key is required for payload version %d", input.PayloadVersion)
		}
		if input.SandboxOutput.PayloadVersion != ActivityPayloadVersion {
			return nil, fmt.Errorf("sandbox output payload version %d does not match store payload version %d", input.SandboxOutput.PayloadVersion, input.PayloadVersion)
		}
		for i, tc := range input.TestCases {
			if tc.InputRef != "" {
				return nil, fmt.Errorf("test input %d uses forbidden worker-local legacy ref", i)
			}
		}
		for i, ref := range input.SandboxOutput.OutputRefs {
			if ref != "" {
				return nil, fmt.Errorf("sandbox output %d uses forbidden worker-local legacy ref", i)
			}
		}
		for i, ref := range input.SourceArtifacts {
			if ref == nil {
				return nil, fmt.Errorf("source artifact %d is nil", i)
			}
			if err := ref.Validate(a.deps.MinioBucket); err != nil {
				return nil, fmt.Errorf("source artifact %d: %w", i, err)
			}
		}
		var err error
		inputHash, err = storeInputHash(input)
		if err != nil {
			return nil, err
		}
		problemID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("algoforge:store-problem:"+input.IdempotencyKey))
		operation, err := a.beginOperation(ctx, input.IdempotencyKey, "store_problem/v1", inputHash)
		if err != nil {
			return nil, err
		}
		if operation.Status == repository.OperationStatusCompleted {
			var cached StoreResult
			if err := json.Unmarshal(operation.ResultJSON, &cached); err != nil {
				return nil, fmt.Errorf("decoding cached store problem result: %w", err)
			}
			return &cached, nil
		}
		operationStarted = true
		defer func() {
			if retErr != nil && operationStarted {
				a.recordOperationFailure(ctx, input.IdempotencyKey, inputHash, retErr)
			}
		}()
		if input.PayloadVersion == StoreProblemStandardEvidencePayloadVersion {
			recovered, ok, recoverErr := a.recoverGenerationStandardEvidenceResult(
				ctx, input, problemID, testManifestSHA256,
			)
			if recoverErr != nil {
				return nil, recoverErr
			}
			if ok {
				if err := a.completeOperation(ctx, input.IdempotencyKey, inputHash, recovered); err != nil {
					return nil, err
				}
				return recovered, nil
			}
		}
	}

	testManifestPath := ""
	if len(testManifestJSON) > 0 {
		manifestVersion := "v1"
		if input.PayloadVersion == StoreProblemS3QualityDraftPayloadVersion {
			manifestVersion = "v2"
		}
		testManifestPath = fmt.Sprintf("problems/%s/test_manifest.%s.json", problemID.String(), manifestVersion)
	}
	upload := a.uploadToMinIO
	if isVersioned {
		upload = func(ctx context.Context, objectPath string, data []byte) error {
			return a.runOperationStep(
				ctx,
				input.IdempotencyKey,
				"minio:"+objectPath,
				sha256Bytes(data),
				func() error { return a.uploadToMinIO(ctx, objectPath, data) },
			)
		}
	}

	// -----------------------------------------------------------------------
	// 1. Create Problem record in the database
	// -----------------------------------------------------------------------

	activity.RecordHeartbeat(ctx, "creating problem record")
	// Keep the persisted/public hint compatible with the UI, which renders it
	// as plain text. Apply this only after the idempotency input hash has been
	// computed so retries of older workflow payloads retain their original
	// operation identity.
	publicHint := NormalizeOneLineHintV1(input.Statement.OneLineHint)
	editorial := input.Editorial
	if strings.TrimSpace(editorial) != "" {
		// The editorial column is Markdown, not the provider's JSON transport
		// envelope.  Normalize before persistence so newly generated records do
		// not reproduce the historical double-encoded payload.  When editorial
		// generation was requested, reject an invalid envelope rather than
		// silently storing content that the UI cannot render correctly.
		if normalized, parseErr := domain.ParseEditorialMarkdownV1(editorial); parseErr != nil {
			if input.Params.GenerateEditorial {
				return nil, fmt.Errorf("normalizing editorial: %w", parseErr)
			}
			editorial = domain.NormalizeEditorialMarkdownV1(editorial)
		} else {
			editorial = normalized
		}
	}

	problem := &domain.Problem{
		ID:               problemID,
		Title:            input.Statement.Title,
		Statement:        input.Statement.Statement,
		Level:            input.Params.Level,
		Difficulty:       input.Params.Difficulty,
		Tags:             input.Statement.Tags,
		OneLineHint:      publicHint,
		DetailedSolution: editorial,
		TimeLimit:        input.Params.TimeLimit,
		MemoryLimit:      input.Params.MemoryLimit,
		Source:           "algoforge_workflow",
		Status:           initialStoreProblemStatus(input),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if input.WorkflowID != "" {
		problem.WorkflowID = &input.WorkflowID
	}

	// Generate metadata JSON.
	metadataBytes, err := problem.MetadataJSONBytes()
	if err != nil {
		return nil, fmt.Errorf("generating metadata JSON: %w", err)
	}
	// Merge MetadataExtras (e.g. GPLT batch_id, tier) into the stored metadata.
	var meta map[string]interface{}
	if err := json.Unmarshal(metadataBytes, &meta); err != nil {
		return nil, fmt.Errorf("decoding generated metadata: %w", err)
	}
	for key, value := range input.Params.MetadataExtras {
		meta[key] = value
	}
	if conformance := knowledgePointConformanceMetadata(input.Params, input.Statement.Tags); conformance != nil {
		meta[KnowledgePointConformanceMetadataKey] = conformance
	}
	if isVersioned {
		meta["store_idempotency_key"] = input.IdempotencyKey
		meta["store_input_sha256"] = inputHash
		meta["activity_payload_version"] = input.PayloadVersion
	}
	if len(input.SourceArtifacts) > 0 {
		meta["source_artifacts"] = input.SourceArtifacts
	}
	if len(input.DedupReports) > 0 {
		meta["dedup_reports"] = input.DedupReports
	}
	if input.ReviewQuarantine != nil {
		meta["review_quarantine"] = map[string]interface{}{
			"reason":                input.ReviewQuarantine.Reason,
			"workflow_run_id":       input.ReviewQuarantine.WorkflowRunID,
			"review_gate_change_id": input.ReviewQuarantine.ReviewGateChangeID,
			"review_gate_version":   input.ReviewQuarantine.ReviewGateVersion,
			"review_result_sha256":  input.ReviewQuarantine.ReviewResultSHA256,
		}
	}
	if testManifestPath != "" {
		manifestSchema := TestManifestSchemaVersion
		if input.PayloadVersion == StoreProblemS3QualityDraftPayloadVersion {
			manifestSchema = TestManifestSchemaVersionV2
		}
		meta["test_manifest"] = map[string]interface{}{
			"schema_version": manifestSchema,
			"sha256":         testManifestSHA256,
			"path":           testManifestPath,
		}
	}
	if input.QualityPassDraft != nil {
		meta[generationapi.S3QualityMaterializationMetadataKey] = input.QualityPassDraft
		if manifest, ok := meta["test_manifest"].(map[string]interface{}); ok {
			manifest["artifact"] = input.QualityPassDraft.TestManifestArtifact
		}
	}
	if input.Params.GenerationEvidence != nil {
		meta[domain.GenerationStandardEvidenceRequestMetaKey] = input.Params.GenerationEvidence
	}
	metadataBytes, err = json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("encoding problem metadata: %w", err)
	}
	problem.MetadataJSON = metadataBytes

	problemExists := false
	if isVersioned {
		existing, err := a.deps.ProblemRepo.GetByID(ctx, problemID)
		switch {
		case err == nil:
			if err := verifyStoredOperation(existing.MetadataJSON, input.IdempotencyKey, inputHash); err != nil {
				return nil, err
			}
			problemExists = true
			problem.SerialNumber = existing.SerialNumber
			problem.CreatedAt = existing.CreatedAt
		case errors.Is(err, sql.ErrNoRows):
			// The first delivery creates the stable problem row below.
		default:
			return nil, fmt.Errorf("checking idempotent problem record: %w", err)
		}
	}

	// Insert problem into the database. A racing duplicate delivery fetches and
	// verifies the stable row instead of creating a second problem.
	if !problemExists {
		if err := a.deps.ProblemRepo.Create(ctx, problem); err != nil {
			if !isVersioned {
				return nil, fmt.Errorf("inserting problem into database: %w", err)
			}
			existing, getErr := a.deps.ProblemRepo.GetByID(ctx, problemID)
			if getErr != nil {
				return nil, fmt.Errorf("inserting problem into database: %w", err)
			}
			if verifyErr := verifyStoredOperation(existing.MetadataJSON, input.IdempotencyKey, inputHash); verifyErr != nil {
				return nil, verifyErr
			}
			problem.SerialNumber = existing.SerialNumber
			problem.CreatedAt = existing.CreatedAt
		}
	}

	// Read back the auto-generated serial_number.
	serialNumber := problem.SerialNumber

	// -----------------------------------------------------------------------
	// 2. Create Solution records in the database
	// -----------------------------------------------------------------------

	activity.RecordHeartbeat(ctx, "storing solutions")

	mainSol := input.Solutions.MainSolution
	mainSol.ProblemID = problemID
	if isVersioned {
		mainSol.ID = uuid.NewSHA1(problemID, []byte("solution:main"))
	} else if mainSol.ID == uuid.Nil {
		mainSol.ID = uuid.New()
	}
	mainSol.CompileStatus = "success"
	mainSol.CreatedAt = now

	bruteSol := input.Solutions.BruteSolution
	bruteSol.ProblemID = problemID
	if isVersioned {
		bruteSol.ID = uuid.NewSHA1(problemID, []byte("solution:brute"))
	} else if bruteSol.ID == uuid.Nil {
		bruteSol.ID = uuid.New()
	}
	bruteSol.CompileStatus = "success"
	bruteSol.CreatedAt = now

	// Insert solutions into the database.
	solQuery := `INSERT INTO solutions (id, problem_id, solution_type, language, source_code, compile_status, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	if isVersioned {
		solQuery += ` ON CONFLICT (id) DO NOTHING`
	}
	if err := a.deps.ProblemRepo.ExecRaw(ctx, solQuery,
		mainSol.ID, mainSol.ProblemID, mainSol.SolutionType, mainSol.Language, mainSol.SourceCode, mainSol.CompileStatus, mainSol.CreatedAt); err != nil {
		return nil, fmt.Errorf("inserting main solution: %w", err)
	}
	if err := a.deps.ProblemRepo.ExecRaw(ctx, solQuery,
		bruteSol.ID, bruteSol.ProblemID, bruteSol.SolutionType, bruteSol.Language, bruteSol.SourceCode, bruteSol.CompileStatus, bruteSol.CreatedAt); err != nil {
		return nil, fmt.Errorf("inserting brute solution: %w", err)
	}

	// -----------------------------------------------------------------------
	// 3. Upload test data to MinIO
	// -----------------------------------------------------------------------

	activity.RecordHeartbeat(ctx, "uploading test data to MinIO")

	testCases := make([]*domain.TestCase, 0, len(input.TestCases))

	// Precompute per-case score allocation from TestDataConfig groups:
	// each group's Score is divided evenly across its NumCases (the last case
	// in the group absorbs any rounding remainder).  Sample cases always
	// receive score 0.  If no groups are configured, all cases get score 0.
	caseScores := computeCaseScores(input.Params.TestDataConfig, input.TestCases)

	for i, tc := range input.TestCases {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("uploading test case %d/%d", i+1, len(input.TestCases)))

		tcID := uuid.New()
		if isVersioned {
			tcID = uuid.NewSHA1(problemID, []byte(fmt.Sprintf("testcase:%d", i)))
		}

		// Upload input file. Durable artifacts are preferred; InputRef remains
		// only as a replay fallback for legacy workflow histories.
		inputPath := fmt.Sprintf("problems/%s/tests/%03d.in", problemID.String(), i+1)
		if tc.InputArtifact != nil {
			data, err := a.getArtifact(ctx, tc.InputArtifact)
			if err != nil {
				return nil, fmt.Errorf("reading test input artifact %d: %w", i, err)
			}
			if err := upload(ctx, inputPath, data); err != nil {
				return nil, fmt.Errorf("uploading externalized test input %d: %w", i, err)
			}
		} else if tc.InputRef != "" {
			data, err := os.ReadFile(tc.InputRef)
			if err != nil {
				return nil, fmt.Errorf("reading externalized test input %d from %s: %w", i, tc.InputRef, err)
			}
			if err := upload(ctx, inputPath, data); err != nil {
				return nil, fmt.Errorf("uploading externalized test input %d: %w", i, err)
			}
		} else {
			if err := upload(ctx, inputPath, []byte(tc.Input)); err != nil {
				return nil, fmt.Errorf("uploading test input %d: %w", i, err)
			}
		}

		// Upload output file (from the main solution's output).
		outputPath := fmt.Sprintf("problems/%s/tests/%03d.out", problemID.String(), i+1)
		var outputData string
		// Resolve output from a durable artifact, then the legacy local ref, or
		// finally inline output.
		if len(input.SandboxOutput.OutputArtifacts) > i && input.SandboxOutput.OutputArtifacts[i] != nil {
			data, err := a.getArtifact(ctx, input.SandboxOutput.OutputArtifacts[i])
			if err != nil {
				return nil, fmt.Errorf("reading output artifact %d: %w", i, err)
			}
			outputData = string(data)
		} else if len(input.SandboxOutput.OutputRefs) > i && input.SandboxOutput.OutputRefs[i] != "" {
			data, err := os.ReadFile(input.SandboxOutput.OutputRefs[i])
			if err != nil {
				return nil, fmt.Errorf("reading externalized output %d: %w", i, err)
			}
			outputData = string(data)
		} else if i < len(input.SandboxOutput.Outputs) {
			outputData = input.SandboxOutput.Outputs[i]
		}
		if err := upload(ctx, outputPath, []byte(outputData)); err != nil {
			return nil, fmt.Errorf("uploading test output %d: %w", i, err)
		}

		testCase := &domain.TestCase{
			ID:          tcID,
			ProblemID:   problemID,
			TestIndex:   i,
			GroupID:     tc.GroupID,
			IsSample:    tc.IsSample,
			InputPath:   inputPath,
			OutputPath:  outputPath,
			Score:       caseScores[i],
			Description: tc.Description,
			CreatedAt:   now,
		}

		testCases = append(testCases, testCase)
	}

	// Insert test cases into the database.
	activity.RecordHeartbeat(ctx, "storing test cases in database")
	var createTestCasesErr error
	if isVersioned {
		createTestCasesErr = a.deps.TestCaseRepo.CreateBatchIdempotent(ctx, testCases)
	} else {
		createTestCasesErr = a.deps.TestCaseRepo.CreateBatch(ctx, testCases)
	}
	if createTestCasesErr != nil {
		return nil, fmt.Errorf("inserting test cases into database: %w", createTestCasesErr)
	}

	// -----------------------------------------------------------------------
	// 4. Store metadata JSON in MinIO
	// -----------------------------------------------------------------------

	activity.RecordHeartbeat(ctx, "storing metadata in MinIO")

	if testManifestPath != "" {
		if err := upload(ctx, testManifestPath, testManifestJSON); err != nil {
			return nil, fmt.Errorf("uploading test manifest: %w", err)
		}
	}

	fullMetadata := buildFullMetadata(problem, &mainSol, &bruteSol, testCases, input.Params)
	metadataPath := fmt.Sprintf("problems/%s/metadata.json", problemID.String())
	if err := upload(ctx, metadataPath, fullMetadata); err != nil {
		return nil, fmt.Errorf("uploading metadata: %w", err)
	}

	// Upload solutions to MinIO.
	mainSolPath := fmt.Sprintf("problems/%s/solutions/main%s", problemID.String(), languageExtension(mainSol.Language))
	if err := upload(ctx, mainSolPath, []byte(mainSol.SourceCode)); err != nil {
		return nil, fmt.Errorf("uploading main solution: %w", err)
	}

	bruteSolPath := fmt.Sprintf("problems/%s/solutions/brute%s", problemID.String(), languageExtension(bruteSol.Language))
	if err := upload(ctx, bruteSolPath, []byte(bruteSol.SourceCode)); err != nil {
		return nil, fmt.Errorf("uploading brute solution: %w", err)
	}

	// -----------------------------------------------------------------------
	// 5. Generate and store embedding
	// -----------------------------------------------------------------------

	activity.RecordHeartbeat(ctx, "generating and storing embedding")

	embeddingModelVersionID, err := a.deps.configuredStatementModelVersion()
	if err != nil {
		return nil, fmt.Errorf("resolving configured statement embedding model: %w", err)
	}
	embeddingText := buildEmbeddingText(problem.Title, problem.Statement, problem.OneLineHint)
	embeddingContentHash := sha256Bytes([]byte(embeddingText))
	var embedding []float32
	if isVersioned {
		effectKey, keyErr := namedProviderEffectKey("store-problem-embedding", input.IdempotencyKey)
		if keyErr != nil {
			return nil, keyErr
		}
		embedding, err = a.cachedProblemEmbedding(ctx, effectKey, embeddingText)
	} else {
		embedding, err = a.deps.Embedding.Embed(ctx, embeddingText)
	}
	if err != nil {
		return nil, fmt.Errorf("generating required problem embedding: %w", err)
	} else {
		embeddingHash, err := sha256JSON(embedding)
		if err != nil {
			return nil, fmt.Errorf("hashing required problem embedding: %w", err)
		}
		embeddingAssignmentHash, err := sha256JSON(struct {
			ModelVersionID string `json:"model_version_id"`
			ContentHash    string `json:"content_hash"`
			EmbeddingSHA   string `json:"embedding_sha256"`
		}{
			ModelVersionID: embeddingModelVersionID.String(),
			ContentHash:    embeddingContentHash,
			EmbeddingSHA:   embeddingHash,
		})
		if err != nil {
			return nil, fmt.Errorf("hashing required problem embedding assignment: %w", err)
		}
		storeVector := func() error {
			return a.deps.VectorRepo.UpdateEmbeddingForVersion(ctx, repository.EmbeddingWrite{
				ProblemID:      problemID,
				ModelVersionID: embeddingModelVersionID,
				Kind:           repository.EmbeddingKindStatement,
				ContentHash:    embeddingContentHash,
				Embedding:      embedding,
			})
		}
		if isVersioned {
			err = a.runOperationStep(ctx, input.IdempotencyKey, "vector:problem_embeddings", embeddingAssignmentHash, storeVector)
		} else {
			err = storeVector()
		}
		if err != nil {
			return nil, fmt.Errorf("storing required problem embedding: %w", err)
		}
	}

	publicationOperationKey := input.IdempotencyKey
	if publicationOperationKey == "" {
		publicationOperationKey = "legacy-store-problem:" + problemID.String()
	}
	var status domain.ProblemStatus
	var quarantineReason string
	if input.QualityPassDraft != nil {
		status = domain.ProblemStatusDraft
		quarantineReason = ""
	} else if input.ReviewQuarantine != nil {
		status, quarantineReason, err = a.deps.ProblemRepo.ApplyReviewQuarantine(ctx, repository.ReviewQuarantineRecord{
			ProblemID:          problemID,
			OperationKey:       publicationOperationKey,
			Reason:             input.ReviewQuarantine.Reason,
			WorkflowID:         input.WorkflowID,
			WorkflowRunID:      input.ReviewQuarantine.WorkflowRunID,
			ReviewGateChangeID: input.ReviewQuarantine.ReviewGateChangeID,
			ReviewGateVersion:  input.ReviewQuarantine.ReviewGateVersion,
			ReviewResultSHA256: input.ReviewQuarantine.ReviewResultSHA256,
			ReviewResultJSON:   reviewJSON,
			ReviewText:         input.ReviewQuarantine.ReviewResult.FullText,
			SourceAncestryJSON: sourceAncestryJSON,
		})
	} else {
		status, quarantineReason, err = a.deps.ProblemRepo.ApplyPublicReleaseGate(ctx, problemID, publicationOperationKey)
	}
	if err != nil {
		return nil, err
	}
	if autoApprovalCandidate(input, status, quarantineReason) && a.deps.ReviewSettings != nil {
		settings, settingsErr := a.deps.ReviewSettings.GetReviewSettings(ctx)
		switch {
		case settingsErr != nil:
			logger.Error("failed to read automatic review settings; leaving problem quarantined",
				"problem_id", problemID.String(), "error", settingsErr)
		case settings.AutoApprovePublicRelease:
			report, approvalErr := a.deps.ProblemRepo.ApprovePublicRelease(
				ctx, problemID, "local-test-auto-review",
			)
			if approvalErr != nil {
				logger.Warn("automatic public release approval failed; leaving problem quarantined",
					"problem_id", problemID.String(), "error", approvalErr)
			} else {
				status = report.ReleaseStatus
				quarantineReason = report.QuarantineReason
				logger.Info("automatic test review approved eligible problem",
					"problem_id", problemID.String(), "status", status,
					"approved_by", report.ApprovedBy)
			}
		}
	}
	if status == domain.ProblemStatusQuarantined {
		logger.Warn("problem completed but quarantined by publication policy",
			"problem_id", problemID.String(),
			"reason", quarantineReason,
		)
	}

	var standardEvidenceRef *domain.GenerationStandardEvidenceReference
	if input.Params.GenerationEvidence != nil {
		ref, err := a.storeGenerationStandardEvidence(
			ctx,
			input,
			problemID,
			status,
			quarantineReason,
			testManifestSHA256,
			upload,
		)
		if err != nil {
			return nil, err
		}
		standardEvidenceRef = &ref
	}

	logger.Info("problem storage completed",
		"problem_id", problemID.String(),
		"serial_number", serialNumber,
		"status", status,
		"test_count", len(testCases),
	)

	// Clean up local staging directories for externalized test inputs and outputs.
	cleanedDirs := map[string]bool{}
	for _, tc := range input.TestCases {
		if tc.InputRef != "" {
			dir := filepath.Dir(tc.InputRef)
			if !cleanedDirs[dir] {
				os.RemoveAll(dir)
				cleanedDirs[dir] = true
			}
		}
	}
	for _, ref := range input.SandboxOutput.OutputRefs {
		if ref != "" {
			dir := filepath.Dir(ref)
			if !cleanedDirs[dir] {
				os.RemoveAll(dir)
				cleanedDirs[dir] = true
			}
		}
	}

	result = &StoreResult{
		ProblemID:        problemID,
		SerialNumber:     serialNumber,
		Status:           status,
		QuarantineReason: quarantineReason,
		StandardEvidence: standardEvidenceRef,
	}
	if isVersioned {
		if err := a.completeOperation(ctx, input.IdempotencyKey, inputHash, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validateReviewQuarantineEvidence(input StoreInput) (json.RawMessage, json.RawMessage, error) {
	evidence := input.ReviewQuarantine
	if evidence == nil {
		return nil, nil, nil
	}
	if input.PayloadVersion != StoreProblemPayloadVersion &&
		input.PayloadVersion != StoreProblemTestManifestPayloadVersion &&
		input.PayloadVersion != StoreProblemKnowledgePointCombinationPayloadVersion &&
		input.PayloadVersion != StoreProblemStandardEvidencePayloadVersion {
		return nil, nil, fmt.Errorf("review quarantine requires a publication-aware Store payload")
	}
	if strings.TrimSpace(input.WorkflowID) == "" || strings.TrimSpace(evidence.WorkflowRunID) == "" {
		return nil, nil, fmt.Errorf("review quarantine requires workflow and run lineage")
	}
	if evidence.ReviewGateChangeID != "problem-generation-review-gate-v2" || evidence.ReviewGateVersion != 2 {
		return nil, nil, fmt.Errorf("unsupported review quarantine gate %q version %d", evidence.ReviewGateChangeID, evidence.ReviewGateVersion)
	}
	if strings.TrimSpace(evidence.Reason) == "" {
		return nil, nil, fmt.Errorf("review quarantine reason is required")
	}
	if evidence.ReviewResult.Approved || evidence.ReviewResult.IsDuplicate {
		return nil, nil, fmt.Errorf("review quarantine requires a non-duplicate denied review")
	}
	if strings.TrimSpace(evidence.ReviewResult.FullText) == "" {
		return nil, nil, fmt.Errorf("review quarantine requires the full review text")
	}
	if len(evidence.SourceAncestry) == 0 {
		return nil, nil, fmt.Errorf("review quarantine requires review source ancestry")
	}
	for i, ancestor := range evidence.SourceAncestry {
		if ancestor == nil {
			return nil, nil, fmt.Errorf("review quarantine source ancestor %d is nil", i)
		}
		found := false
		for _, source := range input.SourceArtifacts {
			if source != nil && source.Equal(*ancestor) {
				found = true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("review quarantine source ancestor %d is absent from Store source artifacts", i)
		}
	}
	reviewJSON, reviewHash, err := CanonicalReviewResultJSON(evidence.ReviewResult)
	if err != nil {
		return nil, nil, err
	}
	if reviewHash != evidence.ReviewResultSHA256 {
		return nil, nil, fmt.Errorf("review quarantine result hash does not match review evidence")
	}
	ancestryJSON, err := json.Marshal(evidence.SourceAncestry)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding review source ancestry: %w", err)
	}
	reviewAncestryJSON, err := json.Marshal(evidence.ReviewResult.SourceArtifacts)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding review result ancestry: %w", err)
	}
	if !bytes.Equal(ancestryJSON, reviewAncestryJSON) {
		return nil, nil, fmt.Errorf("review quarantine source ancestry does not match review result")
	}
	return reviewJSON, ancestryJSON, nil
}

func prepareTestManifestForStore(input StoreInput) (json.RawMessage, string, error) {
	if input.PayloadVersion == StoreProblemS3QualityDraftPayloadVersion {
		if input.TestManifest != nil || input.TestManifestV2 == nil {
			return nil, "", fmt.Errorf("S3 quality draft Store requires only TestManifest v2")
		}
		encoded, digest, err := CanonicalTestManifestV2JSON(*input.TestManifestV2)
		if err != nil {
			return nil, "", fmt.Errorf("encoding TestManifest v2 for Store: %w", err)
		}
		return encoded, digest, nil
	}
	if input.PayloadVersion != StoreProblemTestManifestPayloadVersion &&
		input.PayloadVersion != StoreProblemKnowledgePointCombinationPayloadVersion &&
		input.PayloadVersion != StoreProblemStandardEvidencePayloadVersion {
		if input.TestManifest != nil {
			return nil, "", fmt.Errorf("test manifest requires Store payload version %d, %d, or %d", StoreProblemTestManifestPayloadVersion, StoreProblemKnowledgePointCombinationPayloadVersion, StoreProblemStandardEvidencePayloadVersion)
		}
		return nil, "", nil
	}
	if input.TestManifest == nil {
		return nil, "", fmt.Errorf("Store payload version %d requires a test manifest", input.PayloadVersion)
	}
	if err := input.TestManifest.Validate(len(input.TestCases)); err != nil {
		return nil, "", fmt.Errorf("validating test manifest for Store: %w", err)
	}
	encoded, digest, err := CanonicalTestManifestJSON(*input.TestManifest)
	if err != nil {
		return nil, "", fmt.Errorf("encoding test manifest for Store: %w", err)
	}
	return encoded, digest, nil
}

func (a *Activities) validateTestManifestStoreInput(ctx context.Context, input StoreInput) error {
	if input.PayloadVersion == StoreProblemS3QualityDraftPayloadVersion {
		manifest := input.TestManifestV2
		if manifest == nil || input.TestManifest != nil || len(manifest.Cases) != len(input.TestCases) || len(input.SandboxOutput.OutputArtifacts) != len(input.TestCases) {
			return fmt.Errorf("S3 quality draft TestManifest v2 does not match Store cases")
		}
		if err := manifest.Validate(); err != nil {
			return fmt.Errorf("validating S3 quality draft TestManifest v2: %w", err)
		}
		for index, item := range manifest.Cases {
			testCase := input.TestCases[index]
			if testCase.InputArtifact == nil || input.SandboxOutput.OutputArtifacts[index] == nil ||
				testCase.InputArtifact.SHA256 != item.InputSHA256 || item.InputArtifact == nil || item.OutputArtifact == nil ||
				!testCase.InputArtifact.Equal(*item.InputArtifact) || !input.SandboxOutput.OutputArtifacts[index].Equal(*item.OutputArtifact) {
				return fmt.Errorf("S3 quality draft TestManifest case %d artifact binding mismatch", index)
			}
		}
		return nil
	}
	if input.PayloadVersion != StoreProblemTestManifestPayloadVersion &&
		input.PayloadVersion != StoreProblemKnowledgePointCombinationPayloadVersion &&
		input.PayloadVersion != StoreProblemStandardEvidencePayloadVersion {
		return nil
	}
	manifest := input.TestManifest
	if manifest == nil {
		return fmt.Errorf("Store payload version %d requires a test manifest", input.PayloadVersion)
	}
	if manifest.MainSolutionSHA256 != manifestSolutionSHA256(input.Solutions.MainSolution) ||
		manifest.BruteSolutionSHA256 != manifestSolutionSHA256(input.Solutions.BruteSolution) {
		return fmt.Errorf("test manifest solution identity does not match Store solutions")
	}
	if manifest.MainSandbox != testManifestSandboxIdentity(input.SandboxOutput.Audit) {
		return fmt.Errorf("test manifest main sandbox identity does not match Store sandbox output")
	}
	for index, testCase := range input.TestCases {
		entry := manifest.Cases[index]
		if entry.GroupID != testCase.GroupID || entry.IsSample != testCase.IsSample ||
			entry.Purpose != strings.TrimSpace(testCase.Description) || entry.Origin != testCase.Origin ||
			!reflect.DeepEqual(entry.Coverage, normalizeCoverageTags(testCase.Coverage)) ||
			entry.GeneratorSeed != testCase.GeneratorSeed || !testManifestGeneratorReplayMetadataMatches(entry, testCase) {
			return fmt.Errorf("test manifest entry %d metadata does not match Store test case", index)
		}
		inputBytes, err := a.resolveTestManifestInput(ctx, testCase)
		if err != nil {
			return fmt.Errorf("resolving Store test input %d for manifest verification: %w", index, err)
		}
		if manifestSHA256(inputBytes) != entry.InputSHA256 {
			return fmt.Errorf("test manifest input digest does not match Store test %d", index)
		}
		outputBytes, err := a.resolveTestManifestOutput(ctx, input.SandboxOutput, index)
		if err != nil {
			return fmt.Errorf("resolving Store test output %d for manifest verification: %w", index, err)
		}
		if manifestSHA256(outputBytes) != entry.ExpectedOutputSHA256 {
			return fmt.Errorf("test manifest expected-output digest does not match Store test %d", index)
		}
	}
	return nil
}

func testManifestGeneratorReplayMetadataMatches(entry TestManifestCase, testCase TestCaseData) bool {
	if (entry.GeneratorCaseIndex == nil) != (testCase.GeneratorCaseIndex == nil) {
		return false
	}
	if entry.GeneratorCaseIndex != nil && *entry.GeneratorCaseIndex != *testCase.GeneratorCaseIndex {
		return false
	}
	if (entry.GeneratorBatchIndex == nil) != (testCase.GeneratorBatchIndex == nil) {
		return false
	}
	if entry.GeneratorBatchIndex != nil && *entry.GeneratorBatchIndex != *testCase.GeneratorBatchIndex {
		return false
	}
	return true
}

func validateStoreProblemPayloadVersion(version int) error {
	if version == StoreProblemPayloadVersion ||
		version == StoreProblemTestManifestPayloadVersion ||
		version == StoreProblemKnowledgePointCombinationPayloadVersion ||
		version == StoreProblemStandardEvidencePayloadVersion ||
		version == StoreProblemS3QualityDraftPayloadVersion {
		return nil
	}
	return validateActivityPayloadVersion(version)
}

func isVersionedStorePayload(version int) bool {
	return version == ActivityPayloadVersion ||
		version == StoreProblemPayloadVersion ||
		version == StoreProblemTestManifestPayloadVersion ||
		version == StoreProblemKnowledgePointCombinationPayloadVersion ||
		version == StoreProblemStandardEvidencePayloadVersion ||
		version == StoreProblemS3QualityDraftPayloadVersion
}

func validateKnowledgePointStorePayload(input StoreInput) error {
	hasContract := input.Params.KnowledgePointCombination != nil
	versionRequiresContract := input.PayloadVersion == StoreProblemKnowledgePointCombinationPayloadVersion ||
		input.PayloadVersion == StoreProblemStandardEvidencePayloadVersion ||
		input.PayloadVersion == StoreProblemS3QualityDraftPayloadVersion
	if versionRequiresContract && !hasContract {
		return fmt.Errorf("Store payload version %d requires a knowledge-point combination contract", input.PayloadVersion)
	}
	if hasContract && !versionRequiresContract {
		return fmt.Errorf("knowledge-point combination requires Store payload version %d, %d, or %d", StoreProblemKnowledgePointCombinationPayloadVersion, StoreProblemStandardEvidencePayloadVersion, StoreProblemS3QualityDraftPayloadVersion)
	}
	return nil
}

func validateGenerationEvidenceStorePayload(input StoreInput) error {
	hasContract := input.Params.GenerationEvidence != nil
	if input.PayloadVersion == StoreProblemStandardEvidencePayloadVersion && !hasContract {
		return fmt.Errorf("Store payload version %d requires a generation evidence contract", input.PayloadVersion)
	}
	if hasContract && input.PayloadVersion != StoreProblemStandardEvidencePayloadVersion {
		return fmt.Errorf("generation evidence requires Store payload version %d", StoreProblemStandardEvidencePayloadVersion)
	}
	if hasContract {
		if err := generationapi.ValidateGenerationEvidenceContractV1(input.Params.GenerationEvidence); err != nil {
			return fmt.Errorf("validating generation evidence Store contract: %w", err)
		}
	}
	return nil
}

type storedGenerationEvidenceMetadata struct {
	Request                     *domain.GenerationEvidenceContract `json:"generation_standard_evidence_request"`
	PublicationGateVersion      string                             `json:"publication_gate_version"`
	PublicationPolicyVersion    string                             `json:"publication_policy_version"`
	PublicationGateStatus       string                             `json:"publication_gate_status"`
	PublicationQuarantineReason string                             `json:"publication_quarantine_reason"`
}

func (a *Activities) storeGenerationStandardEvidence(
	ctx context.Context,
	input StoreInput,
	problemID uuid.UUID,
	status domain.ProblemStatus,
	quarantineReason string,
	testManifestSHA256 string,
	upload func(context.Context, string, []byte) error,
) (domain.GenerationStandardEvidenceReference, error) {
	var empty domain.GenerationStandardEvidenceReference
	if input.PayloadVersion != StoreProblemStandardEvidencePayloadVersion || input.Params.GenerationEvidence == nil {
		return empty, fmt.Errorf("generation standard evidence requires Store payload version %d", StoreProblemStandardEvidencePayloadVersion)
	}
	if !isCanonicalStoreSHA256(testManifestSHA256) {
		return empty, fmt.Errorf("generation standard evidence requires a canonical test manifest identity")
	}
	persisted, err := a.deps.ProblemRepo.GetByID(ctx, problemID)
	if err != nil {
		return empty, fmt.Errorf("reading final problem state for generation standard evidence: %w", err)
	}
	if persisted.Status != status {
		return empty, fmt.Errorf("final problem status changed while building generation standard evidence: %q != %q", persisted.Status, status)
	}
	var metadata storedGenerationEvidenceMetadata
	if err := json.Unmarshal(persisted.MetadataJSON, &metadata); err != nil {
		return empty, fmt.Errorf("decoding final problem metadata for generation standard evidence: %w", err)
	}
	if metadata.Request == nil || *metadata.Request != *input.Params.GenerationEvidence {
		return empty, fmt.Errorf("stored generation standard evidence request does not match Store input")
	}

	refs := []generationapi.EvidenceRef{{Kind: "test_manifest", SHA256: testManifestSHA256}}
	outcomeCategory := generationapi.OutcomeCategoryPublicationEligibility
	var outcomeEvidence generationapi.EvidenceRef
	if input.ReviewQuarantine != nil {
		if status != domain.ProblemStatusQuarantined {
			return empty, fmt.Errorf("review standard evidence requires quarantined final status")
		}
		outcomeCategory = generationapi.OutcomeCategoryReview
		outcomeEvidence = generationapi.EvidenceRef{
			Kind:   "review_result",
			SHA256: input.ReviewQuarantine.ReviewResultSHA256,
		}
		refs = append(refs, outcomeEvidence)
	} else {
		if strings.TrimSpace(metadata.PublicationGateVersion) == "" || metadata.PublicationGateStatus != string(status) {
			return empty, fmt.Errorf("stored publication decision does not match final problem status")
		}
		decision, err := generationapi.PublicationDecisionEvidenceV0(
			metadata.PublicationGateVersion,
			metadata.PublicationPolicyVersion,
			metadata.PublicationGateStatus,
			metadata.PublicationQuarantineReason,
		)
		if err != nil {
			return empty, fmt.Errorf("building publication decision evidence: %w", err)
		}
		outcomeEvidence = decision
		refs = append(refs, outcomeEvidence)
	}

	body, digest, err := generationapi.CanonicalGenerationStandardEvidenceV1(
		input.Params.GenerationEvidence,
		status,
		outcomeCategory,
		refs,
	)
	if err != nil {
		return empty, fmt.Errorf("building generation standard evidence: %w", err)
	}
	path := fmt.Sprintf("problems/%s/generation_standard_evidence.v1.json", problemID.String())
	ref := domain.GenerationStandardEvidenceReference{
		SchemaVersion: domain.GenerationStandardEvidenceSchemaV1,
		SHA256:        digest,
		Path:          path,
	}
	binding := domain.GenerationStandardEvidenceBinding{
		Reference:        ref,
		FinalStatus:      status,
		QuarantineReason: quarantineReason,
		OutcomeCategory:  string(outcomeCategory),
		OutcomeKind:      outcomeEvidence.Kind,
		OutcomeSHA256:    outcomeEvidence.SHA256,
	}
	if err := binding.Validate(problemID.String()); err != nil {
		return empty, fmt.Errorf("validating generation standard evidence binding: %w", err)
	}
	if err := a.runOperationStep(
		ctx,
		input.IdempotencyKey,
		"db:generation-standard-evidence",
		digest,
		func() error {
			return a.deps.ProblemRepo.AttachGenerationStandardEvidence(ctx, problemID, binding)
		},
	); err != nil {
		return empty, fmt.Errorf("binding generation standard evidence receipt: %w", err)
	}
	if err := upload(ctx, path, body); err != nil {
		return empty, fmt.Errorf("uploading generation standard evidence: %w", err)
	}
	return ref, nil
}

// recoverGenerationStandardEvidenceResult treats the immutable receipt
// binding as the durable completion marker between the last Store side effect
// and workflow_operations completion. A retry therefore cannot re-run a
// publication gate and diverge from an already-issued receipt.
func (a *Activities) recoverGenerationStandardEvidenceResult(
	ctx context.Context,
	input StoreInput,
	problemID uuid.UUID,
	testManifestSHA256 string,
) (*StoreResult, bool, error) {
	binding, ok, err := a.deps.ProblemRepo.GetGenerationStandardEvidence(ctx, problemID)
	if err != nil {
		return nil, false, fmt.Errorf("checking generation standard evidence recovery: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	problem, err := a.deps.ProblemRepo.GetByID(ctx, problemID)
	if err != nil {
		return nil, false, fmt.Errorf("reading problem for generation standard evidence recovery: %w", err)
	}
	var metadata storedGenerationEvidenceMetadata
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return nil, false, fmt.Errorf("decoding generation standard evidence recovery metadata: %w", err)
	}
	if metadata.Request == nil || input.Params.GenerationEvidence == nil ||
		*metadata.Request != *input.Params.GenerationEvidence {
		return nil, false, fmt.Errorf("generation standard evidence recovery request is inconsistent")
	}
	body, digest, err := generationStandardEvidenceBodyFromBinding(
		input.Params.GenerationEvidence,
		binding,
		testManifestSHA256,
	)
	if err != nil {
		return nil, false, fmt.Errorf("rebuilding generation standard evidence for recovery: %w", err)
	}
	if err := a.runOperationStep(
		ctx,
		input.IdempotencyKey,
		"db:generation-standard-evidence",
		digest,
		func() error {
			return a.deps.ProblemRepo.AttachGenerationStandardEvidence(ctx, problemID, binding)
		},
	); err != nil {
		return nil, false, fmt.Errorf("recovering generation standard evidence binding: %w", err)
	}
	if err := a.runOperationStep(
		ctx,
		input.IdempotencyKey,
		"minio:"+binding.Reference.Path,
		digest,
		func() error { return a.uploadToMinIO(ctx, binding.Reference.Path, body) },
	); err != nil {
		return nil, false, fmt.Errorf("recovering generation standard evidence object: %w", err)
	}
	ref := binding.Reference
	return &StoreResult{
		ProblemID:        problemID,
		SerialNumber:     problem.SerialNumber,
		Status:           binding.FinalStatus,
		QuarantineReason: binding.QuarantineReason,
		StandardEvidence: &ref,
	}, true, nil
}

func generationStandardEvidenceBodyFromBinding(
	contract *domain.GenerationEvidenceContract,
	binding domain.GenerationStandardEvidenceBinding,
	testManifestSHA256 string,
) ([]byte, string, error) {
	refs := []generationapi.EvidenceRef{
		{Kind: "test_manifest", SHA256: testManifestSHA256},
		{Kind: binding.OutcomeKind, SHA256: binding.OutcomeSHA256},
	}
	body, digest, err := generationapi.CanonicalGenerationStandardEvidenceV1(
		contract,
		binding.FinalStatus,
		generationapi.OutcomeCategory(binding.OutcomeCategory),
		refs,
	)
	if err != nil {
		return nil, "", err
	}
	if digest != binding.Reference.SHA256 {
		return nil, "", fmt.Errorf("rebuilt receipt sha256 does not match immutable binding")
	}
	return body, digest, nil
}

func isCanonicalStoreSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func initialStoreProblemStatus(input StoreInput) domain.ProblemStatus {
	if input.QualityPassDraft != nil {
		return domain.ProblemStatusDraft
	}
	if input.ReviewQuarantine != nil {
		// Keep a review-denied candidate off every default publication surface
		// even if a later Store side effect fails and the activity must retry.
		return domain.ProblemStatusQuarantined
	}
	return domain.ProblemStatusGenerating
}

func autoApprovalCandidate(
	input StoreInput,
	status domain.ProblemStatus,
	quarantineReason string,
) bool {
	return input.ReviewQuarantine == nil &&
		status == domain.ProblemStatusQuarantined &&
		strings.TrimSpace(quarantineReason) == autoApprovalQuarantineReason
}

// uploadToMinIO uploads a byte slice to the configured MinIO bucket at the
// given object path.
func (a *Activities) uploadToMinIO(ctx context.Context, objectPath string, data []byte) error {
	reader := bytes.NewReader(data)

	_, err := a.deps.MinIO.PutObject(
		ctx,
		a.deps.MinioBucket,
		objectPath,
		reader,
		int64(len(data)),
		minio.PutObjectOptions{
			ContentType: detectContentType(objectPath),
		},
	)
	if err != nil {
		return fmt.Errorf("uploading to minio path %s: %w", objectPath, err)
	}

	return nil
}

// detectContentType returns a MIME type based on the file extension.
func detectContentType(path string) string {
	if strings.HasSuffix(path, ".json") {
		return "application/json"
	}
	if strings.HasSuffix(path, ".in") || strings.HasSuffix(path, ".out") {
		return "text/plain"
	}
	if strings.HasSuffix(path, ".cpp") || strings.HasSuffix(path, ".c") {
		return "text/x-c"
	}
	if strings.HasSuffix(path, ".py") {
		return "text/x-python"
	}
	if strings.HasSuffix(path, ".java") {
		return "text/x-java"
	}
	return "application/octet-stream"
}

// fullMetadata is a comprehensive metadata structure stored alongside the
// problem in MinIO for offline access and archival purposes.
type fullMetadata struct {
	ProblemID    string              `json:"problem_id"`
	SerialNumber string              `json:"serial_number"`
	Title        string              `json:"title"`
	Level        domain.ProblemLevel `json:"level"`
	Difficulty   int                 `json:"difficulty"`
	Tags         []string            `json:"tags"`
	TimeLimit    int                 `json:"time_limit_ms"`
	MemoryLimit  int                 `json:"memory_limit_mb"`
	TestCount    int                 `json:"test_count"`
	Solutions    []solutionMeta      `json:"solutions"`
	GeneratedAt  string              `json:"generated_at"`
	ContestStyle string              `json:"contest_style,omitempty"`
}

type solutionMeta struct {
	Type     domain.SolutionType `json:"type"`
	Language string              `json:"language"`
	Path     string              `json:"path"`
}

// buildFullMetadata constructs the comprehensive metadata JSON for storage.
func buildFullMetadata(
	problem *domain.Problem,
	mainSol, bruteSol *domain.Solution,
	testCases []*domain.TestCase,
	params domain.ProblemGenParams,
) []byte {
	meta := fullMetadata{
		ProblemID:    problem.ID.String(),
		SerialNumber: problem.SerialNumber,
		Title:        problem.Title,
		Level:        problem.Level,
		Difficulty:   problem.Difficulty,
		Tags:         problem.Tags,
		TimeLimit:    problem.TimeLimit,
		MemoryLimit:  problem.MemoryLimit,
		TestCount:    len(testCases),
		Solutions: []solutionMeta{
			{
				Type:     mainSol.SolutionType,
				Language: mainSol.Language,
				Path:     fmt.Sprintf("solutions/main%s", languageExtension(mainSol.Language)),
			},
			{
				Type:     bruteSol.SolutionType,
				Language: bruteSol.Language,
				Path:     fmt.Sprintf("solutions/brute%s", languageExtension(bruteSol.Language)),
			},
		},
		GeneratedAt:  problem.CreatedAt.UTC().Format(time.RFC3339),
		ContestStyle: params.ContestStyle,
	}

	data, _ := json.MarshalIndent(meta, "", "  ")
	return data
}

func storeInputHash(input StoreInput) (string, error) {
	input.IdempotencyKey = ""
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encoding store input for idempotency: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func verifyStoredOperation(metadata json.RawMessage, idempotencyKey, inputHash string) error {
	var values map[string]interface{}
	if err := json.Unmarshal(metadata, &values); err != nil {
		return fmt.Errorf("decoding existing problem idempotency metadata: %w", err)
	}
	storedKey, _ := values["store_idempotency_key"].(string)
	storedHash, _ := values["store_input_sha256"].(string)
	if storedKey != idempotencyKey {
		return fmt.Errorf("idempotency key collision: existing operation key does not match")
	}
	if storedHash != inputHash {
		return fmt.Errorf("idempotency key %q was already used with a different payload", idempotencyKey)
	}
	return nil
}

// computeCaseScores returns a slice (parallel to the input test-case list) of
// per-case scores derived from the TestDataConfig groups. Within each group,
// its Score is split evenly across its non-sample cases (the last case absorbs
// any remainder). Sample cases always receive score 0. Cases whose group_id
// does not appear in the config — or configs with no groups — receive score 0.
func computeCaseScores(cfg domain.TestDataConfig, cases []TestCaseData) []int {
	scores := make([]int, len(cases))
	if len(cfg.Groups) == 0 {
		return scores
	}

	// Collect the indices of non-sample cases per group (in input order).
	groupIdx := make(map[int][]int, len(cfg.Groups))
	for i, tc := range cases {
		if tc.IsSample {
			continue
		}
		groupIdx[tc.GroupID] = append(groupIdx[tc.GroupID], i)
	}

	for _, g := range cfg.Groups {
		idxs := groupIdx[g.GroupID]
		if len(idxs) == 0 || g.Score <= 0 {
			continue
		}
		base := g.Score / len(idxs)
		remainder := g.Score - base*len(idxs)
		for _, i := range idxs {
			scores[i] = base
		}
		if remainder > 0 {
			scores[idxs[len(idxs)-1]] += remainder
		}
	}
	return scores
}
