package main

import (
	"context"
	"fmt"
	"os"

	"github.com/futurxlab/golanggraph/prebuilt/agent"
	"github.com/futurxlab/golanggraph/prebuilt/node/tools"
	"github.com/futurxlab/golanggraph/state"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/prebuilt/langchaingoextension/native"

	"github.com/tmc/langchaingo/llms"
)

type WeatherTool struct{}

func (w *WeatherTool) Tools(ctx context.Context) []llms.Tool {
	return []llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "get_weather",
				Description: "Get current weather for a given city",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{
							"type":        "string",
							"description": "City name, e.g. Sydney, Tokyo",
						},
					},
					"required": []string{"city"},
				},
			},
		},
	}
}

func (w *WeatherTool) Run(ctx context.Context, toolCall llms.ToolCall) (llms.ToolCallResponse, error) {
	return llms.ToolCallResponse{
		ToolCallID: toolCall.ID,
		Name:       toolCall.FunctionCall.Name,
		Content:    fmt.Sprintf("Weather in %s: 22°C, partly cloudy", toolCall.FunctionCall.Arguments),
	}, nil
}

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	llm, err := native.NewChatLLM(
		[]string{
			fmt.Sprintf("openai;https://api.openai.com/v1;%s;gpt-4o-mini", apiKey),
		},
	)
	if err != nil {
		panic(err)
	}

	a, err := agent.NewAgent(
		agent.WithName("weather_agent"),
		agent.WithModel(llm),
		agent.WithTools([]tools.ITool{&WeatherTool{}}),
		agent.WithMaxToolCalls(5),
		agent.WithContextWindow(20),
	)
	if err != nil {
		panic(err)
	}

	s := &state.State{
		History: []llms.MessageContent{
			{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextPart("What's the weather like in Sydney and Tokyo?")},
			},
		},
		Metadata: make(map[string]interface{}),
	}

	err = a.Run(context.Background(), s, func(ctx context.Context, event *flowcontract.FlowStreamEvent) error {
		if event.Chunk != "" {
			fmt.Print(event.Chunk)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	fmt.Println()
}
