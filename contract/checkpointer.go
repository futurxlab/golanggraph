package flowcontract

import (
	"context"

	"github.com/futurxlab/golanggraph/checkpointer"
)

// Checkpointer 定义检查点存储接口，支持superstep级别的保存和恢复
type Checkpointer interface {
	// Save 保存一个superstep完成后的检查点
	Save(ctx context.Context, threadID string, entry *checkpointer.CheckpointEntry) error

	// SaveWrite 在节点执行完成时立即保存PendingWrite（用于crash recovery）
	SaveWrite(ctx context.Context, threadID string, checkpointID string, write checkpointer.PendingWrite) error

	// GetLatest 获取指定thread的最新检查点
	GetLatest(ctx context.Context, threadID string) (*checkpointer.CheckpointEntry, error)

	// GetByID 通过检查点ID获取特定检查点
	GetByID(ctx context.Context, threadID string, checkpointID string) (*checkpointer.CheckpointEntry, error)

	// List 列出指定thread的所有检查点（按时间排序）
	List(ctx context.Context, threadID string) ([]*checkpointer.CheckpointEntry, error)

	// GetPendingWrites 获取指定检查点的所有PendingWrite
	GetPendingWrites(ctx context.Context, threadID string, checkpointID string) ([]checkpointer.PendingWrite, error)
}
