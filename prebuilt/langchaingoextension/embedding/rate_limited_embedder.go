package embedding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go"
	"github.com/Yet-Another-AI-Project/kiwi-lib/logger"
	"github.com/futurxlab/golanggraph/utils"
	"github.com/tmc/langchaingo/embeddings"
	"golang.org/x/time/rate"
)

var (
	ErrRateLimitExceeded = fmt.Errorf("rate limit exceeded")
)

type RateLimitedEmbedder struct {
	internalEmbedder embeddings.Embedder
	tpmLimiter       *rate.Limiter
	rpmLimiter       *rate.Limiter
	logger           logger.ILogger
}

func (e *RateLimitedEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	var (
		wg         = sync.WaitGroup{}
		errorSlice = make([]string, 0)
		vectors    = make([][]float32, 0)
	)
	for _, text := range texts {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			vector, err := e.EmbedQuery(ctx, text)
			if err != nil {
				errorSlice = append(errorSlice, err.Error())
				return
			}

			vectors = append(vectors, vector)
		}(text)
	}

	wg.Wait()

	if len(errorSlice) != 0 {
		return nil, fmt.Errorf("%s", strings.Join(errorSlice, ","))
	}

	return vectors, nil
}

func (e *RateLimitedEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if err := e.rpmLimiter.Wait(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("RPM limit exceeded, %w", ErrRateLimitExceeded)
		}
		return nil, fmt.Errorf("rate limit error: %w", err)
	}

	encoded, _ := utils.EncodeStringByModel(text, "text-embedding-3-large")
	tokenCount := len(encoded)

	if err := e.tpmLimiter.WaitN(ctx, tokenCount); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("TPM limit exceeded, %w", ErrRateLimitExceeded)
		}
		return nil, fmt.Errorf("token limit error: %w", err)
	}

	var (
		vector []float32
		err    error
	)

	err = retry.Do(
		func() error {
			vector, err = e.internalEmbedder.EmbedQuery(ctx, text)
			if err != nil {
				e.logger.Errorf(ctx, "embed query failed, %v", err)
				return err
			}
			e.logger.Debugf(ctx, "embed query success, vector_count: %d", len(vector))
			return nil
		},
		retry.Delay(5*time.Second),
		retry.Attempts(20),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			e.logger.Warnf(ctx, "retrying, attempt: %d, error: %v", n, err)
		}),
	)

	return vector, err
}

func NewRateLimitedEmbedder(embedder embeddings.Embedder, tpmLimit, rpmLimit int, logger logger.ILogger) *RateLimitedEmbedder {
	return &RateLimitedEmbedder{
		internalEmbedder: embedder,
		tpmLimiter:       rate.NewLimiter(rate.Limit(tpmLimit)/60, tpmLimit),
		rpmLimiter:       rate.NewLimiter(rate.Limit(rpmLimit)/60, rpmLimit),
		logger:           logger,
	}
}
