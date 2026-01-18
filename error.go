package liteq

import (
	"time"

	"github.com/dereulenspiegel/liteq/internal"
)

func WithRetryDelay(delay time.Duration) internal.WorkerErrorOpt {
	return func(we *internal.WorkerError) {
		we.DelayRetry = delay
	}
}

func WithRemainingAttemps(attempts int) internal.WorkerErrorOpt {
	return func(we *internal.WorkerError) {
		we.RemainingAttempts = &attempts
	}
}

func NewWorkerError(cause error, opts ...internal.WorkerErrorOpt) *internal.WorkerError {
	return internal.NewWorkerError(cause, opts...)
}
