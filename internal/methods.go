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

	dedupingKey := params.DedupingKey.String()

	doParams := doQueueJobIgnoreDupeParams{
		Queue:             params.Queue,
		Job:               params.Job,
		ExecuteAfter:      params.ExecuteAfter,
		RemainingAttempts: params.RemainingAttempts,
		DedupingKey:       scopeDedupingKey(params.Queue, dedupingKey),
	}

	if dedupingKey == "" {
		return q.doQueueJobIgnoreDupe(ctx, doParams)
	}

	if params.DedupingKey.ReplaceDuplicate() {
		return q.doQueueJobReplaceDupe(ctx, doQueueJobReplaceDupeParams(doParams))
	}

	return q.doQueueJobIgnoreDupe(ctx, doParams)
}

// Make the deduping key scoped to a single queue.
// 2 jobs can have the same deduping key on different queues and still to in parallel
func scopeDedupingKey(queue, key string) string {
	if key == "" {
		return ""
	}
	// length of queue ensures we don't have conflicts with
	// queue=a:b key=c
	// queue=a   key=b:c
	return fmt.Sprintf("%d:%s:%s", len(queue), queue, key)
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
	completed := make(chan completion, params.PoolSize)
	flushed := make(chan struct{})
	go func() {
		// completions still in flight must be saved after ctx is canceled
		q.completeInBatches(context.WithoutCancel(ctx), completed)
		close(flushed)
	}()
	defer func() {
		workers.StopAndWait()
		close(completed)
		<-flushed
	}()

	sleep := params.OnEmptySleep
	if sleep == 0 {
		sleep = 1 * time.Second
	}
	// the timeout has a 1s resolution, checking more often than 1/10th of it gains nothing
	resetEvery := time.Duration(params.VisibilityTimeout) * time.Second / 10
	var lastReset time.Time
	for {
		// If the context gets canceled for example, stop consuming
		if ctx.Err() != nil {
			return nil
		}

		if params.VisibilityTimeout > 0 && time.Since(lastReset) >= resetEvery {
			lastReset = time.Now()
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

				done := make(chan error, 1)
				completed <- completion{job.ID, done}
				<-done
			})
		}
	}
}

type completion struct {
	id   int64
	done chan error
}

// Each SQLite commit is costly and serialized, so completions are grouped:
// the workers waiting at the same time are written in one UPDATE. A worker is
// released only once its job is committed. Under low load a batch is a single
// id and no latency is added.
func (q *Queries) completeInBatches(ctx context.Context, completions <-chan completion) {
	var batch []completion
	var ids []int64
	for c := range completions {
		batch = append(batch[:0], c)
	drain:
		for {
			select {
			case c, ok := <-completions:
				if !ok {
					break drain
				}
				batch = append(batch, c)
			default:
				break drain
			}
		}
		ids = ids[:0]
		for _, c := range batch {
			ids = append(ids, c.id)
		}
		// on error, the visibility timeout puts the jobs back, as with CompleteJob before
		err := q.CompleteJobs(ctx, ids)
		for _, c := range batch {
			c.done <- err
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
