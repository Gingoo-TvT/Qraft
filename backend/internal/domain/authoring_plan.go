package domain

const AuthoringPlanSchemaV1 = "algoforge.authoring-plan.v1"

// AuthoringPlanV1 contains generation guidance that must not be exposed to an
// independent verifier together with SemanticSpecV1.
type AuthoringPlanV1 struct {
	SchemaVersion           string                   `json:"schema_version"`
	BriefSHA256             string                   `json:"brief_sha256"`
	SemanticSpecSHA256      string                   `json:"semantic_spec_sha256"`
	CoreIdea                string                   `json:"core_idea"`
	ConceptRoles            []AuthoringConceptRoleV1 `json:"concept_roles"`
	IntendedSolution        AuthoringAlgorithmPlanV1 `json:"intended_solution"`
	BruteForceBaseline      AuthoringAlgorithmPlanV1 `json:"brute_force_baseline"`
	OracleCandidateStrategy AuthoringAlgorithmPlanV1 `json:"oracle_candidate_strategy"`
	FailureModes            []AuthoringFailureModeV1 `json:"failure_modes"`
	TestIntents             []AuthoringTestIntentV1  `json:"test_intents"`
	TargetDifficulty        int                      `json:"target_difficulty"`
	TeachingObjectives      []string                 `json:"teaching_objectives"`
	CreativeIntent          string                   `json:"creative_intent"`
}

type AuthoringConceptRoleV1 struct {
	Slug      string `json:"slug"`
	Role      string `json:"role"`
	Necessity string `json:"necessity"`
}

type AuthoringAlgorithmPlanV1 struct {
	Summary         string `json:"summary"`
	TimeComplexity  string `json:"time_complexity"`
	SpaceComplexity string `json:"space_complexity"`
}

type AuthoringFailureModeV1 struct {
	ID            string `json:"id"`
	Description   string `json:"description"`
	WitnessIntent string `json:"witness_intent"`
}

type AuthoringTestIntentV1 struct {
	Purpose          string   `json:"purpose"`
	ConstraintRegion string   `json:"constraint_region"`
	BoundaryRefs     []string `json:"boundary_refs,omitempty"`
}
