package logging

import (
	"context"
	"log"
)

type loggerKeyType string

const loggerKey loggerKeyType = "logger"

func WithLogger(ctx context.Context, logger *log.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

func FromContext(ctx context.Context) *log.Logger {
	if ctx != nil {
		if logger, ok := ctx.Value(loggerKey).(*log.Logger); ok && logger != nil {
			return logger
		}
	}
	return log.Default()
}
