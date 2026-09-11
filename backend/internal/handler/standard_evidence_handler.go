package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type getStandardEvidenceProblemFunc func(context.Context, uuid.UUID) (*domain.Problem, error)
type getStandardEvidenceBindingFunc func(context.Context, uuid.UUID) (domain.GenerationStandardEvidenceBinding, bool, error)
type downloadStandardEvidenceFunc func(context.Context, string) ([]byte, error)

func problemStandardEvidenceReference(
	problem *domain.Problem,
	binding *domain.GenerationStandardEvidenceBinding,
) (domain.GenerationStandardEvidenceReference, bool, error) {
	var empty domain.GenerationStandardEvidenceReference
	if problem == nil || len(problem.MetadataJSON) == 0 {
		return empty, false, nil
	}
	var metadata struct {
		Request *domain.GenerationEvidenceContract `json:"generation_standard_evidence_request"`
	}
	if err := json.Unmarshal(problem.MetadataJSON, &metadata); err != nil {
		return empty, false, errors.New("problem metadata is not valid JSON")
	}
	if metadata.Request == nil {
		if binding != nil {
			return empty, false, errors.New("standard evidence binding has no request contract")
		}
		return empty, false, nil
	}
	if err := generationapi.ValidateGenerationEvidenceContractV1(metadata.Request); err != nil {
		return empty, false, errors.New("standard evidence request contract is inconsistent")
	}
	if binding == nil {
		return empty, false, errors.New("standard evidence request is missing its receipt reference")
	}
	if err := binding.Validate(problem.ID.String()); err != nil {
		return empty, false, errors.New("standard evidence binding is inconsistent")
	}
	return binding.Reference, true, nil
}

func standardEvidenceBytesMatch(ref domain.GenerationStandardEvidenceReference, data []byte) bool {
	digest := sha256.Sum256(data)
	return ref.SHA256 == hex.EncodeToString(digest[:])
}

// HandleGetStandardEvidence returns the exact immutable receipt bytes bound by
// the independent database binding and the jobs v1 EvidenceRef.
func (h *ProblemHandler) HandleGetStandardEvidence(c echo.Context) error {
	return handleGetStandardEvidence(
		c,
		func(ctx context.Context, id uuid.UUID) (*domain.Problem, error) {
			return h.problemService.GetProblem(ctx, id)
		},
		func(ctx context.Context, id uuid.UUID) (domain.GenerationStandardEvidenceBinding, bool, error) {
			return h.problemService.GetGenerationStandardEvidence(ctx, id)
		},
		func(ctx context.Context, path string) ([]byte, error) {
			return h.minioClient.DownloadFile(ctx, path)
		},
	)
}

func handleGetStandardEvidence(
	c echo.Context,
	getProblem getStandardEvidenceProblemFunc,
	getBinding getStandardEvidenceBindingFunc,
	download downloadStandardEvidenceFunc,
) error {
	id, err := parseUUID(c, "id")
	if err != nil {
		return err
	}
	problem, err := getProblem(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return notFound(c, "problem not found")
		}
		return internalError(c, "failed to get problem: "+err.Error())
	}
	binding, hasBinding, bindingErr := getBinding(c.Request().Context(), id)
	if bindingErr != nil {
		return internalError(c, "failed to get standard evidence binding")
	}
	var bindingPtr *domain.GenerationStandardEvidenceBinding
	if hasBinding {
		bindingPtr = &binding
	}
	ref, ok, refErr := problemStandardEvidenceReference(problem, bindingPtr)
	if refErr != nil {
		return internalError(c, "stored standard evidence reference is inconsistent")
	}
	if !ok {
		return notFound(c, "standard evidence not found")
	}
	data, err := download(c.Request().Context(), ref.Path)
	if err != nil {
		return internalError(c, "failed to download standard evidence: "+err.Error())
	}
	if !standardEvidenceBytesMatch(ref, data) {
		return internalError(c, "stored standard evidence content does not match binding")
	}
	return c.Blob(http.StatusOK, "application/json", data)
}
