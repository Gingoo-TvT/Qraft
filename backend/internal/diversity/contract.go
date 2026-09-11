// Package diversity defines deterministic, side-effect-free contracts for the
// S5 concept-pool, micro-batch selection, and dedup-observation surfaces.
package diversity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	ConceptPoolSchemaV1            = "algoforge.s5-concept-pool.v1"
	ConceptSpecSchemaV1            = "algoforge.s5-concept-spec.v1"
	ConceptCanonicalizerVersionV1  = "algoforge.s5-concept-canonicalizer.v1"
	DedupObservationSchemaV1       = "algoforge.s5-dedup-observation.v1"
	ProvisionalReservationSchemaV1 = "algoforge.s5-provisional-reservation.v1"

	QualityTierInvalid   = "invalid"
	QualityTierViable    = "viable"
	QualityTierUncertain = "uncertain"
	UnknownValue         = "unknown"

	DedupDecisionPass        = "pass"
	DedupDecisionWarn        = "warn"
	DedupDecisionRejected    = "rejected"
	DedupDecisionCheckFailed = "check_failed"

	DedupStageConceptSelection = "concept_selection"
	DedupStagePreGeneration    = "pre_generation"
	DedupStagePostStatement    = "post_statement"

	DedupKindStatement = "statement"
	DedupKindSolution  = "solution"
	DedupKindStructure = "structure"

	CorpusTierSelectionCandidate = "selection_candidate"
	CorpusTierQuarantineAdvisory = "quarantine_advisory"
	CorpusTierHistoricalAdvisory = "historical_advisory"
)

const (
	conceptAttemptsPerPool = 2
	conceptsPerAttempt     = 2
	conceptsPerPool        = conceptAttemptsPerPool * conceptsPerAttempt
	maxTopKV1              = 100
)

