package embedding

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/futurxlab/golanggraph/logger"
	"github.com/futurxlab/golanggraph/utils/cache"
	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/llms/openai"
)

var (
	ErrEmbedderNotFound = fmt.Errorf("embedder not found")
)

type ContextKey string

var (
	ModelKey ContextKey = "model"
)

type Embedder struct {
	cache     *cache.MemCache
	embedders map[string][]embeddings.Embedder
	logger    logger.ILogger
}

func (embedder *Embedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {

	wg := sync.WaitGroup{}

	errors := make([]string, 0)

	vectors := make([][]float32, 0)

	for _, text := range texts {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			vector, err := embedder.EmbedQuery(ctx, text)
			if err != nil {
				errors = append(errors, err.Error())
				return
			}

			vectors = append(vectors, vector)
		}(text)
	}

	wg.Wait()

	if len(errors) != 0 {
		return nil, fmt.Errorf("%s", strings.Join(errors, ","))
	}

	return vectors, nil
}

func (embedder *Embedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {

	client, model := embedder.getEmbedder(ctx)

	embedder.logger.Infof(ctx, "using embedder, model: %s", model)

	if client == nil {
		return nil, ErrEmbedderNotFound
	}

	if vector, ok := embedder.cache.Get(text); ok {
		return vector.([]float32), nil
	}

	vector, err := client.EmbedQuery(ctx, text)
	if err != nil {
		return nil, err
	}

	if ok := embedder.cache.SetWithTTL(text, vector, 0, time.Hour); !ok {
		embedder.logger.Warnf(ctx, "failed to set cache")
	}

	return vector, nil
}

func (embedder *Embedder) getEmbedder(ctx context.Context) (embeddings.Embedder, string) {
	model := ctx.Value(ModelKey)

	modelStr := ""

	if model != nil {
		modelStr = model.(string)
	}

	embedders := embedder.embedders[modelStr]

	if len(embedders) > 0 {
		num := rand.Intn(len(embedders))

		return embedders[num], modelStr
	}

	for modelStr, embedders = range embedder.embedders {
		if len(embedders) > 0 {
			return embedders[0], modelStr
		}
	}

	return nil, ""
}

func NewEmbedder(connectionStrings []string, cache *cache.MemCache, logger logger.ILogger) (*Embedder, error) {
	embedders := make(map[string][]embeddings.Embedder)
	for _, conn := range connectionStrings {

		llmOption := parseLLMConnectionString(conn)

		switch llmOption.Provider {
		case "openai":
			openaiLLM, err := openai.New(
				openai.WithAPIType(openai.APITypeOpenAI),
				openai.WithToken(llmOption.APIKey),
				openai.WithBaseURL(llmOption.BaseURL),
				openai.WithEmbeddingModel(llmOption.Model),
				openai.WithModel("dummy"))
			if err != nil {
				return nil, err
			}

			embedder, err := embeddings.NewEmbedder(openaiLLM)
			if err != nil {
				return nil, err
			}

			if embedders[llmOption.Model] == nil {
				embedders[llmOption.Model] = make([]embeddings.Embedder, 0)
			}

			embedders[llmOption.Model] = append(embedders[llmOption.Model], embedder)
		case "azure":
			llm, err := openai.New(
				openai.WithAPIType(openai.APITypeAzure),
				openai.WithToken(llmOption.APIKey),
				openai.WithBaseURL(llmOption.BaseURL),
				openai.WithEmbeddingModel(llmOption.Model),
				openai.WithModel("dummy"))

			if err != nil {
				return nil, err
			}

			embedder, err := embeddings.NewEmbedder(llm)
			if err != nil {
				return nil, err
			}

			if embedders[llmOption.Model] == nil {
				embedders[llmOption.Model] = make([]embeddings.Embedder, 0)
			}

			embedders[llmOption.Model] = append(embedders[llmOption.Model], embedder)
		}
	}

	return &Embedder{
		cache:     cache,
		embedders: embedders,
		logger:    logger,
	}, nil
}
