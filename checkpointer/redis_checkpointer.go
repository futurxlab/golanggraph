package checkpointer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/futurxlab/golanggraph/state"
	"github.com/futurxlab/golanggraph/xerror"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RedisCheckpointer 实现了 Checkpointer 接口，使用 Redis 存储状态
type RedisCheckpointer struct {
	client *redis.Client
}

// NewRedisCheckpointer 创建一个新的 RedisCheckpointer 实例
func NewRedisCheckpointer(client *redis.Client) *RedisCheckpointer {
	return &RedisCheckpointer{
		client: client,
	}
}

// getNamespaceKey 生成 Redis key
func getNamespaceKey(namespace string) string {
	return fmt.Sprintf("checkpointer:%s:states", namespace)
}

// getStateKey 生成单个状态的 Redis key
func getStateKey(namespace, id string) string {
	return fmt.Sprintf("checkpointer:%s:state:%s", namespace, id)
}

// Save 保存状态到 Redis
func (c *RedisCheckpointer) Save(ctx context.Context, namespace string, state *state.State) (string, error) {
	checkpointerID := uuid.New().String()

	// 序列化状态
	stateData, err := json.Marshal(state)
	if err != nil {
		return "", xerror.Wrap(fmt.Errorf("failed to marshal state: %w", err))
	}

	// 使用 Redis Pipeline 来保证原子性
	pipe := c.client.Pipeline()

	// 保存状态数据
	stateKey := getStateKey(namespace, checkpointerID)
	pipe.Set(ctx, stateKey, stateData, 0)

	// 将 ID 添加到有序列表
	namespaceKey := getNamespaceKey(namespace)
	pipe.RPush(ctx, namespaceKey, checkpointerID)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return "", xerror.Wrap(fmt.Errorf("failed to save state: %w", err))
	}

	return checkpointerID, nil
}

// GetByID 通过 ID 获取状态
func (c *RedisCheckpointer) GetByID(ctx context.Context, namespace string, checkpointerID string) (*state.State, error) {
	stateKey := getStateKey(namespace, checkpointerID)
	data, err := c.client.Get(ctx, stateKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, xerror.Wrap(fmt.Errorf("state not found for namespace %s and ID %s", namespace, checkpointerID))
		}
		return nil, xerror.Wrap(fmt.Errorf("failed to get state: %w", err))
	}

	var state state.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &state, nil
}

// GetLastest 获取最新的状态
func (c *RedisCheckpointer) GetLastest(ctx context.Context, namespace string) (*state.State, error) {
	namespaceKey := getNamespaceKey(namespace)

	// 获取最后一个 ID
	lastID, err := c.client.LIndex(ctx, namespaceKey, -1).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, xerror.Wrap(fmt.Errorf("no states found for namespace %s", namespace))
		}
		return nil, xerror.Wrap(fmt.Errorf("failed to get latest state ID: %w", err))
	}

	return c.GetByID(ctx, namespace, lastID)
}

// GetAll 获取所有状态
func (c *RedisCheckpointer) GetAll(ctx context.Context, namespace string) ([]*state.State, error) {
	namespaceKey := getNamespaceKey(namespace)

	// 获取所有 ID
	ids, err := c.client.LRange(ctx, namespaceKey, 0, -1).Result()
	if err != nil {
		return nil, xerror.Wrap(fmt.Errorf("failed to get state IDs: %w", err))
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("no states found for namespace %s", namespace)
	}

	states := make([]*state.State, len(ids))
	for i, id := range ids {
		state, err := c.GetByID(ctx, namespace, id)
		if err != nil {
			return nil, xerror.Wrap(fmt.Errorf("failed to get state %s: %w", id, err))
		}
		states[i] = state
	}

	return states, nil
}
