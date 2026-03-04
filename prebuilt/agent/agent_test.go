package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/futurxlab/golanggraph/prebuilt/node/tools"
	"github.com/futurxlab/golanggraph/state"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/tmc/langchaingo/llms"
)

type mockLLM struct {
	responses []*llms.ContentResponse
	callIndex int
}

func (m *mockLLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	if m.callIndex >= len(m.responses) {
		return &llms.ContentResponse{
			Choices: []*llms.ContentChoice{{Content: "fallback response"}},
		}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func (m *mockLLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return "", fmt.Errorf("not implemented")
}

type mockTool struct {
	name      string
	runResult llms.ToolCallResponse
}

func (t *mockTool) Tools(ctx context.Context) []llms.Tool {
	return []llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        t.name,
				Description: "mock tool",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
}

func (t *mockTool) Run(ctx context.Context, toolCall llms.ToolCall) (llms.ToolCallResponse, error) {
	return t.runResult, nil
}

func TestAgentBasicRun(t *testing.T) {
	mock := &mockLLM{
		responses: []*llms.ContentResponse{
			{Choices: []*llms.ContentChoice{{Content: "Hello, I'm the agent"}}},
		},
	}

	mt := &mockTool{
		name: "test_tool",
		runResult: llms.ToolCallResponse{
			ToolCallID: "call_1",
			Name:       "test_tool",
			Content:    "tool result",
		},
	}

	agent, err := NewAgent(
		WithName("test_agent"),
		WithModel(mock),
		WithTools([]tools.ITool{mt}),
		WithMaxToolCalls(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := &state.State{
		History: []llms.MessageContent{
			{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextContent{Text: "Hello"}},
			},
		},
		Metadata: make(map[string]interface{}),
	}

	err = agent.Run(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}

	lastResponse := s.GetLastResponse()
	if lastResponse != "Hello, I'm the agent" {
		t.Errorf("expected 'Hello, I'm the agent', got %q", lastResponse)
	}
}

func TestAgentWithToolCall(t *testing.T) {
	mock := &mockLLM{
		responses: []*llms.ContentResponse{
			{
				Choices: []*llms.ContentChoice{{
					ToolCalls: []llms.ToolCall{{
						ID:   "call_1",
						Type: "function",
						FunctionCall: &llms.FunctionCall{
							Name:      "test_tool",
							Arguments: "{}",
						},
					}},
				}},
			},
			{Choices: []*llms.ContentChoice{{Content: "Done using tool"}}},
		},
	}

	mt := &mockTool{
		name: "test_tool",
		runResult: llms.ToolCallResponse{
			ToolCallID: "call_1",
			Name:       "test_tool",
			Content:    "tool result",
		},
	}

	agent, err := NewAgent(
		WithName("test_agent"),
		WithModel(mock),
		WithTools([]tools.ITool{mt}),
		WithMaxToolCalls(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := &state.State{
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "Use the tool"}}},
		},
		Metadata: make(map[string]interface{}),
	}

	err = agent.Run(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}

	if s.GetLastResponse() != "Done using tool" {
		t.Errorf("expected 'Done using tool', got %q", s.GetLastResponse())
	}

	hasToolMessage := false
	for _, msg := range s.History {
		if msg.Role == llms.ChatMessageTypeTool {
			hasToolMessage = true
			break
		}
	}
	if !hasToolMessage {
		t.Error("expected tool message in history")
	}
}

func TestAgentContextCompressHook(t *testing.T) {
	mock := &mockLLM{
		responses: []*llms.ContentResponse{
			{Choices: []*llms.ContentChoice{{Content: "compressed response"}}},
		},
	}

	mt := &mockTool{name: "test_tool", runResult: llms.ToolCallResponse{ToolCallID: "call_1", Name: "test_tool", Content: "ok"}}

	agent, err := NewAgent(
		WithName("test_agent"),
		WithModel(mock),
		WithTools([]tools.ITool{mt}),
		WithContextWindow(3),
	)
	if err != nil {
		t.Fatal(err)
	}

	history := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "system prompt"}}},
	}
	for i := 0; i < 10; i++ {
		history = append(history, llms.MessageContent{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: fmt.Sprintf("msg %d", i)}},
		})
	}

	s := &state.State{
		History:  history,
		Metadata: make(map[string]interface{}),
	}

	err = agent.Run(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}

	if s.GetLastResponse() != "compressed response" {
		t.Errorf("expected 'compressed response', got %q", s.GetLastResponse())
	}
}

