package flowcontract

import (
	"context"

	"github.com/futurxlab/golanggraph/state"
)

type HookResult struct {
	JumpToNode string
}

type BeforeRunHook interface {
	BeforeRun(ctx context.Context, state *state.State) *HookResult
}

type AfterRunHook interface {
	AfterRun(ctx context.Context, state *state.State) *HookResult
}
