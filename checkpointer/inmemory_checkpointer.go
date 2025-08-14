package checkpointer

import (
	"context"
	"fmt"
	"sync"

	"golanggraph/state"

	"github.com/google/uuid"
)

// StateEntry 用于存储状态及其ID
type StateEntry struct {
	ID    string
	State *state.State
}

// InMemoryCheckpointer 实现了 Checkpointer 接口，使用内存存储状态
type InMemoryCheckpointer struct {
	mu sync.RWMutex
	// 使用 map 存储不同 namespace 的状态切片
	// key 是 namespace，value 是有序的状态切片
	states map[string][]StateEntry
}

// NewInMemoryCheckpointer 创建一个新的 InMemoryCheckpointer 实例
func NewInMemoryCheckpointer() *InMemoryCheckpointer {
	return &InMemoryCheckpointer{
		states: make(map[string][]StateEntry),
	}
}

// Save 保存状态到内存中
func (c *InMemoryCheckpointer) Save(ctx context.Context, namespace string, state *state.State) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 使用时间戳作为 checkpointerID
	checkpointerID := uuid.New().String()
	entry := StateEntry{
		ID:    checkpointerID,
		State: state,
	}

	// 将新状态追加到切片末尾
	c.states[namespace] = append(c.states[namespace], entry)

	return checkpointerID, nil
}

// GetByID 通过 ID 获取状态
func (c *InMemoryCheckpointer) GetByID(ctx context.Context, namespace string, checkpointerID string) (*state.State, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if states, exists := c.states[namespace]; exists {
		for _, entry := range states {
			if entry.ID == checkpointerID {
				return entry.State, nil
			}
		}
	}

	return nil, fmt.Errorf("state not found for namespace %s and ID %s", namespace, checkpointerID)
}

// GetLastest 获取最新的状态
func (c *InMemoryCheckpointer) GetLastest(ctx context.Context, namespace string) (*state.State, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if states, exists := c.states[namespace]; exists {
		if len(states) > 0 {
			// 返回切片中的最后一个状态
			return states[len(states)-1].State, nil
		}
	}

	return nil, fmt.Errorf("no states found for namespace %s", namespace)
}

// GetAll 获取所有状态
func (c *InMemoryCheckpointer) GetAll(ctx context.Context, namespace string) ([]*state.State, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if states, exists := c.states[namespace]; exists {
		result := make([]*state.State, len(states))
		for i, entry := range states {
			result[i] = entry.State
		}
		return result, nil
	}

	return nil, fmt.Errorf("no states found for namespace %s", namespace)
}
