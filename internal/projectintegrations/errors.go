package projectintegrations

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

var ErrProviderRequestFailed = errors.New("integration provider request failed")

type ErrorCategory string

const (
	ErrorCategoryAuthFailed            ErrorCategory = "auth_failed"
	ErrorCategoryPermissionDenied      ErrorCategory = "permission_denied"
	ErrorCategoryNotFound              ErrorCategory = "not_found"
	ErrorCategoryRateLimited           ErrorCategory = "rate_limited"
	ErrorCategoryUnexpectedStatus      ErrorCategory = "unexpected_status"
	ErrorCategoryRequestFailed         ErrorCategory = "request_failed"
	ErrorCategoryDecodeFailed          ErrorCategory = "decode_failed"
	ErrorCategoryCredentialUnavailable ErrorCategory = "credential_unavailable"
)

type ProviderError struct {
	Provider   string
	Operation  string
	Category   ErrorCategory
	StatusCode int
	RetryAfter time.Duration
}

func (err *ProviderError) Error() string {
	if err == nil {
		return ErrProviderRequestFailed.Error()
	}
	if err.StatusCode > 0 {
		return fmt.Sprintf("%v: provider=%s operation=%s category=%s status=%d", ErrProviderRequestFailed, err.Provider, err.Operation, err.Category, err.StatusCode)
	}
	return fmt.Sprintf("%v: provider=%s operation=%s category=%s", ErrProviderRequestFailed, err.Provider, err.Operation, err.Category)
}

func (err *ProviderError) Unwrap() error {
	return ErrProviderRequestFailed
}

func ProviderErrorFromStatus(provider, operation string, statusCode int, retryAfter time.Duration) *ProviderError {
	category := ErrorCategoryUnexpectedStatus
	switch statusCode {
	case http.StatusUnauthorized:
		category = ErrorCategoryAuthFailed
	case http.StatusForbidden:
		category = ErrorCategoryPermissionDenied
	case http.StatusNotFound:
		category = ErrorCategoryNotFound
	case http.StatusTooManyRequests:
		category = ErrorCategoryRateLimited
	}
	return &ProviderError{
		Provider:   provider,
		Operation:  operation,
		Category:   category,
		StatusCode: statusCode,
		RetryAfter: retryAfter,
	}
}

func RequestError(provider, operation string) *ProviderError {
	return &ProviderError{Provider: provider, Operation: operation, Category: ErrorCategoryRequestFailed}
}

func DecodeError(provider, operation string) *ProviderError {
	return &ProviderError{Provider: provider, Operation: operation, Category: ErrorCategoryDecodeFailed}
}
