package flowcontract

import (
	"context"

	"github.com/futurxlab/golanggraph/state"
)

type Checkpointer interface {
	Save(ctx context.Context, namespace string, state *state.State) (string, error)
	GetByID(ctx context.Context, namespace string, checkpointerID string) (*state.State, error)
	GetLastest(ctx context.Context, namespace string) (*state.State, error)
	GetAll(ctx context.Context, namespace string) ([]*state.State, error)
}
