package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"
	"github.com/tmc/langchaingo/llms"
)

type delegateTaskArgs struct {
	AgentName string `json:"agent_name"`
	Task      string `json:"task"`
}

type delegateTaskTool struct {
	subAgents map[string]flowcontract.Node
}

func newDelegateTaskTool(subAgents map[string]flowcontract.Node) *delegateTaskTool {
	return &delegateTaskTool{subAgents: subAgents}
}

func (d *delegateTaskTool) agentNames() []string {
	names := make([]string, 0, len(d.subAgents))
	for name := range d.subAgents {
		names = append(names, name)
	}
	return names
}

func (d *delegateTaskTool) Tools(ctx context.Context) []llms.Tool {
	names := d.agentNames()
	description := fmt.Sprintf(
		"Delegate a task to a sub-agent. Available agents: [%s]. "+
			"Provide the agent_name and a clear task description. "+
			"The sub-agent will execute the task and return its result.",
		strings.Join(names, ", "),
	)

	return []llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "delegate_task",
				Description: description,
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent_name": map[string]any{
							"type":        "string",
							"description": fmt.Sprintf("Name of the sub-agent to delegate to. Must be one of: %s", strings.Join(names, ", ")),
							"enum":        names,
						},
						"task": map[string]any{
							"type":        "string",
							"description": "A clear description of the task for the sub-agent to perform",
						},
					},
					"required": []string{"agent_name", "task"},
				},
			},
		},
	}
}

func (d *delegateTaskTool) Run(ctx context.Context, toolCall llms.ToolCall) (llms.ToolCallResponse, error) {
	var args delegateTaskArgs
	if err := json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &args); err != nil {
		return llms.ToolCallResponse{
			ToolCallID: toolCall.ID,
			Name:       toolCall.FunctionCall.Name,
			Content:    fmt.Sprintf("[DELEGATE ERROR] Failed to parse arguments: %s", err.Error()),
		}, nil
	}

	subAgent, ok := d.subAgents[args.AgentName]
	if !ok {
		return llms.ToolCallResponse{
			ToolCallID: toolCall.ID,
			Name:       toolCall.FunctionCall.Name,
			Content:    fmt.Sprintf("[DELEGATE ERROR] Agent %q not found. Available agents: %s", args.AgentName, strings.Join(d.agentNames(), ", ")),
		}, nil
	}

	subState := &state.State{
		History: []llms.MessageContent{
			{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextContent{Text: args.Task}},
			},
		},
		Metadata: make(map[string]interface{}),
	}

	if err := subAgent.Run(ctx, subState, nil); err != nil {
		return llms.ToolCallResponse{
			ToolCallID: toolCall.ID,
			Name:       toolCall.FunctionCall.Name,
			Content:    fmt.Sprintf("[DELEGATE ERROR] Agent %q failed: %s", args.AgentName, err.Error()),
		}, nil
	}

	response := subState.GetLastResponse()
	if response == "" {
		response = "[DELEGATE] Agent completed but produced no text response."
	}

	return llms.ToolCallResponse{
		ToolCallID: toolCall.ID,
		Name:       toolCall.FunctionCall.Name,
		Content:    fmt.Sprintf("[%s] %s", args.AgentName, response),
	}, nil
}
