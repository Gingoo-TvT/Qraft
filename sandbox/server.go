package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	maxRequestBytes                   = 64 << 20
	maxSourceBytes                    = 2 << 20
	maxInputBytes                     = 8 << 20
	maxInputsBytes                    = 32 << 20
	maxCases                          = 256
	maxBatchExecutionBudgetMS         = 120_000
	maxBatchOutputBudgetBytes   int64 = 64 << 20
	maxHTTPResponseBytes              = 64 << 20
	protocolMinTimeLimitMS            = 1
	protocolMaxTimeLimitMS            = 30_000
	protocolMinMemoryLimitMB          = 16
	protocolMaxMemoryLimitMB          = 4096
	protocolMaxOutputLimitBytes       = 64 << 20
	protocolMaxProcesses              = 64
)

type executeLimitCeiling struct {
	TimeLimitMS   int
	MemoryLimitMB int
}

var protocolExecuteLimitCeiling = executeLimitCeiling{
	TimeLimitMS:   protocolMaxTimeLimitMS,
	MemoryLimitMB: protocolMaxMemoryLimitMB,
}

type sandboxEngine interface {
	Compile(context.Context, compileRequest) (compileResponse, error)
	Execute(context.Context, executeRequest) (executeResponse, error)
	Health(context.Context) healthResponse
}

type serviceError struct {
	code   string
	status int
	err    error
}

func (e *serviceError) Error() string { return e.err.Error() }
func (e *serviceError) Unwrap() error { return e.err }

func newServiceError(code string, status int, format string, args ...any) error {
	return &serviceError{code: code, status: status, err: fmt.Errorf(format, args...)}
}

type server struct {
	engine              sandboxEngine
	sem                 chan struct{}
	audit               *auditSink
	executeLimitCeiling executeLimitCeiling
}

func newServer(engine sandboxEngine, maxConcurrent int) http.Handler {
	return newServerWithAudit(engine, maxConcurrent, nil)
}

func newServerWithAudit(engine sandboxEngine, maxConcurrent int, audit *auditSink) http.Handler {
	return newServerWithExecuteLimits(engine, maxConcurrent, audit, protocolExecuteLimitCeiling)
}

func newServerWithExecuteLimits(engine sandboxEngine, maxConcurrent int, audit *auditSink, ceiling executeLimitCeiling) http.Handler {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	s := &server{
		engine:              engine,
		sem:                 make(chan struct{}, maxConcurrent),
		audit:               audit,
		executeLimitCeiling: ceiling,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/v1/compile", s.compile)
	mux.HandleFunc("/v1/execute", s.execute)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		s.health(w, r)
	})
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeError(w, newServiceError("method_not_allowed", http.StatusMethodNotAllowed, "method %s is not allowed", r.Method))
		return
	}
	resp := s.engine.Health(r.Context())
	status := http.StatusOK
	if !resp.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

func (s *server) compile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, newServiceError("method_not_allowed", http.StatusMethodNotAllowed, "method %s is not allowed", r.Method))
		return
	}
	if !s.acquire(w, r) {
		return
	}
	defer s.release()

	var req compileRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, err)
		return
	}
	if err := validateCompileRequest(req); err != nil {
		s.writeError(w, err)
		return
	}
	attempt, err := newCompileAuditAttempt(req)
	if err != nil {
		s.writeError(w, newServiceError("audit_unavailable", http.StatusServiceUnavailable, "creating compile audit attempt: %v", err))
		return
	}
	if err := s.audit.recordAttempt(attempt); err != nil {
		s.writeError(w, newServiceError("audit_unavailable", http.StatusServiceUnavailable, "persisting compile audit attempt: %v", err))
		return
	}

	req.RunID = attempt.RunID
	resp, operationErr := s.engine.Compile(r.Context(), req)
	resp.Audit.RunID = attempt.RunID
	var encoded []byte
	if operationErr == nil {
		encoded, operationErr = prepareSuccessJSON(resp)
	}
	if err := s.audit.recordCompileTerminal(attempt, resp, operationErr); err != nil {
		s.writeError(w, newServiceError("audit_unavailable", http.StatusServiceUnavailable, "persisting compile terminal audit: %v", err))
		return
	}
	if operationErr != nil {
		s.writeError(w, operationErr)
		return
	}
	writePreparedJSON(w, http.StatusOK, encoded)
}