var (
	sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	uuidPattern   = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

// ArtifactRefV1 binds a compact contract to an immutable receipt without
// copying model responses into workflow history.
type ArtifactRefV1 struct {
	SHA256 string `json:"sha256"`
	URI    string `json:"uri"`
}

// ConceptBudgetV1 is frozen before sampling. Creative attempts and concepts
// are fixed at 2x2; all call, retry, token, and wall budgets also include the
// separate normalization invocation.
type ConceptBudgetV1 struct {
	MaxCreativeAttempts int   `json:"max_creative_attempts"`
	MaxConcepts         int   `json:"max_concepts"`
	MaxModelCalls       int   `json:"max_model_calls"`
	MaxNetworkRetries   int   `json:"max_network_retries"`
	MaxTokens           int64 `json:"max_tokens"`
	MaxWallMilliseconds int64 `json:"max_wall_milliseconds"`
}

// ConceptAttemptUsageV1 distinguishes a creative resample from transport
// retries of the same logical attempt.
type ConceptAttemptUsageV1 struct {
	ModelCalls       int   `json:"model_calls"`
	NetworkRetries   int   `json:"network_retries"`
	Tokens           int64 `json:"tokens"`
	WallMilliseconds int64 `json:"wall_milliseconds"`
}

type ConceptPoolV1 struct {
	SchemaVersion  string                 `json:"schema_version"`
	BatchID        string                 `json:"batch_id"`
	SlotIndex      int                    `json:"slot_index"`
	SlotID         string                 `json:"slot_id"`
	BriefSHA256    string                 `json:"brief_sha256"`
	CorpusRevision string                 `json:"corpus_revision"`
	Budget         ConceptBudgetV1        `json:"budget"`
	Attempts       []ConceptAttemptV1     `json:"attempts"`
	Normalization  ConceptNormalizationV1 `json:"normalization"`
}

type ConceptAttemptV1 struct {
	AttemptIndex     int                   `json:"attempt_index"`
	LogicalAttemptID string                `json:"logical_attempt_id"`
	Receipt          ArtifactRefV1         `json:"receipt"`
	Usage            ConceptAttemptUsageV1 `json:"usage"`
	Concepts         []ConceptCardV1       `json:"concepts"`
}

// ConceptNormalizationV1 binds the separate low-temperature extraction call
// that turns free ConceptCards into ConceptSpecs.
type ConceptNormalizationV1 struct {
	NormalizerVersion string                `json:"normalizer_version"`
	Receipt           ArtifactRefV1         `json:"receipt"`
	Usage             ConceptAttemptUsageV1 `json:"usage"`
}

type ConceptCardV1 struct {
	ConceptIndex        int           `json:"concept_index"`
	ConceptID           string        `json:"concept_id"`
	OneParagraphPitch   string        `json:"one_paragraph_pitch"`
	UnresolvedQuestions []string      `json:"unresolved_questions"`
	Spec                ConceptSpecV1 `json:"spec"`
}

// ConceptSpecV1 is deliberately permissive about unknown extracted facts.
// Unknown does not mean invalid; QualityTier is the only selection category.
type ConceptSpecV1 struct {
	SchemaVersion         string   `json:"schema_version"`
	CanonicalizerVersion  string   `json:"canonicalizer_version"`
	ExtractionConfidence  float64  `json:"extraction_confidence"`
	QualityTier           string   `json:"quality_tier"`
	ProblemMode           string   `json:"problem_mode"`
	InputObject           string   `json:"input_object"`
	Topology              string   `json:"topology"`
	OperationModel        string   `json:"operation_model"`
	Objective             string   `json:"objective"`
	StateDimensions       []string `json:"state_dimensions"`
	TransitionOrInvariant string   `json:"transition_or_invariant"`
	SolutionOperatorSeq   []string `json:"solution_operator_sequence"`
	OutputForm            string   `json:"output_form"`
	ComplexityClass       string   `json:"complexity_class"`
	ConstraintRegime      string   `json:"constraint_regime"`
	WrongSolutionFamilies []string `json:"wrong_solution_families"`
}

// DedupObservationV1 preserves the distinction between a successful query
// returning zero neighbors and a query that never produced a count.
type DedupObservationV1 struct {
	SchemaVersion      string            `json:"schema_version"`
	Stage              string            `json:"stage"`
	ModelVersion       string            `json:"model_version,omitempty"`
	Kind               string            `json:"kind"`
	ContentHash        string            `json:"content_hash"`
	CorpusRevision     string            `json:"corpus_revision"`
	RequestedTopK      int               `json:"requested_top_k"`
	Threshold          float64           `json:"threshold"`
	Decision           string            `json:"decision"`
	NeighborCount      *int              `json:"neighbor_count,omitempty"`
	Neighbors          []DedupNeighborV1 `json:"neighbors,omitempty"`
	ExcludedLineageIDs []string          `json:"excluded_lineage_ids,omitempty"`
	Reason             string            `json:"reason,omitempty"`
}

type DedupNeighborV1 struct {
	Rank        int     `json:"rank"`
	ProblemID   string  `json:"problem_id"`
	ContentHash string  `json:"content_hash"`
	CorpusTier  string  `json:"corpus_tier"`
	Similarity  float64 `json:"similarity"`
}

// CanonicalConceptPoolV1 returns canonical JSON and its digest without
// mutating the caller's pool.
func CanonicalConceptPoolV1(pool ConceptPoolV1) ([]byte, string, error) {
	canonical, err := canonicalizeConceptPoolV1(pool)
	if err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", err
	}
	return encoded, SHA256Hex(encoded), nil
}

func canonicalizeConceptPoolV1(pool ConceptPoolV1) (ConceptPoolV1, error) {
	result := pool
	result.BatchID = canonicalText(pool.BatchID)
	result.SlotID = canonicalText(pool.SlotID)
	result.BriefSHA256 = strings.ToLower(strings.TrimSpace(pool.BriefSHA256))
	result.CorpusRevision = strings.ToLower(strings.TrimSpace(pool.CorpusRevision))
	result.Normalization.NormalizerVersion = canonicalText(pool.Normalization.NormalizerVersion)
	result.Normalization.Receipt.SHA256 = strings.ToLower(strings.TrimSpace(pool.Normalization.Receipt.SHA256))
	result.Normalization.Receipt.URI = strings.TrimSpace(pool.Normalization.Receipt.URI)
	result.Attempts = append([]ConceptAttemptV1(nil), pool.Attempts...)
	for attemptIndex := range result.Attempts {
		attempt := &result.Attempts[attemptIndex]
		attempt.LogicalAttemptID = canonicalText(attempt.LogicalAttemptID)
		attempt.Receipt.SHA256 = strings.ToLower(strings.TrimSpace(attempt.Receipt.SHA256))
		attempt.Receipt.URI = strings.TrimSpace(attempt.Receipt.URI)
		attempt.Concepts = append([]ConceptCardV1(nil), attempt.Concepts...)
		for conceptIndex := range attempt.Concepts {
			card := &attempt.Concepts[conceptIndex]
			card.ConceptID = canonicalText(card.ConceptID)
			card.OneParagraphPitch = canonicalText(card.OneParagraphPitch)
			card.UnresolvedQuestions = canonicalStringSet(card.UnresolvedQuestions)
			card.Spec = canonicalConceptSpecV1(card.Spec)
		}
		sort.Slice(attempt.Concepts, func(i, j int) bool {
			return attempt.Concepts[i].ConceptIndex < attempt.Concepts[j].ConceptIndex
		})
	}
	sort.Slice(result.Attempts, func(i, j int) bool {
		return result.Attempts[i].AttemptIndex < result.Attempts[j].AttemptIndex
	})
	if err := result.Validate(); err != nil {
		return ConceptPoolV1{}, err
	}
	return result, nil
}

