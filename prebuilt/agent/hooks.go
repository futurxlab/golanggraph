package agent

import (
	"context"
	"fmt"

	flowcontract "github.com/futurxlab/golanggraph/contract"
	"github.com/futurxlab/golanggraph/state"
	"github.com/tmc/langchaingo/llms"
)

func (a *Agent) contextCompressHook() func(ctx context.Context, s *state.State) *flowcontract.HookResult {
	return func(ctx context.Context, s *state.State) *flowcontract.HookResult {
		if len(s.History) <= a.contextWindow {
			return nil
		}

		systemMessages := make([]llms.MessageContent, 0)
		nonSystemMessages := make([]llms.MessageContent, 0)

		for _, msg := range s.History {
			if msg.Role == llms.ChatMessageTypeSystem {
				systemMessages = append(systemMessages, msg)
			} else {
				nonSystemMessages = append(nonSystemMessages, msg)
			}
		}

		if len(nonSystemMessages) <= a.contextWindow {
			return nil
		}

		trimmed := nonSystemMessages[len(nonSystemMessages)-a.contextWindow:]
		s.History = append(systemMessages, trimmed...)

		return nil
	}
}

func (a *Agent) responseValidationHook(modelNodeName string) func(ctx context.Context, s *state.State) *flowcontract.HookResult {
	return func(ctx context.Context, s *state.State) *flowcontract.HookResult {
		response := s.GetLastResponse()
		if response == "" {
			return nil
		}

		if err := a.responseValidator(response); err != nil {
			s.History = append(s.History, llms.MessageContent{
				Role: llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: fmt.Sprintf("[VALIDATION ERROR] Your response did not pass validation: %s. Please try again.", err.Error())},
				},
			})
			return &flowcontract.HookResult{JumpToNode: modelNodeName}
		}

		return nil
	}
}

func (a *Agent) maxToolCallHook(modelNodeName string) func(ctx context.Context, s *state.State) *flowcontract.HookResult {
	return func(ctx context.Context, s *state.State) *flowcontract.HookResult {
		if s.Metadata == nil {
			return nil
		}

		countRaw, ok := s.Metadata["tool_count"]
		if !ok {
			return nil
		}

		count, ok := countRaw.(int)
		if !ok {
			return nil
		}

		if count >= a.maxToolCalls {
			s.History = append(s.History, llms.MessageContent{
				Role: llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: fmt.Sprintf("[MAX TOOL CALLS] You have reached the maximum number of tool calls (%d). Please provide your final answer now without using any more tools.", a.maxToolCalls)},
				},
			})
			return &flowcontract.HookResult{JumpToNode: modelNodeName}
		}

		return nil
	}
}
