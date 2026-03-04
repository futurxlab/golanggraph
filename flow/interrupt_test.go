package flow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/futurxlab/golanggraph/checkpointer"
	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/state"
)

func TestInterruptError_ImplementsError(t *testing.T) {
	ie := &flowcontract.InterruptError{Payload: "test"}
	var _ error = ie
}

func TestInterruptError_CarriesPayload(t *testing.T) {
	payload := "test payload"
	ie := &flowcontract.InterruptError{Payload: payload}
	if ie.Payload != payload {
		t.Errorf("expected payload %v, got %v", payload, ie.Payload)
	}
}

func TestInterruptError_IsInterrupt(t *testing.T) {
	payload := "test payload"
	ie := flowcontract.Interrupt(payload)

	recovered, ok := flowcontract.IsInterrupt(ie)
	if !ok {
		t.Errorf("IsInterrupt should return true for InterruptError")
	}
	if recovered.Payload != payload {
		t.Errorf("expected payload %q, got %q", payload, recovered.Payload)
	}
}

func TestInterrupt_CreatesError(t *testing.T) {
	payload := "my payload"
	err := flowcontract.Interrupt(payload)

	if err == nil {
		t.Fatal("Interrupt() should return an error")
	}

	if err.Error() != "interrupt" {
		t.Errorf("expected error message 'interrupt', got %q", err.Error())
	}
}

type interruptNode struct {
	name    string
	payload interface{}
}

func (n *interruptNode) Name() string { return n.name }
func (n *interruptNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}
	s.Metadata[n.name] = "executed"
	return flowcontract.Interrupt(n.payload)
}

type trackedNode struct {
	name      string
	value     interface{}
	calls     *[]string
	callMu    *sync.Mutex
	afterCall func()
}

func (n *trackedNode) Name() string { return n.name }
func (n *trackedNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}
	s.Metadata[n.name] = n.value
	n.callMu.Lock()
	*n.calls = append(*n.calls, n.name)
	n.callMu.Unlock()
	if n.afterCall != nil {
		n.afterCall()
	}
	return nil
}

type waitingInterruptNode struct {
	name    string
	payload interface{}
	waitCh  <-chan struct{}
}

func (n *waitingInterruptNode) Name() string { return n.name }
func (n *waitingInterruptNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	<-n.waitCh
	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}
	s.Metadata[n.name] = "executed"
	return flowcontract.Interrupt(n.payload)
}

