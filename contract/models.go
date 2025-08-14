package flowcontract

import (
	"context"

	"github.com/futurxlab/golanggraph/state"
)

type StreamFunc func(ctx context.Context, event *FlowStreamEvent) error

type FlowStreamEvent struct {
	Chunk     string
	FullState *state.State
}
