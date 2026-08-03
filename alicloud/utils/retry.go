package utils

import (
	"time"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/cenkalti/backoff/v4"
)

// HandleAPIError classifies an API error for backoff retry.
// Retryable SDK errors are returned as-is (backoff retries them).
// Non-retryable errors and non-SDK errors are wrapped in backoff.Permanent.
func HandleAPIError(err error) error {
	if err == nil {
		return nil
	}
	if t, ok := err.(*tea.SDKError); ok {
		if IsAbleToRetry(tea.StringValue(t.Code)) {
			return err
		}
		return backoff.Permanent(err)
	}
	return backoff.Permanent(err)
}

// RetryWithBackoff wraps a function with exponential backoff retry logic.
// Non-retryable errors are made permanent via HandleAPIError.
func RetryWithBackoff(fn func() error, timeout time.Duration) error {
	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = timeout
	return backoff.Retry(func() error {
		err := fn()
		if err == nil {
			return nil
		}
		return HandleAPIError(err)
	}, bo)
}
