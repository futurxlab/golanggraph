package checkpointer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/futurxlab/golanggraph/state"
	"github.com/google/uuid"
)

// testCheckpointer mirrors contract.Checkpointer to avoid import cycle.
// Since we're inside package checkpointer, we can't import contract which
// itself imports checkpointer.
type testCheckpointer interface {
	Save(ctx context.Context, threadID string, entry *CheckpointEntry) error
	SaveWrite(ctx context.Context, threadID string, checkpointID string, write PendingWrite) error
	GetLatest(ctx context.Context, threadID string) (*CheckpointEntry, error)
	GetByID(ctx context.Context, threadID string, checkpointID string) (*CheckpointEntry, error)
	List(ctx context.Context, threadID string) ([]*CheckpointEntry, error)
	GetPendingWrites(ctx context.Context, threadID string, checkpointID string) ([]PendingWrite, error)
}

// --- Test State Factory ---

func newTestState(threadID string, metadata map[string]interface{}) state.State {
	s := state.State{
		Metadata: metadata,
	}
	if threadID != "" {
		s.SetThreadID(threadID)
	}
	return s
}

// --- CheckpointEntry Factory ---

type entryOption func(*CheckpointEntry)

func withParentID(parentID string) entryOption {
	return func(e *CheckpointEntry) {
		e.ParentID = parentID
	}
}

func withStep(step int) entryOption {
	return func(e *CheckpointEntry) {
		e.Step = step
	}
}

func withSource(source string) entryOption {
	return func(e *CheckpointEntry) {
		e.Source = source
	}
}

func withMetadata(metadata map[string]string) entryOption {
	return func(e *CheckpointEntry) {
		e.Metadata = metadata
	}
}

func withState(s *state.State) entryOption {
	return func(e *CheckpointEntry) {
		e.State = s
	}
}

func newCheckpointEntry(opts ...entryOption) *CheckpointEntry {
	s := newTestState("", map[string]interface{}{"test": true})
	e := &CheckpointEntry{
		ID:        uuid.New().String(),
		Step:      0,
		State:     &s,
		Source:    "input",
		Timestamp: time.Now(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// --- PendingWrite Factory ---

func newPendingWrite(taskID, nodeName string) PendingWrite {
	s := newTestState("", map[string]interface{}{"node": nodeName})
	return PendingWrite{
		TaskID:   taskID,
		NodeName: nodeName,
		State:    &s,
	}
}

// --- Checkpoint Chain Builder ---
// buildCheckpointChain creates a chain of linked CheckpointEntry objects.
// Returns entries in order [root, child1, child2, ...].
func buildCheckpointChain(length int) []*CheckpointEntry {
	entries := make([]*CheckpointEntry, length)
	for i := 0; i < length; i++ {
		source := "loop"
		if i == 0 {
			source = "input"
		}
		parentID := ""
		if i > 0 {
			parentID = entries[i-1].ID
		}
		entries[i] = newCheckpointEntry(
			withStep(i),
			withSource(source),
			withParentID(parentID),
		)
	}
	return entries
}

// --- Unique Thread ID Generator ---

func uniqueThreadID(t *testing.T) string {
	return fmt.Sprintf("test:%s:%s", t.Name(), uuid.New().String())
}
