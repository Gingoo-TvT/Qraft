package provenance

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	FixtureCatalogSchemaVersion = "1.0.0"
	fixturePolicySchemaVersion  = "1.0.0"
	termsSnapshotSchemaVersion  = "1.0.0"
)

// FixtureRecord binds one versioned fixture file to its provenance record and
// the terms snapshot used to make the three purpose decisions.
type FixtureRecord struct {
	Path              string   `json:"path"`
	TermsSnapshotID   string   `json:"terms_snapshot_id"`
	TermsSnapshotPath string   `json:"terms_snapshot_path"`
	Artifact          Artifact `json:"artifact"`
}

// FixtureCatalog is the checked-in input to the scoped S1 provenance audit.
// AsOf is frozen so the same catalog produces byte-identical decisions.
type FixtureCatalog struct {
	SchemaVersion string          `json:"schema_version"`
	PolicyVersion string          `json:"policy_version"`
	PolicyPath    string          `json:"policy_path"`
	PolicySHA256  string          `json:"policy_sha256"`
	AsOf          time.Time       `json:"as_of"`
	FixtureRoots  []string        `json:"fixture_roots"`
	Artifacts     []FixtureRecord `json:"artifacts"`
}

type explicitPermissionSet struct {
	InternalEval    *bool `json:"internal_eval_allowed"`
	PrivateTraining *bool `json:"private_training_allowed"`
	PublicRelease   *bool `json:"public_release_allowed"`
}

type fixturePolicyDocument struct {
	SchemaVersion        string                `json:"schema_version"`
	PolicyVersion        string                `json:"policy_version"`
	Default              explicitPermissionSet `json:"default"`
	Rules                []fixturePolicyRule   `json:"rules"`
	Inheritance          string                `json:"inheritance"`
	UnknownValueBehavior string                `json:"unknown_value_behavior"`
	TakedownBehavior     string                `json:"takedown_behavior"`
}

type fixturePolicyRule struct {
	LicenseBasis string `json:"license_basis"`
	explicitPermissionSet
	Requires []string `json:"requires"`
}

type fixtureTermsSnapshot struct {
	SchemaVersion    string                `json:"schema_version"`
	SnapshotID       string                `json:"snapshot_id"`
	Source           string                `json:"source"`
	CapturedAt       time.Time             `json:"captured_at"`
	EffectiveAt      time.Time             `json:"effective_at"`
	ContentOwner     string                `json:"content_owner,omitempty"`
	LicenseBasis     string                `json:"license_basis"`
	Scope            []string              `json:"scope"`
	Permissions      explicitPermissionSet `json:"permissions"`
	Evidence         map[string]string     `json:"evidence,omitempty"`
	Reviewer         string                `json:"reviewer"`
	ReviewStatus     string                `json:"review_status"`
	StorageReference string                `json:"storage_reference"`
	PolicyVersion    string                `json:"policy_version"`
}

type FixturePurposeDecisions struct {
	InternalEval    Decision `json:"internal_eval"`
	PrivateTraining Decision `json:"private_training"`
	PublicRelease   Decision `json:"public_release"`
}

type FixtureAuditRecord struct {
	ArtifactID        string                  `json:"artifact_id"`
	Path              string                  `json:"path"`
	ContentHash       string                  `json:"content_hash"`
	LicenseBasis      string                  `json:"license_basis"`
	TermsSnapshotID   string                  `json:"terms_snapshot_id"`
	TermsSnapshotHash string                  `json:"terms_snapshot_hash"`
	Decisions         FixturePurposeDecisions `json:"decisions"`
}

type FixtureAuditReport struct {
	SchemaVersion       string               `json:"schema_version"`
	PolicyVersion       string               `json:"policy_version"`
	PolicyPath          string               `json:"policy_path"`
	PolicySHA256        string               `json:"policy_sha256"`
	CatalogSHA256       string               `json:"catalog_sha256"`
	AsOf                time.Time            `json:"as_of"`
	FixtureRoots        []string             `json:"fixture_roots"`
	ArtifactCount       int                  `json:"artifact_count"`
	EligibleCount       int                  `json:"eligible_count"`
	EligibilityCoverage float64              `json:"eligibility_coverage"`
	Records             []FixtureAuditRecord `json:"records"`
}

