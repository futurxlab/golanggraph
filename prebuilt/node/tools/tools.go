package tools

import (
	"context"
	"sync"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"

	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/utils"
	"github.com/futurxlab/golanggraph/xerror"

	"github.com/tmc/langchaingo/llms"
)

const (
	DefaultNodeName = "ToolsNode"
)

type Options struct {
	Tools    []ITool
	Logger   logger.ILogger
	NodeName string
}

type Option func(*Options)

func WithTools(tools []ITool) Option {
	return func(o *Options) {
		o.Tools = tools
	}
}

func WithLogger(logger logger.ILogger) Option {
	return func(o *Options) {
		o.Logger = logger
	}
}

func WithNodeName(name string) Option {
	return func(o *Options) {
		o.NodeName = name
	}
}

type Tools struct {
	tools  []ITool
	logger logger.ILogger
	name   string
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

func (m *Tools) Run(ctx context.Context, currentState *state.State, streamFunc flowcontract.StreamFunc) error {
	if len(currentState.History) == 0 {
		return nil
	}

	var systemMessage llms.MessageContent

	for _, part := range currentState.History {
		if part.Role == llms.ChatMessageTypeSystem {
			systemMessage = part
			break
		}
	}

	lastHistory := currentState.History[len(currentState.History)-1]

	nameToTool := make(map[string]ITool)
	for _, tool := range m.tools {
		for _, t := range tool.Tools(ctx) {
			nameToTool[t.Function.Name] = tool
		}
	}

	executedTools := make(map[string]bool)

	groupedMessage := make(map[string]llms.MessageContent)

	for _, part := range lastHistory.Parts {
		if toolCallPart, ok := part.(llms.ToolCall); ok {

			if _, ok := nameToTool[toolCallPart.FunctionCall.Name]; !ok {
				m.logger.Warnf(ctx, "tool not found %s", toolCallPart.FunctionCall.Name)
				continue
			}

			if _, ok := executedTools[toolCallPart.ID]; !ok {
				if _, ok := groupedMessage[toolCallPart.FunctionCall.Name]; !ok {
					groupedMessage[toolCallPart.FunctionCall.Name] = llms.MessageContent{
						Role: llms.ChatMessageTypeAI,
						Parts: []llms.ContentPart{
							toolCallPart,
						},
					}
				} else {
					newParts := append(groupedMessage[toolCallPart.FunctionCall.Name].Parts, toolCallPart)
					groupedMessage[toolCallPart.FunctionCall.Name] = llms.MessageContent{
						Role:  llms.ChatMessageTypeAI,
						Parts: newParts,
					}
				}
			} else {
				executedTools[toolCallPart.ID] = true
			}
		}
	}

	mutex := sync.Mutex{}

	wg := sync.WaitGroup{}
	for name, message := range groupedMessage {

		tool, ok := nameToTool[name]
		if !ok {
			m.logger.Warnf(ctx, "tool not found %s", name)
			continue
		}

		wg.Add(1)

		utils.SafeGo(ctx, m.logger, func() {
			defer wg.Done()
			messages := make([]llms.MessageContent, 0)
			if systemMessage.Role != "" {
				messages = append(messages, systemMessage)
			}
			messages = append(messages, message)

			state := &state.State{
				History:  messages,
				Metadata: currentState.Metadata,
			}

			if err := tool.Run(ctx, state, streamFunc); err != nil {
				m.logger.Errorf(ctx, "tool run failed %s", err)
				return
			}

			mutex.Lock()
			currentState.Merge(state)
			mutex.Unlock()
		})
	}

	wg.Wait()

	toolCount := 1
	if currentState.Metadata["tool_count"] != nil {
		toolCount = currentState.Metadata["tool_count"].(int) + 1
	}
	currentState.Metadata["tool_count"] = toolCount

	return nil
}

func NewTools(opts ...Option) (*Tools, error) {
	defaultLogger, err := logger.NewLogger()
	if err != nil {
		return nil, xerror.Wrap(err)
	}

	options := &Options{
		Logger: defaultLogger,
	}

	for _, opt := range opts {
		opt(options)
	}

	return &Tools{
		logger: options.Logger,
		tools:  options.Tools,
		name:   options.NodeName,
	}, nil
}
