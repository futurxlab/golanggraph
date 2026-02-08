package toolcondition

import (
	"context"

	"github.com/futurxlab/golanggraph/state"

	"github.com/tmc/langchaingo/llms"
)

type ToolCondition struct {
	limit      int
	toolNode   string
	modelNode  string
	fanoutNode string
}

func (e *ToolCondition) Condition(ctx context.Context, state state.State) (string, error) {
	if len(state.History) == 0 {
		return e.fanoutNode, nil
	}

	hasToolCall := false
	for _, part := range state.History[len(state.History)-1].Parts {
		if _, ok := part.(llms.ToolCall); ok {
			hasToolCall = true
			break
		}
	}

	if !hasToolCall {
		return e.fanoutNode, nil
	}

	// Has tool call — check if limit exceeded
	if toolCount, ok := state.Metadata["tool_count"].(int); ok {
		// +2 to prevent hook check be skipped
		deadLimit := e.limit + 2
		if toolCount >= deadLimit {
			// remove orphan tool call message
			for i := len(state.History) - 1; i >= 0; i-- {
				if _, ok := state.History[i].Parts[0].(llms.ToolCall); ok {
					state.History = state.History[:i]
				} else {
					break
				}
			}

			return e.fanoutNode, nil
		}
	}

	return e.toolNode, nil
}

func NewToolCondition(limit int, toolNode string, modelNode string, fanoutNode string) *ToolCondition {
	return &ToolCondition{
		limit:      limit,
		toolNode:   toolNode,
		modelNode:  modelNode,
		fanoutNode: fanoutNode,
	}
}
