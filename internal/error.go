package internal

import "time"

type WorkerErrorOpt func(*WorkerError)

type WorkerError struct {
	cause error

	DelayRetry time.Duration
}

func NewWorkerError(cause error, opts ...WorkerErrorOpt) *WorkerError {
	w := &WorkerError{
		cause: cause,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

func (w *WorkerError) Error() string {
	return w.cause.Error()
}

func (w *WorkerError) Unwrap() error {
	return w.cause
}

func (w *WorkerError) Is(err error) bool {
	_, ok := err.(*WorkerError)
	return ok
}
