package main

import (
	"context"
	"fmt"
	"os"

	"golanggraph/checkpointer"
	flowcontract "golanggraph/contract"
	"golanggraph/edge"
	"golanggraph/flow"
	"golanggraph/logger"
	"golanggraph/prebuilt/node/chat"
	"golanggraph/state"

	"github.com/tmc/langchaingo/llms"
)

func main() {
	// init dependecy
	logger, err := logger.NewLogger(logger.WithLevel("info"))
	if err != nil {
		panic(err)
	}

	// create chat node
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	chat, err := chat.NewChatNode(
		chat.WithLLM([]string{
			fmt.Sprintf("openai;https://api.openai.com/v1;%s;gpt-4o-mini", apiKey),
		}),
	)

	if err != nil {
		panic(err)
	}

	// create agent flow
	flow, err := flow.NewFlowBuilder(logger).
		SetName("mcp_demo_flow").
		SetCheckpointer(checkpointer.NewInMemoryCheckpointer()).
		AddNode(chat).
		AddEdge(edge.Edge{From: flow.StartNode, To: chat.Name()}).
		AddEdge(edge.Edge{From: chat.Name(), To: flow.EndNode}).
		Compile()

	if err != nil {
		panic(err)
	}

	question := "Tell me a joke about Sydney's weather"

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

		// print in stream
		if event.Chunk != "" {
			fmt.Print(event.Chunk)
		}

		return nil
	})

	if err != nil {
		panic(err)
	}
}
