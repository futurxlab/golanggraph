package checkpointer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/futurxlab/golanggraph/state"
)

// ========================================================================
// Edge Case Tests
// ========================================================================

func TestInMemory_NonExistentCheckpointID(t *testing.T) {
	testNonExistentCheckpointID(t, NewInMemoryCheckpointer())
}

func TestRedis_NonExistentCheckpointID(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testNonExistentCheckpointID(t, NewRedisCheckpointer(client))
}

func testNonExistentCheckpointID(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	_, err := cp.GetByID(ctx, threadID, "does-not-exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint not found")
}

// --- Non-existent thread ---

func TestInMemory_NonExistentThread(t *testing.T) {
	testNonExistentThread(t, NewInMemoryCheckpointer())
}

func TestRedis_NonExistentThread(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testNonExistentThread(t, NewRedisCheckpointer(client))
}

func testNonExistentThread(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()

	_, err := cp.GetLatest(ctx, "nonexistent-thread-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no checkpoints found")

	_, err = cp.List(ctx, "nonexistent-thread-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no checkpoints found")
}

// --- Unicode / special characters in IDs ---

func TestInMemory_UnicodeInIDs(t *testing.T) {
	testUnicodeInIDs(t, NewInMemoryCheckpointer())
}

func TestRedis_UnicodeInIDs(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testUnicodeInIDs(t, NewRedisCheckpointer(client))
}

func testUnicodeInIDs(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()

	// Test with Unicode threadID
	threadID := "test:线程:🚀:" + uniqueThreadID(t)
	entry := newCheckpointEntry()

	err := cp.Save(ctx, threadID, entry)
	require.NoError(t, err)

	got, err := cp.GetByID(ctx, threadID, entry.ID)
	require.NoError(t, err)
	assert.Equal(t, entry.ID, got.ID)
}

// --- Very long threadID ---

func TestInMemory_VeryLongThreadID(t *testing.T) {
	testVeryLongThreadID(t, NewInMemoryCheckpointer())
}

func TestRedis_VeryLongThreadID(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testVeryLongThreadID(t, NewRedisCheckpointer(client))
}

func testVeryLongThreadID(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()

	// 1KB threadID
	longID := "test:" + strings.Repeat("a", 1024)
	entry := newCheckpointEntry()

	err := cp.Save(ctx, longID, entry)
	require.NoError(t, err)

	got, err := cp.GetByID(ctx, longID, entry.ID)
	require.NoError(t, err)
	assert.Equal(t, entry.ID, got.ID)
}

// --- Nil State ---

func TestInMemory_NilState(t *testing.T) {
	testNilState(t, NewInMemoryCheckpointer())
}

func TestRedis_NilState(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testNilState(t, NewRedisCheckpointer(client))
}

func testNilState(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	entry := newCheckpointEntry(withState(nil))
	err := cp.Save(ctx, threadID, entry)

	if err != nil {
		// If Save returns error for nil state, that's acceptable behavior
		t.Logf("Save with nil state returned error (acceptable): %v", err)
		return
	}

	// If saved successfully, verify we can retrieve it
	got, err := cp.GetByID(ctx, threadID, entry.ID)
	require.NoError(t, err)
	assert.Nil(t, got.State)
}

// --- Empty Metadata ---

func TestInMemory_EmptyMetadata(t *testing.T) {
	testEmptyMetadata(t, NewInMemoryCheckpointer())
}

func TestRedis_EmptyMetadata(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testEmptyMetadata(t, NewRedisCheckpointer(client))
}

func testEmptyMetadata(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	entry := newCheckpointEntry(withMetadata(nil))
	err := cp.Save(ctx, threadID, entry)
	require.NoError(t, err)

	got, err := cp.GetByID(ctx, threadID, entry.ID)
	require.NoError(t, err)
	assert.Equal(t, entry.ID, got.ID)
}

// --- Timestamp validity ---

func TestInMemory_TimestampValidity(t *testing.T) {
	testTimestampValidity(t, NewInMemoryCheckpointer())
}

func TestRedis_TimestampValidity(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testTimestampValidity(t, NewRedisCheckpointer(client))
}

