// Package diversityapi defines the additive product contract for S5
// three-slot micro-batches. The stable generation-jobs v1 contract remains a
// single-candidate API and is deliberately reused, not widened.
package diversityapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
)

const (
	ContractVersion = "algoforge.generation-micro-batch.v1"
	BatchIDPrefix   = "generation-micro-batch-v1-"
	SlotCountV1     = 3
	MaxRequestBytes = SlotCountV1*generationapi.MaxRequestBytes + 4096

	WorkflowTypeV1 = "S5MicroBatchGenerationWorkflowV1"

	MemoContractVersionKey = "s5_micro_batch_contract_version"
	MemoPayloadSHA256Key   = "s5_micro_batch_payload_sha256"
	MemoPrincipalScopeKey  = "s5_micro_batch_principal_scope_sha256"
)

type RequestV1 struct {
	SchemaVersion string                  `json:"schema_version"`
	Slots         []generationapi.Request `json:"slots"`
}

// BatchLinksV1 keeps Temporal details behind the product resource. Child
// workflows are intentionally not exposed as standalone generation jobs:
// their lifecycle is owned by this parent micro-batch.
type BatchLinksV1 struct {
	Status string `json:"status"`
	Result string `json:"result"`
	Cancel string `json:"cancel"`
}

type BatchAcceptedV1 struct {
	ContractVersion  string       `json:"contract_version"`
	BatchID          string       `json:"batch_id"`
	Status           string       `json:"status"`
	IdempotentReplay bool         `json:"idempotent_replay"`
	Links            BatchLinksV1 `json:"links"`
}

type SlotStatusV1 struct {
	SlotIndex int    `json:"slot_index"`
	ChildID   string `json:"child_id"`
	Status    string `json:"status"`
}

type BatchStatusV1 struct {
	ContractVersion   string                  `json:"contract_version"`
	BatchID           string                  `json:"batch_id"`
	Status            string                  `json:"status"`
	Phase             string                  `json:"phase"`
	Progress          int                     `json:"progress"`
	ResultAvailable   bool                    `json:"result_available"`
	CorpusRevision    string                  `json:"corpus_revision,omitempty"`
	ReservationSHA256 string                  `json:"reservation_sha256,omitempty"`
	ManifestSHA256    string                  `json:"manifest_sha256,omitempty"`
	Slots             []SlotStatusV1          `json:"slots"`
	Error             *generationapi.JobError `json:"error,omitempty"`
	Links             BatchLinksV1            `json:"links"`
}

type SlotResultV1 struct {
	SlotIndex                 int                     `json:"slot_index"`
	ChildID                   string                  `json:"child_id"`
	Status                    string                  `json:"status"`
	ConceptID                 string                  `json:"concept_id,omitempty"`
	PoolSHA256                string                  `json:"pool_sha256,omitempty"`
	StructuralSignatureSHA256 string                  `json:"structural_signature_sha256,omitempty"`
	ProblemID                 string                  `json:"problem_id,omitempty"`
	ProblemURL                string                  `json:"problem_url,omitempty"`
	Error                     *generationapi.JobError `json:"error,omitempty"`
}

type BatchResultV1 struct {
	ContractVersion   string                         `json:"contract_version"`
	BatchID           string                         `json:"batch_id"`
	Status            string                         `json:"status"`
	CorpusRevision    string                         `json:"corpus_revision,omitempty"`
	ReservationSHA256 string                         `json:"reservation_sha256,omitempty"`
	ManifestSHA256    string                         `json:"manifest_sha256,omitempty"`
	DedupObservations []diversity.DedupObservationV1 `json:"dedup_observations,omitempty"`
	Slots             []SlotResultV1                 `json:"slots"`
	Error             *generationapi.JobError        `json:"error,omitempty"`
}

type BatchCancellationV1 struct {
	ContractVersion string       `json:"contract_version"`
	BatchID         string       `json:"batch_id"`
	Status          string       `json:"status"`
	Links           BatchLinksV1 `json:"links"`
}

// Canonical preserves slot order because 0, 1, and 2 are part of the frozen
// selector input. Fields inside each slot use the stable Job API
// canonicalizer.
func (request RequestV1) Canonical() RequestV1 {
	request.SchemaVersion = strings.TrimSpace(request.SchemaVersion)
	request.Slots = append([]generationapi.Request(nil), request.Slots...)
	for index := range request.Slots {
		request.Slots[index] = request.Slots[index].Canonical()
	}
	return request
}

func (request RequestV1) Validate() error {
	request = request.Canonical()
	if request.SchemaVersion != ContractVersion {
		return fmt.Errorf("schema_version must be %q", ContractVersion)
	}
	if len(request.Slots) != SlotCountV1 {
		return fmt.Errorf("slots must contain exactly %d generation requests", SlotCountV1)
	}
	for index, slot := range request.Slots {
		if err := slot.ValidateV1(); err != nil {
			return fmt.Errorf("slots[%d]: %w", index, err)
		}
		if slot.CandidateCount != 1 {
			return fmt.Errorf("slots[%d].candidate_count must remain 1", index)
		}
	}
	return nil
}

func (request RequestV1) CanonicalPayload() ([]byte, string, error) {
	request = request.Canonical()
	if err := request.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical S5 micro-batch request: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

func BatchIDForIdempotencyKey(principalScope, key string) (string, string, error) {
	principalScope = strings.TrimSpace(principalScope)
	if principalScope == "" {
		return "", "", fmt.Errorf("principal scope is required")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("idempotency key is required")
	}
	if len(key) > 256 {
		return "", "", fmt.Errorf("idempotency key exceeds 256 bytes")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return "", "", fmt.Errorf("idempotency key contains control characters")
		}
	}
	scopeDigest := sha256.Sum256([]byte(principalScope))
	batchDigest := sha256.Sum256([]byte(principalScope + "\x00" + key))
	return BatchIDPrefix + hex.EncodeToString(batchDigest[:]), hex.EncodeToString(scopeDigest[:]), nil
}

func IsBatchID(value string) bool {
	if !strings.HasPrefix(value, BatchIDPrefix) {
		return false
	}
	digest := strings.TrimPrefix(value, BatchIDPrefix)
	if len(digest) != sha256.Size*2 || digest != strings.ToLower(digest) {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size
}

// ChildJobID returns a stable opaque identifier accepted by the existing S3
// product materializer. No caller-controlled content is exposed in the ID.
func ChildJobID(batchID string, slotIndex int) (string, error) {
	if !IsBatchID(batchID) {
		return "", fmt.Errorf("invalid S5 micro-batch id")
	}
	if slotIndex < 0 || slotIndex >= SlotCountV1 {
		return "", fmt.Errorf("slot index must be in [0,%d]", SlotCountV1-1)
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00slot:%d", batchID, slotIndex)))
	return generationapi.JobIDPrefix + hex.EncodeToString(digest[:]), nil
}

func (request RequestV1) WorkflowTimeout() time.Duration {
	request = request.Canonical()
	var maximum time.Duration
	for _, slot := range request.Slots {
		if duration := slot.WorkflowTimeout(); duration > maximum {
			maximum = duration
		}
	}
	return maximum
}