// AuditFixtureCatalog validates hashes and policy decisions for every fixture.
// It returns no partial report if any file or provenance record is invalid.
func AuditFixtureCatalog(root string, catalogJSON []byte) (FixtureAuditReport, error) {
	if strings.TrimSpace(root) == "" {
		return FixtureAuditReport{}, errors.New("fixture root is required")
	}
	resolvedRoot, err := resolveFixtureAuditRoot(root)
	if err != nil {
		return FixtureAuditReport{}, err
	}
	var catalog FixtureCatalog
	if err := decodeStrictFixtureJSON(catalogJSON, &catalog); err != nil {
		return FixtureAuditReport{}, fmt.Errorf("decode fixture catalog: %w", err)
	}
	if err := validateExplicitPurposeDecisions(catalogJSON); err != nil {
		return FixtureAuditReport{}, err
	}
	if catalog.SchemaVersion != FixtureCatalogSchemaVersion {
		return FixtureAuditReport{}, fmt.Errorf("unsupported fixture catalog schema_version %q", catalog.SchemaVersion)
	}
	if strings.TrimSpace(catalog.PolicyVersion) == "" {
		return FixtureAuditReport{}, errors.New("fixture catalog policy_version is required")
	}
	policy, policyRules, cleanPolicyPath, err := loadFixturePolicy(resolvedRoot, catalog)
	if err != nil {
		return FixtureAuditReport{}, err
	}
	if catalog.AsOf.IsZero() {
		return FixtureAuditReport{}, errors.New("fixture catalog as_of is required")
	}
	if len(catalog.FixtureRoots) == 0 {
		return FixtureAuditReport{}, errors.New("fixture catalog must declare at least one fixture_root")
	}
	if len(catalog.Artifacts) == 0 {
		return FixtureAuditReport{}, errors.New("fixture catalog must contain at least one artifact")
	}

	artifacts := make(map[string]Artifact, len(catalog.Artifacts))
	seenPaths := make(map[string]string, len(catalog.Artifacts))
	seenSnapshots := make(map[string]string)
	for i := range catalog.Artifacts {
		record := &catalog.Artifacts[i]
		id := strings.TrimSpace(record.Artifact.ArtifactID)
		if id == "" {
			return FixtureAuditReport{}, fmt.Errorf("artifact[%d] artifact_id is required", i)
		}
		if _, exists := artifacts[id]; exists {
			return FixtureAuditReport{}, fmt.Errorf("duplicate artifact_id %q", id)
		}
		fixturePath, cleanPath, err := fixtureFile(resolvedRoot, record.Path)
		if err != nil {
			return FixtureAuditReport{}, fmt.Errorf("artifact %s: %w", id, err)
		}
		if previousID, exists := seenPaths[cleanPath]; exists {
			return FixtureAuditReport{}, fmt.Errorf("fixture path %q is shared by artifacts %s and %s", cleanPath, previousID, id)
		}
		seenPaths[cleanPath] = id
		record.Path = cleanPath

		fixtureData, contentHash, err := readFileSHA256(fixturePath)
		if err != nil {
			return FixtureAuditReport{}, fmt.Errorf("artifact %s: hash fixture: %w", id, err)
		}
		if contentHash != record.Artifact.ContentHash {
			return FixtureAuditReport{}, fmt.Errorf("artifact %s: content_hash mismatch: got %s, want %s", id, contentHash, record.Artifact.ContentHash)
		}

		rule, ok := policyRules[record.Artifact.LicenseBasis]
		if !ok {
			return FixtureAuditReport{}, fmt.Errorf("artifact %s: license_basis %q has no rule in policy %s", id, record.Artifact.LicenseBasis, policy.PolicyVersion)
		}
		if err := validateFixtureTermsBinding(resolvedRoot, cleanPath, record, rule, seenSnapshots); err != nil {
			return FixtureAuditReport{}, fmt.Errorf("artifact %s: %w", id, err)
		}
		if err := validateEmbeddedFixtureReferences(cleanPath, fixtureData, record.TermsSnapshotID, record.Artifact.TermsSnapshotHash, record.Artifact.LicenseBasis); err != nil {
			return FixtureAuditReport{}, fmt.Errorf("artifact %s: %w", id, err)
		}
		artifacts[id] = record.Artifact
	}
	fixtureRoots, err := auditFixtureCoverage(resolvedRoot, catalog.FixtureRoots, seenPaths)
	if err != nil {
		return FixtureAuditReport{}, err
	}

	evaluator := Evaluator{
		PolicyVersion: catalog.PolicyVersion,
		Artifacts:     artifacts,
		Now:           func() time.Time { return catalog.AsOf },
	}
	report := FixtureAuditReport{
		SchemaVersion: FixtureCatalogSchemaVersion,
		PolicyVersion: catalog.PolicyVersion,
		PolicyPath:    cleanPolicyPath,
		PolicySHA256:  catalog.PolicySHA256,
		AsOf:          catalog.AsOf.UTC(),
		FixtureRoots:  fixtureRoots,
		ArtifactCount: len(catalog.Artifacts),
		Records:       make([]FixtureAuditRecord, 0, len(catalog.Artifacts)),
	}
	digest := sha256.Sum256(catalogJSON)
	report.CatalogSHA256 = hex.EncodeToString(digest[:])

	for _, record := range catalog.Artifacts {
		id := record.Artifact.ArtifactID
		decisions := FixturePurposeDecisions{
			InternalEval:    evaluator.Evaluate(id, PurposeInternalEval),
			PrivateTraining: evaluator.Evaluate(id, PurposePrivateTraining),
			PublicRelease:   evaluator.Evaluate(id, PurposePublicRelease),
		}
		if !decisions.InternalEval.Allowed {
			return FixtureAuditReport{}, fmt.Errorf("artifact %s is not eligible for internal evaluation: %s", id, strings.Join(decisions.InternalEval.BlockingReasons, "; "))
		}
		report.EligibleCount++
		report.Records = append(report.Records, FixtureAuditRecord{
			ArtifactID:        id,
			Path:              record.Path,
			ContentHash:       record.Artifact.ContentHash,
			LicenseBasis:      record.Artifact.LicenseBasis,
			TermsSnapshotID:   record.TermsSnapshotID,
			TermsSnapshotHash: record.Artifact.TermsSnapshotHash,
			Decisions:         decisions,
		})
	}
	sort.Slice(report.Records, func(i, j int) bool { return report.Records[i].ArtifactID < report.Records[j].ArtifactID })
	report.EligibilityCoverage = float64(report.EligibleCount) / float64(report.ArtifactCount)
	if report.EligibilityCoverage != 1 {
		return FixtureAuditReport{}, fmt.Errorf("internal evaluation eligibility coverage is %.4f, want 1.0000", report.EligibilityCoverage)
	}
	return report, nil
}

