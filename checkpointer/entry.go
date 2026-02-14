package checkpointer

import (
	"time"

	"github.com/futurxlab/golanggraph/state"
)

// CheckpointEntry 表示一个superstep级别的检查点
// 每个superstep（BFS层）执行完毕后保存一个CheckpointEntry
// 通过ParentID形成链表，支持time travel
type CheckpointEntry struct {
	ID        string            // 检查点唯一标识
	ParentID  string            // 父检查点ID，形成链表用于time travel
	Step      int               // superstep编号（BFS层数）
	State     *state.State      // 该superstep结束后的完整状态快照
	Source    string            // 检查点来源："input" | "loop" | "resume"
	Metadata  map[string]string // 额外元数据
	Timestamp time.Time         // 创建时间
}

// PendingWrite 表示一个节点在superstep执行中的中间输出
// 用于crash recovery：并行节点在完成时立即保存PendingWrite
// 恢复时通过TaskID匹配，跳过已完成的节点
type PendingWrite struct {
	TaskID   string       // 确定性任务ID = hash(checkpointID + nodeName + step)
	NodeName string       // 节点名称
	State    *state.State // 该节点产出的状态
}
