package toolcondition

import (
	"context"
	"testing"

	"github.com/futurxlab/golanggraph/state"
	"github.com/tmc/langchaingo/llms"
)

func TestToolConditionRouting(t *testing.T) {
	condition := NewToolCondition(5, "toolNode", "modelNode", "fanoutNode")

	t.Run("NoHistory", func(t *testing.T) {
		s := state.State{
			History:  []llms.MessageContent{},
			Metadata: map[string]interface{}{},
		}

		got, err := condition.Condition(context.Background(), s)
		if err != nil {
			t.Errorf("Condition returned error: %v", err)
		}

		expected := "fanoutNode"
		if got != expected {
			t.Errorf("Condition() = %q, want %q", got, expected)
		}
	})

	t.Run("NoToolCall", func(t *testing.T) {
		s := state.State{
			History: []llms.MessageContent{{
				Role:  llms.ChatMessageTypeAI,
				Parts: []llms.ContentPart{llms.TextContent{Text: "hello"}},
			}},
			Metadata: map[string]interface{}{},
		}

		got, err := condition.Condition(context.Background(), s)
		if err != nil {
			t.Errorf("Condition returned error: %v", err)
		}

		expected := "fanoutNode"
		if got != expected {
			t.Errorf("Condition() = %q, want %q", got, expected)
		}
	})

	t.Run("HasToolCall_UnderLimit", func(t *testing.T) {
		s := state.State{
			History: []llms.MessageContent{{
				Role: llms.ChatMessageTypeAI,
				Parts: []llms.ContentPart{llms.ToolCall{
					ID:   "call_1",
					Type: "function",
					FunctionCall: &llms.FunctionCall{
						Name:      "test_tool",
						Arguments: "{}",
					},
				}},
			}},
			Metadata: map[string]interface{}{"tool_count": 3},
		}

		got, err := condition.Condition(context.Background(), s)
		if err != nil {
			t.Errorf("Condition returned error: %v", err)
		}

		expected := "toolNode"
		if got != expected {
			t.Errorf("Condition() = %q, want %q", got, expected)
		}
	})

	t.Run("HasToolCall_AtLimit", func(t *testing.T) {
		s := state.State{
			History: []llms.MessageContent{{
				Role: llms.ChatMessageTypeAI,
				Parts: []llms.ContentPart{llms.ToolCall{
					ID:   "call_1",
					Type: "function",
					FunctionCall: &llms.FunctionCall{
						Name:      "test_tool",
						Arguments: "{}",
					},
				}},
			}},
			Metadata: map[string]interface{}{"tool_count": 5},
		}

		got, err := condition.Condition(context.Background(), s)
		if err != nil {
			t.Errorf("Condition returned error: %v", err)
		}

		expected := "toolNode"
		if got != expected {
			t.Errorf("Condition() = %q, want %q", got, expected)
		}
	})

	t.Run("HasToolCall_NoCount", func(t *testing.T) {
		s := state.State{
			History: []llms.MessageContent{{
				Role: llms.ChatMessageTypeAI,
				Parts: []llms.ContentPart{llms.ToolCall{
					ID:   "call_1",
					Type: "function",
					FunctionCall: &llms.FunctionCall{
						Name:      "test_tool",
						Arguments: "{}",
					},
				}},
			}},
			Metadata: map[string]interface{}{},
		}

		got, err := condition.Condition(context.Background(), s)
		if err != nil {
			t.Errorf("Condition returned error: %v", err)
		}

		expected := "toolNode"
		if got != expected {
			t.Errorf("Condition() = %q, want %q", got, expected)
		}
	})
}