func (pool ConceptPoolV1) Validate() error {
	if pool.SchemaVersion != ConceptPoolSchemaV1 {
		return fmt.Errorf("schema_version must be %q", ConceptPoolSchemaV1)
	}
	if err := validateStableID("batch_id", pool.BatchID); err != nil {
		return err
	}
	if pool.SlotIndex < 0 {
		return errors.New("slot_index must be non-negative")
	}
	if err := validateStableID("slot_id", pool.SlotID); err != nil {
		return err
	}
	if !sha256Pattern.MatchString(pool.BriefSHA256) || !sha256Pattern.MatchString(pool.CorpusRevision) {
		return errors.New("brief_sha256 and corpus_revision must be canonical SHA-256")
	}
	if err := pool.Budget.Validate(); err != nil {
		return err
	}
	if len(pool.Attempts) != conceptAttemptsPerPool {
		return fmt.Errorf("concept pool requires exactly %d creative attempts", conceptAttemptsPerPool)
	}
	seenAttempts := make(map[string]struct{}, conceptAttemptsPerPool)
	seenConcepts := make(map[string]struct{}, conceptsPerPool)
	seenPitches := make(map[string]struct{}, conceptsPerPool)
	if err := pool.Normalization.Validate(); err != nil {
		return err
	}
	usedCalls := pool.Normalization.Usage.ModelCalls
	usedRetries := pool.Normalization.Usage.NetworkRetries
	usedTokens := pool.Normalization.Usage.Tokens
	usedWall := pool.Normalization.Usage.WallMilliseconds
	for index, attempt := range pool.Attempts {
		if attempt.AttemptIndex != index {
			return fmt.Errorf("attempt %d has non-canonical attempt_index %d", index, attempt.AttemptIndex)
		}
		if err := validateStableID("logical_attempt_id", attempt.LogicalAttemptID); err != nil {
			return err
		}
		if _, duplicate := seenAttempts[attempt.LogicalAttemptID]; duplicate {
			return fmt.Errorf("logical_attempt_id %q is duplicated", attempt.LogicalAttemptID)
		}
		seenAttempts[attempt.LogicalAttemptID] = struct{}{}
		if err := attempt.Receipt.Validate(); err != nil {
			return fmt.Errorf("attempt %d receipt: %w", index, err)
		}
		if err := attempt.Usage.Validate(); err != nil {
			return fmt.Errorf("attempt %d usage: %w", index, err)
		}
		usedCalls += attempt.Usage.ModelCalls
		usedRetries += attempt.Usage.NetworkRetries
		usedTokens += attempt.Usage.Tokens
		usedWall += attempt.Usage.WallMilliseconds
		if len(attempt.Concepts) != conceptsPerAttempt {
			return fmt.Errorf("attempt %d requires exactly %d concepts", index, conceptsPerAttempt)
		}
		for conceptIndex, card := range attempt.Concepts {
			if card.ConceptIndex != conceptIndex {
				return fmt.Errorf("attempt %d concept %d has non-canonical concept_index %d", index, conceptIndex, card.ConceptIndex)
			}
			if err := validateStableID("concept_id", card.ConceptID); err != nil {
				return err
			}
			if _, duplicate := seenConcepts[card.ConceptID]; duplicate {
				return fmt.Errorf("concept_id %q is duplicated", card.ConceptID)
			}
			seenConcepts[card.ConceptID] = struct{}{}
			if card.OneParagraphPitch == "" || len(card.OneParagraphPitch) > 4096 {
				return fmt.Errorf("concept %q pitch is empty or too long", card.ConceptID)
			}
			pitchKey := strings.ToLower(card.OneParagraphPitch)
			if _, duplicate := seenPitches[pitchKey]; duplicate {
				return fmt.Errorf("concept pitch %q is duplicated", card.OneParagraphPitch)
			}
			seenPitches[pitchKey] = struct{}{}
			if len(card.UnresolvedQuestions) > 16 {
				return fmt.Errorf("concept %q has too many unresolved questions", card.ConceptID)
			}
			if err := validateCanonicalStringSet("unresolved_questions", card.UnresolvedQuestions); err != nil {
				return fmt.Errorf("concept %q: %w", card.ConceptID, err)
			}
			if err := card.Spec.Validate(); err != nil {
				return fmt.Errorf("concept %q spec: %w", card.ConceptID, err)
			}
		}
	}
	if usedCalls > pool.Budget.MaxModelCalls || usedRetries > pool.Budget.MaxNetworkRetries ||
		usedTokens > pool.Budget.MaxTokens || usedWall > pool.Budget.MaxWallMilliseconds {
		return errors.New("concept pool usage exceeds its frozen budget")
	}
	return nil
}

