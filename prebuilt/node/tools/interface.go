package tools

import (
	"context"

	"github.com/tmc/langchaingo/llms"
)

type ITool interface {
	Tools(ctx context.Context) []llms.Tool
	Run(ctx context.Context, toolCall llms.ToolCall) (llms.ToolCallResponse, error)
}
