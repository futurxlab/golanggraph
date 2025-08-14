package flowcontract

import (
	"context"

	"golanggraph/state"
)

type StreamFunc func(ctx context.Context, event *FlowStreamEvent) error

type FlowStreamEvent struct {
	Chunk     string
	FullState *state.State
}
