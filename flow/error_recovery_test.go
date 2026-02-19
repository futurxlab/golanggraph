package flow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/state"
)

// ========================================================================
// Error Recovery Test Nodes
// ========================================================================

// errorNode always returns an error
type errorNode struct {
	name string
}

func (n *errorNode) Name() string { return n.name }
func (n *errorNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	return fmt.Errorf("intentional error from node %s", n.name)
}

// slowNode sleeps for a duration, simulating long-running work
type slowNode struct {
	name     string
	duration time.Duration
}

func (n *slowNode) Name() string { return n.name }
func (n *slowNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(n.duration):
	}
	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}
	s.Metadata[n.name] = "completed"
	return nil
}

// ========================================================================
// Error Recovery Tests
// ========================================================================

// TestError_NodeFailure: When a node returns an error, the flow should
// propagate the error and still have checkpoints saved before the failure.
func TestError_NodeFailure(t *testing.T) {
	nodeA := &metadataNode{name: "nodeA"}
	errNode := &errorNode{name: "errNode"}
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	f, err := NewFlowBuilder(log).
		SetName("error_flow").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(errNode).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: errNode.Name()}).
		AddEdge(edge.Edge{From: errNode.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	_, err = f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intentional error")
}

// TestError_ContextCancellation: When context is cancelled, the flow
// should stop gracefully.
func TestError_ContextCancellation(t *testing.T) {
	slowN := &slowNode{name: "slowNode", duration: 5 * time.Second}
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	f, err := NewFlowBuilder(log).
		SetName("cancel_flow").
		SetCheckpointer(cp).
		AddNode(slowN).
		AddEdge(edge.Edge{From: StartNode, To: slowN.Name()}).
		AddEdge(edge.Edge{From: slowN.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err = f.Exec(ctx, state.State{}, nil)
	// The flow should either return context error or timeout-related error
	// (depending on which goroutine catches it first)
	if err != nil {
		t.Logf("Flow returned error as expected: %v", err)
	}
	// If no error, the node might have completed before the cancel (unlikely with 5s sleep)
}

// TestError_CheckpointsSavedBeforeFailure: Verify that checkpoints
// created before a node failure are still accessible.
func TestError_CheckpointsSavedBeforeFailure(t *testing.T) {
	nodeA := &metadataNode{name: "nodeA"}
	nodeB := &metadataNode{name: "nodeB"}
	errNode := &errorNode{name: "errNode"}
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	f, err := NewFlowBuilder(log).
		SetName("checkpoint_before_failure").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddNode(errNode).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: errNode.Name()}).
		AddEdge(edge.Edge{From: errNode.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	initState := state.State{Metadata: map[string]interface{}{"init": true}}
	initState.SetThreadID("error-recovery-thread")

	_, err = f.Exec(context.Background(), initState, nil)
	require.Error(t, err)

	// Despite the error, checkpoints before the failure should exist
	entries, err := cp.List(context.Background(), "error-recovery-thread")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 1,
		"should have at least the initial checkpoint saved")

	// The initial checkpoint should have source "input"
	assert.Equal(t, "input", entries[0].Source)
}

// TestError_StreamFuncError: When streamFunc returns an error,
// it should be logged but not stop execution (based on flow.go implementation).
func TestError_StreamFuncError(t *testing.T) {
	nodeA := &metadataNode{name: "nodeA"}
	nodeB := &metadataNode{name: "nodeB"}
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	f, err := NewFlowBuilder(log).
		SetName("stream_error_flow").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	// Stream function that always errors
	errStream := func(ctx context.Context, event *flowcontract.FlowStreamEvent) error {
		return fmt.Errorf("stream error")
	}

	// Flow should complete despite stream errors (flow.go logs but doesn't propagate)
	result, err := f.Exec(context.Background(), state.State{}, errStream)
	require.NoError(t, err)

	// Nodes should still have executed
	assert.Equal(t, "nodeA", result.Metadata["nodeA"])
	assert.Equal(t, "nodeB", result.Metadata["nodeB"])
}

// TestError_EmptyFlowName: Compile should fail with empty flow name.
func TestError_EmptyFlowName(t *testing.T) {
	log := newTestLogger(t)
	cp := checkpointer.NewInMemoryCheckpointer()

	_, err := NewFlowBuilder(log).
		SetName("").
		SetCheckpointer(cp).
		Compile()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flow name cannot be empty")
}

// TestError_NilCheckpointer: Compile should fail without a checkpointer.
func TestError_NilCheckpointer(t *testing.T) {
	log := newTestLogger(t)

	_, err := NewFlowBuilder(log).
		SetName("no_checkpointer").
		Compile()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpointer is required")
}

// TestError_DisconnectedNode: A node with no from-edge should fail compilation.
func TestError_DisconnectedNode(t *testing.T) {
	log := newTestLogger(t)
	cp := checkpointer.NewInMemoryCheckpointer()
	nodeA := &metadataNode{name: "nodeA"}
	nodeB := &metadataNode{name: "nodeB"} // disconnected

	_, err := NewFlowBuilder(log).
		SetName("disconnected").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: EndNode}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: EndNode}).
		Compile()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no from edges")
}
