package model

import (
	"context"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"

	"github.com/Yet-Another-AI-Project/kiwi-lib/logger"
	"github.com/Yet-Another-AI-Project/kiwi-lib/xerror"

	"github.com/tmc/langchaingo/llms"
)

const (
	NodeName = "ModelNode"
)

type ModelOption func(*ModelNode)

func WithLLM(llm llms.Model) ModelOption {
	return func(c *ModelNode) {
		c.llm = llm
	}
}

func WithLLMOptions(options ...llms.CallOption) ModelOption {
	return func(c *ModelNode) {
		c.llmOptions = options
	}
}

func WithTools(tools []llms.Tool) ModelOption {
	return func(c *ModelNode) {
		c.tools = tools
	}
}

func WithName(name string) ModelOption {
	return func(c *ModelNode) {
		c.name = name
	}
}

func WithLogger(logger logger.ILogger) ModelOption {
	return func(c *ModelNode) {
		c.logger = logger
	}
}

func WithBeforeRunHook(hooks ...func(ctx context.Context, currentState *state.State) *flowcontract.HookResult) ModelOption {
	return func(c *ModelNode) {
		c.beforeRunHooks = append(c.beforeRunHooks, hooks...)
	}
}

func WithAfterRunHook(hooks ...func(ctx context.Context, currentState *state.State) *flowcontract.HookResult) ModelOption {
	return func(c *ModelNode) {
		c.afterRunHooks = append(c.afterRunHooks, hooks...)
	}
}

type ModelNode struct {
	llmOptions []llms.CallOption
	name       string
	llm        llms.Model
	tools      []llms.Tool
	logger     logger.ILogger

	// hooks
	beforeRunHooks []func(ctx context.Context, currentState *state.State) *flowcontract.HookResult
	afterRunHooks  []func(ctx context.Context, currentState *state.State) *flowcontract.HookResult
}

func (c *ModelNode) Name() string {
	if c.name != "" {
		return c.name
	}
	return NodeName
}

func (c *ModelNode) BeforeRun(ctx context.Context, currentState *state.State) *flowcontract.HookResult {
	for _, hook := range c.beforeRunHooks {
		if result := hook(ctx, currentState); result != nil {
			return result
		}
	}
	return nil
}

func (c *ModelNode) AfterRun(ctx context.Context, currentState *state.State) *flowcontract.HookResult {
	for _, hook := range c.afterRunHooks {
		result := hook(ctx, currentState)
		if result != nil && result.JumpToNode != "" {
			return result
		}
	}
	return nil
}

func (c *ModelNode) Run(ctx context.Context, currentState *state.State, streamFunc flowcontract.StreamFunc) error {

	messages := currentState.History

	llmOptions := append(c.llmOptions, []llms.CallOption{
		llms.WithTools(c.tools),
		llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
			if streamFunc != nil {
				return streamFunc(ctx, &flowcontract.FlowStreamEvent{
					FullState: currentState,
					Chunk:     string(chunk),
				})
			}
			return nil
		}),
	}...)

	contentResponse, err := c.llm.GenerateContent(
		ctx,
		messages,
		llmOptions...,
	)
	if err != nil {
		return xerror.Wrap(err)
	}

	messageContent := llms.MessageContent{
		Role:  llms.ChatMessageTypeAI,
		Parts: make([]llms.ContentPart, 0),
	}

	if len(contentResponse.Choices) == 0 {
		return xerror.New("no response from model")
	}

	for _, choice := range contentResponse.Choices {
		if len(choice.Content) > 0 {
			messageContent.Parts = append(messageContent.Parts, llms.TextContent{Text: choice.Content})
		}

		if len(choice.ToolCalls) > 0 {
			for _, toolCall := range choice.ToolCalls {
				messageContent.Parts = append(messageContent.Parts, llms.ToolCall{
					ID:   toolCall.ID,
					Type: "function",
					FunctionCall: &llms.FunctionCall{
						Name:      toolCall.FunctionCall.Name,
						Arguments: toolCall.FunctionCall.Arguments,
					},
				})
			}
		}
	}

	currentState.History = append(currentState.History, messageContent)

	return nil
}

func NewModelNode(options ...ModelOption) (*ModelNode, error) {
	logger, err := logger.NewLogger(logger.WithLevel("info"))
	if err != nil {
		return nil, err
	}
	node := &ModelNode{
		logger: logger,
	}

	for _, option := range options {
		option(node)
	}

	if node.llm == nil {
		return nil, xerror.New("llm is required")
	}

	return node, nil
}
