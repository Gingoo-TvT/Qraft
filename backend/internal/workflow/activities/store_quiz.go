package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

func (a *Activities) StoreQuizActivity(ctx context.Context, in QuizStoreInput) (*QuizStoreResult, error) {
	if err := validateActivityPayloadVersion(in.PayloadVersion); err != nil {
		return nil, err
	}
	if in.PayloadVersion == ActivityPayloadVersion {
		return a.storeQuizV1(ctx, in)
	}

	startCode, err := a.deps.QuizRepo.NextCodeForType(ctx, in.Params.Type)
	if err != nil {
		return nil, err
	}
	prefix := in.Params.Type.CodePrefix()
	startNum, err := strconv.Atoi(startCode[1:])
	if err != nil {
		return nil, fmt.Errorf("parsing next quiz code %q: %w", startCode, err)
	}
	quizzes := make([]*domain.QuizProblem, 0, len(in.Drafts))
	for i, draft := range in.Drafts {
		q := &domain.QuizProblem{
			ID:          uuid.New(),
			Code:        fmt.Sprintf("%s%d", prefix, startNum+i),
			Title:       draft.Title,
			Statement:   draft.Statement,
			Type:        in.Params.Type,
			Options:     draft.Options,
			Answers:     draft.Answers,
			Difficulty:  in.Params.Difficulty,
			Visibility:  in.Params.Visibility,
			IsVIP:       in.Params.IsVIP,
			Tags:        in.Params.Tags,
			Explanation: draft.Explanation,
			Subject:     in.Params.Subject,
		}
		quizzes = append(quizzes, q)
	}
	if _, _, err := a.deps.QuizRepo.BulkCreate(ctx, quizzes, "error"); err != nil {
		return nil, fmt.Errorf("storing quiz drafts: %w", err)
	}
	out := &QuizStoreResult{
		InsertedIDs: make([]uuid.UUID, 0, len(quizzes)),
		Codes:       make([]string, 0, len(quizzes)),
	}
	for _, q := range quizzes {
		if len(in.KnowledgePointIDs) > 0 {
			if err := a.deps.QuizRepo.LinkKnowledgePoints(ctx, q.ID, in.KnowledgePointIDs); err != nil {
				return nil, err
			}
		}
		out.InsertedIDs = append(out.InsertedIDs, q.ID)
		out.Codes = append(out.Codes, q.Code)
	}
	return out, nil
}

func (a *Activities) storeQuizV1(ctx context.Context, in QuizStoreInput) (result *QuizStoreResult, retErr error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("idempotency key is required for payload version %d", in.PayloadVersion)
	}
	for i, ref := range in.SourceArtifacts {
		if ref == nil {
			return nil, fmt.Errorf("quiz source artifact %d is nil", i)
		}
		if err := ref.Validate(a.deps.MinioBucket); err != nil {
			return nil, fmt.Errorf("quiz source artifact %d: %w", i, err)
		}
	}
	payloadHash, err := quizStoreInputHash(in)
	if err != nil {
		return nil, err
	}
	operation, err := a.beginOperation(ctx, in.IdempotencyKey, "store_quiz/v1", payloadHash)
	if err != nil {
		return nil, err
	}
	if operation.Status == repository.OperationStatusCompleted {
		var cached QuizStoreResult
		if err := json.Unmarshal(operation.ResultJSON, &cached); err != nil {
			return nil, fmt.Errorf("decoding cached store quiz result: %w", err)
		}
		return &cached, nil
	}
	defer func() {
		if retErr != nil {
			a.recordOperationFailure(ctx, in.IdempotencyKey, payloadHash, retErr)
		}
	}()

	quizzes := make([]*domain.QuizProblem, 0, len(in.Drafts))
	for i, draft := range in.Drafts {
		quizzes = append(quizzes, &domain.QuizProblem{
			ID:          uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("algoforge:store-quiz:%s:%d", in.IdempotencyKey, i))),
			Title:       draft.Title,
			Statement:   draft.Statement,
			Type:        in.Params.Type,
			Options:     draft.Options,
			Answers:     draft.Answers,
			Difficulty:  in.Params.Difficulty,
			Visibility:  in.Params.Visibility,
			IsVIP:       in.Params.IsVIP,
			Tags:        in.Params.Tags,
			Explanation: draft.Explanation,
			Subject:     in.Params.Subject,
		})
	}
	if err := a.deps.QuizRepo.CreateGeneratedBatch(ctx, in.IdempotencyKey, in.Params.Type, quizzes, in.KnowledgePointIDs); err != nil {
		return nil, fmt.Errorf("storing idempotent quiz batch: %w", err)
	}

	result = &QuizStoreResult{
		SourceArtifacts: in.SourceArtifacts,
		InsertedIDs:     make([]uuid.UUID, 0, len(quizzes)),
		Codes:           make([]string, 0, len(quizzes)),
	}
	for _, quiz := range quizzes {
		result.InsertedIDs = append(result.InsertedIDs, quiz.ID)
		result.Codes = append(result.Codes, quiz.Code)
	}
	if err := a.completeOperation(ctx, in.IdempotencyKey, payloadHash, result); err != nil {
		return nil, err
	}
	return result, nil
}

func quizStoreInputHash(in QuizStoreInput) (string, error) {
	in.IdempotencyKey = ""
	hash, err := sha256JSON(in)
	if err != nil {
		return "", fmt.Errorf("encoding quiz store input for idempotency: %w", err)
	}
	return hash, nil
}
