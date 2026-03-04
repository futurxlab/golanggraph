package checkpointer

import (
	"context"
	"fmt"
	"sync"
)

// InMemoryCheckpointer 实现了 Checkpointer 接口，使用内存存储检查点
type InMemoryCheckpointer struct {
	mu            sync.RWMutex
	checkpoints   map[string][]*CheckpointEntry
	pendingWrites map[string]map[string][]PendingWrite
}

func NewInMemoryCheckpointer() *InMemoryCheckpointer {
	return &InMemoryCheckpointer{
		checkpoints:   make(map[string][]*CheckpointEntry),
		pendingWrites: make(map[string]map[string][]PendingWrite),
	}
}

func (c *InMemoryCheckpointer) Save(ctx context.Context, threadID string, entry *CheckpointEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.checkpoints[threadID] = append(c.checkpoints[threadID], entry)
	return nil
}

func (c *InMemoryCheckpointer) SaveWrite(ctx context.Context, threadID string, checkpointID string, write PendingWrite) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pendingWrites[threadID] == nil {
		c.pendingWrites[threadID] = make(map[string][]PendingWrite)
	}
	c.pendingWrites[threadID][checkpointID] = append(c.pendingWrites[threadID][checkpointID], write)
	return nil
}

func (c *InMemoryCheckpointer) GetLatest(ctx context.Context, threadID string) (*CheckpointEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entries, exists := c.checkpoints[threadID]
	if !exists || len(entries) == 0 {
		return nil, fmt.Errorf("no checkpoints found for thread %s", threadID)
	}

	latest := entries[0]
	for _, e := range entries[1:] {
		if e.Step > latest.Step {
			latest = e
		}
	}
	return latest, nil
}

func (c *InMemoryCheckpointer) GetByID(ctx context.Context, threadID string, checkpointID string) (*CheckpointEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entries, exists := c.checkpoints[threadID]
	if !exists {
		return nil, fmt.Errorf("checkpoint not found for thread %s and ID %s", threadID, checkpointID)
	}

	for _, entry := range entries {
		if entry.ID == checkpointID {
			return entry, nil
		}
	}

	return nil, fmt.Errorf("checkpoint not found for thread %s and ID %s", threadID, checkpointID)
}

func (c *InMemoryCheckpointer) List(ctx context.Context, threadID string) ([]*CheckpointEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entries, exists := c.checkpoints[threadID]
	if !exists || len(entries) == 0 {
		return nil, fmt.Errorf("no checkpoints found for thread %s", threadID)
	}

	result := make([]*CheckpointEntry, len(entries))
	copy(result, entries)
	return result, nil
}

func (c *InMemoryCheckpointer) GetPendingWrites(ctx context.Context, threadID string, checkpointID string) ([]PendingWrite, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	threadWrites, exists := c.pendingWrites[threadID]
	if !exists {
		return nil, nil
	}

	writes := threadWrites[checkpointID]
	if len(writes) == 0 {
		return nil, nil
	}

	result := make([]PendingWrite, len(writes))
	copy(result, writes)
	return result, nil
}
