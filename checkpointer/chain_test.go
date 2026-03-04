package checkpointer

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================================================
// Checkpoint Chain (ParentID linkage) Tests
// ========================================================================

func TestInMemory_ParentIDLinkage(t *testing.T) {
	testParentIDLinkage(t, NewInMemoryCheckpointer())
}

func TestRedis_ParentIDLinkage(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testParentIDLinkage(t, NewRedisCheckpointer(client))
}

func testParentIDLinkage(t *testing.T, cp interface {
	Save(ctx context.Context, threadID string, entry *CheckpointEntry) error
	GetByID(ctx context.Context, threadID string, checkpointID string) (*CheckpointEntry, error)
	List(ctx context.Context, threadID string) ([]*CheckpointEntry, error)
}) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	chain := buildCheckpointChain(5)
	for _, e := range chain {
		require.NoError(t, cp.Save(ctx, threadID, e))
	}

	// Verify: each entry's ParentID points to the previous entry
	for i := 1; i < len(chain); i++ {
		got, err := cp.GetByID(ctx, threadID, chain[i].ID)
		require.NoError(t, err)
		assert.Equal(t, chain[i-1].ID, got.ParentID,
			"entry[%d].ParentID should point to entry[%d]", i, i-1)
	}

	// Root entry has no parent
	root, err := cp.GetByID(ctx, threadID, chain[0].ID)
	require.NoError(t, err)
	assert.Empty(t, root.ParentID, "root entry should have empty ParentID")
}

func TestInMemory_ChainTraversal(t *testing.T) {
	testChainTraversal(t, NewInMemoryCheckpointer())
}

func TestRedis_ChainTraversal(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testChainTraversal(t, NewRedisCheckpointer(client))
}

func testChainTraversal(t *testing.T, cp interface {
	Save(ctx context.Context, threadID string, entry *CheckpointEntry) error
	GetByID(ctx context.Context, threadID string, checkpointID string) (*CheckpointEntry, error)
	GetLatest(ctx context.Context, threadID string) (*CheckpointEntry, error)
}) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	chain := buildCheckpointChain(4)
	for _, e := range chain {
		require.NoError(t, cp.Save(ctx, threadID, e))
	}

	// Start from latest, walk back to root via ParentID
	latest, err := cp.GetLatest(ctx, threadID)
	require.NoError(t, err)

	visited := make([]string, 0)
	current := latest
	for current != nil {
		visited = append(visited, current.ID)
		if current.ParentID == "" {
			break
		}
		current, err = cp.GetByID(ctx, threadID, current.ParentID)
		require.NoError(t, err)
	}

	// Should have traversed all 4 entries
	assert.Len(t, visited, 4)

	// Last visited should be the root
	assert.Equal(t, chain[0].ID, visited[len(visited)-1])

	// First visited should be the latest
	assert.Equal(t, chain[3].ID, visited[0])
}

func TestInMemory_ChainStepOrder(t *testing.T) {
	testChainStepOrder(t, NewInMemoryCheckpointer())
}

func TestRedis_ChainStepOrder(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testChainStepOrder(t, NewRedisCheckpointer(client))
}

func testChainStepOrder(t *testing.T, cp interface {
	Save(ctx context.Context, threadID string, entry *CheckpointEntry) error
	List(ctx context.Context, threadID string) ([]*CheckpointEntry, error)
}) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	chain := buildCheckpointChain(6)
	for _, e := range chain {
		require.NoError(t, cp.Save(ctx, threadID, e))
	}

	listed, err := cp.List(ctx, threadID)
	require.NoError(t, err)

	// Verify step numbers are monotonically increasing
	for i := 1; i < len(listed); i++ {
		assert.Greater(t, listed[i].Step, listed[i-1].Step,
			"steps should be increasing: step[%d]=%d, step[%d]=%d",
			i-1, listed[i-1].Step, i, listed[i].Step)
	}
}

// ========================================================================
// Redis helper
// ========================================================================

func redisClientOrSkip(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available at localhost:6379: %v", err)
	}
	cleanup := func() {
		// Flush only test-related keys
		ctx := context.Background()
		keys, _ := client.Keys(ctx, "checkpoint:test:*").Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
		client.Close()
	}
	return client, cleanup
}
