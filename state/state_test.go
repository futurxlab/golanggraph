package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// ========================================================================
// State.Merge Tests
// ========================================================================

func TestMerge_MetadataOverwrite(t *testing.T) {
	s1 := &State{
		Metadata: map[string]interface{}{
			"key1": "original",
			"key2": "only_in_s1",
		},
	}
	s2 := &State{
		Metadata: map[string]interface{}{
			"key1": "overwritten",
			"key3": "only_in_s2",
		},
	}

	s1.Merge(s2)

	// key1 should be overwritten by s2's value
	assert.Equal(t, "overwritten", s1.Metadata["key1"])
	// key2 should still exist from s1
	assert.Equal(t, "only_in_s1", s1.Metadata["key2"])
	// key3 should exist from s2
	assert.Equal(t, "only_in_s2", s1.Metadata["key3"])
}

func TestMerge_HistoryAppend(t *testing.T) {
	s1 := &State{
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "hello"}}},
		},
	}
	s2 := &State{
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: "world"}}},
		},
	}

	s1.Merge(s2)

	assert.Len(t, s1.History, 2)
	assert.Equal(t, llms.ChatMessageTypeHuman, s1.History[0].Role)
	assert.Equal(t, llms.ChatMessageTypeAI, s1.History[1].Role)
}

func TestMerge_NilMetadata(t *testing.T) {
	s1 := &State{Metadata: nil}
	s2 := &State{Metadata: map[string]interface{}{"key": "value"}}

	s1.Merge(s2)

	// mergeMetadata creates a new map, so s1.Metadata should have key
	assert.Equal(t, "value", s1.Metadata["key"])
}

func TestMerge_BothNilMetadata(t *testing.T) {
	s1 := &State{Metadata: nil}
	s2 := &State{Metadata: nil}

	s1.Merge(s2)

	// Should not panic and metadata should be empty or nil-safe
	assert.NotNil(t, s1.Metadata)
	assert.Len(t, s1.Metadata, 0)
}

func TestMerge_EmptyHistory(t *testing.T) {
	s1 := &State{
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "hello"}}},
		},
	}
	s2 := &State{
		History: nil,
	}

	s1.Merge(s2)

	// s1's history should remain intact
	assert.Len(t, s1.History, 1)
}

func TestMerge_NodeUpdate(t *testing.T) {
	s1 := &State{}
	s1.SetNode("nodeA")

	s2 := &State{}
	s2.SetNode("nodeB")

	s1.Merge(s2)

	// After merge, node should be updated to s2's node
	assert.Equal(t, "nodeB", s1.GetNode())
}

func TestMerge_MultipleSequentialMerges(t *testing.T) {
	s1 := &State{
		Metadata: map[string]interface{}{"step": 1},
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "q1"}}},
		},
	}

	s2 := &State{
		Metadata: map[string]interface{}{"step": 2, "nodeA": true},
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: "a1"}}},
		},
	}

	s3 := &State{
		Metadata: map[string]interface{}{"step": 3, "nodeB": true},
		History: []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "q2"}}},
		},
	}

	s1.Merge(s2)
	s1.Merge(s3)

	// Final metadata should have latest "step" and all node keys
	assert.Equal(t, 3, s1.Metadata["step"])
	assert.Equal(t, true, s1.Metadata["nodeA"])
	assert.Equal(t, true, s1.Metadata["nodeB"])

	// History should be concatenated
	assert.Len(t, s1.History, 3)
}

// ========================================================================
// State Interrupt / Resume Tests
// ========================================================================

func TestState_InterruptPayload_SetGet(t *testing.T) {
	s := &State{}

	// Initially nil
	assert.Nil(t, s.GetInterruptPayload())

	// Set payload
	payload := map[string]interface{}{"reason": "user_requested"}
	s.SetInterruptPayload(payload)
	assert.Equal(t, payload, s.GetInterruptPayload())

	// Can set to different types
	s.SetInterruptPayload("string_payload")
	assert.Equal(t, "string_payload", s.GetInterruptPayload())
}

func TestState_ResumeValue_SetGet(t *testing.T) {
	s := &State{}

	// Initially nil
	assert.Nil(t, s.GetResumeValue())

	// Set resume value
	value := map[string]interface{}{"result": "success"}
	s.SetResumeValue(value)
	assert.Equal(t, value, s.GetResumeValue())

	// Can set to different types
	s.SetResumeValue(42)
	assert.Equal(t, 42, s.GetResumeValue())
}

func TestState_IsInterrupted(t *testing.T) {
	s := &State{}

	// Initially false
	assert.False(t, s.IsInterrupted())

	// Set to true
	s.SetInterrupted(true)
	assert.True(t, s.IsInterrupted())

	// Set back to false
	s.SetInterrupted(false)
	assert.False(t, s.IsInterrupted())
}

func TestState_ClearInterrupt(t *testing.T) {
	s := &State{}

	// Set all interrupt fields
	s.SetInterruptPayload(map[string]interface{}{"key": "value"})
	s.SetResumeValue("test")
	s.SetInterrupted(true)

	// Verify they're set
	assert.NotNil(t, s.GetInterruptPayload())
	assert.NotNil(t, s.GetResumeValue())
	assert.True(t, s.IsInterrupted())

	// Clear all
	s.ClearInterrupt()

	// Verify all cleared
	assert.Nil(t, s.GetInterruptPayload())
	assert.Nil(t, s.GetResumeValue())
	assert.False(t, s.IsInterrupted())
}

func TestState_Serialize_WithInterrupt(t *testing.T) {
	s := &State{
		History:  []llms.MessageContent{},
		Metadata: map[string]interface{}{"key": "value"},
	}
	s.SetThreadID("thread-123")
	s.SetNode("nodeA")
	s.SetNextNodes([]string{"nodeB", "nodeC"})
	s.SetInterruptPayload(map[string]interface{}{"reason": "paused"})
	s.SetResumeValue("resume_token")
	s.SetInterrupted(true)

	data, err := s.Serialize()
	assert.NoError(t, err)

	assert.Contains(t, string(data), "\"interrupted\":true")
	assert.Contains(t, string(data), "\"resume_token\"")
	assert.Contains(t, string(data), "\"paused\"")
}

func TestState_Merge_PreservesInterrupt(t *testing.T) {
	s1 := &State{
		Metadata: map[string]interface{}{"key": "value1"},
	}
	s1.SetInterruptPayload("s1_payload")
	s1.SetResumeValue("s1_resume")
	s1.SetInterrupted(true)

	s2 := &State{
		Metadata: map[string]interface{}{"key": "value2"},
	}
	s2.SetInterruptPayload("s2_payload")
	s2.SetResumeValue("s2_resume")
	s2.SetInterrupted(false)

	// Merge s2 into s1
	s1.Merge(s2)

	// Interrupt fields should NOT be copied from s2 (should preserve s1's values)
	assert.Equal(t, "s1_payload", s1.GetInterruptPayload())
	assert.Equal(t, "s1_resume", s1.GetResumeValue())
	assert.True(t, s1.IsInterrupted())

	// Metadata should be merged (s2's values take precedence)
	assert.Equal(t, "value2", s1.Metadata["key"])
}

// ========================================================================
// State Serialize / Deserialize not tested here because the internal
// fields (threadID, node, nextNodes) require type assertions that may
// break. These are integration-tested through flow tests.
// ========================================================================
