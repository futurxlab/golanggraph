package checkpointer

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================================================
// Redis-specific Tests
// ========================================================================

// --- Key format verification ---

func TestRedis_KeyFormat(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer client.Close()

	// Verify key helper functions produce expected format
	threadID := "my-thread"
	cpID := "my-checkpoint"

	assert.Equal(t, "checkpoint:my-thread:entries", getEntriesKey(threadID))
	assert.Equal(t, "checkpoint:my-thread:entry:my-checkpoint", getEntryKey(threadID, cpID))
	assert.Equal(t, "checkpoint:my-thread:writes:my-checkpoint", getWritesKey(threadID, cpID))
}

// --- Connection failure ---

func TestRedis_ConnectionFailure(t *testing.T) {
	// Create a client pointing to a non-existent Redis
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:59999", // unlikely to be running
	})
	defer client.Close()

	cp := NewRedisCheckpointer(client)
	ctx := context.Background()
	threadID := "conn-fail-thread"

	entry := newCheckpointEntry()
	err := cp.Save(ctx, threadID, entry)
	require.Error(t, err, "Save to unreachable Redis should fail")

	_, err = cp.GetLatest(ctx, threadID)
	require.Error(t, err, "GetLatest from unreachable Redis should fail")

	_, err = cp.GetByID(ctx, threadID, "some-id")
	require.Error(t, err, "GetByID from unreachable Redis should fail")

	_, err = cp.List(ctx, threadID)
	require.Error(t, err, "List from unreachable Redis should fail")

	write := newPendingWrite("task-x", "nodeX")
	err = cp.SaveWrite(ctx, threadID, "some-cp", write)
	require.Error(t, err, "SaveWrite to unreachable Redis should fail")

	_, err = cp.GetPendingWrites(ctx, threadID, "some-cp")
	require.Error(t, err, "GetPendingWrites from unreachable Redis should fail")
}

// --- Data corruption (invalid JSON in Redis) ---

func TestRedis_DataCorruption_InvalidJSON(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	threadID := uniqueThreadID(t) + ":corruption"
	cpID := "corrupt-checkpoint"

	// Manually write invalid JSON into the entry key
	entryKey := getEntryKey(threadID, cpID)
	entriesKey := getEntriesKey(threadID)

	client.Set(ctx, entryKey, "this-is-not-valid-json{{{", 0)
	client.RPush(ctx, entriesKey, cpID)

	defer func() {
		client.Del(ctx, entryKey)
		client.Del(ctx, entriesKey)
	}()

	cp := NewRedisCheckpointer(client)

	// GetByID should fail with unmarshal error
	_, err := cp.GetByID(ctx, threadID, cpID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")

	// GetLatest should also fail (it calls GetByID internally)
	_, err = cp.GetLatest(ctx, threadID)
	require.Error(t, err)

	// List should also fail
	_, err = cp.List(ctx, threadID)
	require.Error(t, err)
}

// --- PendingWrite corruption ---

func TestRedis_PendingWriteCorruption(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	threadID := uniqueThreadID(t) + ":pw-corruption"
	cpID := "pw-corrupt-cp"

	// Manually write invalid JSON into writes key
	writesKey := getWritesKey(threadID, cpID)
	client.RPush(ctx, writesKey, "not-json!!!")

	defer client.Del(ctx, writesKey)

	cp := NewRedisCheckpointer(client)

	_, err := cp.GetPendingWrites(ctx, threadID, cpID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// --- Pipeline atomicity: Save creates both entry key and list entry ---

func TestRedis_SavePipelineAtomicity(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	threadID := uniqueThreadID(t) + ":pipeline"
	cp := NewRedisCheckpointer(client)

	entry := newCheckpointEntry()
	require.NoError(t, cp.Save(ctx, threadID, entry))

	defer func() {
		client.Del(ctx, getEntriesKey(threadID))
		client.Del(ctx, getEntryKey(threadID, entry.ID))
	}()

	// Verify both the entry key and the entries list exist
	exists, err := client.Exists(ctx, getEntryKey(threadID, entry.ID)).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "entry key should exist")

	length, err := client.LLen(ctx, getEntriesKey(threadID)).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), length, "entries list should have 1 item")

	// Verify the list contains the correct ID
	ids, err := client.LRange(ctx, getEntriesKey(threadID), 0, -1).Result()
	require.NoError(t, err)
	assert.Equal(t, []string{entry.ID}, ids)
}

// --- Multiple saves to Redis and list consistency ---

func TestRedis_ListOrderConsistency(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	threadID := uniqueThreadID(t) + ":list-order"
	cp := NewRedisCheckpointer(client)

	chain := buildCheckpointChain(5)
	for _, e := range chain {
		require.NoError(t, cp.Save(ctx, threadID, e))
	}

	defer func() {
		client.Del(ctx, getEntriesKey(threadID))
		for _, e := range chain {
			client.Del(ctx, getEntryKey(threadID, e.ID))
		}
	}()

	// List should return entries in insertion order
	entries, err := cp.List(ctx, threadID)
	require.NoError(t, err)
	require.Len(t, entries, 5)

	for i, e := range entries {
		assert.Equal(t, chain[i].ID, e.ID, "entry[%d] ID mismatch", i)
		assert.Equal(t, chain[i].Step, e.Step, "entry[%d] Step mismatch", i)
	}
}
