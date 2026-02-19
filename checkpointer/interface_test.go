package checkpointer

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// ========================================================================
// Contract Suite: shared test logic for any Checkpointer implementation
// ========================================================================

type CheckpointerContractSuite struct {
	suite.Suite
	cp       testCheckpointer
	threadID string
}

func (s *CheckpointerContractSuite) SetupTest() {
	s.threadID = uniqueThreadID(s.T())
}

// --- Save & GetByID ---

func (s *CheckpointerContractSuite) TestSaveAndGetByID() {
	ctx := context.Background()
	entry := newCheckpointEntry(withStep(0), withSource("input"))

	err := s.cp.Save(ctx, s.threadID, entry)
	s.Require().NoError(err)

	got, err := s.cp.GetByID(ctx, s.threadID, entry.ID)
	s.Require().NoError(err)
	s.Equal(entry.ID, got.ID)
	s.Equal(entry.Step, got.Step)
	s.Equal(entry.Source, got.Source)
	s.Equal(entry.ParentID, got.ParentID)
}

// --- GetLatest ---

func (s *CheckpointerContractSuite) TestGetLatest() {
	ctx := context.Background()

	entry1 := newCheckpointEntry(withStep(0), withSource("input"))
	entry2 := newCheckpointEntry(withStep(1), withSource("loop"), withParentID(entry1.ID))

	s.Require().NoError(s.cp.Save(ctx, s.threadID, entry1))
	s.Require().NoError(s.cp.Save(ctx, s.threadID, entry2))

	got, err := s.cp.GetLatest(ctx, s.threadID)
	s.Require().NoError(err)
	s.Equal(entry2.ID, got.ID)
	s.Equal(entry2.Step, got.Step)
}

func (s *CheckpointerContractSuite) TestGetLatest_NoCheckpoints() {
	ctx := context.Background()
	_, err := s.cp.GetLatest(ctx, s.threadID)
	s.Error(err)
	s.Contains(err.Error(), "no checkpoints found")
}

// --- List ---

func (s *CheckpointerContractSuite) TestList() {
	ctx := context.Background()

	entries := buildCheckpointChain(5)
	for _, e := range entries {
		s.Require().NoError(s.cp.Save(ctx, s.threadID, e))
	}

	got, err := s.cp.List(ctx, s.threadID)
	s.Require().NoError(err)
	s.Len(got, 5)

	// verify order matches insertion order
	for i, e := range got {
		s.Equal(entries[i].ID, e.ID)
		s.Equal(entries[i].Step, e.Step)
	}
}

func (s *CheckpointerContractSuite) TestList_NoCheckpoints() {
	ctx := context.Background()
	_, err := s.cp.List(ctx, s.threadID)
	s.Error(err)
	s.Contains(err.Error(), "no checkpoints found")
}

// --- SaveWrite & GetPendingWrites ---

func (s *CheckpointerContractSuite) TestSaveWriteAndGetPendingWrites() {
	ctx := context.Background()

	entry := newCheckpointEntry()
	s.Require().NoError(s.cp.Save(ctx, s.threadID, entry))

	write1 := newPendingWrite("task-1", "nodeA")
	write2 := newPendingWrite("task-2", "nodeB")

	s.Require().NoError(s.cp.SaveWrite(ctx, s.threadID, entry.ID, write1))
	s.Require().NoError(s.cp.SaveWrite(ctx, s.threadID, entry.ID, write2))

	writes, err := s.cp.GetPendingWrites(ctx, s.threadID, entry.ID)
	s.Require().NoError(err)
	s.Len(writes, 2)

	s.Equal("task-1", writes[0].TaskID)
	s.Equal("nodeA", writes[0].NodeName)
	s.Equal("task-2", writes[1].TaskID)
	s.Equal("nodeB", writes[1].NodeName)
}

func (s *CheckpointerContractSuite) TestGetPendingWrites_NoneExist() {
	ctx := context.Background()

	writes, err := s.cp.GetPendingWrites(ctx, s.threadID, "nonexistent-cp")
	s.NoError(err)
	s.Nil(writes)
}

// --- GetByID not found ---

func (s *CheckpointerContractSuite) TestGetByID_NotFound() {
	ctx := context.Background()
	_, err := s.cp.GetByID(ctx, s.threadID, "nonexistent-id")
	s.Error(err)
	s.Contains(err.Error(), "checkpoint not found")
}

// --- Multiple threads isolation ---

func (s *CheckpointerContractSuite) TestMultipleThreadsIsolation() {
	ctx := context.Background()

	threadA := uniqueThreadID(s.T()) + ":A"
	threadB := uniqueThreadID(s.T()) + ":B"

	entryA := newCheckpointEntry(withStep(0))
	entryB := newCheckpointEntry(withStep(0))

	s.Require().NoError(s.cp.Save(ctx, threadA, entryA))
	s.Require().NoError(s.cp.Save(ctx, threadB, entryB))

	gotA, err := s.cp.GetLatest(ctx, threadA)
	s.Require().NoError(err)
	s.Equal(entryA.ID, gotA.ID)

	gotB, err := s.cp.GetLatest(ctx, threadB)
	s.Require().NoError(err)
	s.Equal(entryB.ID, gotB.ID)

	// thread A should not see thread B's checkpoints
	listA, err := s.cp.List(ctx, threadA)
	s.Require().NoError(err)
	s.Len(listA, 1)
	s.Equal(entryA.ID, listA[0].ID)
}

