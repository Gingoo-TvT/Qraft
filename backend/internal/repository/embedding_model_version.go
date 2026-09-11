package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	EmbeddingModelStatusShadow  = "shadow"
	EmbeddingModelStatusActive  = "active"
	EmbeddingModelStatusRetired = "retired"
)

// EmbeddingModelVersionRegistration is the immutable identity for one vector
// space plus operator-owned metadata needed to reproduce it.
type EmbeddingModelVersionRegistration struct {
	Provider            string          `json:"provider"`
	ModelID             string          `json:"model_id"`
	Revision            string          `json:"revision"`
	WeightsHash         string          `json:"weights_hash"`
	Dimensions          int             `json:"dimensions"`
	Normalization       string          `json:"normalization"`
	Quantization        string          `json:"quantization"`
	InstructionTemplate string          `json:"instruction_template"`
	IndexParams         json.RawMessage `json:"index_params"`
}

type EmbeddingModelVersionRecord struct {
	ID                  uuid.UUID       `json:"id"`
	Provider            string          `json:"provider"`
	ModelID             string          `json:"model_id"`
	Revision            string          `json:"revision"`
	WeightsHash         string          `json:"weights_hash"`
	Dimensions          int             `json:"dimensions"`
	Normalization       string          `json:"normalization"`
	Quantization        string          `json:"quantization"`
	InstructionTemplate string          `json:"instruction_template"`
	IndexParams         json.RawMessage `json:"index_params"`
	Status              string          `json:"status"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type ActiveEmbeddingModelStatus struct {
	EmbeddingKind      string                      `json:"embedding_kind"`
	ExpectedDimensions int                         `json:"expected_dimensions"`
	UpdatedBy          string                      `json:"updated_by"`
	Reason             string                      `json:"reason"`
	UpdatedAt          time.Time                   `json:"updated_at"`
	ModelVersion       EmbeddingModelVersionRecord `json:"model_version"`
}

var (
	ErrConfiguredModelVersionNotFound  = errors.New("configured embedding model version not found")
	ErrConfiguredModelVersionAmbiguous = errors.New("configured embedding model version is ambiguous")
)

// GetEmbeddingModelVersion returns the immutable registry row for one exact
// model version. Missing rows wrap sql.ErrNoRows so API callers can map the
// condition without depending on the pgx-specific sentinel.
func (r *VectorRepository) GetEmbeddingModelVersion(
	ctx context.Context,
	id uuid.UUID,
) (EmbeddingModelVersionRecord, error) {
	if r == nil || r.db == nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("vector repository database is required")
	}
	if id == uuid.Nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("embedding model version id is required")
	}
	record, err := scanEmbeddingModelVersion(r.db.QueryRow(ctx, `
		SELECT id, provider, model_id, revision, weights_hash, dimensions,
		       normalization, quantization, instruction_template, index_params,
		       status, created_at, updated_at
		FROM embedding_model_versions
		WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("embedding model version %s not found: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("get embedding model version %s: %w", id, err)
	}
	return record, nil
}

// ResolveConfiguredModelVersion treats deployment configuration as the
// runtime authority. The active pointer is deliberately not consulted: it is
// an operational cutover pointer, not a substitute for the configured model
// identity used by a running process.
func (r *VectorRepository) ResolveConfiguredModelVersion(
	ctx context.Context,
	expectedModelVersionID string,
	provider string,
	modelID string,
	dimensions int,
) (EmbeddingModelVersionRecord, error) {
	expectedModelVersionID = strings.TrimSpace(expectedModelVersionID)
	provider, modelID, err := normalizeConfiguredModelIdentity(provider, modelID, dimensions)
	if err != nil {
		return EmbeddingModelVersionRecord{}, err
	}
	if expectedModelVersionID == "" {
		return r.resolveUniqueConfiguredModelVersion(ctx, provider, modelID, dimensions)
	}
	id, err := uuid.Parse(expectedModelVersionID)
	if err != nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("configured statement model version must be a UUID: %w", err)
	}
	record, err := r.GetEmbeddingModelVersion(ctx, id)
	if err != nil {
		return EmbeddingModelVersionRecord{}, err
	}
	if err := validateConfiguredModelVersion(record, provider, modelID, dimensions); err != nil {
		return EmbeddingModelVersionRecord{}, err
	}
	return record, nil
}

