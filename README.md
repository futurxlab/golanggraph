# golanggraph

Lightweight AI Agent SDK inspired from LangGraph, written in Golang, so called golanggraph.

**Core Philosophy**: The heart of golanggraph is **flexible context management**. You can freely assemble and pass context between various nodes in your agent, giving you complete control over what information flows to the next step in your workflow.

## Features

- **Graph-based Workflow Design**: Define workflows as directed graphs with nodes and edges
- **Conditional Routing**: Dynamic edge routing based on state conditions
- **State Management**: Persistent state across workflow execution with checkpointing
- **Streaming Execution**: Real-time streaming of workflow events and state changes
- **Extensible Architecture**: Easy to create custom nodes and edges
- **Built-in LLM Integration**: Native support for LangChain Go and MCP tools
- **Multiple Checkpointing Backends**: In-memory and Redis-based checkpointing
- **Concurrent Execution**: Parallel processing of independent workflow branches

## Architecture

### Core Concepts

#### Flow
A `Flow` represents a complete workflow that consists of nodes connected by edges. It manages the execution order, state propagation, and error handling.

#### Node
A `Node` is a processing unit that performs a specific task. Each node implements the `Node` interface and can:
- Process input state
- Generate output state
- Stream events during execution
- Handle errors gracefully

#### Edge
An `Edge` connects nodes and defines the flow direction. Edges can be:
- **Simple**: Direct connection from one node to another
- **Conditional**: Route to different nodes based on state conditions
- **Dynamic**: Determine routing at runtime

#### State
The `State` object carries data throughout the workflow execution, including:
- Conversation history
- Current node context
- Thread ID for tracking
- Custom metadata

#### Checkpointer
A `Checkpointer` provides persistence and recovery capabilities:
- Save workflow state at checkpoints
- Resume execution from failures
- Support for multiple storage backends

## Installation

```bash
go get github.com/futurxlab/golanggraph
```

## 🔧 Quick Start

### Basic Workflow

```go
package main

import (
    "context"
    "github.com/futurxlab/golanggraph/flow"
    "github.com/futurxlab/golanggraph/checkpointer"
    "github.com/futurxlab/golanggraph/logger"
)

func main() {
    // Create a simple workflow
    flow, err := flow.NewFlowBuilder(logger).
        SetName("my_workflow").
        SetCheckpointer(checkpointer.NewInMemoryCheckpointer()).
        AddNode(myNode).
        AddEdge(edge.Edge{From: flow.StartNode, To: myNode.Name()}).
        AddEdge(edge.Edge{From: myNode.Name(), To: flow.EndNode}).
        Compile()
    
    if err != nil {
        panic(err)
    }
    
    // Execute the workflow
    state, err := flow.Exec(context.Background(), initialState, nil)
}
```

### Custom Node Implementation

```go
type MyCustomNode struct {
    name string
}

func (n *MyCustomNode) Name() string {
    return n.name
}

func (n *MyCustomNode) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {
    // Your custom logic here
    // Process state, call APIs, etc.
    
    // Stream progress if needed
    if streamFunc != nil {
        streamFunc(ctx, &flowcontract.FlowStreamEvent{
            Chunk: "Processing...",
            FullState: state,
        })
    }
    
    return nil
}
```

## Examples

### 1. Simple Chat Example

A basic example demonstrating how to create a simple chat workflow with an LLM.

**Key Features:**
- Direct node-to-node connection
- LLM integration via LangChain Go
- Streaming response handling

**Usage:**
```bash
cd examples/simplechat
export OPENAI_API_KEY="your_api_key"
go run main.go
```

**What it does:**
- Creates a chat node with Anthropic's Claude LLM
- Builds a simple flow: Start → Chat → End
- Processes user questions and streams responses

### 2. RAG (Retrieval-Augmented Generation) Example

Demonstrates a RAG workflow that enhances user queries with knowledge base information.

**Key Features:**
- Custom RAG node implementation
- Knowledge base simulation
- Enhanced prompt construction

**Usage:**
```bash
cd examples/rag
export OPENAI_API_KEY="your_api_key"
go run main.go
```

**What it does:**
- Intercepts user queries
- Searches simulated knowledge base for relevant information
- Enhances the prompt with retrieved context
- Generates more informed responses

### 3. MCP (Model Context Protocol) Tools Example

Shows how to integrate external tools via MCP servers for enhanced AI capabilities.

**Key Features:**
- MCP server integration
- Conditional tool usage
- Dynamic workflow routing

**Usage:**
```bash
cd examples/mcp
export OPENAI_API_KEY="your_api_key"
go run main.go
```

**What it does:**
- Connects to MCP servers for web content fetching
- Uses conditional edges to route between chat and tools
- Demonstrates tool selection and execution
- Shows how AI can use external tools dynamically

## Advanced Usage

### Conditional Edges

```go
// Create a conditional edge that routes based on tool usage
AddEdge(edge.Edge{
    From:          chat.Name(),
    ConditionalTo: []string{flow.EndNode, tools.Name()},
    ConditionFunc: toolcondition.NewToolCondition(1, tools.Name(), flow.EndNode).Condition,
})
```

### State Management

```go
// Access and modify state during execution
func (n *MyNode) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {
    // Get conversation history
    history := state.History
    
    // Set current node context
    state.SetNode(n.Name())
    
    // Add custom metadata
    state.Metadata["key"] = "value"
    
    return nil
}
```

### Streaming Events

```go
// Handle streaming events during execution
flow.Exec(context.Background(), initialState, func(ctx context.Context, event *flowcontract.FlowStreamEvent) error {
    if event.Chunk != "" {
        fmt.Print(event.Chunk) // Print streaming chunks
    }
    
    if event.FullState != nil {
        // Handle full state updates
        fmt.Printf("Current node: %s\n", event.FullState.GetNode())
    }
    
    return nil
})
```

## Extensions

### Prebuilt Nodes

- **Chat Node**: LLM-powered conversation handling
  - **Connection String Format**: `provider;base_url;api_key;model_name` (e.g., `openai;https://api.openai.com/v1;sk-xxx;gpt-4`)
  - **Supported APIs**: OpenAI Compatible APIs (we recommend using [LiteLLM](https://github.com/BerriAI/litellm) as a proxy to convert other model APIs to OpenAI-compatible format)
  - **Use Cases**: Chatbots, RAG applications, tool integration, streaming conversations

- **MCP Tools Node**: Integration with MCP servers
- **Tool Condition Edge**: Conditional routing based on tool usage

### Prebuilt Edges

- **Tool Condition**: Routes based on tool usage patterns
- **Custom Conditions**: User-defined routing logic

## Contributing

We welcome contributions! Please see our contributing guidelines for details on:

- Code style and standards
- Testing requirements
- Pull request process
- Issue reporting

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Support

- **Issues**: Report bugs and request features via GitHub Issues
- **Discussions**: Join community discussions on GitHub Discussions
- **Documentation**: Check the code examples and inline documentation

## Related Projects

- [LangChain Go](https://github.com/tmc/langchaingo) - LLM framework integration
- [MCP Go](https://github.com/mark3labs/mcp-go) - Model Context Protocol implementation
- [litellm](https://github.com/BerriAI/litellm) - LLM Gateway

---

**GolangGraph** - Building intelligent workflows, one node at a time. 