func TestAgentResponseValidationHook(t *testing.T) {
	callCount := 0
	mock := &mockLLM{
		responses: []*llms.ContentResponse{
			{Choices: []*llms.ContentChoice{{Content: "bad response"}}},
			{Choices: []*llms.ContentChoice{{Content: "good response"}}},
		},
	}

	mt := &mockTool{name: "test_tool", runResult: llms.ToolCallResponse{ToolCallID: "call_1", Name: "test_tool", Content: "ok"}}

	agent, err := NewAgent(
		WithName("test_agent"),
		WithModel(mock),
		WithTools([]tools.ITool{mt}),
		WithResponseValidator(func(response string) error {
			callCount++
			if response == "bad response" {
				return fmt.Errorf("response must not be 'bad response'")
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := &state.State{
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "test"}}},
		},
		Metadata: make(map[string]interface{}),
	}

	err = agent.Run(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}

	if s.GetLastResponse() != "good response" {
		t.Errorf("expected 'good response' after retry, got %q", s.GetLastResponse())
	}

	if callCount < 2 {
		t.Errorf("expected validator to be called at least 2 times, got %d", callCount)
	}
}

func TestAgentMaxToolCallHook(t *testing.T) {
	mock := &mockLLM{
		responses: []*llms.ContentResponse{
			{Choices: []*llms.ContentChoice{{ToolCalls: []llms.ToolCall{{ID: "c1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "test_tool", Arguments: "{}"}}}}}},
			{Choices: []*llms.ContentChoice{{ToolCalls: []llms.ToolCall{{ID: "c2", Type: "function", FunctionCall: &llms.FunctionCall{Name: "test_tool", Arguments: "{}"}}}}}},
			{Choices: []*llms.ContentChoice{{Content: "final answer after max"}}},
		},
	}

	mt := &mockTool{name: "test_tool", runResult: llms.ToolCallResponse{ToolCallID: "x", Name: "test_tool", Content: "ok"}}

	agent, err := NewAgent(
		WithName("test_agent"),
		WithModel(mock),
		WithTools([]tools.ITool{mt}),
		WithMaxToolCalls(2),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := &state.State{
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "do stuff"}}},
		},
		Metadata: make(map[string]interface{}),
	}

	err = agent.Run(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}

	if s.GetLastResponse() != "final answer after max" {
		t.Errorf("expected 'final answer after max', got %q", s.GetLastResponse())
	}
}

type mockSubAgent struct {
	response string
}

func (m *mockSubAgent) Name() string {
	return "mock_sub_agent"
}

func (m *mockSubAgent) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	s.History = append(s.History, llms.MessageContent{
		Role:  llms.ChatMessageTypeAI,
		Parts: []llms.ContentPart{llms.TextContent{Text: m.response}},
	})
	return nil
}

func TestDelegateTaskTool(t *testing.T) {
	mock := &mockLLM{
		responses: []*llms.ContentResponse{
			{
				Choices: []*llms.ContentChoice{{
					ToolCalls: []llms.ToolCall{{
						ID:   "call_d1",
						Type: "function",
						FunctionCall: &llms.FunctionCall{
							Name:      "delegate_task",
							Arguments: `{"agent_name":"sub1","task":"do something"}`,
						},
					}},
				}},
			},
			{Choices: []*llms.ContentChoice{{Content: "orchestrated result"}}},
		},
	}

	sub := &mockSubAgent{response: "sub1 did the thing"}

	a, err := NewAgent(
		WithName("orchestrator"),
		WithModel(mock),
		WithSubAgent("sub1", sub),
		WithMaxToolCalls(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := &state.State{
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "delegate please"}}},
		},
		Metadata: make(map[string]interface{}),
	}

	err = a.Run(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}

	if s.GetLastResponse() != "orchestrated result" {
		t.Errorf("expected 'orchestrated result', got %q", s.GetLastResponse())
	}

	hasToolMsg := false
	for _, msg := range s.History {
		if msg.Role == llms.ChatMessageTypeTool {
			for _, part := range msg.Parts {
				if resp, ok := part.(llms.ToolCallResponse); ok {
					if resp.Content == "[sub1] sub1 did the thing" {
						hasToolMsg = true
					}
				}
			}
		}
	}
	if !hasToolMsg {
		t.Error("expected delegate tool response with sub-agent result in history")
	}
}

func TestDelegateTaskToolNotFound(t *testing.T) {
	mock := &mockLLM{
		responses: []*llms.ContentResponse{
			{
				Choices: []*llms.ContentChoice{{
					ToolCalls: []llms.ToolCall{{
						ID:   "call_d2",
						Type: "function",
						FunctionCall: &llms.FunctionCall{
							Name:      "delegate_task",
							Arguments: `{"agent_name":"nonexistent","task":"do something"}`,
						},
					}},
				}},
			},
			{Choices: []*llms.ContentChoice{{Content: "handled missing agent"}}},
		},
	}

	sub := &mockSubAgent{response: "sub1 result"}

	a, err := NewAgent(
		WithName("orchestrator"),
		WithModel(mock),
		WithSubAgent("sub1", sub),
		WithMaxToolCalls(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := &state.State{
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "delegate to unknown"}}},
		},
		Metadata: make(map[string]interface{}),
	}

	err = a.Run(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}

	if s.GetLastResponse() != "handled missing agent" {
		t.Errorf("expected 'handled missing agent', got %q", s.GetLastResponse())
	}
}