// --- Save multiple and GetByID any ---

func (s *CheckpointerContractSuite) TestSaveMultipleAndGetByIDAny() {
	ctx := context.Background()

	entries := buildCheckpointChain(3)
	for _, e := range entries {
		s.Require().NoError(s.cp.Save(ctx, s.threadID, e))
	}

	// should be able to get any checkpoint by ID
	for _, expected := range entries {
		got, err := s.cp.GetByID(ctx, s.threadID, expected.ID)
		s.Require().NoError(err)
		s.Equal(expected.ID, got.ID)
		s.Equal(expected.Step, got.Step)
	}
}

// --- Source field preservation ---

func (s *CheckpointerContractSuite) TestSourceFieldPreservation() {
	ctx := context.Background()

	sources := []string{"input", "loop", "resume"}
	for _, src := range sources {
		entry := newCheckpointEntry(withSource(src))
		s.Require().NoError(s.cp.Save(ctx, s.threadID, entry))

		got, err := s.cp.GetByID(ctx, s.threadID, entry.ID)
		s.Require().NoError(err)
		s.Equal(src, got.Source, "source field should be preserved for: %s", src)
	}
}

// --- Metadata field preservation ---

func (s *CheckpointerContractSuite) TestMetadataFieldPreservation() {
	ctx := context.Background()

	meta := map[string]string{"key1": "value1", "key2": "value2"}
	entry := newCheckpointEntry(withMetadata(meta))

	s.Require().NoError(s.cp.Save(ctx, s.threadID, entry))

	got, err := s.cp.GetByID(ctx, s.threadID, entry.ID)
	s.Require().NoError(err)
	s.Equal(meta, got.Metadata)
}

// ========================================================================
// InMemory Contract Suite
// ========================================================================

type InMemoryContractSuite struct {
	CheckpointerContractSuite
}

func (s *InMemoryContractSuite) SetupTest() {
	s.cp = NewInMemoryCheckpointer()
	s.CheckpointerContractSuite.SetupTest()
}

func TestInMemoryContract(t *testing.T) {
	suite.Run(t, new(InMemoryContractSuite))
}

// ========================================================================
// Redis Contract Suite
// ========================================================================

type RedisContractSuite struct {
	CheckpointerContractSuite
	client *redis.Client
}

func (s *RedisContractSuite) SetupSuite() {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		s.T().Skipf("Redis not available at localhost:6379: %v", err)
	}
	s.client = client
}

func (s *RedisContractSuite) SetupTest() {
	s.cp = NewRedisCheckpointer(s.client)
	s.CheckpointerContractSuite.SetupTest()
}

func (s *RedisContractSuite) TearDownTest() {
	// Clean up test keys for this thread
	ctx := context.Background()
	s.cleanupThread(ctx, s.threadID)
}

func (s *RedisContractSuite) TearDownSuite() {
	if s.client != nil {
		s.client.Close()
	}
}

func (s *RedisContractSuite) cleanupThread(ctx context.Context, threadID string) {
	// Delete entries list
	s.client.Del(ctx, getEntriesKey(threadID))

	// Find and delete all entry keys
	entryKeys, _ := s.client.Keys(ctx, "checkpoint:"+threadID+":entry:*").Result()
	for _, key := range entryKeys {
		s.client.Del(ctx, key)
	}

	// Find and delete all writes keys
	writeKeys, _ := s.client.Keys(ctx, "checkpoint:"+threadID+":writes:*").Result()
	for _, key := range writeKeys {
		s.client.Del(ctx, key)
	}
}

func TestRedisContract(t *testing.T) {
	suite.Run(t, new(RedisContractSuite))
}

// ========================================================================
// Standalone tests (non-suite) for additional coverage
// ========================================================================

func TestInMemory_SaveAndGetByID(t *testing.T) {
	cp := NewInMemoryCheckpointer()
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	entry := newCheckpointEntry()
	require.NoError(t, cp.Save(ctx, threadID, entry))

	got, err := cp.GetByID(ctx, threadID, entry.ID)
	require.NoError(t, err)
	assert.Equal(t, entry.ID, got.ID)
}

func TestRedis_SaveAndGetByID(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer client.Close()

	cp := NewRedisCheckpointer(client)
	ctx := context.Background()
	threadID := uniqueThreadID(t)

	entry := newCheckpointEntry()
	require.NoError(t, cp.Save(ctx, threadID, entry))

	got, err := cp.GetByID(ctx, threadID, entry.ID)
	require.NoError(t, err)
	assert.Equal(t, entry.ID, got.ID)

	// cleanup
	client.Del(ctx, getEntriesKey(threadID))
	client.Del(ctx, getEntryKey(threadID, entry.ID))
}