func TestFlow_Interrupt_StopsExecution(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	payload := "stop-payload"
	intNode := &interruptNode{name: "interruptor", payload: payload}

	f, err := NewFlowBuilder(log).
		SetName("interrupt_stops").
		SetCheckpointer(cp).
		AddNode(intNode).
		AddEdge(edge.Edge{From: StartNode, To: intNode.Name()}).
		AddEdge(edge.Edge{From: intNode.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	result, err := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, err)
	require.NotEmpty(t, result.Metadata)
	assert.Equal(t, "executed", result.Metadata[intNode.Name()])

	interruptErr, ok := flowcontract.IsInterrupt(err)
	require.True(t, ok)
	assert.Equal(t, payload, interruptErr.Payload)
}

func TestFlow_Interrupt_SavesCheckpoint(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	payload := "checkpoint-payload"
	intNode := &interruptNode{name: "interruptor", payload: payload}

	f, err := NewFlowBuilder(log).
		SetName("interrupt_checkpoint").
		SetCheckpointer(cp).
		AddNode(intNode).
		AddEdge(edge.Edge{From: StartNode, To: intNode.Name()}).
		AddEdge(edge.Edge{From: intNode.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	execCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, execErr := f.Exec(execCtx, state.State{}, nil)
	require.Error(t, execErr)
	require.True(t, result.GetThreadID() != "")

	latest, err := cp.GetLatest(context.Background(), result.GetThreadID())
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.NotNil(t, latest.State)
	assert.Equal(t, "interrupt", latest.Source)
	assert.True(t, latest.State.IsInterrupted())
	assert.Equal(t, payload, latest.State.GetInterruptPayload())
}

func TestFlow_Interrupt_StateContainsPayload(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	payload := map[string]interface{}{"reason": "human_review", "attempt": 1}
	intNode := &interruptNode{name: "interruptor", payload: payload}

	f, err := NewFlowBuilder(log).
		SetName("interrupt_payload_state").
		SetCheckpointer(cp).
		AddNode(intNode).
		AddEdge(edge.Edge{From: StartNode, To: intNode.Name()}).
		AddEdge(edge.Edge{From: intNode.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	execCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, execErr := f.Exec(execCtx, state.State{}, nil)
	require.Error(t, execErr)
	assert.Equal(t, payload, result.GetInterruptPayload())
}

func TestFlow_Interrupt_DoesNotExecuteSubsequentNodes(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	var callMu sync.Mutex
	calls := make([]string, 0)
	payload := "halt-after-b"

	nodeA := &trackedNode{name: "A", value: "done", calls: &calls, callMu: &callMu}
	nodeB := &interruptNode{name: "B", payload: payload}
	nodeC := &trackedNode{name: "C", value: "done", calls: &calls, callMu: &callMu}

	f, err := NewFlowBuilder(log).
		SetName("interrupt_stops_next_nodes").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddNode(nodeC).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: nodeC.Name()}).
		AddEdge(edge.Edge{From: nodeC.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	execCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, execErr := f.Exec(execCtx, state.State{}, nil)
	require.Error(t, execErr)
	_, ok := flowcontract.IsInterrupt(execErr)
	require.True(t, ok)

	assert.Equal(t, "done", result.Metadata["A"])
	_, hasC := result.Metadata["C"]
	assert.False(t, hasC)

	callMu.Lock()
	defer callMu.Unlock()
	assert.Contains(t, calls, "A")
	assert.NotContains(t, calls, "C")
}

func TestFlow_Interrupt_ConcurrentNodes(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	payload := "parallel-interrupt"

	aDone := make(chan struct{})
	nodeA := &trackedNode{name: "A", value: "done", calls: &[]string{}, callMu: &sync.Mutex{}, afterCall: func() {
		close(aDone)
	}}
	nodeB := &waitingInterruptNode{name: "B", payload: payload, waitCh: aDone}
	nodeC := &metadataNode{name: "C"}

	f, err := NewFlowBuilder(log).
		SetName("interrupt_concurrent").
		SetCheckpointer(cp).
		SetWorkerCount(2).
		AddNode(nodeA).
		AddNode(nodeB).
		AddNode(nodeC, nodeA.Name(), nodeB.Name()).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: StartNode, To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeC.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: nodeC.Name()}).
		AddEdge(edge.Edge{From: nodeC.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	execCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, execErr := f.Exec(execCtx, state.State{}, nil)
	require.Error(t, execErr)
	_, ok := flowcontract.IsInterrupt(execErr)
	require.True(t, ok)

	entries, err := cp.List(context.Background(), result.GetThreadID())
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	foundAWrite := false
	for _, entry := range entries {
		writes, writeErr := cp.GetPendingWrites(context.Background(), result.GetThreadID(), entry.ID)
		require.NoError(t, writeErr)
		for _, w := range writes {
			if w.NodeName == "A" {
				foundAWrite = true
				break
			}
		}
		if foundAWrite {
			break
		}
	}
	assert.True(t, foundAWrite, "expected pending write for node A")
}

type conditionalInterruptNode struct {
	name      string
	payload   interface{}
	callCount int
	mu        sync.Mutex
}

func (n *conditionalInterruptNode) Name() string { return n.name }
func (n *conditionalInterruptNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}

	n.mu.Lock()
	n.callCount++
	count := n.callCount
	n.mu.Unlock()

	s.Metadata[n.name+"_calls"] = count

	if resumeVal := s.GetResumeValue(); resumeVal != nil {
		s.Metadata[n.name+"_resume_value"] = resumeVal
		s.Metadata[n.name] = "completed"
		return nil
	}

	s.Metadata[n.name] = "interrupted"
	return flowcontract.Interrupt(n.payload)
}

type countBasedInterruptNode struct {
	name      string
	payload   interface{}
	callCount int
	mu        sync.Mutex
}

func (n *countBasedInterruptNode) Name() string { return n.name }
func (n *countBasedInterruptNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}

	n.mu.Lock()
	n.callCount++
	count := n.callCount
	n.mu.Unlock()

	s.Metadata[n.name+"_calls"] = count
	if count == 1 {
		return flowcontract.Interrupt(n.payload)
	}

	s.Metadata[n.name] = "completed"
	s.Metadata[n.name+"_resume_value"] = s.GetResumeValue()
	return nil
}

func TestFlow_ResumeWithValue_Basic(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	node := &conditionalInterruptNode{name: "gate", payload: "need-approval"}

	f, err := NewFlowBuilder(log).
		SetName("resume_with_value_basic").
		SetCheckpointer(cp).
		AddNode(node).
		AddEdge(edge.Edge{From: StartNode, To: node.Name()}).
		AddEdge(edge.Edge{From: node.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	result, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, execErr)
	_, ok := flowcontract.IsInterrupt(execErr)
	require.True(t, ok)
	assert.Equal(t, "need-approval", result.GetInterruptPayload())

	finalState, resumeErr := f.ResumeWithValue(context.Background(), result.GetThreadID(), "approved", nil)
	require.NoError(t, resumeErr)
	assert.Equal(t, "completed", finalState.Metadata[node.Name()])

	node.mu.Lock()
	defer node.mu.Unlock()
	assert.Equal(t, 2, node.callCount)
}

func TestFlow_ResumeWithValue_NodeReceivesResumeValue(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	node := &conditionalInterruptNode{name: "gate", payload: "need-input"}

	f, err := NewFlowBuilder(log).
		SetName("resume_with_value_received").
		SetCheckpointer(cp).
		AddNode(node).
		AddEdge(edge.Edge{From: StartNode, To: node.Name()}).
		AddEdge(edge.Edge{From: node.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	result, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, execErr)

	resumed, resumeErr := f.ResumeWithValue(context.Background(), result.GetThreadID(), "user_input_data", nil)
	require.NoError(t, resumeErr)
	assert.Equal(t, "user_input_data", resumed.Metadata[node.Name()+"_resume_value"])
}

func TestFlow_ResumeWithValue_ClearsInterruptState(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	node := &conditionalInterruptNode{name: "gate", payload: "payload"}

	f, err := NewFlowBuilder(log).
		SetName("resume_with_value_clears_interrupt").
		SetCheckpointer(cp).
		AddNode(node).
		AddEdge(edge.Edge{From: StartNode, To: node.Name()}).
		AddEdge(edge.Edge{From: node.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	interruptedState, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, execErr)
	assert.True(t, interruptedState.IsInterrupted())

	resumedState, resumeErr := f.ResumeWithValue(context.Background(), interruptedState.GetThreadID(), "ok", nil)
	require.NoError(t, resumeErr)
	assert.False(t, resumedState.IsInterrupted())
	assert.Nil(t, resumedState.GetInterruptPayload())
}

func TestFlow_ResumeWithValue_NonInterruptedThread(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	node := &metadataNode{name: "normal"}

	f, err := NewFlowBuilder(log).
		SetName("resume_with_value_non_interrupted").
		SetCheckpointer(cp).
		AddNode(node).
		AddEdge(edge.Edge{From: StartNode, To: node.Name()}).
		AddEdge(edge.Edge{From: node.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	result, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.NoError(t, execErr)

	_, resumeErr := f.ResumeWithValue(context.Background(), result.GetThreadID(), "value", nil)
	require.Error(t, resumeErr)
	assert.Contains(t, resumeErr.Error(), "not interrupted")
}

func TestFlow_ResumeWithValue_ContinuesAfterNode(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	nodeA := &metadataNode{name: "A"}
	nodeB := &conditionalInterruptNode{name: "B", payload: "pause"}
	nodeC := &metadataNode{name: "C"}

	f, err := NewFlowBuilder(log).
		SetName("resume_with_value_continues_after_node").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddNode(nodeC).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: nodeC.Name()}).
		AddEdge(edge.Edge{From: nodeC.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	interruptedState, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, execErr)
	assert.Equal(t, "A", interruptedState.Metadata["A"])
	assert.Equal(t, "interrupted", interruptedState.Metadata["B"])
	_, hasC := interruptedState.Metadata["C"]
	assert.False(t, hasC)

	resumedState, resumeErr := f.ResumeWithValue(context.Background(), interruptedState.GetThreadID(), "approved", nil)
	require.NoError(t, resumeErr)
	assert.Equal(t, "A", resumedState.Metadata["A"])
	assert.Equal(t, "completed", resumedState.Metadata["B"])
	assert.Equal(t, "approved", resumedState.Metadata["B_resume_value"])
	assert.Equal(t, "C", resumedState.Metadata["C"])
}

func TestFlow_ResumeWithValue_NilResumeValue(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)
	node := &countBasedInterruptNode{name: "special", payload: "need-resume"}

	f, err := NewFlowBuilder(log).
		SetName("resume_with_value_nil").
		SetCheckpointer(cp).
		AddNode(node).
		AddEdge(edge.Edge{From: StartNode, To: node.Name()}).
		AddEdge(edge.Edge{From: node.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	interruptedState, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, execErr)

	resumedState, resumeErr := f.ResumeWithValue(context.Background(), interruptedState.GetThreadID(), nil, nil)
	require.NoError(t, resumeErr)
	assert.Equal(t, "completed", resumedState.Metadata[node.Name()])
	resumeValue, ok := resumedState.Metadata[node.Name()+"_resume_value"]
	require.True(t, ok)
	assert.Nil(t, resumeValue)
}

// ========================================================================
// E2E Integration Tests — Human-in-the-Loop Scenarios
// ========================================================================

type routingInterruptNode struct {
	name      string
	payload   interface{}
	callCount int
	mu        sync.Mutex
}

func (n *routingInterruptNode) Name() string { return n.name }
func (n *routingInterruptNode) Run(ctx context.Context, s *state.State, streamFunc flowcontract.StreamFunc) error {
	if s.Metadata == nil {
		s.Metadata = make(map[string]interface{})
	}

	n.mu.Lock()
	n.callCount++
	count := n.callCount
	n.mu.Unlock()

	s.Metadata[n.name+"_calls"] = count

	if resumeVal := s.GetResumeValue(); resumeVal != nil {
		s.Metadata["route"] = resumeVal.(string)
		s.Metadata[n.name] = "decided"
		return nil
	}

	s.Metadata[n.name] = "pending"
	return flowcontract.Interrupt(n.payload)
}

func TestInterrupt_E2E_ToolApproval(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	var callMu sync.Mutex
	calls := make([]string, 0)

	chatNode := &trackedNode{name: "chat", value: "processed", calls: &calls, callMu: &callMu}
	toolPayload := map[string]interface{}{"tool": "delete_file", "args": "important.txt"}
	toolNode := &conditionalInterruptNode{name: "tool", payload: toolPayload}

	f, err := NewFlowBuilder(log).
		SetName("interrupt_e2e_tool_approval").
		SetCheckpointer(cp).
		AddNode(chatNode).
		AddNode(toolNode).
		AddEdge(edge.Edge{From: StartNode, To: chatNode.Name()}).
		AddEdge(edge.Edge{From: chatNode.Name(), To: toolNode.Name()}).
		AddEdge(edge.Edge{From: toolNode.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	interruptedState, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, execErr)
	interruptErr, ok := flowcontract.IsInterrupt(execErr)
	require.True(t, ok)
	assert.Equal(t, toolPayload, interruptErr.Payload)

	callMu.Lock()
	chatCalls := 0
	for _, call := range calls {
		if call == "chat" {
			chatCalls++
		}
	}
	callMu.Unlock()
	assert.Equal(t, 1, chatCalls)

	finalState, resumeErr := f.ResumeWithValue(context.Background(), interruptedState.GetThreadID(), "approved", nil)
	require.NoError(t, resumeErr)

	assert.Equal(t, "processed", finalState.Metadata["chat"])
	assert.Equal(t, "completed", finalState.Metadata["tool"])
	assert.Equal(t, "approved", finalState.Metadata["tool_resume_value"])

	toolNode.mu.Lock()
	assert.Equal(t, 2, toolNode.callCount)
	toolNode.mu.Unlock()
}

func TestInterrupt_E2E_MultiNodeFlow(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	nodeA := &metadataNode{name: "A"}
	nodeB := &metadataNode{name: "B"}
	nodeC := &conditionalInterruptNode{name: "C", payload: "need-go-ahead"}
	nodeD := &metadataNode{name: "D"}

	f, err := NewFlowBuilder(log).
		SetName("interrupt_e2e_multi_node").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddNode(nodeC).
		AddNode(nodeD).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: nodeC.Name()}).
		AddEdge(edge.Edge{From: nodeC.Name(), To: nodeD.Name()}).
		AddEdge(edge.Edge{From: nodeD.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	interruptedState, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, execErr)
	_, ok := flowcontract.IsInterrupt(execErr)
	require.True(t, ok)

	assert.Equal(t, "A", interruptedState.Metadata["A"])
	assert.Equal(t, "B", interruptedState.Metadata["B"])
	assert.Equal(t, "interrupted", interruptedState.Metadata["C"])
	_, hasD := interruptedState.Metadata["D"]
	assert.False(t, hasD)

	resumedState, resumeErr := f.ResumeWithValue(context.Background(), interruptedState.GetThreadID(), "go_ahead", nil)
	require.NoError(t, resumeErr)

	assert.Equal(t, "A", resumedState.Metadata["A"])
	assert.Equal(t, "B", resumedState.Metadata["B"])
	assert.Equal(t, "completed", resumedState.Metadata["C"])
	assert.Equal(t, "go_ahead", resumedState.Metadata["C_resume_value"])
	assert.Equal(t, "D", resumedState.Metadata["D"])
}

func TestInterrupt_E2E_ConditionalEdgeAfterResume(t *testing.T) {
	testCases := []struct {
		name           string
		resumeValue    string
		expectedBranch string
		notExpected    string
	}{
		{name: "approve_path", resumeValue: "approve_path", expectedBranch: "approve_path", notExpected: "reject_path"},
		{name: "reject_path", resumeValue: "reject_path", expectedBranch: "reject_path", notExpected: "approve_path"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cp := checkpointer.NewInMemoryCheckpointer()
			log := newTestLogger(t)

			decisionNode := &routingInterruptNode{name: "decision", payload: "choose"}
			approveNode := &metadataNode{name: "approve_path"}
			rejectNode := &metadataNode{name: "reject_path"}

			conditionFunc := func(ctx context.Context, s state.State) (string, error) {
				if route, ok := s.Metadata["route"]; ok {
					return route.(string), nil
				}
				return "", nil
			}

			f, err := NewFlowBuilder(log).
				SetName("interrupt_e2e_conditional_after_resume_" + tc.name).
				SetCheckpointer(cp).
				AddNode(decisionNode).
				AddNode(approveNode).
				AddNode(rejectNode).
				AddEdge(edge.Edge{From: StartNode, To: decisionNode.Name()}).
				AddEdge(edge.Edge{
					From:          decisionNode.Name(),
					ConditionalTo: []string{approveNode.Name(), rejectNode.Name()},
					ConditionFunc: conditionFunc,
				}).
				AddEdge(edge.Edge{From: approveNode.Name(), To: EndNode}).
				AddEdge(edge.Edge{From: rejectNode.Name(), To: EndNode}).
				Compile()
			require.NoError(t, err)

			interruptedState, execErr := f.Exec(context.Background(), state.State{}, nil)
			require.Error(t, execErr)
			interruptErr, ok := flowcontract.IsInterrupt(execErr)
			require.True(t, ok)
			assert.Equal(t, "choose", interruptErr.Payload)

			finalState, resumeErr := f.ResumeWithValue(context.Background(), interruptedState.GetThreadID(), tc.resumeValue, nil)
			require.NoError(t, resumeErr)

			assert.Equal(t, "decided", finalState.Metadata["decision"])
			assert.Equal(t, tc.resumeValue, finalState.Metadata["route"])
			assert.Equal(t, tc.expectedBranch, finalState.Metadata[tc.expectedBranch])
			_, hasUnexpectedBranch := finalState.Metadata[tc.notExpected]
			assert.False(t, hasUnexpectedBranch)
		})
	}
}

func TestInterrupt_E2E_CheckpointPersistence(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	nodeA := &metadataNode{name: "A"}
	nodeB := &conditionalInterruptNode{name: "B", payload: "pause"}
	nodeC := &metadataNode{name: "C"}

	f, err := NewFlowBuilder(log).
		SetName("interrupt_e2e_checkpoint_persistence").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddNode(nodeC).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: nodeC.Name()}).
		AddEdge(edge.Edge{From: nodeC.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	interruptedState, execErr := f.Exec(context.Background(), state.State{}, nil)
	require.Error(t, execErr)
	_, ok := flowcontract.IsInterrupt(execErr)
	require.True(t, ok)

	entriesBeforeResume, err := cp.List(context.Background(), interruptedState.GetThreadID())
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entriesBeforeResume), 3)
	assert.Equal(t, "input", entriesBeforeResume[0].Source)
	assert.Equal(t, "interrupt", entriesBeforeResume[len(entriesBeforeResume)-1].Source)

	foundALoopCheckpoint := false
	for _, entry := range entriesBeforeResume {
		if entry.Source != "loop" || entry.State == nil || entry.State.Metadata == nil {
			continue
		}
		if entry.State.Metadata["A"] == "A" {
			foundALoopCheckpoint = true
			break
		}
	}
	assert.True(t, foundALoopCheckpoint)

	resumedState, resumeErr := f.ResumeWithValue(context.Background(), interruptedState.GetThreadID(), "continue", nil)
	require.NoError(t, resumeErr)
	assert.Equal(t, "A", resumedState.Metadata["A"])
	assert.Equal(t, "completed", resumedState.Metadata["B"])
	assert.Equal(t, "C", resumedState.Metadata["C"])

	entriesAfterResume, err := cp.List(context.Background(), interruptedState.GetThreadID())
	require.NoError(t, err)

	interruptEntry := entriesBeforeResume[len(entriesBeforeResume)-1]
	foundResumeCheckpoint := false
	for _, entry := range entriesAfterResume {
		if entry.Source == "resume" {
			foundResumeCheckpoint = true
			assert.Equal(t, interruptEntry.ID, entry.ParentID)
			break
		}
	}
	assert.True(t, foundResumeCheckpoint)

	latest, err := cp.GetLatest(context.Background(), interruptedState.GetThreadID())
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.NotNil(t, latest.State)
	assert.Equal(t, "A", latest.State.Metadata["A"])
	assert.Equal(t, "completed", latest.State.Metadata["B"])
	assert.Equal(t, "C", latest.State.Metadata["C"])
}
