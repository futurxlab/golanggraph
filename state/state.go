package state

import (
	"encoding/json"

	"github.com/tmc/langchaingo/llms"
)

type State struct {
	History  []llms.MessageContent
	Metadata map[string]interface{}

	// internal paramters
	threadID         string
	node             string
	nextNodes        []string
	interruptPayload interface{}
	resumeValue      interface{}
	interrupted      bool
}

func (s *State) GetThreadID() string {
	return s.threadID
}

func (s *State) GetNode() string {
	return s.node
}

func (s *State) GetNextNodes() []string {
	return s.nextNodes
}

func (s *State) SetThreadID(threadID string) {
	s.threadID = threadID
}

func (s *State) SetNode(node string) {
	s.node = node
}

func (s *State) SetNextNodes(nextNodes []string) {
	s.nextNodes = nextNodes
}

func (s *State) SetInterruptPayload(payload interface{}) {
	s.interruptPayload = payload
}

func (s *State) GetInterruptPayload() interface{} {
	return s.interruptPayload
}

func (s *State) SetResumeValue(value interface{}) {
	s.resumeValue = value
}

func (s *State) GetResumeValue() interface{} {
	return s.resumeValue
}

func (s *State) IsInterrupted() bool {
	return s.interrupted
}

func (s *State) SetInterrupted(v bool) {
	s.interrupted = v
}

func (s *State) ClearInterrupt() {
	s.interruptPayload = nil
	s.resumeValue = nil
	s.interrupted = false
}

type stateJSON struct {
	ThreadID         string                 `json:"threadID"`
	Node             string                 `json:"node"`
	NextNodes        []string               `json:"nextNodes"`
	History          []llms.MessageContent  `json:"history"`
	Metadata         map[string]interface{} `json:"metadata"`
	InterruptPayload interface{}            `json:"interruptPayload,omitempty"`
	ResumeValue      interface{}            `json:"resumeValue,omitempty"`
	Interrupted      bool                   `json:"interrupted"`
}

func (s *State) MarshalJSON() ([]byte, error) {
	return json.Marshal(stateJSON{
		ThreadID:         s.threadID,
		Node:             s.node,
		NextNodes:        s.nextNodes,
		History:          s.History,
		Metadata:         s.Metadata,
		InterruptPayload: s.interruptPayload,
		ResumeValue:      s.resumeValue,
		Interrupted:      s.interrupted,
	})
}

func (s *State) UnmarshalJSON(data []byte) error {
	var j stateJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	s.threadID = j.ThreadID
	s.node = j.Node
	s.nextNodes = j.NextNodes
	s.History = j.History
	s.Metadata = j.Metadata
	s.interruptPayload = j.InterruptPayload
	s.resumeValue = j.ResumeValue
	s.interrupted = j.Interrupted
	return nil
}

func (s *State) Serialize() ([]byte, error) {
	return s.MarshalJSON()
}

func (s *State) Deserialize(data []byte) error {
	return s.UnmarshalJSON(data)
}

func (s *State) Merge(other *State) {
	s.History = append(s.History, other.History...)
	s.Metadata = mergeMetadata(s.Metadata, other.Metadata)
	s.node = other.node
}

func mergeMetadata(a, b map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range a {
		result[k] = v
	}

	for k, v := range b {
		result[k] = v
	}

	return result
}

func (s *State) GetLastResponse() string {
	if len(s.History) == 0 {
		return ""
	}

	for i := len(s.History) - 1; i >= 0; i-- {
		if s.History[i].Role == llms.ChatMessageTypeAI {
			for _, part := range s.History[i].Parts {
				if textPart, ok := part.(llms.TextContent); ok {
					return textPart.Text
				}
			}
		}
	}

	return ""
}

func (s *State) GetLastMessageRelatedTools() []llms.MessageContent {
	if len(s.History) == 0 {
		return nil
	}

	toolList := make([]llms.MessageContent, 0)

	foundLastAIMessage := false

	for i := len(s.History) - 1; i >= 0; i-- {
		// AI Last Response
		if !foundLastAIMessage && s.History[i].Role == llms.ChatMessageTypeAI {
			foundLastAIMessage = true
			continue
		}

		if foundLastAIMessage && s.History[i].Role == llms.ChatMessageTypeTool {
			toolList = append(toolList, s.History[i])
		} else if foundLastAIMessage && s.History[i].Role == llms.ChatMessageTypeAI {
			toolList = append(toolList, s.History[i])
		} else if foundLastAIMessage && s.History[i].Role == llms.ChatMessageTypeHuman {
			break
		}
	}

	// reverse tool list
	for i := 0; i < len(toolList)/2; i++ {
		toolList[i], toolList[len(toolList)-i-1] = toolList[len(toolList)-i-1], toolList[i]
	}

	return toolList
}
