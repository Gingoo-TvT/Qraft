package activities

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"go.temporal.io/sdk/temporal"
)

// LLMAuthenticationErrorType is emitted only for a provider's HTTP 401/403.
// Changing the candidate cannot repair its credentials or access permissions.
const LLMAuthenticationErrorType = "LLMAuthenticationError"

func classifyLLMActivityError(err error) error {
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) ||
		(apiErr.StatusCode != http.StatusUnauthorized && apiErr.StatusCode != http.StatusForbidden) {
		return err
	}
	return temporal.NewNonRetryableApplicationError(
		fmt.Sprintf("model provider authentication/access failed (HTTP %d); check the API key and model access in model settings: %v", apiErr.StatusCode, err),
		LLMAuthenticationErrorType,
		err,
	)
}

// IsLLMAuthenticationError also recognizes an error carried through a Temporal
// activity or child workflow, so business repair loops stop with the same cause.
func IsLLMAuthenticationError(err error) bool {
	for err != nil {
		if appErr, ok := err.(*temporal.ApplicationError); ok && appErr.Type() == LLMAuthenticationErrorType {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
