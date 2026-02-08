package tools

import (
	"context"
	"encoding/json"
	"sync"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"

	"github.com/Yet-Another-AI-Project/kiwi-lib/logger"
	"github.com/Yet-Another-AI-Project/kiwi-lib/xerror"
	"github.com/futurxlab/golanggraph/utils"

	"github.com/tmc/langchaingo/llms"
)

const (
	DefaultNodeName = "ToolsNode"
)

type ToolEvent struct {
	ID     string
	Type   string
	Name   string
	Input  string
	Result string
}

func (e *ToolEvent) String() string {
	json, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	return string(json)
}

type Option func(*Tools)

func WithTools(tools []ITool) Option {
	return func(m *Tools) {
		m.tools = tools
	}
}

func WithLogger(logger logger.ILogger) Option {
	return func(m *Tools) {
		m.logger = logger
	}
}

func WithNodeName(name string) Option {
	return func(m *Tools) {
		m.name = name
	}
}

func WithBeforeRunHook(hooks ...func(ctx context.Context, currentState *state.State) *flowcontract.HookResult) Option {
	return func(m *Tools) {
		m.beforeRunHooks = append(m.beforeRunHooks, hooks...)
	}
}

func WithAfterRunHook(hooks ...func(ctx context.Context, currentState *state.State) *flowcontract.HookResult) Option {
	return func(m *Tools) {
		m.afterRunHooks = append(m.afterRunHooks, hooks...)
	}
}

type Tools struct {
	tools  []ITool
	logger logger.ILogger
	name   string

	// hooks
	beforeRunHooks []func(ctx context.Context, currentState *state.State) *flowcontract.HookResult
	afterRunHooks  []func(ctx context.Context, currentState *state.State) *flowcontract.HookResult
}

func (m *Tools) ExportITools() []ITool {
	return m.tools
}

func (m *Tools) ListTools(ctx context.Context) []llms.Tool {
	tools := make([]llms.Tool, 0)
	for _, tool := range m.tools {
		tools = append(tools, tool.Tools(ctx)...)
	}
	return tools
}

func (m *Tools) Name() string {
	if m.name == "" {
		return DefaultNodeName
	}
	return m.name
}

func (m *Tools) BeforeRun(ctx context.Context, currentState *state.State) *flowcontract.HookResult {
	for _, hook := range m.beforeRunHooks {
		if result := hook(ctx, currentState); result != nil {
			return result
		}
	}
	return nil
}

func (m *Tools) AfterRun(ctx context.Context, currentState *state.State) *flowcontract.HookResult {
	for _, hook := range m.afterRunHooks {
		result := hook(ctx, currentState)
		if result != nil && result.JumpToNode != "" {
			return result
		}
	}
	return nil
}

func (m *Tools) Run(ctx context.Context, currentState *state.State, streamFunc flowcontract.StreamFunc) error {
	if len(currentState.History) == 0 {
		return nil
	}
	if currentState.Metadata == nil {
		currentState.Metadata = make(map[string]interface{})
	}

	lastHistory := currentState.History[len(currentState.History)-1]

	nameToTool := make(map[string]ITool)
	for _, tool := range m.tools {
		for _, t := range tool.Tools(ctx) {
			nameToTool[t.Function.Name] = tool
		}
	}

	executedTools := make(map[string]bool)
	notFoundTools := make([]llms.ToolCall, 0)
	mutex := sync.Mutex{}
	wg := sync.WaitGroup{}

	for _, part := range lastHistory.Parts {
		toolCallPart, ok := part.(llms.ToolCall)
		if !ok {
			continue
		}

		if executedTools[toolCallPart.ID] {
			continue
		}
		executedTools[toolCallPart.ID] = true

		tool, ok := nameToTool[toolCallPart.FunctionCall.Name]
		if !ok {
			m.logger.Warnf(ctx, "tool not found %s", toolCallPart.FunctionCall.Name)
			notFoundTools = append(notFoundTools, toolCallPart)
			continue
		}

		if streamFunc != nil {
			toolEvent := ToolEvent{
				ID:     toolCallPart.ID,
				Type:   "tool_start",
				Name:   toolCallPart.FunctionCall.Name,
				Input:  toolCallPart.FunctionCall.Arguments,
				Result: "",
			}
			_ = streamFunc(ctx, &flowcontract.FlowStreamEvent{
				FullState: currentState,
				Chunk:     toolEvent.String(),
			})
		}

		wg.Add(1)
		toolCall := toolCallPart
		utils.SafeGo(ctx, m.logger, func() {
			defer wg.Done()

			toolResponse, err := tool.Run(ctx, toolCall)
			if err != nil {
				m.logger.Errorf(ctx, "tool run failed %s", err)
				toolResponse = llms.ToolCallResponse{
					ToolCallID: toolCall.ID,
					Name:       toolCall.FunctionCall.Name,
					Content:    "[TOOL ERROR] " + err.Error(),
				}
			}

			response := llms.MessageContent{
				Role:  llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{toolResponse},
			}

			toolEvent := ToolEvent{
				ID:     toolCall.ID,
				Type:   "tool_end",
				Name:   toolCall.FunctionCall.Name,
				Result: extractToolResult(response),
			}

			mutex.Lock()
			if streamFunc != nil {
				_ = streamFunc(ctx, &flowcontract.FlowStreamEvent{
					FullState: currentState,
					Chunk:     toolEvent.String(),
				})
			}
			currentState.History = append(currentState.History, response)
			mutex.Unlock()
		})
	}

	wg.Wait()

	toolCount := 0
	if existing, ok := currentState.Metadata["tool_count"]; ok {
		if v, ok := existing.(int); ok {
			toolCount = v
		}
	}
	currentState.Metadata["tool_count"] = toolCount + 1

	// append not found tools anyway to history to avoid api complaints
	for _, tool := range notFoundTools {
		currentState.History = append(currentState.History, llms.MessageContent{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{llms.ToolCallResponse{
				ToolCallID: tool.ID,
				Content:    "Tool not found",
			}},
		})
	}

	return nil
}

func extractToolResult(message llms.MessageContent) string {
	for _, part := range message.Parts {
		if toolCallResponse, ok := part.(llms.ToolCallResponse); ok {
			return toolCallResponse.Content
		}
		if text, ok := part.(llms.TextContent); ok {
			return text.Text
		}
	}

	return ""
}

func NewTools(opts ...Option) (*Tools, error) {
	defaultLogger, err := logger.NewLogger()
	if err != nil {
		return nil, xerror.Wrap(err)
	}

	tools := &Tools{
		logger: defaultLogger,
		tools:  make([]ITool, 0),
		name:   DefaultNodeName,
	}
	for _, opt := range opts {
		opt(tools)
	}

	return tools, nil
}