func (normalization ConceptNormalizationV1) Validate() error {
	if err := validateStableID("normalizer_version", normalization.NormalizerVersion); err != nil {
		return err
	}
	if err := normalization.Receipt.Validate(); err != nil {
		return fmt.Errorf("normalization receipt: %w", err)
	}
	if err := normalization.Usage.Validate(); err != nil {
		return fmt.Errorf("normalization usage: %w", err)
	}
	return nil
}

func (budget ConceptBudgetV1) Validate() error {
	if budget.MaxCreativeAttempts != conceptAttemptsPerPool || budget.MaxConcepts != conceptsPerPool {
		return fmt.Errorf("concept budget must freeze %d attempts and %d concepts", conceptAttemptsPerPool, conceptsPerPool)
	}
	if budget.MaxModelCalls < conceptAttemptsPerPool+1 || budget.MaxNetworkRetries < 0 ||
		budget.MaxTokens <= 0 || budget.MaxWallMilliseconds <= 0 {
		return errors.New("concept budget call, retry, token, or wall limit is invalid")
	}
	return nil
}

func (usage ConceptAttemptUsageV1) Validate() error {
	if usage.NetworkRetries < 0 || usage.ModelCalls != 1+usage.NetworkRetries ||
		usage.Tokens < 0 || usage.WallMilliseconds < 0 {
		return errors.New("model calls must equal one logical invocation plus network retries and usage must be non-negative")
	}
	return nil
}

func (ref ArtifactRefV1) Validate() error {
	if !sha256Pattern.MatchString(ref.SHA256) || strings.TrimSpace(ref.URI) == "" || len(ref.URI) > 2048 {
		return errors.New("artifact ref requires a canonical SHA-256 and bounded URI")
	}
	return nil
}

func canonicalConceptSpecV1(spec ConceptSpecV1) ConceptSpecV1 {
	result := spec
	result.QualityTier = canonicalLowerText(spec.QualityTier)
	result.ProblemMode = canonicalLowerText(spec.ProblemMode)
	result.InputObject = canonicalLowerText(spec.InputObject)
	result.Topology = canonicalLowerText(spec.Topology)
	result.OperationModel = canonicalLowerText(spec.OperationModel)
	result.Objective = canonicalLowerText(spec.Objective)
	result.StateDimensions = canonicalLowerStringSet(spec.StateDimensions)
	result.TransitionOrInvariant = canonicalLowerText(spec.TransitionOrInvariant)
	result.SolutionOperatorSeq = canonicalLowerStringList(spec.SolutionOperatorSeq)
	result.OutputForm = canonicalLowerText(spec.OutputForm)
	result.ComplexityClass = canonicalLowerText(spec.ComplexityClass)
	result.ConstraintRegime = canonicalLowerText(spec.ConstraintRegime)
	result.WrongSolutionFamilies = canonicalLowerStringSet(spec.WrongSolutionFamilies)
	return result
}

