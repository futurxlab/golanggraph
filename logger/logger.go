package logger

import (
	"context"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ILogger interface {
	Debugf(ctx context.Context, msg string, args ...interface{})
	Infof(ctx context.Context, msg string, args ...interface{})
	Warnf(ctx context.Context, msg string, args ...interface{})
	Errorf(ctx context.Context, msg string, args ...interface{})
	DPanicf(ctx context.Context, msg string, args ...interface{})
	Panicf(ctx context.Context, msg string, args ...interface{})
}

type Logger struct {
	level         string
	DefaultLogger *zap.Logger
}

func (log *Logger) Debugf(ctx context.Context, msg string, args ...interface{}) {
	log.DefaultLogger.Sugar().Debugf(msg, args...)
}

func (log *Logger) Infof(ctx context.Context, msg string, args ...interface{}) {
	log.DefaultLogger.Sugar().Infof(msg, args...)
}

func (log *Logger) Warnf(ctx context.Context, msg string, args ...interface{}) {
	log.DefaultLogger.Sugar().Warnf(msg, args...)
}

func (log *Logger) Errorf(ctx context.Context, msg string, args ...interface{}) {
	log.DefaultLogger.Sugar().Errorf(msg, args...)
}

func (log *Logger) DPanicf(ctx context.Context, msg string, args ...interface{}) {
	log.DefaultLogger.Sugar().DPanicf(msg, args...)
}

func (log *Logger) Panicf(ctx context.Context, msg string, args ...interface{}) {
	log.DefaultLogger.Sugar().Panicf(msg, args...)
}

func (log *Logger) Fatalf(ctx context.Context, msg string, args ...interface{}) {
	log.DefaultLogger.Sugar().Fatalf(msg, args...)
}

func newDefaultLogger(level string) (*zap.Logger, error) {

	pe := zap.NewProductionEncoderConfig()
	pe.EncodeTime = zapcore.ISO8601TimeEncoder

	encoder := zapcore.NewJSONEncoder(pe)

	syncer := zapcore.AddSync(os.Stdout)

	zaplevel := zap.InfoLevel

	switch level {
	case "debug":
		zaplevel = zap.DebugLevel
	case "info":
		zaplevel = zap.InfoLevel
	case "error":
		zaplevel = zap.ErrorLevel
	case "dev":
		zaplevel = zap.DPanicLevel
	}

	core := zapcore.NewCore(encoder, syncer, zap.NewAtomicLevelAt(zaplevel))

	logger := zap.New(core, zap.WithCaller(false))

	zap.ReplaceGlobals(logger)

	return logger, nil
}

type Option func(*Logger)

func WithLevel(level string) Option {
	return func(logger *Logger) {
		logger.level = level
	}
}

func NewLogger(opts ...Option) (*Logger, error) {
	logger := &Logger{}

	for _, opt := range opts {
		opt(logger)
	}

	defaultLogger, err := newDefaultLogger(logger.level)
	if err != nil {
		return nil, err
	}

	logger.DefaultLogger = defaultLogger
	return logger, nil
}