func loadFixturePolicy(root string, catalog FixtureCatalog) (fixturePolicyDocument, map[string]fixturePolicyRule, string, error) {
	if !isSHA256(catalog.PolicySHA256) {
		return fixturePolicyDocument{}, nil, "", errors.New("fixture catalog policy_sha256 must be a lowercase SHA-256 digest")
	}
	policyPath, cleanPolicyPath, err := fixtureFile(root, catalog.PolicyPath)
	if err != nil {
		return fixturePolicyDocument{}, nil, "", fmt.Errorf("fixture policy: %w", err)
	}
	policyJSON, policyHash, err := readFileSHA256(policyPath)
	if err != nil {
		return fixturePolicyDocument{}, nil, "", fmt.Errorf("hash fixture policy: %w", err)
	}
	if policyHash != catalog.PolicySHA256 {
		return fixturePolicyDocument{}, nil, "", fmt.Errorf("fixture policy_sha256 mismatch: got %s, want %s", policyHash, catalog.PolicySHA256)
	}
	var policy fixturePolicyDocument
	if err := decodeStrictFixtureJSON(policyJSON, &policy); err != nil {
		return fixturePolicyDocument{}, nil, "", fmt.Errorf("decode fixture policy: %w", err)
	}
	if policy.SchemaVersion != fixturePolicySchemaVersion {
		return fixturePolicyDocument{}, nil, "", fmt.Errorf("unsupported fixture policy schema_version %q", policy.SchemaVersion)
	}
	if policy.PolicyVersion != catalog.PolicyVersion {
		return fixturePolicyDocument{}, nil, "", fmt.Errorf("fixture policy_version %q does not match catalog %q", policy.PolicyVersion, catalog.PolicyVersion)
	}
	defaultPermissions, err := policy.Default.permissionSet("policy default")
	if err != nil {
		return fixturePolicyDocument{}, nil, "", err
	}
	if anyPermission(defaultPermissions) {
		return fixturePolicyDocument{}, nil, "", errors.New("fixture policy default must deny every purpose")
	}
	if policy.UnknownValueBehavior != "deny" {
		return fixturePolicyDocument{}, nil, "", errors.New("fixture policy unknown_value_behavior must be deny")
	}
	if policy.Inheritance != "all_derivatives_inherit_the_strictest_transitive_ancestor" {
		return fixturePolicyDocument{}, nil, "", errors.New("fixture policy must require strictest transitive ancestry inheritance")
	}
	if strings.TrimSpace(policy.TakedownBehavior) == "" {
		return fixturePolicyDocument{}, nil, "", errors.New("fixture policy takedown_behavior is required")
	}
	if len(policy.Rules) == 0 {
		return fixturePolicyDocument{}, nil, "", errors.New("fixture policy must contain at least one license rule")
	}
	rules := make(map[string]fixturePolicyRule, len(policy.Rules))
	for i, rule := range policy.Rules {
		basis := strings.TrimSpace(rule.LicenseBasis)
		if basis == "" {
			return fixturePolicyDocument{}, nil, "", fmt.Errorf("policy rule[%d] license_basis is required", i)
		}
		if _, exists := rules[basis]; exists {
			return fixturePolicyDocument{}, nil, "", fmt.Errorf("duplicate fixture policy license_basis %q", basis)
		}
		if _, err := rule.explicitPermissionSet.permissionSet(fmt.Sprintf("policy rule %q", basis)); err != nil {
			return fixturePolicyDocument{}, nil, "", err
		}
		if rule.Requires == nil {
			return fixturePolicyDocument{}, nil, "", fmt.Errorf("policy rule %q requires must be an explicit array", basis)
		}
		seenRequirements := make(map[string]struct{}, len(rule.Requires))
		for requirementIndex := range rule.Requires {
			requirement := strings.TrimSpace(rule.Requires[requirementIndex])
			if requirement == "" {
				return fixturePolicyDocument{}, nil, "", fmt.Errorf("policy rule %q contains an empty requirement", basis)
			}
			if _, duplicate := seenRequirements[requirement]; duplicate {
				return fixturePolicyDocument{}, nil, "", fmt.Errorf("policy rule %q repeats requirement %q", basis, requirement)
			}
			seenRequirements[requirement] = struct{}{}
			rule.Requires[requirementIndex] = requirement
		}
		rules[basis] = rule
	}
	return policy, rules, cleanPolicyPath, nil
}

