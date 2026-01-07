package liteq

import (
	"time"

	"github.com/khepin/liteq/internal"
)

func WithRetryDelay(delay time.Duration) internal.WorkerErrorOpt {
	return func(we *internal.WorkerError) {
		we.DelayRetry = delay
	}
}

func NewWorkerError(cause error, opts ...internal.WorkerErrorOpt) *internal.WorkerError {
	return internal.NewWorkerError(cause, opts...)
}
