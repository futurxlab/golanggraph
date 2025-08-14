package state

import (
	"encoding/json"

	"github.com/tmc/langchaingo/llms"
)

type State struct {
	History  []llms.MessageContent
	Metadata map[string]interface{}

	// internal paramters
	threadID  string
	node      string
	nextNodes []string
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

func (s *State) Serialize() ([]byte, error) {
	m := make(map[string]interface{})
	m["threadID"] = s.threadID
	m["node"] = s.node
	m["nextNodes"] = s.nextNodes
	m["history"] = s.History
	m["metadata"] = s.Metadata
	json, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return json, nil
}

func (s *State) Deserialize(data []byte) error {
	m := make(map[string]interface{})
	err := json.Unmarshal(data, &m)
	if err != nil {
		return err
	}

	s.threadID = m["threadID"].(string)
	s.node = m["node"].(string)
	s.nextNodes = m["nextNodes"].([]string)
	s.History = m["history"].([]llms.MessageContent)
	s.Metadata = m["metadata"].(map[string]interface{})

	return nil
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