func validateFixtureTermsBinding(root, fixturePath string, record *FixtureRecord, rule fixturePolicyRule, seenSnapshots map[string]string) error {
	if strings.TrimSpace(record.TermsSnapshotID) == "" || strings.TrimSpace(record.TermsSnapshotID) != record.TermsSnapshotID {
		return errors.New("terms_snapshot_id is required")
	}
	for field, value := range map[string]string{
		"source_type":     record.Artifact.SourceType,
		"source_uri":      record.Artifact.SourceURI,
		"source_revision": record.Artifact.SourceRevision,
		"creator":         record.Artifact.Creator,
	} {
		if !hasEvidenceValue(value) {
			return fmt.Errorf("artifact has no usable %s", field)
		}
	}
	termsPath, cleanTermsPath, err := fixtureFile(root, record.TermsSnapshotPath)
	if err != nil {
		return fmt.Errorf("terms snapshot: %w", err)
	}
	record.TermsSnapshotPath = cleanTermsPath
	termsJSON, termsHash, err := readFileSHA256(termsPath)
	if err != nil {
		return fmt.Errorf("hash terms snapshot: %w", err)
	}
	if termsHash != record.Artifact.TermsSnapshotHash {
		return fmt.Errorf("terms_snapshot_hash mismatch: got %s, want %s", termsHash, record.Artifact.TermsSnapshotHash)
	}
	var snapshot fixtureTermsSnapshot
	if err := decodeStrictFixtureJSON(termsJSON, &snapshot); err != nil {
		return fmt.Errorf("decode terms snapshot: %w", err)
	}
	if snapshot.SchemaVersion != termsSnapshotSchemaVersion {
		return fmt.Errorf("unsupported terms snapshot schema_version %q", snapshot.SchemaVersion)
	}
	if snapshot.SnapshotID != record.TermsSnapshotID {
		return fmt.Errorf("terms snapshot_id %q does not match catalog %q", snapshot.SnapshotID, record.TermsSnapshotID)
	}
	if previous, exists := seenSnapshots[snapshot.SnapshotID]; exists && previous != cleanTermsPath {
		return fmt.Errorf("terms snapshot_id %q is bound to both %q and %q", snapshot.SnapshotID, previous, cleanTermsPath)
	}
	seenSnapshots[snapshot.SnapshotID] = cleanTermsPath
	if snapshot.PolicyVersion != record.Artifact.PolicyVersion {
		return fmt.Errorf("terms snapshot policy_version %q does not match artifact %q", snapshot.PolicyVersion, record.Artifact.PolicyVersion)
	}
	if snapshot.LicenseBasis != record.Artifact.LicenseBasis {
		return fmt.Errorf("terms snapshot license_basis %q does not match artifact %q", snapshot.LicenseBasis, record.Artifact.LicenseBasis)
	}
	if snapshot.ReviewStatus != "approved" {
		return fmt.Errorf("terms snapshot %q is not approved", snapshot.SnapshotID)
	}
	if !hasEvidenceValue(snapshot.Source) || !hasEvidenceValue(snapshot.Reviewer) || snapshot.CapturedAt.IsZero() || snapshot.EffectiveAt.IsZero() {
		return errors.New("terms snapshot source, reviewer, captured_at, and effective_at are required")
	}
	if snapshot.StorageReference != "repository:"+cleanTermsPath {
		return fmt.Errorf("terms snapshot storage_reference %q does not match repository path %q", snapshot.StorageReference, cleanTermsPath)
	}
	if len(snapshot.Scope) == 0 {
		return errors.New("terms snapshot scope must not be empty")
	}
	allowed, err := snapshotScopeAllows(snapshot.Scope, fixturePath)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("fixture path %q is outside terms snapshot %q scope", fixturePath, snapshot.SnapshotID)
	}
	snapshotPermissions, err := snapshot.Permissions.permissionSet("terms snapshot permissions")
	if err != nil {
		return err
	}
	rulePermissions, err := rule.explicitPermissionSet.permissionSet(fmt.Sprintf("policy rule %q", rule.LicenseBasis))
	if err != nil {
		return err
	}
	if permissionsExceed(snapshotPermissions, rulePermissions) {
		return fmt.Errorf("terms snapshot permissions exceed policy/license ceiling for %q", rule.LicenseBasis)
	}
	if permissionsExceed(record.Artifact.Permissions, snapshotPermissions) {
		return errors.New("artifact permissions exceed terms snapshot permissions")
	}
	if anyPermission(record.Artifact.Permissions) {
		for _, requirement := range rule.Requires {
			if err := validateFixtureEvidence(requirement, record.Artifact, snapshot); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFixtureEvidence(requirement string, artifact Artifact, snapshot fixtureTermsSnapshot) error {
	var value string
	switch requirement {
	case "terms_snapshot":
		return nil
	case "ownership_record":
		value = snapshot.ContentOwner
	case "provider":
		value = artifact.Provider
	case "model_revision":
		value = artifact.ModelRevision
	case "source_uri":
		value = artifact.SourceURI
	case "source_revision":
		value = artifact.SourceRevision
	default:
		value = snapshot.Evidence[requirement]
	}
	if !hasEvidenceValue(value) {
		return fmt.Errorf("artifact is missing required evidence %q", requirement)
	}
	return nil
}

func hasEvidenceValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "unknown", "not_applicable", "quarantined_unknown":
		return false
	default:
		return true
	}
}

