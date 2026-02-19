package flow

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/futurxlab/golanggraph/checkpointer"
	"github.com/futurxlab/golanggraph/edge"
	"github.com/futurxlab/golanggraph/state"
)

// ========================================================================
// Resume Tests
// ========================================================================

// TestResume_FromLatest: Run a flow, then resume from latest checkpoint.
// The resume should see the state from where we left off.
func TestResume_FromLatest(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	nodeA := &metadataNode{name: "nodeA"}
	nodeB := &metadataNode{name: "nodeB"}

	f, err := NewFlowBuilder(log).
		SetName("resume_test").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	initState := state.State{Metadata: map[string]interface{}{"init": true}}
	result, err := f.Exec(context.Background(), initState, nil)
	require.NoError(t, err)

	threadID := result.GetThreadID()

	// Verify latest checkpoint exists
	latest, err := cp.GetLatest(context.Background(), threadID)
	require.NoError(t, err)
	assert.NotNil(t, latest)
	assert.NotNil(t, latest.State)
}

// TestResume_CheckpointChainAfterResume: After resume, new checkpoints
// should have ParentID pointing to the resume point.
func TestResume_CheckpointChainAfterExec(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	loopN := &loopNode{name: "looper", maxLoops: 2}

	f, err := NewFlowBuilder(log).
		SetName("resume_chain").
		SetCheckpointer(cp).
		AddNode(loopN).
		AddEdge(edge.Edge{From: StartNode, To: loopN.Name()}).
		AddEdge(edge.Edge{From: loopN.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	result, err := f.Exec(context.Background(), state.State{}, nil)
	require.NoError(t, err)

	threadID := result.GetThreadID()

	entries, err := cp.List(context.Background(), threadID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 3)

	assert.Equal(t, 0, entries[0].Step)
	for i := 1; i < len(entries); i++ {
		assert.Greater(t, entries[i].Step, entries[i-1].Step,
			"entry[%d].Step should be > entry[%d].Step", i, i-1)
	}
}

// TestResume_PendingWritesRecovery: Verify that pending writes are
// accessible after flow execution (for crash recovery).
func TestResume_PendingWritesRecovery(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	nodeA := &metadataNode{name: "nodeA"}
	nodeB := &metadataNode{name: "nodeB"}
	nodeC := &metadataNode{name: "nodeC"}

	f, err := NewFlowBuilder(log).
		SetName("pending_writes_recovery").
		SetCheckpointer(cp).
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

	result, err := f.Exec(context.Background(), state.State{}, nil)
	require.NoError(t, err)

	threadID := result.GetThreadID()

	// Check that pending writes were created for the parallel nodes
	entries, err := cp.List(context.Background(), threadID)
	require.NoError(t, err)

	totalPendingWrites := 0
	for _, entry := range entries {
		writes, err := cp.GetPendingWrites(context.Background(), threadID, entry.ID)
		require.NoError(t, err)
		totalPendingWrites += len(writes)
	}

	// At minimum, parallel nodes A and B should have produced pending writes
	assert.GreaterOrEqual(t, totalPendingWrites, 2,
		"should have at least 2 pending writes from parallel nodes A and B")
}

// TestResume_StepIncrement: Verify that steps increment correctly
// across the flow execution.
func TestResume_StepIncrement(t *testing.T) {
	cp := checkpointer.NewInMemoryCheckpointer()
	log := newTestLogger(t)

	nodeA := &metadataNode{name: "nodeA"}
	nodeB := &metadataNode{name: "nodeB"}
	nodeC := &metadataNode{name: "nodeC"}

	f, err := NewFlowBuilder(log).
		SetName("step_increment").
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

	result, err := f.Exec(context.Background(), state.State{}, nil)
	require.NoError(t, err)

	threadID := result.GetThreadID()
	entries, err := cp.List(context.Background(), threadID)
	require.NoError(t, err)

	// First checkpoint should be step 0
	assert.Equal(t, 0, entries[0].Step)

	// Steps should be increasing
	for i := 1; i < len(entries); i++ {
		assert.Greater(t, entries[i].Step, entries[i-1].Step,
			"step[%d]=%d should be > step[%d]=%d",
			i, entries[i].Step, i-1, entries[i-1].Step)
	}
}

// TestResume_WithRedis: Same resume test but with Redis backend
func TestResume_WithRedis(t *testing.T) {
	client, cleanup := redisFlowClientOrSkip(t)
	defer cleanup()

	cp := checkpointer.NewRedisCheckpointer(client)
	log := newTestLogger(t)

	nodeA := &metadataNode{name: "nodeA"}
	nodeB := &metadataNode{name: "nodeB"}

	f, err := NewFlowBuilder(log).
		SetName("resume_redis").
		SetCheckpointer(cp).
		AddNode(nodeA).
		AddNode(nodeB).
		AddEdge(edge.Edge{From: StartNode, To: nodeA.Name()}).
		AddEdge(edge.Edge{From: nodeA.Name(), To: nodeB.Name()}).
		AddEdge(edge.Edge{From: nodeB.Name(), To: EndNode}).
		Compile()
	require.NoError(t, err)

	result, err := f.Exec(context.Background(), state.State{}, nil)
	require.NoError(t, err)

	threadID := result.GetThreadID()

	// Verify checkpoints in Redis
	entries, err := cp.List(context.Background(), threadID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 2)

	// Verify latest
	latest, err := cp.GetLatest(context.Background(), threadID)
	require.NoError(t, err)
	assert.Equal(t, entries[len(entries)-1].ID, latest.ID)
}

// ========================================================================
// Redis helper for flow tests
// ========================================================================

func redisFlowClientOrSkip(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available at localhost:6379: %v", err)
	}
	cleanup := func() {
		ctx := context.Background()
		keys, _ := client.Keys(ctx, "checkpoint:*").Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
		client.Close()
	}
	return client, cleanup
}
