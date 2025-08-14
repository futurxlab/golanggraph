package flowcontract

import (
	"context"

	"github.com/futurxlab/golanggraph/state"
)

type ConditionEdgeFunc func(ctx context.Context, state state.State) (string, error)
