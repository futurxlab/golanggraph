package main

import (
	"context"
	"fmt"
	"os"

	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/flow"
	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/prebuilt/edge/toolcondition"
	"github.com/futurxlab/golanggraph/prebuilt/node/chat"
	"github.com/futurxlab/golanggraph/prebuilt/node/mcptools"
	"github.com/futurxlab/golanggraph/state"

	"github.com/tmc/langchaingo/llms"
)

func main() {
	// init dependecy
	logger, err := logger.NewLogger(logger.WithLevel("info"))
	if err != nil {
		panic(err)
	}

	// init mcp tools with mcp servers
	// fetch_web: mcp server for fetch web content
	mcpTools, err := mcptools.NewMCPTools(
		mcptools.WithLogger(logger),
		mcptools.WithMCPServers(map[string]mcptools.MCPServer{
			"fetch_web": {
				URL: "https://remote.mcpservers.org/fetch",
			},
		}),
	)
	if err != nil {
		panic(err)
	}

	// create tools node
	tools := mcpTools.Tools(context.Background())

	// create chat node
	apiKey := os.Getenv("OPENAI_API_KEY")
	chat, err := chat.NewChatNode(
		chat.WithLLM([]string{
			fmt.Sprintf("openai;https://api.openai.com/v1;%s;gpt-4o-mini", apiKey),
		}),
		chat.WithTools(tools),
	)

	if err != nil {
		panic(err)
	}

	// create agent flow
	flow, err := flow.NewFlowBuilder(logger).
		SetName("mcp_demo_flow").
		SetCheckpointer(checkpointer.NewInMemoryCheckpointer()).
		AddNode(chat).
		AddNode(mcpTools).
		AddEdge(edge.Edge{From: flow.StartNode, To: chat.Name()}).
		AddEdge(edge.Edge{
			From:          chat.Name(),
			ConditionalTo: []string{flow.EndNode, mcpTools.Name()},
			ConditionFunc: toolcondition.NewToolCondition(1, mcpTools.Name(), flow.EndNode).Condition,
		}).
		AddEdge(edge.Edge{From: mcpTools.Name(), To: chat.Name()}).
		Compile()

	if err != nil {
		panic(err)
	}

	question := "Please tell me content of this page https://github.com/futurxlab/golanggraph"

	_, err = flow.Exec(context.Background(), state.State{
		History: []llms.MessageContent{
			{
				Role: llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{
					llms.TextPart(question),
				},
			},
		},
	}, func(ctx context.Context, event *flowcontract.FlowStreamEvent) error {

		if event.Chunk != "" {
			fmt.Print(event.Chunk)
		}

		return nil
	})

	if err != nil {
		panic(err)
	}
}
