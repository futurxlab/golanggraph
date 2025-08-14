package tools

import (
	"context"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"

	"github.com/tmc/langchaingo/llms"
)

type ITool interface {
	Tools(ctx context.Context) []llms.Tool
	Run(ctx context.Context, currentState *state.State, streamFunc flowcontract.StreamFunc) error
}
