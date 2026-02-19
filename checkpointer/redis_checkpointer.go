package checkpointer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Yet-Another-AI-Project/kiwi-lib/xerror"
	"github.com/redis/go-redis/v9"
)

// RedisCheckpointer 实现了 Checkpointer 接口，使用 Redis 存储检查点
type RedisCheckpointer struct {
	client *redis.Client
}

func NewRedisCheckpointer(client *redis.Client) *RedisCheckpointer {
	return &RedisCheckpointer{
		client: client,
	}
}

func getEntriesKey(threadID string) string {
	return fmt.Sprintf("checkpoint:%s:entries", threadID)
}

func getEntryKey(threadID, checkpointID string) string {
	return fmt.Sprintf("checkpoint:%s:entry:%s", threadID, checkpointID)
}

func getWritesKey(threadID, checkpointID string) string {
	return fmt.Sprintf("checkpoint:%s:writes:%s", threadID, checkpointID)
}

func (c *RedisCheckpointer) Save(ctx context.Context, threadID string, entry *CheckpointEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return xerror.Wrap(fmt.Errorf("failed to marshal checkpoint entry: %w", err))
	}

	pipe := c.client.Pipeline()
	pipe.Set(ctx, getEntryKey(threadID, entry.ID), data, 0)
	pipe.RPush(ctx, getEntriesKey(threadID), entry.ID)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return xerror.Wrap(fmt.Errorf("failed to save checkpoint: %w", err))
	}

	return nil
}

func (c *RedisCheckpointer) SaveWrite(ctx context.Context, threadID string, checkpointID string, write PendingWrite) error {
	data, err := json.Marshal(write)
	if err != nil {
		return xerror.Wrap(fmt.Errorf("failed to marshal pending write: %w", err))
	}

	if err := c.client.RPush(ctx, getWritesKey(threadID, checkpointID), data).Err(); err != nil {
		return xerror.Wrap(fmt.Errorf("failed to save pending write: %w", err))
	}

	return nil
}

func (c *RedisCheckpointer) GetLatest(ctx context.Context, threadID string) (*CheckpointEntry, error) {
	entries, err := c.List(ctx, threadID)
	if err != nil {
		return nil, err
	}

	latest := entries[0]
	for _, e := range entries[1:] {
		if e.Step > latest.Step {
			latest = e
		}
	}
	return latest, nil
}

func (c *RedisCheckpointer) GetByID(ctx context.Context, threadID string, checkpointID string) (*CheckpointEntry, error) {
	data, err := c.client.Get(ctx, getEntryKey(threadID, checkpointID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("checkpoint not found for thread %s and ID %s", threadID, checkpointID)
		}
		return nil, xerror.Wrap(fmt.Errorf("failed to get checkpoint: %w", err))
	}

	var entry CheckpointEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, xerror.Wrap(fmt.Errorf("failed to unmarshal checkpoint: %w", err))
	}

	return &entry, nil
}

func (c *RedisCheckpointer) List(ctx context.Context, threadID string) ([]*CheckpointEntry, error) {
	ids, err := c.client.LRange(ctx, getEntriesKey(threadID), 0, -1).Result()
	if err != nil {
		return nil, xerror.Wrap(fmt.Errorf("failed to get checkpoint IDs: %w", err))
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("no checkpoints found for thread %s", threadID)
	}

	entries := make([]*CheckpointEntry, len(ids))
	for i, id := range ids {
		entry, err := c.GetByID(ctx, threadID, id)
		if err != nil {
			return nil, xerror.Wrap(fmt.Errorf("failed to get checkpoint %s: %w", id, err))
		}
		entries[i] = entry
	}

	return entries, nil
}

func (c *RedisCheckpointer) GetPendingWrites(ctx context.Context, threadID string, checkpointID string) ([]PendingWrite, error) {
	items, err := c.client.LRange(ctx, getWritesKey(threadID, checkpointID), 0, -1).Result()
	if err != nil {
		return nil, xerror.Wrap(fmt.Errorf("failed to get pending writes: %w", err))
	}

	if len(items) == 0 {
		return nil, nil
	}

	writes := make([]PendingWrite, len(items))
	for i, item := range items {
		if err := json.Unmarshal([]byte(item), &writes[i]); err != nil {
			return nil, xerror.Wrap(fmt.Errorf("failed to unmarshal pending write: %w", err))
		}
	}

	return writes, nil
}
