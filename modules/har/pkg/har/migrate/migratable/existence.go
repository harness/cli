package migratable

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/rs/zerolog"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

var existenceCheckBackoff = []time.Duration{2 * time.Second, 5 * time.Second}

// checkExistsWithRetry retries a destination existence check. A returned error means
// existence is unknown: callers must record a failure rather than upload, because a
// re-upload silently overwrites the destination artifact.
func checkExistsWithRetry[T any](ctx context.Context, logger zerolog.Logger, check func() (T, error)) (T, error) {
	result, err := check()
	for _, wait := range existenceCheckBackoff {
		if err == nil || isPermanentCheckError(err) {
			return result, err
		}
		logger.Warn().Err(err).Msgf("Destination existence check failed, retrying in %s", wait)
		select {
		case <-ctx.Done():
			return result, err
		case <-time.After(wait):
		}
		result, err = check()
	}
	return result, err
}

// isPermanentCheckError reports a 4xx response that a retry cannot fix. Errors without
// a status code (timeouts, connection resets) are treated as transient.
func isPermanentCheckError(err error) bool {
	code := 0
	var statusErr *types.StatusError
	var ociErr *transport.Error
	switch {
	case errors.As(err, &statusErr):
		code = statusErr.StatusCode
	case errors.As(err, &ociErr):
		code = ociErr.StatusCode
	}
	return code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests
}

func existenceCheckError(err error) error {
	return fmt.Errorf("destination existence check failed, not uploading: %w", err)
}