func (r *VectorRepository) resolveUniqueConfiguredModelVersion(
	ctx context.Context,
	provider string,
	modelID string,
	dimensions int,
) (EmbeddingModelVersionRecord, error) {
	if r == nil || r.db == nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("vector repository database is required")
	}
	rows, err := r.db.Query(ctx, configuredModelVersionLookupSQL(), provider, modelID, dimensions)
	if err != nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("query configured embedding model versions: %w", err)
	}
	defer rows.Close()

	candidates := make([]EmbeddingModelVersionRecord, 0, 2)
	for rows.Next() {
		record, err := scanEmbeddingModelVersion(rows)
		if err != nil {
			return EmbeddingModelVersionRecord{}, fmt.Errorf("scan configured embedding model version: %w", err)
		}
		candidates = append(candidates, record)
	}
	if err := rows.Err(); err != nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("iterate configured embedding model versions: %w", err)
	}
	return selectUniqueConfiguredModelVersion(candidates, provider, modelID, dimensions)
}

func configuredModelVersionLookupSQL() string {
	return `
		SELECT id, provider, model_id, revision, weights_hash, dimensions,
		       normalization, quantization, instruction_template, index_params,
		       status, created_at, updated_at
		FROM embedding_model_versions
		WHERE provider = $1
		  AND model_id = $2
		  AND dimensions = $3
		  AND status <> 'retired'
		ORDER BY id
		LIMIT 2`
}

func selectUniqueConfiguredModelVersion(
	candidates []EmbeddingModelVersionRecord,
	provider string,
	modelID string,
	dimensions int,
) (EmbeddingModelVersionRecord, error) {
	matching := make([]EmbeddingModelVersionRecord, 0, 2)
	for _, candidate := range candidates {
		if candidate.Status != EmbeddingModelStatusRetired &&
			candidate.Provider == provider &&
			candidate.ModelID == modelID &&
			candidate.Dimensions == dimensions {
			matching = append(matching, candidate)
		}
	}
	switch len(matching) {
	case 0:
		return EmbeddingModelVersionRecord{}, fmt.Errorf(
			"%w for provider=%q model=%q dimensions=%d",
			ErrConfiguredModelVersionNotFound, provider, modelID, dimensions,
		)
	case 1:
		return matching[0], nil
	default:
		return EmbeddingModelVersionRecord{}, fmt.Errorf(
			"%w for provider=%q model=%q dimensions=%d; set expected_statement_model_version_id",
			ErrConfiguredModelVersionAmbiguous, provider, modelID, dimensions,
		)
	}
}

func normalizeConfiguredModelIdentity(provider, modelID string, dimensions int) (string, string, error) {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	if provider == "" {
		return "", "", fmt.Errorf("configured embedding provider is required")
	}
	if modelID == "" {
		return "", "", fmt.Errorf("configured embedding model is required")
	}
	if dimensions <= 0 {
		return "", "", fmt.Errorf("configured embedding dimensions must be positive")
	}
	return provider, modelID, nil
}

