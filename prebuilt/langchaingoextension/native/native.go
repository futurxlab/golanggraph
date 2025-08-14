package native

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/xerror"

	"github.com/avast/retry-go"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

var (
	ErrLLMNotFound    = fmt.Errorf("LLm not found")
	ErrLLMNotResponse = fmt.Errorf("LLM responses no content")
)

type ContextKey string

var (
	ModelKey ContextKey = "model"
)

type ChatLLM struct {
	llms   map[string][]llms.Model
	logger logger.ILogger
}

func (chat *ChatLLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {

	content, err := chat.GenerateContent(ctx, []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextPart(prompt),
			},
		},
	}, options...)

	if err != nil {
		return "", err
	}

	return content.Choices[0].Content, nil
}

func (chat *ChatLLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {

	var generation *llms.ContentResponse

	if err := retry.Do(
		func() error {
			var err error
			llm, model := chat.getLLM(ctx)

			chat.logger.Infof(ctx, "using native llms model %s", model)

			if llm == nil {
				return retry.Unrecoverable(ErrLLMNotFound)
			}

			generation, err = llm.GenerateContent(ctx, messages, options...)
			if err != nil {
				return xerror.Wrap(err)
			}

			return nil
		},
		retry.Delay(2*time.Second),
		retry.Attempts(3),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			chat.logger.Warnf(ctx, "retrying native llm generate content attempt: %d, error: %s", n, err)
		}),
		retry.RetryIf(func(err error) bool {

			if err == nil {
				return false
			}

			if errors.Is(err, context.Canceled) {
				return false
			}

			return true
		}),
	); err != nil {
		return nil, xerror.Wrap(err)
	}

	return generation, nil
}

func (chat *ChatLLM) getLLM(ctx context.Context) (llms.Model, string) {

	model := ctx.Value(ModelKey)

	modelStr := ""

	if model != nil {
		modelStr = model.(string)
	}

	llmDeployments := chat.llms[modelStr]

	if len(llmDeployments) > 0 {
		num := rand.Intn(len(llmDeployments))

		return llmDeployments[num], modelStr
	}

	for modelStr, llmDeployments = range chat.llms {
		if len(llmDeployments) > 0 {
			return llmDeployments[0], modelStr
		}
	}

	return nil, ""
}

func NewChatLLM(connectionStrings []string, logger logger.ILogger) (*ChatLLM, error) {

	llmDeployments := make(map[string][]llms.Model)
	for _, conn := range connectionStrings {
		llmOption := parseLLMConnectionString(conn)

		switch llmOption.Provider {
		case "openai":
			openaiLLM, err := openai.New(
				openai.WithAPIType(openai.APITypeOpenAI),
				openai.WithToken(llmOption.APIKey),
				openai.WithBaseURL(llmOption.BaseURL),
				openai.WithModel(llmOption.Model))
			if err != nil {
				return nil, err
			}

			if llmDeployments[llmOption.Model] == nil {
				llmDeployments[llmOption.Model] = make([]llms.Model, 0)
			}

			llmDeployments[llmOption.Model] = append(llmDeployments[llmOption.Model], openaiLLM)
		}
	}
	return &ChatLLM{
		llms:   llmDeployments,
		logger: logger,
	}, nil
}