func testTimestampValidity(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	before := time.Now().Add(-time.Second)
	entry := newCheckpointEntry()
	after := time.Now().Add(time.Second)

	require.NoError(t, cp.Save(ctx, threadID, entry))

	got, err := cp.GetByID(ctx, threadID, entry.ID)
	require.NoError(t, err)

	assert.False(t, got.Timestamp.IsZero(), "timestamp should not be zero")
	assert.True(t, got.Timestamp.After(before), "timestamp should be after test start")
	assert.True(t, got.Timestamp.Before(after), "timestamp should be before test end")
}

// --- PendingWrite with empty node name ---

func TestInMemory_PendingWriteEmptyNodeName(t *testing.T) {
	testPendingWriteEmptyNodeName(t, NewInMemoryCheckpointer())
}

func TestRedis_PendingWriteEmptyNodeName(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testPendingWriteEmptyNodeName(t, NewRedisCheckpointer(client))
}

func testPendingWriteEmptyNodeName(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	entry := newCheckpointEntry()
	require.NoError(t, cp.Save(ctx, threadID, entry))

	write := PendingWrite{
		TaskID:   "task-empty",
		NodeName: "",
		State:    &state.State{Metadata: map[string]interface{}{"key": "val"}},
	}

	err := cp.SaveWrite(ctx, threadID, entry.ID, write)
	require.NoError(t, err)

	writes, err := cp.GetPendingWrites(ctx, threadID, entry.ID)
	require.NoError(t, err)
	require.Len(t, writes, 1)
	assert.Equal(t, "", writes[0].NodeName)
}

// --- Multiple PendingWrites for same checkpoint ---

func TestInMemory_MultiplePendingWritesSameCheckpoint(t *testing.T) {
	testMultiplePendingWritesSameCheckpoint(t, NewInMemoryCheckpointer())
}

func TestRedis_MultiplePendingWritesSameCheckpoint(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testMultiplePendingWritesSameCheckpoint(t, NewRedisCheckpointer(client))
}

func testMultiplePendingWritesSameCheckpoint(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	entry := newCheckpointEntry()
	require.NoError(t, cp.Save(ctx, threadID, entry))

	// Save 10 pending writes for the same checkpoint
	for i := 0; i < 10; i++ {
		write := newPendingWrite(
			"task-"+strings.Repeat("x", i+1),
			"node-"+strings.Repeat("y", i+1),
		)
		require.NoError(t, cp.SaveWrite(ctx, threadID, entry.ID, write))
	}

	writes, err := cp.GetPendingWrites(ctx, threadID, entry.ID)
	require.NoError(t, err)
	assert.Len(t, writes, 10)
}

// --- PendingWrites isolation across checkpoints ---

func TestInMemory_PendingWritesIsolationAcrossCheckpoints(t *testing.T) {
	testPendingWritesIsolationAcrossCheckpoints(t, NewInMemoryCheckpointer())
}

func TestRedis_PendingWritesIsolationAcrossCheckpoints(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testPendingWritesIsolationAcrossCheckpoints(t, NewRedisCheckpointer(client))
}

func testPendingWritesIsolationAcrossCheckpoints(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	entry1 := newCheckpointEntry(withStep(0))
	entry2 := newCheckpointEntry(withStep(1), withParentID(entry1.ID))
	require.NoError(t, cp.Save(ctx, threadID, entry1))
	require.NoError(t, cp.Save(ctx, threadID, entry2))

	// Writes to entry1
	require.NoError(t, cp.SaveWrite(ctx, threadID, entry1.ID, newPendingWrite("t1", "nodeA")))
	require.NoError(t, cp.SaveWrite(ctx, threadID, entry1.ID, newPendingWrite("t2", "nodeB")))

	// Writes to entry2
	require.NoError(t, cp.SaveWrite(ctx, threadID, entry2.ID, newPendingWrite("t3", "nodeC")))

	writes1, err := cp.GetPendingWrites(ctx, threadID, entry1.ID)
	require.NoError(t, err)
	assert.Len(t, writes1, 2, "entry1 should have 2 pending writes")

	writes2, err := cp.GetPendingWrites(ctx, threadID, entry2.ID)
	require.NoError(t, err)
	assert.Len(t, writes2, 1, "entry2 should have 1 pending write")
}
