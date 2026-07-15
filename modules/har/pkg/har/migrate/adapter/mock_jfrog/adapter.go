package mock_jfrog

import (
	"context"

	adp "github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter/jfrog"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

func init() {
	adapterType := types.MOCK_JFROG
	if err := adp.RegisterFactory(adapterType, new(factory)); err != nil {
		return
	}
}

type factory struct{}

func (f factory) Create(ctx context.Context, config types.RegistryConfig) (adp.Adapter, error) {
	return jfrog.NewAdapterWithClient(config, NewMockClient()), nil
}
