package tools

import (
	"context"
	"errors"
	"testing"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"
	"github.com/tmc/langchaingo/llms"
)

type mockTool struct {
	name      string
	runResult llms.ToolCallResponse
	runErr    error
}

func (m *mockTool) Tools(ctx context.Context) []llms.Tool {
	return []llms.Tool{{
		Type:     "function",
		Function: &llms.FunctionDefinition{Name: m.name},
	}}
}

func (m *mockTool) Run(ctx context.Context, toolCall llms.ToolCall) (llms.ToolCallResponse, error) {
	if m.runErr != nil {
		return llms.ToolCallResponse{}, m.runErr
	}
	return m.runResult, nil
}

func TestToolsRun(t *testing.T) {
	ctx := context.Background()
	streamFunc := func(ctx context.Context, event *flowcontract.FlowStreamEvent) error { return nil }

	t.Run("EmptyHistory", func(t *testing.T) {
		toolsNode, err := NewTools(WithTools([]ITool{&mockTool{name: "test_tool"}}))
		if err != nil {
			t.Fatalf("NewTools returned error: %v", err)
		}

		testState := &state.State{}

		err = toolsNode.Run(ctx, testState, streamFunc)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}

		if len(testState.History) != 0 {
			t.Errorf("expected History to remain empty, got len=%d", len(testState.History))
		}
		if testState.Metadata != nil {
			t.Errorf("expected Metadata to remain nil, got %#v", testState.Metadata)
		}
	})

	t.Run("ToolCallSuccess", func(t *testing.T) {
		toolsNode, err := NewTools(WithTools([]ITool{&mockTool{
			name: "test_tool",
			runResult: llms.ToolCallResponse{
				ToolCallID: "call_1",
				Name:       "test_tool",
				Content:    "success result",
			},
		}}))
		if err != nil {
			t.Fatalf("NewTools returned error: %v", err)
		}

		testState := &state.State{
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

		err = toolsNode.Run(ctx, testState, streamFunc)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}

		if len(testState.History) != 2 {
			t.Fatalf("expected History len=2, got len=%d", len(testState.History))
		}

		last := testState.History[len(testState.History)-1]
		if last.Role != llms.ChatMessageTypeTool {
			t.Fatalf("expected appended message role %q, got %q", llms.ChatMessageTypeTool, last.Role)
		}
		if len(last.Parts) != 1 {
			t.Fatalf("expected appended message to have 1 part, got %d", len(last.Parts))
		}

		resp, ok := last.Parts[0].(llms.ToolCallResponse)
		if !ok {
			t.Fatalf("expected appended part to be llms.ToolCallResponse, got %T", last.Parts[0])
		}

		if resp.ToolCallID != "call_1" {
			t.Errorf("expected ToolCallID call_1, got %q", resp.ToolCallID)
		}
		if resp.Name != "test_tool" {
			t.Errorf("expected Name test_tool, got %q", resp.Name)
		}
		if resp.Content != "success result" {
			t.Errorf("expected Content success result, got %q", resp.Content)
		}
	})

	t.Run("ToolCallError", func(t *testing.T) {
		toolsNode, err := NewTools(WithTools([]ITool{&mockTool{
			name:   "test_tool",
			runErr: errors.New("boom"),
		}}))
		if err != nil {
			t.Fatalf("NewTools returned error: %v", err)
		}

		testState := &state.State{
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

		err = toolsNode.Run(ctx, testState, streamFunc)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}

		if len(testState.History) != 2 {
			t.Fatalf("expected History len=2, got len=%d", len(testState.History))
		}

		last := testState.History[len(testState.History)-1]
		if len(last.Parts) != 1 {
			t.Fatalf("expected appended message to have 1 part, got %d", len(last.Parts))
		}

		resp, ok := last.Parts[0].(llms.ToolCallResponse)
		if !ok {
			t.Fatalf("expected appended part to be llms.ToolCallResponse, got %T", last.Parts[0])
		}

		const prefix = "[TOOL ERROR]"
		if len(resp.Content) < len(prefix) || resp.Content[:len(prefix)] != prefix {
			t.Errorf("expected ToolCallResponse content to start with %q, got %q", prefix, resp.Content)
		}
	})

	t.Run("NotFoundTool", func(t *testing.T) {
		toolsNode, err := NewTools(WithTools([]ITool{&mockTool{name: "another_tool"}}))
		if err != nil {
			t.Fatalf("NewTools returned error: %v", err)
		}

		testState := &state.State{
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

		err = toolsNode.Run(ctx, testState, streamFunc)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}

		if len(testState.History) != 2 {
			t.Fatalf("expected History len=2, got len=%d", len(testState.History))
		}

		last := testState.History[len(testState.History)-1]
		if len(last.Parts) != 1 {
			t.Fatalf("expected appended message to have 1 part, got %d", len(last.Parts))
		}

		resp, ok := last.Parts[0].(llms.ToolCallResponse)
		if !ok {
			t.Fatalf("expected appended part to be llms.ToolCallResponse, got %T", last.Parts[0])
		}

		if resp.Content != "Tool not found" {
			t.Errorf("expected content Tool not found, got %q", resp.Content)
		}
		if resp.ToolCallID != "call_1" {
			t.Errorf("expected ToolCallID call_1, got %q", resp.ToolCallID)
		}
	})

	t.Run("ToolCountIncrement", func(t *testing.T) {
		toolsNode, err := NewTools(WithTools([]ITool{&mockTool{
			name: "test_tool",
			runResult: llms.ToolCallResponse{
				ToolCallID: "call_1",
				Name:       "test_tool",
				Content:    "success result",
			},
		}}))
		if err != nil {
			t.Fatalf("NewTools returned error: %v", err)
		}

		testState := &state.State{
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
		}

		err = toolsNode.Run(ctx, testState, streamFunc)
		if err != nil {
			t.Fatalf("first Run returned error: %v", err)
		}

		firstCountRaw, ok := testState.Metadata["tool_count"]
		if !ok {
			t.Fatalf("expected Metadata[tool_count] to exist after first run")
		}
		firstCount, ok := firstCountRaw.(int)
		if !ok {
			t.Fatalf("expected first tool_count to be int, got %T", firstCountRaw)
		}
		if firstCount != 1 {
			t.Errorf("expected first tool_count=1, got %d", firstCount)
		}

		testState.History = append(testState.History, llms.MessageContent{
			Role: llms.ChatMessageTypeAI,
			Parts: []llms.ContentPart{llms.ToolCall{
				ID:   "call_2",
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      "test_tool",
					Arguments: "{}",
				},
			}},
		})

		err = toolsNode.Run(ctx, testState, streamFunc)
		if err != nil {
			t.Fatalf("second Run returned error: %v", err)
		}

		secondCountRaw, ok := testState.Metadata["tool_count"]
		if !ok {
			t.Fatalf("expected Metadata[tool_count] to exist after second run")
		}
		secondCount, ok := secondCountRaw.(int)
		if !ok {
			t.Fatalf("expected second tool_count to be int, got %T", secondCountRaw)
		}
		if secondCount != 2 {
			t.Errorf("expected second tool_count=2, got %d", secondCount)
		}
	})
}
