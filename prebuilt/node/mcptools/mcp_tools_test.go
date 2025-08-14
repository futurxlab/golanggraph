package mcptools

import (
	"context"
	"testing"
)

func TestMCPTools(t *testing.T) {
	mcpTools, err := NewMCPTools(
		WithMCPServers(map[string]MCPServer{
			"fetch": {
				URL: "https://remote.mcpservers.org/fetch",
			},
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create MCP tools: %v", err)
	}

	tools := mcpTools.Tools(context.Background())

	for _, tool := range tools {
		t.Logf("MCP tool: %+v", tool.Function)
	}
}
