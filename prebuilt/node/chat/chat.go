package chat

import (
	"context"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/prebuilt/langchaingoextension/native"
	"github.com/futurxlab/golanggraph/state"

	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/xerror"

	"github.com/tmc/langchaingo/llms"
)

const (
	NodeName = "ChatNode"

	TemperatureKey = "temperature"
)

type ChatOption func(*ChatNode)

func WithLLM(llms []string) ChatOption {
	return func(c *ChatNode) {
		c.llms = llms
	}
}

func WithTools(tools []llms.Tool) ChatOption {
	return func(c *ChatNode) {
		c.tools = tools
	}
}

func WithSystemPromptPrefix(systemPromptPrefix string) ChatOption {
	return func(c *ChatNode) {
		c.systemPromptPrefix = systemPromptPrefix
	}
}

func WithName(name string) ChatOption {
	return func(c *ChatNode) {
		c.name = name
	}
}

func WithLogger(logger logger.ILogger) ChatOption {
	return func(c *ChatNode) {
		c.logger = logger
	}
}

type ChatNode struct {
	name               string
	systemPromptPrefix string
	llms               []string
	nativeLLM          *native.ChatLLM
	tools              []llms.Tool
	logger             logger.ILogger
}

func (c *ChatNode) Name() string {
	if c.name != "" {
		return c.name
	}
	return NodeName
}

func (c *ChatNode) Run(ctx context.Context, currentState *state.State, streamFunc flowcontract.StreamFunc) error {

	messages := currentState.History

	if c.systemPromptPrefix != "" {
		messages = append([]llms.MessageContent{{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: c.systemPromptPrefix}},
		}}, currentState.History...)
	}

	var temperature float64
	if currentState.Metadata[TemperatureKey] != nil {
		temperature = currentState.Metadata[TemperatureKey].(float64)
	}

	contentResponse, err := c.nativeLLM.GenerateContent(
		ctx,
		messages,
		llms.WithTools(c.tools),
		llms.WithTemperature(temperature),
		llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
			if streamFunc != nil {
				return streamFunc(ctx, &flowcontract.FlowStreamEvent{
					FullState: currentState,
					Chunk:     string(chunk),
				})
			}
			return nil
		}),
	)
	if err != nil {
		return xerror.Wrap(err)
	}

	messageContent := llms.MessageContent{
		Role:  llms.ChatMessageTypeAI,
		Parts: make([]llms.ContentPart, 0),
	}

	if len(contentResponse.Choices) == 0 {
		return xerror.New("no response from chatLLM")
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

func NewChatNode(options ...ChatOption) (*ChatNode, error) {
	logger, err := logger.NewLogger(logger.WithLevel("info"))
	if err != nil {
		return nil, err
	}
	chat := &ChatNode{
		logger: logger,
	}

	for _, option := range options {
		option(chat)
	}

	llm, err := native.NewChatLLM(chat.llms, chat.logger)
	if err != nil {
		return nil, err
	}

	chat.nativeLLM = llm

	return chat, nil
}
