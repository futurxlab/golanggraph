package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/flow"
	"github.com/futurxlab/golanggraph/state"
	"github.com/redis/go-redis/v9"

	"github.com/Yet-Another-AI-Project/kiwi-lib/logger"
)

// AgentNode simulates an AI agent that decides to use a dangerous tool
type AgentNode struct{}

func (n *AgentNode) Name() string { return "agent" }
func (n *AgentNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	fmt.Println("[Agent] Analyzing user request...")
	fmt.Println("[Agent] I need to delete a file to complete this task.")

	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}
	s.Metadata["tool_name"] = "delete_file"
	s.Metadata["tool_args"] = "/tmp/important.txt"
	s.Metadata["agent_decision"] = "needs_tool_approval"
	return nil
}

// ToolApprovalNode interrupts to ask for human approval before executing a dangerous tool
type ToolApprovalNode struct{}

func (n *ToolApprovalNode) Name() string { return "tool_approval" }
func (n *ToolApprovalNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	// Check if we have a resume value (human already approved/rejected)
	if resumeVal := s.GetResumeValue(); resumeVal != nil {
		decision := resumeVal.(string)
		fmt.Printf("[Tool Approval] Human decided: %s\n", decision)
		if decision == "approve" {
			fmt.Println("[Tool Approval] Executing tool: delete_file /tmp/important.txt")
			s.Metadata["tool_result"] = "file_deleted"
		} else {
			fmt.Println("[Tool Approval] Tool execution cancelled by human.")
			s.Metadata["tool_result"] = "cancelled"
		}
		return nil
	}

	// No resume value — interrupt and ask for human approval
	payload := map[string]interface{}{
		"tool":    s.Metadata["tool_name"],
		"args":    s.Metadata["tool_args"],
		"message": "The agent wants to delete a file. Do you approve?",
	}
	fmt.Println("[Tool Approval] ⚠️  Human approval required!")
	return flowcontract.Interrupt(payload)
}

// SummaryNode provides final summary
type SummaryNode struct{}

func (n *SummaryNode) Name() string { return "summary" }
func (n *SummaryNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	result := s.Metadata["tool_result"]
	fmt.Printf("[Summary] Task completed. Tool result: %v\n", result)
	return nil
}

func main() {
	log, err := logger.NewLogger()
	if err != nil {
		panic(err)
	}

	cp := checkpointer.NewRedisCheckpointer(redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	}))

	agent := &AgentNode{}
	approval := &ToolApprovalNode{}
	summary := &SummaryNode{}

	f, err := flow.NewFlowBuilder(log).
		SetName("human_in_the_loop_demo").
		SetCheckpointer(cp).
		AddNode(agent).
		AddNode(approval).
		AddNode(summary).
		AddEdge(edge.Edge{From: flow.StartNode, To: agent.Name()}).
		AddEdge(edge.Edge{From: agent.Name(), To: approval.Name()}).
		AddEdge(edge.Edge{From: approval.Name(), To: summary.Name()}).
		AddEdge(edge.Edge{From: summary.Name(), To: flow.EndNode}).
		Compile()
	if err != nil {
		panic(fmt.Sprintf("Failed to compile flow: %v", err))
	}

	ctx := context.Background()

	fmt.Println("=== Human-in-the-Loop Demo ===")
	fmt.Println()

	// Step 1: Execute the flow — it will interrupt at the approval node
	result, execErr := f.Exec(ctx, state.State{}, nil)

	interruptErr, isInterrupt := flowcontract.IsInterrupt(execErr)
	if !isInterrupt {
		if execErr != nil {
			panic(fmt.Sprintf("Unexpected error: %v", execErr))
		}
		fmt.Println("Flow completed without interrupt (unexpected).")
		return
	}

	// Step 2: Show the interrupt payload to the human
	payload := interruptErr.Payload.(map[string]interface{})
	fmt.Println()
	fmt.Printf("🔔 Interrupt! %s\n", payload["message"])
	fmt.Printf("   Tool: %s\n", payload["tool"])
	fmt.Printf("   Args: %s\n", payload["args"])
	fmt.Println()

	// Step 3: Get human input
	fmt.Print("Do you approve? (yes/no): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	var decision string
	if input == "yes" || input == "y" {
		decision = "approve"
	} else {
		decision = "reject"
	}

	// Step 4: Resume the flow with the human's decision
	fmt.Println()
	threadID := result.GetThreadID()
	fmt.Printf("threadID: %s\n", threadID)
	finalState, err := f.ResumeWithValue(ctx, threadID, decision, nil)
	if err != nil {
		panic(fmt.Sprintf("Failed to resume: %v", err))
	}

	fmt.Println()
	fmt.Printf("=== Final State ===\n")
	fmt.Printf("Tool Result: %v\n", finalState.Metadata["tool_result"])
	fmt.Printf("Agent Decision: %v\n", finalState.Metadata["agent_decision"])
}