func (spec ConceptSpecV1) Validate() error {
	if spec.SchemaVersion != ConceptSpecSchemaV1 || spec.CanonicalizerVersion != ConceptCanonicalizerVersionV1 {
		return errors.New("concept spec schema or canonicalizer version is invalid")
	}
	if math.IsNaN(spec.ExtractionConfidence) || math.IsInf(spec.ExtractionConfidence, 0) ||
		spec.ExtractionConfidence < 0 || spec.ExtractionConfidence > 1 {
		return errors.New("extraction_confidence must be finite and within [0,1]")
	}
	switch spec.QualityTier {
	case QualityTierInvalid, QualityTierViable, QualityTierUncertain:
	default:
		return fmt.Errorf("unsupported quality_tier %q", spec.QualityTier)
	}
	for name, value := range map[string]string{
		"problem_mode": spec.ProblemMode, "input_object": spec.InputObject, "topology": spec.Topology,
		"operation_model": spec.OperationModel, "objective": spec.Objective,
		"transition_or_invariant": spec.TransitionOrInvariant, "output_form": spec.OutputForm,
		"complexity_class": spec.ComplexityClass, "constraint_regime": spec.ConstraintRegime,
	} {
		if value == "" || len(value) > 512 {
			return fmt.Errorf("%s must be non-empty (use %q when unknown) and bounded", name, UnknownValue)
		}
	}
	for name, values := range map[string][]string{
		"state_dimensions":        spec.StateDimensions,
		"wrong_solution_families": spec.WrongSolutionFamilies,
	} {
		if len(values) == 0 {
			return fmt.Errorf("%s must be non-empty (use %q when unknown)", name, UnknownValue)
		}
		if err := validateCanonicalStringSet(name, values); err != nil {
			return err
		}
	}
	if len(spec.SolutionOperatorSeq) == 0 {
		return fmt.Errorf("solution_operator_sequence must be non-empty (use %q when unknown)", UnknownValue)
	}
	if err := validateCanonicalStringList("solution_operator_sequence", spec.SolutionOperatorSeq); err != nil {
		return err
	}
	return nil
}

func CanonicalDedupObservationV1(observation DedupObservationV1) ([]byte, string, error) {
	canonical := observation
	canonical.Stage = canonicalLowerText(observation.Stage)
	canonical.ModelVersion = canonicalText(observation.ModelVersion)
	canonical.Kind = canonicalLowerText(observation.Kind)
	canonical.ContentHash = strings.ToLower(strings.TrimSpace(observation.ContentHash))
	canonical.CorpusRevision = strings.ToLower(strings.TrimSpace(observation.CorpusRevision))
	canonical.Decision = canonicalLowerText(observation.Decision)
	canonical.Reason = canonicalText(observation.Reason)
	canonical.ExcludedLineageIDs = canonicalLowerStringSet(observation.ExcludedLineageIDs)
	canonical.Neighbors = append([]DedupNeighborV1(nil), observation.Neighbors...)
	for index := range canonical.Neighbors {
		canonical.Neighbors[index].ProblemID = strings.ToLower(strings.TrimSpace(canonical.Neighbors[index].ProblemID))
		canonical.Neighbors[index].ContentHash = strings.ToLower(strings.TrimSpace(canonical.Neighbors[index].ContentHash))
		canonical.Neighbors[index].CorpusTier = canonicalLowerText(canonical.Neighbors[index].CorpusTier)
	}
	if len(canonical.Neighbors) == 0 {
		canonical.Neighbors = nil
	}
	if len(canonical.ExcludedLineageIDs) == 0 {
		canonical.ExcludedLineageIDs = nil
	}
	if err := canonical.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", err
	}
	return encoded, SHA256Hex(encoded), nil
}