func validateConfiguredModelVersion(
	record EmbeddingModelVersionRecord,
	provider string,
	modelID string,
	dimensions int,
) error {
	provider, modelID, err := normalizeConfiguredModelIdentity(provider, modelID, dimensions)
	if err != nil {
		return err
	}
	if record.Status == EmbeddingModelStatusRetired {
		return fmt.Errorf("configured embedding model version %s is retired", record.ID)
	}
	if record.Provider != provider {
		return fmt.Errorf("configured embedding provider mismatch for model version %s: registry=%q configured=%q", record.ID, record.Provider, provider)
	}
	if record.ModelID != modelID {
		return fmt.Errorf("configured embedding model mismatch for model version %s: registry=%q configured=%q", record.ID, record.ModelID, modelID)
	}
	if record.Dimensions != dimensions {
		return fmt.Errorf("configured embedding dimensions mismatch for model version %s: registry=%d configured=%d", record.ID, record.Dimensions, dimensions)
	}
	return nil
}

// RegisterEmbeddingModelVersion creates a shadow model version, or returns the
// existing matching row. A conflict with different non-identity metadata fails
// closed so operators cannot silently change how an existing vector space is
// interpreted.
func (r *VectorRepository) RegisterEmbeddingModelVersion(
	ctx context.Context,
	input EmbeddingModelVersionRegistration,
) (EmbeddingModelVersionRecord, error) {
	if r == nil || r.db == nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("vector repository database is required")
	}
	normalized, err := normalizeEmbeddingModelVersionRegistration(input)
	if err != nil {
		return EmbeddingModelVersionRecord{}, err
	}

	record, err := scanEmbeddingModelVersion(r.db.QueryRow(ctx, `
		INSERT INTO embedding_model_versions (
			provider,
			model_id,
			revision,
			weights_hash,
			dimensions,
			normalization,
			quantization,
			instruction_template,
			index_params,
			status
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'shadow')
		ON CONFLICT (provider, model_id, revision, weights_hash, dimensions, normalization, quantization)
		DO UPDATE SET updated_at = NOW()
		WHERE embedding_model_versions.instruction_template = EXCLUDED.instruction_template
		  AND embedding_model_versions.index_params = EXCLUDED.index_params
		RETURNING
			id,
			provider,
			model_id,
			revision,
			weights_hash,
			dimensions,
			normalization,
			quantization,
			instruction_template,
			index_params,
			status,
			created_at,
			updated_at`,
		normalized.Provider,
		normalized.ModelID,
		normalized.Revision,
		normalized.WeightsHash,
		normalized.Dimensions,
		normalized.Normalization,
		normalized.Quantization,
		normalized.InstructionTemplate,
		normalized.IndexParams,
	))
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("embedding model version already exists with different metadata")
	}
	if err != nil {
		return EmbeddingModelVersionRecord{}, fmt.Errorf("register embedding model version: %w", err)
	}
	return record, nil
}

// ListEmbeddingModelVersions returns active, shadow, and retired registrations
// so a Web operator can resume backfill or activation after a page reload.
func (r *VectorRepository) ListEmbeddingModelVersions(
	ctx context.Context,
	limit int,
) ([]EmbeddingModelVersionRecord, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("vector repository database is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, provider, model_id, revision, weights_hash, dimensions,
		       normalization, quantization, instruction_template, index_params,
		       status, created_at, updated_at
		FROM embedding_model_versions
		ORDER BY created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list embedding model versions: %w", err)
	}
	defer rows.Close()

	records := make([]EmbeddingModelVersionRecord, 0)
	for rows.Next() {
		record, err := scanEmbeddingModelVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan embedding model version: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate embedding model versions: %w", err)
	}
	return records, nil
}

