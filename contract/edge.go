package flowcontract

import (
	"context"

	"golanggraph/state"
)

type ConditionEdgeFunc func(ctx context.Context, state state.State) (string, error)
