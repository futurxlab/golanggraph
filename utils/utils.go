package utils

import (
	"context"
	"runtime/debug"

	"golanggraph/logger"
)

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
