package flow

import (
	"context"
	"fmt"
	"testing"

	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"

	"github.com/Yet-Another-AI-Project/kiwi-lib/logger"
	"github.com/futurxlab/golanggraph/state"
)

type sample1Node struct{}

func (n *sample1Node) Name() string {
	return "sample1"
}

func (n *sample1Node) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {

	if state.Metadata == nil {
		state.Metadata = make(map[string]interface{})
	}

	state.Metadata["sample1"] = "sample1"
	return nil
}

type sample2Node struct{}

func (n *sample2Node) Name() string {
	return "sample2"
}

func (n *sample2Node) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {
	if state.Metadata == nil {
		state.Metadata = make(map[string]interface{})
	}

	state.Metadata["sample2"] = "sample2"
	return nil
}

type sample3Node struct{}

func (n *sample3Node) Name() string {
	return "sample3"
}

func (n *sample3Node) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {

	fmt.Println(state.Metadata)

	state.Metadata["sample3"] = "sample3"
	return nil
}

type hookTestNode struct {
	callOrder    *[]string
	beforeCalled bool
	runCalled    bool
	afterCalled  bool
}

func (n *hookTestNode) Name() string {
	return "hook_test"
}

func (n *hookTestNode) BeforeRun(ctx context.Context, state *state.State) *flowcontract.HookResult {
	*n.callOrder = append(*n.callOrder, "before")
	n.beforeCalled = true
	state.Metadata["before_key"] = "before_value"
	return nil
}

func (n *hookTestNode) Run(ctx context.Context, state *state.State, streamFunc flowcontract.StreamFunc) error {
	*n.callOrder = append(*n.callOrder, "run")
	n.runCalled = true

	beforeValue, ok := state.Metadata["before_key"]
	if !ok || beforeValue != "before_value" {
		return fmt.Errorf("before_key is not propagated to run")
	}

	state.Metadata["run_key"] = "run_value"
	return nil
}

func (n *hookTestNode) AfterRun(ctx context.Context, state *state.State) *flowcontract.HookResult {
	*n.callOrder = append(*n.callOrder, "after")
	n.afterCalled = true

	beforeValue, beforeOK := state.Metadata["before_key"]
	runValue, runOK := state.Metadata["run_key"]
	if !beforeOK || beforeValue != "before_value" {
		panic("before_key is not propagated to afterrun")
	}
	if !runOK || runValue != "run_value" {
		panic("run_key is not propagated to afterrun")
	}

	return nil
}

func TestFlow(t *testing.T) {

	t.Run("test parallel flow", func(t *testing.T) {
		sample1 := &sample1Node{}
		sample2 := &sample2Node{}
		sample3 := &sample3Node{}

		logger, err := logger.NewLogger()
		if err != nil {
			t.Fatal(err)
		}

		checkpointer := checkpointer.NewInMemoryCheckpointer()

		flow, err := NewFlowBuilder(logger).
			SetName("test").
			SetCheckpointer(checkpointer).
			AddNode(sample1).
			AddNode(sample2).
			AddNode(sample3, sample1.Name(), sample2.Name()).
			AddEdge(edge.Edge{
				From: StartNode,
				To:   sample1.Name(),
			}).
			AddEdge(edge.Edge{
				From: StartNode,
				To:   sample2.Name(),
			}).
			AddEdge(edge.Edge{
				From: sample1.Name(),
				To:   sample3.Name(),
			}).
			AddEdge(edge.Edge{
				From: sample2.Name(),
				To:   sample3.Name(),
			}).
			AddEdge(edge.Edge{
				From: sample3.Name(),
				To:   EndNode,
			}).
			Compile()

		if err != nil {
			t.Fatal(err)
		}

		flow.Exec(context.Background(), state.State{}, nil)
	})
}

type jumpSourceNode struct {
	runCount int
}

func (n *jumpSourceNode) Name() string {
	return "jump_source"
}

func (n *jumpSourceNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}
	n.runCount++
	s.Metadata["run_count"] = n.runCount
	return nil
}

func (n *jumpSourceNode) AfterRun(ctx context.Context, s *state.State) *flowcontract.HookResult {
	if n.runCount == 1 {
		return &flowcontract.HookResult{JumpToNode: "jump_source"}
	}
	return nil
}

func TestJumpToNode(t *testing.T) {
	source := &jumpSourceNode{}

	logger, err := logger.NewLogger()
	if err != nil {
		t.Fatal(err)
	}

	cp := checkpointer.NewInMemoryCheckpointer()

	f, err := NewFlowBuilder(logger).
		SetName("test_jump").
		SetCheckpointer(cp).
		AddNode(source).
		AddEdge(edge.Edge{From: StartNode, To: source.Name()}).
		AddEdge(edge.Edge{From: source.Name(), To: EndNode}).
		Compile()
	if err != nil {
		t.Fatal(err)
	}

	result, err := f.Exec(context.Background(), state.State{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if result.Metadata["run_count"] != 2 {
		t.Errorf("expected run_count=2 (initial run + jump re-run), got %v", result.Metadata["run_count"])
	}
}

func TestHookLifecycle(t *testing.T) {
	callOrder := make([]string, 0, 3)
	hookNode := &hookTestNode{callOrder: &callOrder}

	logger, err := logger.NewLogger()
	if err != nil {
		t.Fatal(err)
	}

	checkpointer := checkpointer.NewInMemoryCheckpointer()

	flow, err := NewFlowBuilder(logger).
		SetName("test_hooks").
		SetCheckpointer(checkpointer).
		AddNode(hookNode).
		AddEdge(edge.Edge{
			From: StartNode,
			To:   hookNode.Name(),
		}).
		AddEdge(edge.Edge{
			From: hookNode.Name(),
			To:   EndNode,
		}).
		Compile()
	if err != nil {
		t.Fatal(err)
	}

	_, err = flow.Exec(context.Background(), state.State{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(callOrder) != 3 {
		t.Fatalf("expected 3 hook lifecycle calls, got %d", len(callOrder))
	}

	expectedOrder := []string{"before", "run", "after"}
	for i := range expectedOrder {
		if callOrder[i] != expectedOrder[i] {
			t.Fatalf("expected call order %v, got %v", expectedOrder, callOrder)
		}
	}

	if !hookNode.beforeCalled {
		t.Errorf("BeforeRun was not called")
	}
	if !hookNode.runCalled {
		t.Errorf("Run was not called")
	}
	if !hookNode.afterCalled {
		t.Errorf("AfterRun was not called")
	}
}