// ActiveEmbeddingModelStatuses returns each DB active pointer joined with the
// immutable model metadata needed by operators and service integrations.
func (r *VectorRepository) ActiveEmbeddingModelStatuses(ctx context.Context) ([]ActiveEmbeddingModelStatus, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("vector repository database is required")
	}
	rows, err := r.db.Query(ctx, activeEmbeddingModelStatusSQL())
	if err != nil {
		return nil, fmt.Errorf("querying active embedding model status: %w", err)
	}
	defer rows.Close()

	var statuses []ActiveEmbeddingModelStatus
	for rows.Next() {
		var status ActiveEmbeddingModelStatus
		if err := rows.Scan(
			&status.EmbeddingKind,
			&status.ExpectedDimensions,
			&status.UpdatedBy,
			&status.Reason,
			&status.UpdatedAt,
			&status.ModelVersion.ID,
			&status.ModelVersion.Provider,
			&status.ModelVersion.ModelID,
			&status.ModelVersion.Revision,
			&status.ModelVersion.WeightsHash,
			&status.ModelVersion.Dimensions,
			&status.ModelVersion.Normalization,
			&status.ModelVersion.Quantization,
			&status.ModelVersion.InstructionTemplate,
			&status.ModelVersion.IndexParams,
			&status.ModelVersion.Status,
			&status.ModelVersion.CreatedAt,
			&status.ModelVersion.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning active embedding model status: %w", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating active embedding model statuses: %w", err)
	}
	return statuses, nil
}

func activeEmbeddingModelStatusSQL() string {
	return `
SELECT
    ap.embedding_kind,
    ap.expected_dimensions,
    ap.updated_by,
    ap.reason,
    ap.updated_at,
    mv.id,
    mv.provider,
    mv.model_id,
    mv.revision,
    mv.weights_hash,
    mv.dimensions,
    mv.normalization,
    mv.quantization,
    mv.instruction_template,
    mv.index_params,
    mv.status,
    mv.created_at,
    mv.updated_at
FROM embedding_active_pointers ap
JOIN embedding_model_versions mv
  ON mv.id = ap.model_version_id
ORDER BY ap.embedding_kind`
}

func normalizeEmbeddingModelVersionRegistration(
	input EmbeddingModelVersionRegistration,
) (EmbeddingModelVersionRegistration, error) {
	input.Provider = strings.TrimSpace(input.Provider)
	input.ModelID = strings.TrimSpace(input.ModelID)
	input.Revision = strings.TrimSpace(input.Revision)
	input.WeightsHash = strings.TrimSpace(input.WeightsHash)
	input.Normalization = strings.TrimSpace(input.Normalization)
	input.Quantization = strings.TrimSpace(input.Quantization)
	input.InstructionTemplate = strings.TrimSpace(input.InstructionTemplate)

	for name, value := range map[string]string{
		"provider":      input.Provider,
		"model_id":      input.ModelID,
		"revision":      input.Revision,
		"normalization": input.Normalization,
		"quantization":  input.Quantization,
	} {
		if value == "" {
			return EmbeddingModelVersionRegistration{}, fmt.Errorf("%s is required", name)
		}
	}
	if err := validateContentHash(input.WeightsHash); err != nil {
		return EmbeddingModelVersionRegistration{}, fmt.Errorf("weights_hash: %w", err)
	}
	if input.Dimensions <= 0 {
		return EmbeddingModelVersionRegistration{}, fmt.Errorf("dimensions must be positive")
	}

	indexParams := strings.TrimSpace(string(input.IndexParams))
	if indexParams == "" {
		indexParams = "{}"
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(indexParams), &decoded); err != nil {
		return EmbeddingModelVersionRegistration{}, fmt.Errorf("index_params must be a JSON object: %w", err)
	}
	if decoded == nil {
		return EmbeddingModelVersionRegistration{}, fmt.Errorf("index_params must be a JSON object")
	}
	input.IndexParams = json.RawMessage(indexParams)
	return input, nil
}

func scanEmbeddingModelVersion(row pgx.Row) (EmbeddingModelVersionRecord, error) {
	var record EmbeddingModelVersionRecord
	if err := row.Scan(
		&record.ID,
		&record.Provider,
		&record.ModelID,
		&record.Revision,
		&record.WeightsHash,
		&record.Dimensions,
		&record.Normalization,
		&record.Quantization,
		&record.InstructionTemplate,
		&record.IndexParams,
		&record.Status,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return EmbeddingModelVersionRecord{}, err
	}
	return record, nil
}
