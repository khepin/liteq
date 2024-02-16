package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"database/sql/driver"

	"github.com/alitto/pond"
)

type QueueJobParams struct {
	Queue             string
	Job               string
	ExecuteAfter      int64
	RemainingAttempts int64
	DedupingKey       DedupingKey
}

type DedupingKey interface {
	String() string
	ReplaceDuplicate() bool
}

type IgnoreDuplicate string

func (i IgnoreDuplicate) String() string {
	return string(i)
}
func (i IgnoreDuplicate) ReplaceDuplicate() bool {
	return false
}

type ReplaceDuplicate string

func (r ReplaceDuplicate) String() string {
	return string(r)
}
func (r ReplaceDuplicate) ReplaceDuplicate() bool {
	return true
}

func (q *Queries) QueueJob(ctx context.Context, params QueueJobParams) error {
	if params.RemainingAttempts == 0 {
		params.RemainingAttempts = 1
	}

	if params.DedupingKey == nil {
		params.DedupingKey = IgnoreDuplicate("")
	}

	doParams := doQueueJobIgnoreDupeParams{
		Queue:             params.Queue,
		Job:               params.Job,
		ExecuteAfter:      params.ExecuteAfter,
		RemainingAttempts: params.RemainingAttempts,
		DedupingKey:       params.DedupingKey.String(),
	}

	if params.DedupingKey.String() == "" {
		return q.doQueueJobIgnoreDupe(ctx, doParams)
	}

	if params.DedupingKey.ReplaceDuplicate() {
		return q.doQueueJobReplaceDupe(ctx, doQueueJobReplaceDupeParams(doParams))
	}

	return q.doQueueJobIgnoreDupe(ctx, doParams)
}

type GrabJobsParams struct {
	Queue        string
	ExecuteAfter int64
	Count        int64
}

func (q *Queries) GrabJobs(ctx context.Context, params GrabJobsParams) ([]*Job, error) {
	executeAfter := time.Now().Unix()
	if params.ExecuteAfter > 0 {
		executeAfter = params.ExecuteAfter
	}
	limit := int64(1)
	if params.Count > 0 {
		limit = params.Count
	}

	return q.MarkJobsForConsumer(ctx, MarkJobsForConsumerParams{
		Queue:        params.Queue,
		ExecuteAfter: executeAfter,
		Limit:        limit,
	})
}

type ConsumeParams struct {
	Queue             string
	PoolSize          int
	Worker            func(context.Context, *Job) error
	VisibilityTimeout int64
	OnEmptySleep      time.Duration
}

func (q *Queries) Consume(ctx context.Context, params ConsumeParams) error {
	workers := pond.New(params.PoolSize, params.PoolSize)
	sleep := params.OnEmptySleep
	if sleep == 0 {
		sleep = 1 * time.Second
	}
	for {
		// If the context gets canceled for example, stop consuming
		if ctx.Err() != nil {
			return nil
		}

		if params.VisibilityTimeout > 0 {
			_, err := q.ResetJobs(ctx, ResetJobsParams{
				Queue:             params.Queue,
				ConsumerFetchedAt: time.Now().Unix() - params.VisibilityTimeout,
			})

			if err != nil {
				return fmt.Errorf("error resetting jobs: %w", err)
			}
		}

		jobs, err := q.GrabJobs(ctx, GrabJobsParams{
			Queue: params.Queue,
			Count: int64(params.PoolSize),
		})

		if err != nil {
			return fmt.Errorf("error grabbing jobs: %w", err)
		}

		if len(jobs) == 0 {
			time.Sleep(sleep)
			continue
		}

		for _, job := range jobs {
			job := job
			workers.Submit(func() {
				err := params.Worker(ctx, job)
				if err != nil {
					q.FailJob(ctx, FailJobParams{
						ID:     job.ID,
						Errors: ErrorList(append(job.Errors, err.Error())),
					})
					return
				}

				q.CompleteJob(ctx, job.ID)
			})
		}
	}
}

type ErrorList []string

func (e ErrorList) Value() (driver.Value, error) {
	if len(e) == 0 {
		return "[]", nil
	}
	return json.Marshal(e)
}

func (e *ErrorList) Scan(src interface{}) error {
	switch src := src.(type) {
	case string:
		return json.Unmarshal([]byte(src), e)
	case []byte:
		return json.Unmarshal(src, e)
	default:
		return fmt.Errorf("unsupported type: %T", src)
	}
}