func (p explicitPermissionSet) permissionSet(owner string) (PermissionSet, error) {
	if p.InternalEval == nil || p.PrivateTraining == nil || p.PublicRelease == nil {
		return PermissionSet{}, fmt.Errorf("%s must explicitly set all three purpose permissions to booleans", owner)
	}
	return PermissionSet{
		InternalEval:    *p.InternalEval,
		PrivateTraining: *p.PrivateTraining,
		PublicRelease:   *p.PublicRelease,
	}, nil
}

func anyPermission(permissions PermissionSet) bool {
	return permissions.InternalEval || permissions.PrivateTraining || permissions.PublicRelease
}

func permissionsExceed(grant, ceiling PermissionSet) bool {
	return grant.InternalEval && !ceiling.InternalEval ||
		grant.PrivateTraining && !ceiling.PrivateTraining ||
		grant.PublicRelease && !ceiling.PublicRelease
}

func snapshotScopeAllows(scopes []string, fixturePath string) (bool, error) {
	matchedAny := false
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || strings.Contains(scope, "\\") || strings.HasPrefix(scope, "/") {
			return false, fmt.Errorf("invalid terms snapshot scope %q", scope)
		}
		if strings.HasSuffix(scope, "/**") {
			base := strings.TrimSuffix(scope, "/**")
			if base == "" || pathpkg.Clean(base) != base || base == ".." || strings.HasPrefix(base, "../") {
				return false, fmt.Errorf("invalid terms snapshot scope %q", scope)
			}
			if fixturePath == base || strings.HasPrefix(fixturePath, base+"/") {
				matchedAny = true
			}
			continue
		}
		if pathpkg.Clean(scope) != scope || scope == "." || scope == ".." || strings.HasPrefix(scope, "../") {
			return false, fmt.Errorf("invalid terms snapshot scope %q", scope)
		}
		matched, err := pathpkg.Match(scope, fixturePath)
		if err != nil {
			return false, fmt.Errorf("invalid terms snapshot scope %q: %w", scope, err)
		}
		if matched {
			matchedAny = true
		}
	}
	return matchedAny, nil
}

