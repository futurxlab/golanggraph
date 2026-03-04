package checkpointer

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================================================
// Concurrent Write Tests
// ========================================================================

func TestInMemory_ConcurrentWrites_SameThread(t *testing.T) {
	cp := NewInMemoryCheckpointer()
	testConcurrentWritesSameThread(t, cp)
}

func TestRedis_ConcurrentWrites_SameThread(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testConcurrentWritesSameThread(t, NewRedisCheckpointer(client))
}

func testConcurrentWritesSameThread(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)
	n := 20

	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(step int) {
			defer wg.Done()
			entry := newCheckpointEntry(withStep(step))
			err := cp.Save(ctx, threadID, entry)
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// All entries should be saved
	entries, err := cp.List(ctx, threadID)
	require.NoError(t, err)
	assert.Len(t, entries, n)
}

// ========================================================================
// Concurrent Writes to Different Threads (isolation)
// ========================================================================

func TestInMemory_ConcurrentWrites_DifferentThreads(t *testing.T) {
	cp := NewInMemoryCheckpointer()
	testConcurrentWritesDifferentThreads(t, cp)
}

func TestRedis_ConcurrentWrites_DifferentThreads(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testConcurrentWritesDifferentThreads(t, NewRedisCheckpointer(client))
}

func testConcurrentWritesDifferentThreads(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	numThreads := 5
	entriesPerThread := 4

	threadIDs := make([]string, numThreads)
	for i := 0; i < numThreads; i++ {
		threadIDs[i] = fmt.Sprintf("test:%s:thread-%d:%s", t.Name(), i, uuid.New().String())
	}

	var wg sync.WaitGroup
	wg.Add(numThreads * entriesPerThread)

	for _, tid := range threadIDs {
		for j := 0; j < entriesPerThread; j++ {
			go func(threadID string, step int) {
				defer wg.Done()
				entry := newCheckpointEntry(withStep(step))
				err := cp.Save(ctx, threadID, entry)
				assert.NoError(t, err)
			}(tid, j)
		}
	}

	wg.Wait()

	// Each thread should have exactly entriesPerThread entries
	for _, tid := range threadIDs {
		entries, err := cp.List(ctx, tid)
		require.NoError(t, err)
		assert.Len(t, entries, entriesPerThread,
			"thread %s should have %d entries", tid, entriesPerThread)
	}
}

// ========================================================================
// Concurrent Read+Write
// ========================================================================

func TestInMemory_ConcurrentReadWrite(t *testing.T) {
	cp := NewInMemoryCheckpointer()
	testConcurrentReadWrite(t, cp)
}

func TestRedis_ConcurrentReadWrite(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testConcurrentReadWrite(t, NewRedisCheckpointer(client))
}

func testConcurrentReadWrite(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	// Seed an initial entry so reads don't always fail
	seed := newCheckpointEntry(withStep(0))
	require.NoError(t, cp.Save(ctx, threadID, seed))

	var wg sync.WaitGroup
	writes := 10
	reads := 10
	wg.Add(writes + reads)

	// Concurrent writers
	for i := 0; i < writes; i++ {
		go func(step int) {
			defer wg.Done()
			entry := newCheckpointEntry(withStep(step + 1))
			err := cp.Save(ctx, threadID, entry)
			assert.NoError(t, err)
		}(i)
	}

	// Concurrent readers
	for i := 0; i < reads; i++ {
		go func() {
			defer wg.Done()
			_, err := cp.GetLatest(ctx, threadID)
			// GetLatest should never fail since we seeded an entry
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	// Final state: should have 1 (seed) + writes entries
	entries, err := cp.List(ctx, threadID)
	require.NoError(t, err)
	assert.Len(t, entries, 1+writes)
}

// ========================================================================
// Concurrent SaveWrite
// ========================================================================

func TestInMemory_ConcurrentSaveWrite(t *testing.T) {
	cp := NewInMemoryCheckpointer()
	testConcurrentSaveWrite(t, cp)
}

func TestRedis_ConcurrentSaveWrite(t *testing.T) {
	client, cleanup := redisClientOrSkip(t)
	defer cleanup()
	testConcurrentSaveWrite(t, NewRedisCheckpointer(client))
}

func testConcurrentSaveWrite(t *testing.T, cp testCheckpointer) {
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	entry := newCheckpointEntry()
	require.NoError(t, cp.Save(ctx, threadID, entry))

	n := 15
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			write := newPendingWrite(
				fmt.Sprintf("task-%d", idx),
				fmt.Sprintf("node-%d", idx),
			)
			err := cp.SaveWrite(ctx, threadID, entry.ID, write)
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	writes, err := cp.GetPendingWrites(ctx, threadID, entry.ID)
	require.NoError(t, err)
	assert.Len(t, writes, n)
}

// ========================================================================
// Race condition test (designed to be run with go test -race)
// ========================================================================

func TestInMemory_RaceDetection(t *testing.T) {
	cp := NewInMemoryCheckpointer()
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	// Seed
	seed := newCheckpointEntry()
	require.NoError(t, cp.Save(ctx, threadID, seed))

	var wg sync.WaitGroup
	wg.Add(4)

	// Writer
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = cp.Save(ctx, threadID, newCheckpointEntry(withStep(i+1)))
		}
	}()

	// Reader: GetLatest
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_, _ = cp.GetLatest(ctx, threadID)
		}
	}()

	// Reader: List
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_, _ = cp.List(ctx, threadID)
		}
	}()

	// Writer: PendingWrites
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = cp.SaveWrite(ctx, threadID, seed.ID, newPendingWrite(
				fmt.Sprintf("race-task-%d", i), fmt.Sprintf("node-%d", i)))
		}
	}()

	wg.Wait()

	// Just verify no panic/data corruption
	entries, err := cp.List(ctx, threadID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 1)
}

func TestRedis_RaceDetection(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer func() {
		keys, _ := client.Keys(context.Background(), "checkpoint:test:*").Result()
		if len(keys) > 0 {
			client.Del(context.Background(), keys...)
		}
		client.Close()
	}()

	cp := NewRedisCheckpointer(client)
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	seed := newCheckpointEntry()
	require.NoError(t, cp.Save(ctx, threadID, seed))

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_ = cp.Save(ctx, threadID, newCheckpointEntry(withStep(i+1)))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_, _ = cp.GetLatest(ctx, threadID)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_, _ = cp.List(ctx, threadID)
		}
	}()

	wg.Wait()

	entries, err := cp.List(ctx, threadID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 1)
}
