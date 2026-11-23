package migratable

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/rs/zerolog"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

func TestCheckExistsWithRetryStopsOnPermanentErrors(t *testing.T) {
	disableExistenceBackoff(t)
	tests := []struct {
		name      string
		err       error
		wantCalls int
	}{
		{"transient error without status", errors.New("context deadline exceeded"), 3},
		{"forbidden", types.NewStatusError(403, "failed to get artifact files: 403 Forbidden"), 1},
		{"wrapped unauthorized", fmt.Errorf("lookup: %w", types.NewStatusError(401, "401 Unauthorized")), 1},
		{"oci unauthorized", &transport.Error{StatusCode: 401}, 1},
		{"rate limited", types.NewStatusError(429, "429 Too Many Requests"), 3},
		{"request timeout", types.NewStatusError(408, "408 Request Timeout"), 3},
		{"server error", fmt.Errorf("lookup: %w", types.NewStatusError(503, "503 Service Unavailable")), 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			_, err := checkExistsWithRetry(context.Background(), zerolog.Nop(), func() (bool, error) {
				calls++
				return false, tt.err
			})
			if err == nil {
				t.Fatal("err = nil, want the check error")
			}
			if calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}
