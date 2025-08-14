package native

import (
	"context"
	"fmt"
	"testing"

	"github.com/futurxlab/golanggraph/logger"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/llms"
)

func TestNativeLLM(t *testing.T) {
	logger, err := logger.NewLogger()
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	llm, err := NewChatLLM(
		[]string{"openai;https://litellm.futurx.cc/v1;sk-xxxx;glm-4"},
		logger,
	)

	if err != nil {
		t.Fatalf("Failed to create LLM: %v", err)
	}

	response, err := llm.GenerateContent(context.Background(), []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "今天上海的天气怎么样"},
			},
		},
	}, llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
		// fmt.Print(string(chunk))
		return nil
	}), llms.WithTools([]llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "get_weather",
				Description: "Get the weather for a given city",
				Parameters: mcp.ToolInputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"city": map[string]interface{}{
							"type": "string",
						},
						"date": map[string]interface{}{
							"type": "string",
						},
					},
					Required: []string{"city", "date"},
				},
			},
		},
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "get_date",
				Description: "Get the date for a given city",
				Parameters: mcp.ToolInputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"city": map[string]interface{}{
							"type": "string",
						},
					},
					Required: []string{"city"},
				},
			},
		},
	}))

	if err != nil {
		t.Fatalf("Failed to generate content: %v", err)
	}

	name := ""
	arguments := ""
	if len(response.Choices[0].ToolCalls) > 0 {
		for _, toolCall := range response.Choices[0].ToolCalls {
			if toolCall.FunctionCall.Name != "" {
				name = toolCall.FunctionCall.Name
			}
			arguments += toolCall.FunctionCall.Arguments
		}
	}

	fmt.Println(name)
	fmt.Println(arguments)

	// fmt.Println(response.Choices[0].Content)
	// fmt.Println(response.Choices[0].ReasoningContent)
}