func (s *server) execute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, newServiceError("method_not_allowed", http.StatusMethodNotAllowed, "method %s is not allowed", r.Method))
		return
	}
	if !s.acquire(w, r) {
		return
	}
	defer s.release()

	var req executeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, err)
		return
	}
	if err := validateExecuteRequest(req, s.executeLimitCeiling); err != nil {
		s.writeError(w, err)
		return
	}
	attempt, err := newExecuteAuditAttempt(req)
	if err != nil {
		s.writeError(w, newServiceError("audit_unavailable", http.StatusServiceUnavailable, "creating execution audit attempt: %v", err))
		return
	}
	if err := s.audit.recordAttempt(attempt); err != nil {
		s.writeError(w, newServiceError("audit_unavailable", http.StatusServiceUnavailable, "persisting execution audit attempt: %v", err))
		return
	}

	req.RunID = attempt.RunID
	resp, operationErr := s.engine.Execute(r.Context(), req)
	resp.Audit.RunID = attempt.RunID
	resp.Compile.Audit.RunID = attempt.RunID
	var encoded []byte
	if operationErr == nil {
		encoded, operationErr = prepareSuccessJSON(resp)
	}
	if err := s.audit.recordExecuteTerminal(attempt, resp, operationErr); err != nil {
		s.writeError(w, newServiceError("audit_unavailable", http.StatusServiceUnavailable, "persisting execution terminal audit: %v", err))
		return
	}
	if operationErr != nil {
		s.writeError(w, operationErr)
		return
	}
	writePreparedJSON(w, http.StatusOK, encoded)
}

func (s *server) acquire(w http.ResponseWriter, r *http.Request) bool {
	select {
	case s.sem <- struct{}{}:
		return true
	case <-r.Context().Done():
		s.writeError(w, newServiceError("request_cancelled", http.StatusRequestTimeout, "request cancelled while waiting for sandbox capacity"))
		return false
	default:
		s.writeError(w, newServiceError("sandbox_busy", http.StatusTooManyRequests, "sandbox concurrency limit reached"))
		return false
	}
}

func (s *server) release() { <-s.sem }

func (s *server) writeError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	code := "sandbox_failure"
	message := "sandbox operation failed"
	var svcErr *serviceError
	if errors.As(err, &svcErr) {
		status = svcErr.status
		code = svcErr.code
		message = svcErr.Error()
	}
	writeJSON(w, status, errorResponse{
		Version: apiVersion,
		Error:   apiError{Code: code, Message: message},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return newServiceError("invalid_request", http.StatusUnsupportedMediaType, "Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return newServiceError("invalid_request", http.StatusBadRequest, "invalid JSON request: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return newServiceError("invalid_request", http.StatusBadRequest, "request body must contain one JSON object")
		}
		return newServiceError("invalid_request", http.StatusBadRequest, "invalid trailing request data: %v", err)
	}
	return nil
}

func validateCompileRequest(req compileRequest) error {
	if req.Version != apiVersion {
		return newServiceError("unsupported_version", http.StatusBadRequest, "version must be %q", apiVersion)
	}
	language, err := normalizeLanguage(req.Language)
	if err != nil {
		return err
	}
	if err := validateExecutionProfile(req.Profile, language); err != nil {
		return err
	}
	if req.Source == "" {
		return newServiceError("invalid_request", http.StatusBadRequest, "source must not be empty")
	}
	if len(req.Source) > maxSourceBytes {
		return newServiceError("invalid_request", http.StatusRequestEntityTooLarge, "source exceeds %d bytes", maxSourceBytes)
	}
	return nil
}

