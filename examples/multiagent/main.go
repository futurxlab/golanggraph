package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/futurxlab/golanggraph/prebuilt/agent"
	"github.com/futurxlab/golanggraph/prebuilt/node/tools"
	"github.com/futurxlab/golanggraph/state"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/prebuilt/langchaingoextension/native"

	"github.com/tmc/langchaingo/llms"
)

// SearchTool simulates a web search capability for the researcher agent.
type SearchTool struct{}

func (s *SearchTool) Tools(ctx context.Context) []llms.Tool {
	return []llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "web_search",
				Description: "Search the web for information on a topic",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "The search query",
						},
					},
					"required": []string{"query"},
				},
			},
		},
	}
}

func (s *SearchTool) Run(ctx context.Context, toolCall llms.ToolCall) (llms.ToolCallResponse, error) {
	var args struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &args)

	// Simulated search results
	result := fmt.Sprintf(
		"Search results for %q:\n"+
			"1. Go was created at Google by Robert Griesemer, Rob Pike, and Ken Thompson.\n"+
			"2. Go 1.0 was released in March 2012.\n"+
			"3. Go is known for its simplicity, concurrency support (goroutines), and fast compilation.\n"+
			"4. Go is used by Docker, Kubernetes, Terraform, and many other major projects.",
		args.Query,
	)

	return llms.ToolCallResponse{
		ToolCallID: toolCall.ID,
		Name:       toolCall.FunctionCall.Name,
		Content:    result,
	}, nil
}

// CalculatorTool simulates a calculation capability for the analyst agent.
type CalculatorTool struct{}

func (c *CalculatorTool) Tools(ctx context.Context) []llms.Tool {
	return []llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "calculate",
				Description: "Perform a calculation and return the result",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"expression": map[string]any{
							"type":        "string",
							"description": "A mathematical expression to evaluate, e.g. '2 + 3 * 4'",
						},
					},
					"required": []string{"expression"},
				},
			},
		},
	}
}

func (c *CalculatorTool) Run(ctx context.Context, toolCall llms.ToolCall) (llms.ToolCallResponse, error) {
	var args struct {
		Expression string `json:"expression"`
	}
	_ = json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &args)

	// Simulated calculation
	result := fmt.Sprintf("Result of '%s' = 42 (simulated)", args.Expression)

	return llms.ToolCallResponse{
		ToolCallID: toolCall.ID,
		Name:       toolCall.FunctionCall.Name,
		Content:    result,
	}, nil
}

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Please set OPENAI_API_KEY environment variable")
		os.Exit(1)
	}

	llm, err := native.NewChatLLM(
		[]string{
			fmt.Sprintf("openai;http://localhost:4000/v1;%s;gpt-4o-mini", apiKey),
		},
	)
	if err != nil {
		panic(err)
	}

	// --- Sub-Agent 1: Researcher ---
	// Has a web_search tool; specializes in finding information.
	researcher, err := agent.NewAgent(
		agent.WithName("researcher"),
		agent.WithModel(llm),
		agent.WithTools([]tools.ITool{&SearchTool{}}),
		agent.WithMaxToolCalls(3),
	)
	if err != nil {
		panic(err)
	}

	// --- Sub-Agent 2: Analyst ---
	// Has a calculator tool; specializes in analysis and computation.
	analyst, err := agent.NewAgent(
		agent.WithName("analyst"),
		agent.WithModel(llm),
		agent.WithTools([]tools.ITool{&CalculatorTool{}}),
		agent.WithMaxToolCalls(3),
	)
	if err != nil {
		panic(err)
	}

	// --- Orchestrator Agent ---
	// No tools of its own — delegates to researcher and analyst via delegate_task.
	orchestrator, err := agent.NewAgent(
		agent.WithName("orchestrator"),
		agent.WithModel(llm),
		agent.WithSubAgent("researcher", researcher),
		agent.WithSubAgent("analyst", analyst),
		agent.WithMaxToolCalls(5),
	)
	if err != nil {
		panic(err)
	}

	s := &state.State{
		History: []llms.MessageContent{
			{
				Role: llms.ChatMessageTypeSystem,
				Parts: []llms.ContentPart{llms.TextPart(
					"You are an orchestrator agent. You coordinate tasks between specialized sub-agents:\n" +
						"- 'researcher': Can search the web to find information on any topic.\n" +
						"- 'analyst': Can perform calculations and numerical analysis.\n\n" +
						"When the user asks a question, decide which sub-agent(s) to delegate to. " +
						"You can delegate multiple tasks. After receiving their results, synthesize a final answer.",
				)},
			},
			{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextPart("Research the Go programming language and tell me how old it is in days since its 1.0 release.")},
			},
		},
		Metadata: make(map[string]interface{}),
	}

	fmt.Println("=== Multi-Agent Example ===")
	fmt.Println("User: Research the Go programming language and tell me how old it is in days since its 1.0 release.")
	fmt.Println()
	fmt.Println("--- Orchestrator Response ---")

	err = orchestrator.Run(context.Background(), s, func(ctx context.Context, event *flowcontract.FlowStreamEvent) error {
		if event.Chunk != "" {
			fmt.Print(event.Chunk)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	fmt.Println()
	fmt.Println()
	fmt.Println("--- Final Answer ---")
	fmt.Println(s.GetLastResponse())
}
