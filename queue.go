package liteq

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"time"

	"github.com/khepin/liteq/internal"
)

type ConsumeFunc[J any] func(ctx context.Context, job J) error

type Marshaler[J any] interface {
	Marshal(J) ([]byte, error)
	Unmarshal([]byte) (J, error)
}

type Queue[J any] struct {
	*internal.Queries

	name      string
	marshaler Marshaler[J]
}

type ConsumeOpt func(params *ConsumeParams)

func PoolSize(poolSize int) ConsumeOpt {
	return func(params *ConsumeParams) {
		params.PoolSize = poolSize
	}
}

func VisibilityTimeout(timeout time.Duration) ConsumeOpt {
	return func(params *ConsumeParams) {
		params.VisibilityTimeout = int64(timeout.Seconds())
	}
}

func OnEmptySleep(sleepDuration time.Duration) ConsumeOpt {
	return func(params *ConsumeParams) {
		params.OnEmptySleep = sleepDuration
	}
}

type QueueOption func(qOpts *QueueJobParams)

func ExecuteAfter(after time.Duration) QueueOption {
	return func(qOpts *QueueJobParams) {
		qOpts.ExecuteAfter = time.Now().Add(after).Unix()
	}
}

func DedupeKey(key DedupingKey) QueueOption {
	return func(qOpts *QueueJobParams) {
		qOpts.DedupingKey = key
	}
}

func Retries(retries int) QueueOption {
	return func(qOpts *QueueJobParams) {
		qOpts.RemainingAttempts = int64(retries)
	}
}

func NewQueue[J any](jq *JobQueue, name string, marshaler Marshaler[J]) *Queue[J] {
	return &Queue[J]{
		Queries:   jq.queries,
		name:      name,
		marshaler: marshaler,
	}
}

func (q Queue[J]) Put(ctx context.Context, jobItem J, opts ...QueueOption) error {
	marhaledItem, err := q.marshaler.Marshal(jobItem)
	if err != nil {
		return fmt.Errorf("failed to marshal Job item: %w", err)
	}

	params := &QueueJobParams{
		Queue: q.name,
		Job:   marhaledItem,
	}

	for _, opt := range opts {
		opt(params)
	}

	err = q.Queries.QueueJob(ctx, *params)
	return err
}

func (q *Queue[J]) Consume(ctx context.Context, consumer ConsumeFunc[J], consumeOpts ...ConsumeOpt) error {
	worker := func(consumer ConsumeFunc[J]) func(context.Context, *Job) error {
		return func(ctx context.Context, j *Job) error {
			jobItem, err := q.marshaler.Unmarshal(j.Job)
			if err != nil {
				return fmt.Errorf("failed to unmarshal job item: %w", err)
			}
			return consumer(ctx, jobItem)
		}
	}(consumer)
	params := &ConsumeParams{
		Queue:  q.name,
		Worker: worker,
	}

	for _, opt := range consumeOpts {
		opt(params)
	}
	return q.Queries.Consume(ctx, *params)
}

func (q *Queue[J]) ConsumeChan(ctx context.Context, in chan J) {
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

type GOBMarshaler[J any] struct{}

func (g GOBMarshaler[J]) Marshal(item J) ([]byte, error) {
	buf := &bytes.Buffer{}
	encoder := gob.NewEncoder(buf)
	err := encoder.Encode(item)
	return buf.Bytes(), err
}

func (g GOBMarshaler[J]) Unmarshal(dataBytes []byte) (J, error) {
	buf := bytes.NewBuffer(dataBytes)
	decoder := gob.NewDecoder(buf)
	item := new(J)
	err := decoder.Decode(&item)
	return *item, err
}