func validateExecuteRequest(req executeRequest, ceiling executeLimitCeiling) error {
	if err := validateCompileRequest(compileRequest{Version: req.Version, Language: req.Language, Source: req.Source, Profile: req.Profile}); err != nil {
		return err
	}
	if len(req.Inputs) == 0 || len(req.Inputs) > maxCases {
		return newServiceError("invalid_request", http.StatusBadRequest, "inputs must contain between 1 and %d cases", maxCases)
	}
	total := 0
	for i, input := range req.Inputs {
		if len(input) > maxInputBytes {
			return newServiceError("invalid_request", http.StatusRequestEntityTooLarge, "input %d exceeds %d bytes", i, maxInputBytes)
		}
		total += len(input)
		if total > maxInputsBytes {
			return newServiceError("invalid_request", http.StatusRequestEntityTooLarge, "combined inputs exceed %d bytes", maxInputsBytes)
		}
	}
	if err := validateLimits(req.Limits); err != nil {
		return err
	}
	if req.Limits.TimeLimitMS > ceiling.TimeLimitMS {
		return newServiceError("deployment_limit_exceeded", http.StatusUnprocessableEntity,
			"time_limit_ms %d exceeds deployed execute maximum %d", req.Limits.TimeLimitMS, ceiling.TimeLimitMS)
	}
	if req.Limits.MemoryLimitMB > ceiling.MemoryLimitMB {
		return newServiceError("deployment_limit_exceeded", http.StatusUnprocessableEntity,
			"memory_limit_mb %d exceeds deployed execute maximum %d", req.Limits.MemoryLimitMB, ceiling.MemoryLimitMB)
	}
	if int64(len(req.Inputs))*int64(req.Limits.TimeLimitMS) > maxBatchExecutionBudgetMS {
		return newServiceError("invalid_request", http.StatusBadRequest,
			"declared batch execution budget exceeds %d ms", maxBatchExecutionBudgetMS)
	}
	if int64(len(req.Inputs))*req.Limits.OutputLimitBytes > maxBatchOutputBudgetBytes {
		return newServiceError("invalid_request", http.StatusBadRequest,
			"declared batch output budget exceeds %d bytes", maxBatchOutputBudgetBytes)
	}
	return nil
}

func validateExecutionProfile(profile, language string) error {
	if profile == "" {
		return nil
	}
	if profile != strings.TrimSpace(profile) {
		return newServiceError("unsupported_profile", http.StatusUnprocessableEntity, "execution profile must be canonical")
	}
	switch profile {
	case sanitizerCAndCPPProfileV1:
		if language != "c" && language != "cpp" {
			return newServiceError("unsupported_profile", http.StatusUnprocessableEntity, "%s supports only C and C++", sanitizerCAndCPPProfileV1)
		}
		return nil
	default:
		return newServiceError("unsupported_profile", http.StatusUnprocessableEntity, "unsupported execution profile %q", profile)
	}
}

