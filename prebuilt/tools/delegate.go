package agent

import (
	"context"
	"encoding/json"
	"fmt"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"
	"github.com/tmc/langchaingo/llms"
)

type DelegateTaskArgs struct {
	AgentName string `json:"agent_name"`
	Task      string `json:"task"`
}

type DelegateTaskTool struct {
	subAgents map[string]flowcontract.Node
}

func (d *DelegateTaskTool) Tools(ctx context.Context) []llms.Tool {
	agentNames := make([]string, 0, len(d.subAgents))
	for name := range d.subAgents {
		agentNames = append(agentNames, name)
	}

	return []llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "delegate_task",
				Description: fmt.Sprintf("Delegate a task to a sub-agent. Available agents: %v", agentNames),
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent_name": map[string]any{
							"type":        "string",
							"description": "Name of the sub-agent to delegate to",
						},
						"task": map[string]any{
							"type":        "string",
							"description": "The task description to send to the sub-agent",
						},
					},
					"required": []string{"agent_name", "task"},
				},
			},
		},
	}
}

func (d *DelegateTaskTool) Run(ctx context.Context, toolCall llms.ToolCall) (llms.ToolCallResponse, error) {
	var args DelegateTaskArgs
	if err := json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &args); err != nil {
		return llms.ToolCallResponse{
			ToolCallID: toolCall.ID,
			Name:       toolCall.FunctionCall.Name,
			Content:    fmt.Sprintf("[ERROR] Failed to parse arguments: %s", err.Error()),
		}, nil
	}

	subAgent, ok := d.subAgents[args.AgentName]
	if !ok {
		return llms.ToolCallResponse{
			ToolCallID: toolCall.ID,
			Name:       toolCall.FunctionCall.Name,
			Content:    fmt.Sprintf("[ERROR] Sub-agent '%s' not found", args.AgentName),
		}, nil
	}

	childState := &state.State{
		History: []llms.MessageContent{
			{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextContent{Text: args.Task}},
			},
		},
		Metadata: make(map[string]interface{}),
	}

	if err := subAgent.Run(ctx, childState, nil); err != nil {
		return llms.ToolCallResponse{
			ToolCallID: toolCall.ID,
			Name:       toolCall.FunctionCall.Name,
			Content:    fmt.Sprintf("[ERROR] Sub-agent execution failed: %s", err.Error()),
		}, nil
	}

	response := childState.GetLastResponse()
	if response == "" {
		response = "[No response from sub-agent]"
	}

	return llms.ToolCallResponse{
		ToolCallID: toolCall.ID,
		Name:       toolCall.FunctionCall.Name,
		Content:    response,
	}, nil
}

func NewDelegateTaskTool(subAgents map[string]flowcontract.Node) *DelegateTaskTool {
	return &DelegateTaskTool{subAgents: subAgents}
}
