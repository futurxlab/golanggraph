package flowcontract

import (
	"context"

	"golanggraph/state"
)

type Node interface {
	Name() string
	Run(ctx context.Context, state *state.State, streamFunc StreamFunc) error
}