func validateLimits(l executionLimits) error {
	if l.TimeLimitMS < protocolMinTimeLimitMS || l.TimeLimitMS > protocolMaxTimeLimitMS {
		return newServiceError("invalid_request", http.StatusBadRequest, "time_limit_ms must be between %d and %d", protocolMinTimeLimitMS, protocolMaxTimeLimitMS)
	}
	if l.MemoryLimitMB < protocolMinMemoryLimitMB || l.MemoryLimitMB > protocolMaxMemoryLimitMB {
		return newServiceError("invalid_request", http.StatusBadRequest, "memory_limit_mb must be between %d and %d", protocolMinMemoryLimitMB, protocolMaxMemoryLimitMB)
	}
	if l.OutputLimitBytes < 1 || l.OutputLimitBytes > protocolMaxOutputLimitBytes {
		return newServiceError("invalid_request", http.StatusBadRequest, "output_limit_bytes must be between 1 and %d", protocolMaxOutputLimitBytes)
	}
	if l.MaxProcesses < 1 || l.MaxProcesses > protocolMaxProcesses {
		return newServiceError("invalid_request", http.StatusBadRequest, "max_processes must be between 1 and %d", protocolMaxProcesses)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writePreparedJSON(w http.ResponseWriter, status int, encoded []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

func prepareSuccessJSON(value any) ([]byte, error) {
	size, err := successJSONSize(value)
	if err != nil {
		return nil, newServiceError("sandbox_failure", http.StatusInternalServerError, "measuring sandbox response: %v", err)
	}
	if size > maxHTTPResponseBytes {
		return nil, newServiceError("response_too_large", http.StatusInternalServerError,
			"sandbox response exceeds %d-byte wire limit", maxHTTPResponseBytes)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, newServiceError("sandbox_failure", http.StatusInternalServerError, "encoding sandbox response: %v", err)
	}
	if len(encoded) > maxHTTPResponseBytes {
		return nil, newServiceError("response_too_large", http.StatusInternalServerError,
			"sandbox response exceeds %d-byte wire limit", maxHTTPResponseBytes)
	}
	return encoded, nil
}

func successJSONSize(value any) (int64, error) {
	switch typed := value.(type) {
	case compileResponse:
		return compileResponseJSONSize(typed)
	case executeResponse:
		return executeResponseJSONSize(typed)
	default:
		return 0, fmt.Errorf("unsupported success response type %T", value)
	}
}

func compileResponseJSONSize(resp compileResponse) (int64, error) {
	baseline := resp
	values := clearCompileResponseStrings(&baseline)
	encoded, err := json.Marshal(baseline)
	if err != nil {
		return 0, err
	}
	size := int64(len(encoded))
	for _, value := range values {
		size += encodedJSONStringSize(value) - 2
	}
	return size, nil
}

func executeResponseJSONSize(resp executeResponse) (int64, error) {
	baseline := resp
	values := []string{baseline.Version}
	baseline.Version = ""
	values = append(values, clearCompileResponseStrings(&baseline.Compile)...)
	if resp.Results != nil {
		baseline.Results = make([]caseResult, len(resp.Results))
		copy(baseline.Results, resp.Results)
	}
	var optionalSignalSize int64
	for i := range baseline.Results {
		values = append(values, baseline.Results[i].Verdict, baseline.Results[i].Stdout, baseline.Results[i].Stderr)
		baseline.Results[i].Verdict = ""
		baseline.Results[i].Stdout = ""
		baseline.Results[i].Stderr = ""
		if baseline.Results[i].Signal != "" {
			optionalSignalSize += int64(len(`,"signal":`)) + encodedJSONStringSize(baseline.Results[i].Signal)
			baseline.Results[i].Signal = ""
		}
	}
	values = append(values, clearAuditMetadataStrings(&baseline.Audit)...)
	encoded, err := json.Marshal(baseline)
	if err != nil {
		return 0, err
	}
	size := int64(len(encoded)) + optionalSignalSize
	for _, value := range values {
		size += encodedJSONStringSize(value) - 2
	}
	return size, nil
}

func clearCompileResponseStrings(resp *compileResponse) []string {
	values := []string{
		resp.Version, resp.Language, resp.Stdout, resp.Stderr, resp.Toolchain, resp.SandboxRevision,
	}
	resp.Version = ""
	resp.Language = ""
	resp.Stdout = ""
	resp.Stderr = ""
	resp.Toolchain = ""
	resp.SandboxRevision = ""
	values = append(values, clearAuditMetadataStrings(&resp.Audit)...)
	return values
}

func clearAuditMetadataStrings(meta *auditMetadata) []string {
	values := []string{
		meta.RunID, meta.ManifestDigest, meta.LimitProfile, meta.ImageDigest,
		meta.ToolchainManifestDigest, meta.SeccompPolicyDigest,
	}
	meta.RunID = ""
	meta.ManifestDigest = ""
	meta.LimitProfile = ""
	meta.ImageDigest = ""
	meta.ToolchainManifestDigest = ""
	meta.SeccompPolicyDigest = ""
	return values
}

func encodedJSONStringSize(value string) int64 {
	size := int64(2)
	for index := 0; index < len(value); {
		char := value[index]
		if char < utf8.RuneSelf {
			switch char {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				size += 2
			case '<', '>', '&':
				size += 6
			default:
				if char < 0x20 {
					size += 6
				} else {
					size++
				}
			}
			index++
			continue
		}
		r, width := utf8.DecodeRuneInString(value[index:])
		if r == utf8.RuneError && width == 1 {
			size += 6
			index++
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			size += 6
		} else {
			size += int64(width)
		}
		index += width
	}
	return size
}