func validateEmbeddedFixtureReferences(relativePath string, data []byte, snapshotID, termsHash, licenseBasis string) error {
	extension := strings.ToLower(filepath.Ext(relativePath))
	if extension != ".json" && extension != ".jsonl" {
		return nil
	}
	validate := func(value any) error {
		return walkEmbeddedFixtureReferences(value, snapshotID, termsHash, licenseBasis)
	}
	if extension == ".json" {
		value, err := decodeGenericFixtureJSON(data)
		if err != nil {
			return fmt.Errorf("decode embedded provenance JSON in %q: %w", relativePath, err)
		}
		return validate(value)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			return fmt.Errorf("decode embedded provenance JSONL in %q:%d: blank line", relativePath, line)
		}
		value, err := decodeGenericFixtureJSON(scanner.Bytes())
		if err != nil {
			return fmt.Errorf("decode embedded provenance JSONL in %q:%d: %w", relativePath, line, err)
		}
		if err := validate(value); err != nil {
			return fmt.Errorf("embedded provenance JSONL in %q:%d: %w", relativePath, line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan embedded provenance JSONL in %q: %w", relativePath, err)
	}
	return nil
}

func walkEmbeddedFixtureReferences(value any, snapshotID, termsHash, licenseBasis string) error {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if err := walkEmbeddedFixtureReferences(item, snapshotID, termsHash, licenseBasis); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, child := range typed {
			var expected string
			switch key {
			case "snapshot_id":
				expected = snapshotID
			case "terms_snapshot_hash":
				expected = termsHash
			case "license_basis":
				expected = licenseBasis
			}
			if expected != "" {
				actual, ok := child.(string)
				if !ok || actual != expected {
					return fmt.Errorf("embedded %s does not match fixture catalog", key)
				}
			}
			if err := walkEmbeddedFixtureReferences(child, snapshotID, termsHash, licenseBasis); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeGenericFixtureJSON(data []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func decodeStrictFixtureJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

func auditFixtureCoverage(root string, roots []string, catalogPaths map[string]string) ([]string, error) {
	cleanRoots := make([]string, 0, len(roots))
	coveredPaths := make(map[string]struct{})
	for _, relativeRoot := range roots {
		clean, err := cleanFixtureRelativePath(relativeRoot)
		if err != nil {
			return nil, fmt.Errorf("fixture_root %q must be a relative directory below the fixture root", relativeRoot)
		}
		if err := rejectSymlinkComponents(root, clean); err != nil {
			return nil, fmt.Errorf("fixture_root %q: %w", filepath.ToSlash(clean), err)
		}
		path := filepath.Join(root, clean)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("stat fixture_root %q: %w", filepath.ToSlash(clean), err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("fixture_root %q is not a regular directory", filepath.ToSlash(clean))
		}
		clean = filepath.ToSlash(clean)
		cleanRoots = append(cleanRoots, clean)
		if err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("fixture tree contains symlink %q", path)
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("fixture tree path %q is not a regular file", path)
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if _, ok := catalogPaths[relative]; !ok {
				return fmt.Errorf("fixture file %q has no provenance catalog record", relative)
			}
			coveredPaths[relative] = struct{}{}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(cleanRoots)
	cleanRoots = uniqueSorted(cleanRoots)
	for path := range catalogPaths {
		if _, ok := coveredPaths[path]; !ok {
			return nil, fmt.Errorf("catalog path %q is outside declared fixture_roots", path)
		}
	}
	return cleanRoots, nil
}

func fixtureFile(root, relativePath string) (string, string, error) {
	clean, err := cleanFixtureRelativePath(relativePath)
	if err != nil {
		return "", "", fmt.Errorf("path %q must be a relative file below the fixture root", relativePath)
	}
	if err := rejectSymlinkComponents(root, clean); err != nil {
		return "", "", err
	}
	path := filepath.Join(root, clean)
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", fmt.Errorf("stat %q: %w", clean, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("path %q is not a regular file", clean)
	}
	return path, filepath.ToSlash(clean), nil
}

func resolveFixtureAuditRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve fixture root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve fixture root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat fixture root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("fixture root is not a directory")
	}
	return resolved, nil
}

func cleanFixtureRelativePath(relativePath string) (string, error) {
	raw := strings.TrimSpace(relativePath)
	if raw == "" || strings.Contains(raw, "\\") || filepath.IsAbs(raw) {
		return "", errors.New("invalid relative path")
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.ToSlash(clean) != raw {
		return "", errors.New("invalid relative path")
	}
	return clean, nil
}

func rejectSymlinkComponents(root, relativePath string) error {
	current := root
	for _, component := range strings.Split(filepath.Clean(relativePath), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("stat %q: %w", filepath.ToSlash(relativePath), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path %q contains symlink component %q", filepath.ToSlash(relativePath), component)
		}
	}
	return nil
}

func readFileSHA256(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:]), nil
}

func validateExplicitPurposeDecisions(catalogJSON []byte) error {
	var raw struct {
		Artifacts []struct {
			Artifact struct {
				AncestryIDs json.RawMessage            `json:"ancestry_ids"`
				Permissions map[string]json.RawMessage `json:"permissions"`
			} `json:"artifact"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(catalogJSON, &raw); err != nil {
		return fmt.Errorf("decode fixture catalog purpose decisions: %w", err)
	}
	for i, record := range raw.Artifacts {
		if len(record.Artifact.AncestryIDs) == 0 || string(record.Artifact.AncestryIDs) == "null" {
			return fmt.Errorf("artifact[%d] ancestry_ids must be an explicit array", i)
		}
		var ancestryIDs []string
		if err := json.Unmarshal(record.Artifact.AncestryIDs, &ancestryIDs); err != nil || ancestryIDs == nil {
			return fmt.Errorf("artifact[%d] ancestry_ids must be an explicit array", i)
		}
		for _, field := range []string{"internal_eval_allowed", "private_training_allowed", "public_release_allowed"} {
			value, ok := record.Artifact.Permissions[field]
			if !ok {
				return fmt.Errorf("artifact[%d] permissions.%s must be explicit", i, field)
			}
			var decision *bool
			if err := json.Unmarshal(value, &decision); err != nil || decision == nil {
				return fmt.Errorf("artifact[%d] permissions.%s must be boolean", i, field)
			}
		}
	}
	return nil
}
