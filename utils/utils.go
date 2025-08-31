package utils

import (
	"context"
	"runtime/debug"
	"sync"

	"github.com/futurxlab/golanggraph/logger"
	"github.com/pkoukk/tiktoken-go"
)

var (
	encCache   sync.Map
	tokenCache sync.Map
)

func EncodeStringByModel(content string, model string) ([]int, error) {
	var enc, err = getCachedEncoding(model)
	if err != nil {
		return nil, err
	}

	return enc.Encode(content, nil, nil), nil
}

func DecodeTokensByModel(tokens []int, model string) (string, error) {
	var enc, err = getCachedEncoding(model)
	if err != nil {
		return "", err
	}

	return enc.Decode(tokens), nil
}

func getCachedEncoding(model string) (*tiktoken.Tiktoken, error) {
	if val, ok := encCache.Load(model); ok {
		return val.(*tiktoken.Tiktoken), nil
	}

	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		return nil, err
	}
	encCache.Store(model, enc)
	return enc, nil
}

// 安全地启动一个 goroutine
func SafeGo(ctx context.Context, logger logger.ILogger, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(ctx, "SafeGo recovered %+v, stack: %s", r, string(debug.Stack()))
			}
		}()
		fn()
	}()
}
