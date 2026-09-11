package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeEmbeddingModelVersionRegistration(t *testing.T) {
	input := EmbeddingModelVersionRegistration{
		Provider:      " self-hosted ",
		ModelID:       " Qwen/Qwen3-Embedding-8B ",
		Revision:      " rev-1 ",
		WeightsHash:   strings.Repeat("a", 64),
		Dimensions:    1536,
		Normalization: " mrl-1536+l2 ",
		Quantization:  " fp8 ",
	}

	got, err := normalizeEmbeddingModelVersionRegistration(input)
	if err != nil {
		t.Fatalf("normalizeEmbeddingModelVersionRegistration() error = %v", err)
	}
	if got.Provider != "self-hosted" || got.ModelID != "Qwen/Qwen3-Embedding-8B" ||
		got.Revision != "rev-1" || got.Normalization != "mrl-1536+l2" ||
		got.Quantization != "fp8" {
		t.Fatalf("fields were not normalized: %+v", got)
	}
	if string(got.IndexParams) != "{}" {
		t.Fatalf("default index params = %s, want {}", got.IndexParams)
	}
}

func TestNormalizeEmbeddingModelVersionRegistrationFailsClosed(t *testing.T) {
	valid := EmbeddingModelVersionRegistration{
		Provider:      "self-hosted",
		ModelID:       "Qwen/Qwen3-Embedding-8B",
		Revision:      "rev-1",
		WeightsHash:   strings.Repeat("a", 64),
		Dimensions:    1536,
		Normalization: "mrl-1536+l2",
		Quantization:  "fp8",
		IndexParams:   []byte(`{"index":"ivfflat"}`),
	}
	tests := []struct {
		name    string
		edit    func(*EmbeddingModelVersionRegistration)
		wantErr string
	}{
		{name: "provider", edit: func(v *EmbeddingModelVersionRegistration) { v.Provider = " " }, wantErr: "provider is required"},
		{name: "model id", edit: func(v *EmbeddingModelVersionRegistration) { v.ModelID = "" }, wantErr: "model_id is required"},
		{name: "revision", edit: func(v *EmbeddingModelVersionRegistration) { v.Revision = "" }, wantErr: "revision is required"},
		{name: "weights hash", edit: func(v *EmbeddingModelVersionRegistration) { v.WeightsHash = strings.Repeat("A", 64) }, wantErr: "weights_hash"},
		{name: "dimensions", edit: func(v *EmbeddingModelVersionRegistration) { v.Dimensions = 0 }, wantErr: "dimensions must be positive"},
		{name: "normalization", edit: func(v *EmbeddingModelVersionRegistration) { v.Normalization = "" }, wantErr: "normalization is required"},
		{name: "quantization", edit: func(v *EmbeddingModelVersionRegistration) { v.Quantization = "" }, wantErr: "quantization is required"},
		{name: "index params json", edit: func(v *EmbeddingModelVersionRegistration) { v.IndexParams = []byte(`not json`) }, wantErr: "index_params"},
		{name: "index params object", edit: func(v *EmbeddingModelVersionRegistration) { v.IndexParams = []byte(`[]`) }, wantErr: "index_params"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.edit(&input)
			_, err := normalizeEmbeddingModelVersionRegistration(input)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRegisterEmbeddingModelVersionRequiresDB(t *testing.T) {
	var repo *VectorRepository
	_, err := repo.RegisterEmbeddingModelVersion(context.Background(), EmbeddingModelVersionRegistration{})
	if err == nil || !strings.Contains(err.Error(), "database is required") {
		t.Fatalf("error = %v, want database requirement", err)
	}
}

func TestActiveEmbeddingModelStatusesRequiresDB(t *testing.T) {
	var repo *VectorRepository
	_, err := repo.ActiveEmbeddingModelStatuses(context.Background())
	if err == nil || !strings.Contains(err.Error(), "database is required") {
		t.Fatalf("error = %v, want database requirement", err)
	}
}

func TestActiveEmbeddingModelStatusSQLJoinsPointersAndVersions(t *testing.T) {
	query := activeEmbeddingModelStatusSQL()
	for _, fragment := range []string{
		"embedding_active_pointers ap",
		"embedding_model_versions mv",
		"mv.id = ap.model_version_id",
		"ORDER BY ap.embedding_kind",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("active status SQL missing %q:\n%s", fragment, query)
		}
	}
}

func TestValidateConfiguredModelVersion(t *testing.T) {
	record := EmbeddingModelVersionRecord{
		ID:         uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Provider:   "openai-compatible:https://embedding.example/v1",
		ModelID:    "embedding-model",
		Dimensions: 1536,
	}
	if err := validateConfiguredModelVersion(
		record,
		" openai-compatible:https://embedding.example/v1 ",
		" embedding-model ",
		1536,
	); err != nil {
		t.Fatalf("matching configured identity: %v", err)
	}
	retired := record
	retired.Status = EmbeddingModelStatusRetired
	if err := validateConfiguredModelVersion(
		retired, record.Provider, record.ModelID, record.Dimensions,
	); err == nil || !strings.Contains(err.Error(), "is retired") {
		t.Fatalf("retired configured model error = %v", err)
	}

	tests := []struct {
		name       string
		provider   string
		model      string
		dimensions int
		want       string
	}{
		{name: "provider mismatch", provider: "openai-compatible:https://other.example/v1", model: "embedding-model", dimensions: 1536, want: "provider mismatch"},
		{name: "model mismatch", provider: record.Provider, model: "other-model", dimensions: 1536, want: "model mismatch"},
		{name: "dimensions mismatch", provider: record.Provider, model: record.ModelID, dimensions: 1024, want: "dimensions mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfiguredModelVersion(record, tt.provider, tt.model, tt.dimensions)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("identity error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestResolveConfiguredModelVersionRejectsInvalidExplicitPinBeforeDatabase(t *testing.T) {
	var repo *VectorRepository
	_, err := repo.ResolveConfiguredModelVersion(context.Background(), "not-a-uuid", "provider", "model", 1536)
	if err == nil || !strings.Contains(err.Error(), "must be a UUID") {
		t.Fatalf("invalid configured model version pin error = %v", err)
	}
}

func TestSelectUniqueConfiguredModelVersion(t *testing.T) {
	identityProvider := "openai-compatible:https://embedding.example/v1"
	identityModel := "embedding-model"
	first := EmbeddingModelVersionRecord{
		ID:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Provider: identityProvider, ModelID: identityModel, Dimensions: 1536,
		Status: EmbeddingModelStatusShadow,
	}
	second := first
	second.ID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	second.Status = EmbeddingModelStatusActive

	got, err := selectUniqueConfiguredModelVersion(
		[]EmbeddingModelVersionRecord{first}, identityProvider, identityModel, 1536,
	)
	if err != nil || got.ID != first.ID {
		t.Fatalf("unique configured model = %+v, err=%v", got, err)
	}
	_, err = selectUniqueConfiguredModelVersion(nil, identityProvider, identityModel, 1536)
	if !errors.Is(err, ErrConfiguredModelVersionNotFound) {
		t.Fatalf("zero configured models error = %v", err)
	}
	_, err = selectUniqueConfiguredModelVersion(
		[]EmbeddingModelVersionRecord{first, second}, identityProvider, identityModel, 1536,
	)
	if !errors.Is(err, ErrConfiguredModelVersionAmbiguous) {
		t.Fatalf("ambiguous configured models error = %v", err)
	}
	retired := first
	retired.Status = EmbeddingModelStatusRetired
	_, err = selectUniqueConfiguredModelVersion(
		[]EmbeddingModelVersionRecord{retired}, identityProvider, identityModel, 1536,
	)
	if !errors.Is(err, ErrConfiguredModelVersionNotFound) {
		t.Fatalf("retired configured model error = %v", err)
	}
}

func TestConfiguredModelVersionLookupDoesNotUseActivePointer(t *testing.T) {
	query := configuredModelVersionLookupSQL()
	for _, required := range []string{
		"FROM embedding_model_versions",
		"provider = $1",
		"model_id = $2",
		"dimensions = $3",
		"status <> 'retired'",
		"LIMIT 2",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("configured model lookup SQL missing %q:\n%s", required, query)
		}
	}
	if strings.Contains(query, "embedding_active_pointers") {
		t.Fatalf("configured model lookup must not read active pointer:\n%s", query)
	}
}
