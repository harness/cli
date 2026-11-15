package migratable

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

var existenceCheckBackoff = []time.Duration{2 * time.Second, 5 * time.Second}

// checkExistsWithRetry retries a destination existence check. A returned error means
// existence is unknown: callers must record a failure rather than upload, because a
// re-upload silently overwrites the destination artifact.
func checkExistsWithRetry[T any](ctx context.Context, logger zerolog.Logger, check func() (T, error)) (T, error) {
	result, err := check()
	for _, wait := range existenceCheckBackoff {
		if err == nil {
			return result, nil
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

func existenceCheckError(err error) error {
	return fmt.Errorf("destination existence check failed, not uploading: %w", err)
}
