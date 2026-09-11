package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	auditSchemaVersion        = "sandbox-audit/v2"
	auditPhaseAttempt         = "attempt"
	auditPhaseTerminal        = "terminal"
	auditStatusStarted        = "started"
	auditStatusSuccess        = "success"
	auditStatusCompileError   = "compile_error"
	auditStatusInfrastructure = "infrastructure_error"
	auditStatusCancelled      = "cancelled"
	maxAuditErrorCodeBytes    = 64
	maxAuditErrorMessageBytes = 1024
)

type auditAttempt struct {
	RunID        string
	Operation    string
	Language     string
	SourceDigest string
	InputDigests []string
	Seed         int64
	Limits       executionLimits
	LimitProfile string
	Profile      string
}

type auditRecord struct {
	SchemaVersion           string          `json:"schema_version"`
	RecordedAt              time.Time       `json:"recorded_at"`
	Phase                   string          `json:"phase"`
	Status                  string          `json:"status"`
	Operation               string          `json:"operation"`
	RunID                   string          `json:"run_id"`
	SourceDigest            string          `json:"source_digest"`
	InputDigests            []string        `json:"input_digests"`
	Seed                    int64           `json:"seed"`
	Limits                  executionLimits `json:"limits"`
	LimitProfile            string          `json:"limit_profile"`
	Profile                 string          `json:"profile,omitempty"`
	ManifestDigest          string          `json:"manifest_digest,omitempty"`
	Language                string          `json:"language"`
	Toolchain               string          `json:"toolchain,omitempty"`
	SandboxRevision         string          `json:"sandbox_revision,omitempty"`
	ImageDigest             string          `json:"image_digest,omitempty"`
	ToolchainManifestDigest string          `json:"toolchain_manifest_digest,omitempty"`
	SeccompPolicyDigest     string          `json:"seccomp_policy_digest,omitempty"`
	CompileSuccess          bool            `json:"compile_success"`
	ResultCount             int             `json:"result_count"`
	Verdicts                map[string]int  `json:"verdicts,omitempty"`
	ErrorCode               string          `json:"error_code,omitempty"`
	ErrorMessage            string          `json:"error_message,omitempty"`
}

type auditSink struct {
	mu   sync.Mutex
	file *os.File
}

func openAuditSink(path string) (*auditSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &auditSink{file: file}, nil
}

func (s *auditSink) close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func newCompileAuditAttempt(req compileRequest) (auditAttempt, error) {
	return newAuditAttempt("compile", req.Language, req.Source, nil, executionLimits{
		TimeLimitMS:      int(compileTimeLimit / time.Millisecond),
		MemoryLimitMB:    compileMemoryLimitMB,
		OutputLimitBytes: compileOutputLimit,
		MaxProcesses:     32,
	}, req.Seed, req.Profile)
}

func newExecuteAuditAttempt(req executeRequest) (auditAttempt, error) {
	return newAuditAttempt("execute", req.Language, req.Source, req.Inputs, req.Limits, req.Seed, req.Profile)
}

func newAuditAttempt(operation, language, source string, inputs []string, limits executionLimits, seed int64, profile string) (auditAttempt, error) {
	runBytes := make([]byte, 16)
	if _, err := rand.Read(runBytes); err != nil {
		return auditAttempt{}, err
	}
	canonicalLanguage, err := normalizeLanguage(language)
	if err != nil {
		return auditAttempt{}, err
	}
	inputDigests := make([]string, len(inputs))
	for i, input := range inputs {
		inputDigests[i] = sha256String(input)
	}
	return auditAttempt{
		RunID:        "run_" + hex.EncodeToString(runBytes),
		Operation:    operation,
		Language:     canonicalLanguage,
		SourceDigest: sha256String(source),
		InputDigests: inputDigests,
		Seed:         seed,
		Limits:       limits,
		LimitProfile: canonicalLimitProfile(operation, limits, profile),
		Profile:      profile,
	}, nil
}

func (s *auditSink) recordAttempt(attempt auditAttempt) error {
	if s == nil {
		return nil
	}
	record := auditRecordFromAttempt(attempt, auditPhaseAttempt, auditStatusStarted)
	return s.write(record)
}