func (observation DedupObservationV1) Validate() error {
	if observation.SchemaVersion != DedupObservationSchemaV1 {
		return fmt.Errorf("schema_version must be %q", DedupObservationSchemaV1)
	}
	switch observation.Stage {
	case DedupStageConceptSelection, DedupStagePreGeneration, DedupStagePostStatement:
	default:
		return fmt.Errorf("unsupported dedup stage %q", observation.Stage)
	}
	switch observation.Kind {
	case DedupKindStatement, DedupKindSolution, DedupKindStructure:
	default:
		return fmt.Errorf("unsupported dedup kind %q", observation.Kind)
	}
	if !sha256Pattern.MatchString(observation.ContentHash) || !sha256Pattern.MatchString(observation.CorpusRevision) {
		return errors.New("content_hash and corpus_revision must be canonical SHA-256")
	}
	if observation.RequestedTopK <= 0 || observation.RequestedTopK > maxTopKV1 {
		return fmt.Errorf("requested_top_k must be within [1,%d]", maxTopKV1)
	}
	if math.IsNaN(observation.Threshold) || math.IsInf(observation.Threshold, 0) ||
		observation.Threshold < 0 || observation.Threshold > 1 {
		return errors.New("threshold must be finite and within [0,1]")
	}
	if err := validateCanonicalUUIDSet("excluded_lineage_ids", observation.ExcludedLineageIDs); err != nil {
		return err
	}
	if observation.Decision == DedupDecisionCheckFailed {
		if observation.NeighborCount != nil {
			return errors.New("check_failed must not expose neighbor_count")
		}
		if len(observation.Neighbors) != 0 {
			return errors.New("check_failed must not expose neighbors")
		}
		if observation.Reason == "" {
			return errors.New("check_failed requires a reason")
		}
		return nil
	}
	if observation.ModelVersion == "" {
		return errors.New("a successful dedup query requires model_version")
	}
	switch observation.Decision {
	case DedupDecisionPass, DedupDecisionWarn, DedupDecisionRejected:
	default:
		return fmt.Errorf("unsupported dedup decision %q", observation.Decision)
	}
	if observation.NeighborCount == nil {
		return errors.New("a successful dedup query requires neighbor_count, including explicit zero")
	}
	if *observation.NeighborCount != len(observation.Neighbors) || *observation.NeighborCount < 0 ||
		*observation.NeighborCount > observation.RequestedTopK {
		return errors.New("neighbor_count must match the bounded ordered neighbor list")
	}
	if *observation.NeighborCount == 0 && observation.Decision != DedupDecisionPass {
		return errors.New("zero neighbors can only produce a pass decision")
	}
	excluded := make(map[string]struct{}, len(observation.ExcludedLineageIDs))
	for _, id := range observation.ExcludedLineageIDs {
		excluded[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(observation.Neighbors))
	previousSimilarity := math.Inf(1)
	for index, neighbor := range observation.Neighbors {
		if neighbor.Rank != index+1 || !uuidPattern.MatchString(neighbor.ProblemID) || !sha256Pattern.MatchString(neighbor.ContentHash) {
			return fmt.Errorf("neighbor %d identity or rank is invalid", index)
		}
		if _, duplicate := seen[neighbor.ProblemID]; duplicate {
			return fmt.Errorf("neighbor problem_id %q is duplicated", neighbor.ProblemID)
		}
		seen[neighbor.ProblemID] = struct{}{}
		if _, isLineage := excluded[neighbor.ProblemID]; isLineage {
			return fmt.Errorf("neighbor %q belongs to excluded lineage", neighbor.ProblemID)
		}
		switch neighbor.CorpusTier {
		case CorpusTierSelectionCandidate, CorpusTierQuarantineAdvisory, CorpusTierHistoricalAdvisory:
		default:
			return fmt.Errorf("neighbor %d has unsupported corpus tier %q", index, neighbor.CorpusTier)
		}
		if math.IsNaN(neighbor.Similarity) || math.IsInf(neighbor.Similarity, 0) ||
			neighbor.Similarity < 0 || neighbor.Similarity > 1 || neighbor.Similarity > previousSimilarity {
			return fmt.Errorf("neighbor %d similarity is invalid or not descending", index)
		}
		previousSimilarity = neighbor.Similarity
	}
	return nil
}

func SHA256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func canonicalText(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(value, "\r\n", "\n")), " ")
}

func canonicalLowerText(value string) string {
	return strings.ToLower(canonicalText(value))
}

func canonicalStringSet(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = canonicalText(value)
	}
	sort.Strings(result)
	return result
}

func canonicalLowerStringSet(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = canonicalLowerText(value)
	}
	sort.Strings(result)
	return result
}

func canonicalLowerStringList(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = canonicalLowerText(value)
	}
	return result
}

func validateStableID(name, value string) error {
	if value == "" || len(value) > 256 {
		return fmt.Errorf("%s is empty or too long", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func validateCanonicalStringSet(name string, values []string) error {
	previous := ""
	for index, value := range values {
		if value == "" || len(value) > 512 {
			return fmt.Errorf("%s[%d] is empty or too long", name, index)
		}
		if index > 0 && value <= previous {
			return fmt.Errorf("%s must be strictly ascending and unique", name)
		}
		previous = value
	}
	return nil
}

func validateCanonicalStringList(name string, values []string) error {
	for index, value := range values {
		if value == "" || len(value) > 512 {
			return fmt.Errorf("%s[%d] is empty or too long", name, index)
		}
	}
	return nil
}

func validateCanonicalUUIDSet(name string, values []string) error {
	if err := validateCanonicalStringSet(name, values); err != nil {
		return err
	}
	for index, value := range values {
		if !uuidPattern.MatchString(value) {
			return fmt.Errorf("%s[%d] is not a canonical UUID", name, index)
		}
	}
	return nil
}
