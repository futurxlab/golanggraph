package toolcondition

import (
	"context"

	"golanggraph/state"

	"github.com/tmc/langchaingo/llms"
)

type ToolCondition struct {
	limit      int
	toolNode   string
	fanoutNode string
}

func (e *ToolCondition) Condition(ctx context.Context, state state.State) (string, error) {
	toolCount, ok := state.Metadata["tool_count"]
	if ok {
		if toolCount.(int) >= e.limit {
			return e.fanoutNode, nil
		}
	}

	if len(state.History) == 0 {
		return e.fanoutNode, nil
	}

	for _, part := range state.History[len(state.History)-1].Parts {
		if _, ok := part.(llms.ToolCall); ok {
			return e.toolNode, nil
		}
	}

	return e.fanoutNode, nil
}

func NewToolCondition(limit int, toolNode string, fanoutNode string) *ToolCondition {
	return &ToolCondition{
		limit:      limit,
		toolNode:   toolNode,
		fanoutNode: fanoutNode,
	}
}
