package flowcontract

import (
	"context"

	"github.com/futurxlab/golanggraph/state"
)

type Node interface {
	Name() string
	Run(ctx context.Context, state *state.State, streamFunc StreamFunc) error
}
