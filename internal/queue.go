package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Marshaler[J any] interface {
	Marshal(J) ([]byte, error)
	Unmarshal([]byte) (J, error)
}

type Queue[J any] struct {
	*Queries

	name      string
	marshaler Marshaler[J]
}

type queueOptions struct {
	executeAfter time.Duration
	dedupingKey  DedupingKey
}

type queueOption func(qOpts *queueOptions)

func ExecuteAfter(after time.Duration) queueOption {
	return func(qOpts *queueOptions) {
		qOpts.executeAfter = after
	}
}

func DedupeKey(key DedupingKey) queueOption {
	return func(qOpts *queueOptions) {
		qOpts.dedupingKey = key
	}
}

func newQueue[J any](q *Queries, name string, marshaler Marshaler[J]) *Queue[J] {
	return &Queue[J]{
		Queries:   q,
		name:      name,
		marshaler: marshaler,
	}
}

func (q Queue[J]) Put(ctx context.Context, jobItem J, opts ...queueOption) error {
	qOpts := &queueOptions{
		executeAfter: time.Millisecond * 0,
		dedupingKey:  nil,
	}

	for _, opt := range opts {
		opt(qOpts)
	}

	marhaledItem, err := q.marshaler.Marshal(jobItem)
	if err != nil {
		return fmt.Errorf("failed to marshal Job item: %w", err)
	}
	err = q.Queries.QueueJob(ctx, QueueJobParams{
		Queue:             q.name,
		ExecuteAfter:      time.Now().Add(qOpts.executeAfter).Unix(),
		DedupingKey:       qOpts.dedupingKey,
		RemainingAttempts: 1,
		Job:               marhaledItem,
	})
	return err
}

func (q *Queue[J]) Consume(ctx context.Context, in chan J) {
	go func(ctx context.Context, q *Queue[J], in chan J) {
		// TODO handle error
		q.Queries.Consume(ctx, ConsumeParams{
			Queue:             q.name,
			PoolSize:          1,
			VisibilityTimeout: 10,
			Worker: func(ctx context.Context, j *Job) error {
				item, err := q.marshaler.Unmarshal(j.Job)
				if err != nil {
					return fmt.Errorf("failed to unmarshal job item: %w", err)
				}
				in <- item
				return nil
			},
		})
	}(ctx, q, in)
}

type JSONMarshaler[J any] struct{}

func (j JSONMarshaler[J]) Marshal(item J) ([]byte, error) {
	return json.Marshal(item)
}

func (j JSONMarshaler[J]) Unmarshal(dataBytes []byte) (J, error) {
	item := new(J)
	err := json.Unmarshal(dataBytes, item)
	return *item, err
}