func (s *auditSink) recordCompileTerminal(attempt auditAttempt, resp compileResponse, operationErr error) error {
	if s == nil {
		return nil
	}
	record := auditRecordFromAttempt(attempt, auditPhaseTerminal, terminalAuditStatus(operationErr, resp.Success))
	applyAuditMetadata(&record, resp.Audit)
	record.Toolchain = resp.Toolchain
	record.SandboxRevision = resp.SandboxRevision
	record.CompileSuccess = resp.Success
	applyAuditError(&record, operationErr)
	return s.write(record)
}

func (s *auditSink) recordExecuteTerminal(attempt auditAttempt, resp executeResponse, operationErr error) error {
	if s == nil {
		return nil
	}
	record := auditRecordFromAttempt(attempt, auditPhaseTerminal, terminalAuditStatus(operationErr, resp.Compile.Success))
	metadata := resp.Audit
	if metadata.ManifestDigest == "" {
		metadata = resp.Compile.Audit
	}
	applyAuditMetadata(&record, metadata)
	record.Toolchain = resp.Compile.Toolchain
	record.SandboxRevision = resp.Compile.SandboxRevision
	record.CompileSuccess = resp.Compile.Success
	record.ResultCount = len(resp.Results)
	if len(resp.Results) > 0 {
		record.Verdicts = make(map[string]int)
		for _, result := range resp.Results {
			record.Verdicts[result.Verdict]++
		}
	}
	applyAuditError(&record, operationErr)
	return s.write(record)
}

func auditRecordFromAttempt(attempt auditAttempt, phase, status string) auditRecord {
	return auditRecord{
		SchemaVersion: auditSchemaVersion,
		RecordedAt:    time.Now().UTC(),
		Phase:         phase,
		Status:        status,
		Operation:     attempt.Operation,
		RunID:         attempt.RunID,
		SourceDigest:  attempt.SourceDigest,
		InputDigests:  append([]string(nil), attempt.InputDigests...),
		Seed:          attempt.Seed,
		Limits:        attempt.Limits,
		LimitProfile:  attempt.LimitProfile,
		Profile:       attempt.Profile,
		Language:      attempt.Language,
	}
}

func applyAuditMetadata(record *auditRecord, meta auditMetadata) {
	record.ManifestDigest = meta.ManifestDigest
	record.ImageDigest = meta.ImageDigest
	record.ToolchainManifestDigest = meta.ToolchainManifestDigest
	record.SeccompPolicyDigest = meta.SeccompPolicyDigest
	record.Profile = meta.Profile
}

func terminalAuditStatus(operationErr error, compileSuccess bool) string {
	if operationErr == nil {
		if compileSuccess {
			return auditStatusSuccess
		}
		return auditStatusCompileError
	}
	if errors.Is(operationErr, context.Canceled) || errors.Is(operationErr, context.DeadlineExceeded) {
		return auditStatusCancelled
	}
	var svcErr *serviceError
	if errors.As(operationErr, &svcErr) && svcErr.code == "request_cancelled" {
		return auditStatusCancelled
	}
	return auditStatusInfrastructure
}

func applyAuditError(record *auditRecord, operationErr error) {
	if operationErr == nil {
		return
	}
	record.ErrorCode = "sandbox_failure"
	var svcErr *serviceError
	if errors.As(operationErr, &svcErr) {
		record.ErrorCode = svcErr.code
	} else if errors.Is(operationErr, context.Canceled) || errors.Is(operationErr, context.DeadlineExceeded) {
		record.ErrorCode = "request_cancelled"
	}
	record.ErrorCode = boundedAuditField(record.ErrorCode, maxAuditErrorCodeBytes)
	record.ErrorMessage = boundedAuditField(operationErr.Error(), maxAuditErrorMessageBytes)
}

func boundedAuditField(value string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
	}
	return strings.ToValidUTF8(value, "?")
}

func (s *auditSink) write(record auditRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return os.ErrClosed
	}
	written, err := s.file.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return s.file.Sync()
}
