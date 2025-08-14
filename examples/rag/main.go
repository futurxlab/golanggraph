package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/flow"
	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/prebuilt/node/chat"
	"github.com/futurxlab/golanggraph/state"
	"github.com/futurxlab/golanggraph/xerror"

	"github.com/tmc/langchaingo/llms"
)

// RAGNode simulates a node that searches content from knowledge base
type RAGNode struct {
	name string
}

func (r *RAGNode) Name() string {
	if r.name != "" {
		return r.name
	}
	return "RAGNode"
}

func (r *RAGNode) Run(ctx context.Context, currentState *state.State, streamFunc flowcontract.StreamFunc) error {
	// Get the last user message
	var userMessage string
	for i := len(currentState.History) - 1; i >= 0; i-- {
		if currentState.History[i].Role == llms.ChatMessageTypeHuman {
			for _, part := range currentState.History[i].Parts {
				if textPart, ok := part.(llms.TextContent); ok {
					userMessage = textPart.Text
					break
				}
			}
			break
		}
	}

	if userMessage == "" {
		return xerror.New("no user message found")
	}

	// Simulate searching related content from knowledge base
	searchResults := r.searchKnowledgeBase(userMessage)

	// Create enhanced user message combining question and retrieved knowledge
	enhancedMessage := fmt.Sprintf(`Question: %s

Relevant Knowledge Base Information:
%s

Please answer the above question based on the provided knowledge base information. If there is no relevant information in the knowledge base, please honestly say you don't know.`, userMessage, searchResults)

	// Replace the last user message with the enhanced message
	if len(currentState.History) > 0 {
		lastIndex := len(currentState.History) - 1
		if currentState.History[lastIndex].Role == llms.ChatMessageTypeHuman {
			currentState.History[lastIndex] = llms.MessageContent{
				Role: llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: enhancedMessage},
				},
			}
		}
	}

	// If there's a streaming function, output retrieved content
	if streamFunc != nil {
		streamFunc(ctx, &flowcontract.FlowStreamEvent{
			FullState: currentState,
			Chunk:     fmt.Sprintf("\n[Retrieved related knowledge: %s]\n", searchResults),
		})
	}

	return nil
}

// searchKnowledgeBase simulates searching content from knowledge base
func (r *RAGNode) searchKnowledgeBase(query string) string {
	// Simulate knowledge base content
	knowledgeBase := map[string]string{
		"weather":   "Sydney's weather is generally mild, with average summer temperature of 25°C and winter average of 15°C.",
		"tourism":   "Sydney is Australia's largest city, famous attractions include Sydney Opera House, Sydney Harbour Bridge, Bondi Beach, etc.",
		"culture":   "Sydney is a multicultural city with rich art, music and food culture.",
		"education": "Sydney has several prestigious universities including University of Sydney, University of New South Wales, etc.",
		"economy":   "Sydney is Australia's financial center with developed finance, trade and service industries.",
	}

	// Simple keyword matching
	query = strings.ToLower(query)
	for keyword, content := range knowledgeBase {
		if strings.Contains(query, strings.ToLower(keyword)) {
			return content
		}
	}

	// If no matching content is found, return general information
	return "Sydney is the capital of New South Wales, Australia, a vibrant international metropolis."
}

func NewRAGNode(name string) *RAGNode {
	return &RAGNode{name: name}
}

func main() {
	// Initialize dependencies
	logger, err := logger.NewLogger(logger.WithLevel("info"))
	if err != nil {
		panic(err)
	}

	// Create RAG node
	ragNode := NewRAGNode("knowledge_search")

	// Create chat node using RAG context
	apiKey := os.Getenv("OPENAI_API_KEY")
	chatNode, err := chat.NewChatNode(
		chat.WithLLM([]string{
			fmt.Sprintf("openai;https://api.openai.com/v1;%s;gpt-4o-mini", apiKey),
		}),
		chat.WithSystemPromptPrefix("You are an intelligent assistant, please answer user questions based on retrieved knowledge base information."),
		chat.WithName("chat_with_rag"),
	)

	// Create RAG flow
	flow, err := flow.NewFlowBuilder(logger).
		SetName("rag_demo_flow").
		SetCheckpointer(checkpointer.NewInMemoryCheckpointer()).
		AddNode(ragNode).
		AddNode(chatNode).
		AddEdge(edge.Edge{From: flow.StartNode, To: ragNode.Name()}).
		AddEdge(edge.Edge{From: ragNode.Name(), To: chatNode.Name()}).
		AddEdge(edge.Edge{From: chatNode.Name(), To: flow.EndNode}).
		Compile()

	if err != nil {
		panic(err)
	}

	// Test question
	question := "What's the weather like in Sydney?"

	fmt.Printf("\n=== Question: %s ===\n", question)

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
		// Print streaming output
		if event.Chunk != "" {
			fmt.Print(event.Chunk)
		}
		return nil
	})

	if err != nil {
		fmt.Printf("Error executing flow: %v\n", err)
	}

	fmt.Println("\n" + strings.Repeat("-", 50))
}
