package internal

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"database/sql/driver"

	"github.com/alitto/pond"
)

// Queue serializes its own writes. SQLite has a single writer, and waiting on a
// Go mutex is much cheaper than waiting in SQLite's busy handler.
type Queue struct {
	*Queries
	writeMu sync.Mutex
}

func NewQueue(queries *Queries) *Queue {
	return &Queue{Queries: queries}
}

func (q *Queue) write(f func() error) error {
	q.writeMu.Lock()
	defer q.writeMu.Unlock()
	return f()
}

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

func (q *Queue) QueueJob(ctx context.Context, params QueueJobParams) error {
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

	return q.write(func() error {
		if dedupingKey == "" {
			return q.doQueueJobIgnoreDupe(ctx, doParams)
		}

		if params.DedupingKey.ReplaceDuplicate() {
			return q.doQueueJobReplaceDupe(ctx, doQueueJobReplaceDupeParams(doParams))
		}

		return q.doQueueJobIgnoreDupe(ctx, doParams)
	})
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
	ConsumerID   int64
	ExecuteAfter int64
	Count        int64
}

func (q *Queue) GrabJobs(ctx context.Context, params GrabJobsParams) ([]*Job, error) {
	executeAfter := time.Now().Unix()
	if params.ExecuteAfter > 0 {
		executeAfter = params.ExecuteAfter
	}
	limit := int64(1)
	if params.Count > 0 {
		limit = params.Count
	}

	var jobs []*Job
	err := q.write(func() (err error) {
		jobs, err = q.MarkJobsForConsumer(ctx, MarkJobsForConsumerParams{
			ConsumerID:   params.ConsumerID,
			Queue:        params.Queue,
			ExecuteAfter: executeAfter,
			Limit:        limit,
		})
		return err
	})
	return jobs, err
}

type ConsumeParams struct {
	Queue             string
	PoolSize          int
	Worker            func(context.Context, *Job) error
	VisibilityTimeout int64
	OnEmptySleep      time.Duration
	PrefetchCount     int
}

func (q *Queue) Consume(ctx context.Context, params ConsumeParams) error {
	prefetch := params.PrefetchCount
	if prefetch == 0 {
		prefetch = 4 * params.PoolSize
	}
	slots := newSlots(int64(params.PoolSize + prefetch))
	workers := pond.New(params.PoolSize, int(slots.capacity))
	acks := q.newAcks(ctx, params.PoolSize)
	leases := q.newLeases(params.Queue, params.VisibilityTimeout)
	defer func() {
		workers.StopAndWait()
		leases.release(context.WithoutCancel(ctx))
		acks.close()
		leases.stop()
	}()

	sleep := params.OnEmptySleep
	if sleep == 0 {
		sleep = 1 * time.Second
	}
	for {
		// If the context gets canceled for example, stop consuming
		if ctx.Err() != nil {
			return nil
		}

		if err := leases.maintain(ctx); err != nil {
			return err
		}

		// grab in large batches: wait until half the capacity is free
		free := slots.free()
		if free < (slots.capacity+1)/2 {
			select {
			case <-slots.released():
			case <-leases.tick():
			case <-ctx.Done():
			}
			continue
		}

		jobs, err := q.GrabJobs(ctx, GrabJobsParams{
			Queue:      params.Queue,
			ConsumerID: leases.consumerID,
			Count:      free,
		})

		if err != nil {
			return fmt.Errorf("error grabbing jobs: %w", err)
		}

		if len(jobs) == 0 {
			select {
			case <-time.After(sleep):
			case <-leases.tick():
			case <-ctx.Done():
			}
			continue
		}

		slots.take(len(jobs))
		for _, job := range jobs {
			job := job
			leases.add(job)
			workers.Submit(func() {
				defer slots.release()
				if !leases.start(ctx, job) {
					return
				}
				err := params.Worker(ctx, job)
				if err != nil {
					q.write(func() error {
						return q.FailJob(context.WithoutCancel(ctx), FailJobParams{
							ID:         job.ID,
							ConsumerID: job.ConsumerID,
							Errors:     ErrorList(append(job.Errors, err.Error())),
						})
					})
					return
				}

				acks.send(job)
			})
		}
	}
}

// slots counts the jobs a consumer holds, running or prefetched.
// release wakes the loop that waits for free slots. The signal holds no count:
// one pending wake-up is enough, because the waiter reads free() again.
type slots struct {
	capacity int64
	used     atomic.Int64
	freed    chan struct{} // buffer 1, never blocks a worker
}

func newSlots(capacity int64) *slots {
	return &slots{capacity: capacity, freed: make(chan struct{}, 1)}
}

func (s *slots) free() int64 { return s.capacity - s.used.Load() }
func (s *slots) take(n int)  { s.used.Add(int64(n)) }

func (s *slots) release() {
	s.used.Add(-1) // before the signal, or the waiter can miss it
	select {
	case s.freed <- struct{}{}:
	default:
	}
}

// released fires after at least one release since the last receive.
func (s *slots) released() <-chan struct{} { return s.freed }

func newConsumerID() int64 {
	b := make([]byte, 8)
	rand.Read(b)
	// 0 is the column default: never fetched
	return int64(binary.BigEndian.Uint64(b)>>1) | 1
}

// leases keeps the visibility timeout correct. It renews the jobs fetched but
// not started yet, so they are not reset while they wait in the prefetch
// buffer, and it puts back the expired jobs of all consumers on the queue.
type leases struct {
	q       *Queue
	queue   string
	timeout int64
	every   time.Duration
	lastRun time.Time
	ticker  *time.Ticker
	// A reset needs the timeout to pass, and the id changes more often than
	// that: a stale run never shares its id with a new grab of the same job.
	consumerID int64
	mu         sync.Mutex
	waiting    map[int64]lease   // job id -> lease
	unstarted  map[int64][]int64 // consumer id -> job ids
}

type lease struct {
	consumerID int64
	fetchedAt  int64
}

func (q *Queue) newLeases(queue string, timeout int64) *leases {
	l := &leases{q: q, queue: queue, timeout: timeout, consumerID: newConsumerID(), waiting: map[int64]lease{}, unstarted: map[int64][]int64{}}
	if timeout > 0 {
		// the timeout has a 1s resolution, checking more often than 1/10th of it gains nothing
		l.every = time.Duration(timeout) * time.Second / 10
		l.ticker = time.NewTicker(l.every)
	}
	return l
}

func (l *leases) stop() {
	if l.ticker != nil {
		l.ticker.Stop()
	}
}

// tick fires when maintain is due. Without a timeout it never fires.
func (l *leases) tick() <-chan time.Time {
	if l.ticker == nil {
		return nil
	}
	return l.ticker.C
}

// maintain releases the unstarted jobs at once. Renewal and reset run only
// when they are due.
func (l *leases) maintain(ctx context.Context) error {
	if err := l.release(ctx); err != nil {
		return fmt.Errorf("error releasing jobs: %w", err)
	}
	if l.timeout == 0 || time.Since(l.lastRun) < l.every {
		return nil
	}
	l.lastRun = time.Now()
	l.consumerID = newConsumerID()
	if err := l.renew(ctx); err != nil {
		return fmt.Errorf("error renewing jobs: %w", err)
	}
	err := l.q.write(func() error {
		_, err := l.q.ResetJobs(ctx, ResetJobsParams{
			Queue:             l.queue,
			ConsumerFetchedAt: time.Now().Unix() - l.timeout,
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("error resetting jobs: %w", err)
	}
	return nil
}

func (l *leases) add(job *Job) {
	if l.timeout == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.waiting[job.ID] = lease{job.ConsumerID, job.ConsumerFetchedAt}
}

// start reports whether the job can run: ctx is not canceled and this grab
// still holds the job. A job that cannot run is kept for release.
func (l *leases) start(ctx context.Context, job *Job) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.waiting[job.ID]
	ok = ok && w.consumerID == job.ConsumerID
	if ok {
		delete(l.waiting, job.ID)
	}
	expired := l.timeout > 0 && (!ok || w.fetchedAt < time.Now().Unix()-l.timeout)
	if ctx.Err() != nil || expired {
		l.unstarted[job.ConsumerID] = append(l.unstarted[job.ConsumerID], job.ID)
		return false
	}
	if l.timeout > 0 {
		job.ConsumerFetchedAt = w.fetchedAt
	}
	return true
}

// release puts the unstarted jobs back on the queue without using an attempt.
// A job that a later grab holds is not changed.
func (l *leases) release(ctx context.Context) error {
	l.mu.Lock()
	unstarted := l.unstarted
	l.unstarted = map[int64][]int64{}
	l.mu.Unlock()
	for consumerID, ids := range unstarted {
		err := l.q.write(func() error {
			return l.q.ReleaseJobs(ctx, ReleaseJobsParams{ConsumerID: consumerID, Ids: ids})
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (l *leases) renew(ctx context.Context) error {
	now := time.Now().Unix()
	byConsumer := map[int64][]int64{}
	l.mu.Lock()
	for id, w := range l.waiting {
		if w.fetchedAt < now-l.timeout {
			// already resettable: another consumer may hold it now
			delete(l.waiting, id)
			continue
		}
		byConsumer[w.consumerID] = append(byConsumer[w.consumerID], id)
	}
	l.mu.Unlock()

	for consumerID, ids := range byConsumer {
		var renewed []int64
		err := l.q.write(func() (err error) {
			renewed, err = l.q.RenewJobs(ctx, RenewJobsParams{RenewedAt: now, ConsumerID: consumerID, Ids: ids})
			return err
		})
		if err != nil {
			return err
		}

		isRenewed := make(map[int64]bool, len(renewed))
		for _, id := range renewed {
			isRenewed[id] = true
		}
		l.mu.Lock()
		for _, id := range ids {
			if w, ok := l.waiting[id]; !ok || w.consumerID != consumerID {
				// started during the renewal
				continue
			}
			if isRenewed[id] {
				l.waiting[id] = lease{consumerID, now}
			} else {
				delete(l.waiting, id)
			}
		}
		l.mu.Unlock()
	}
	return nil
}

// acks groups completions. Each SQLite commit is costly and serialized, so the
// workers that ack at the same time are written in one UPDATE. A worker is
// released only once its job is committed. Under low load a batch is a single
// id and no latency is added.
type acks struct {
	q       *Queue
	pending chan ack
	flushed chan struct{}
}

type ack struct {
	id         int64
	consumerID int64
	done       chan error
}

func (q *Queue) newAcks(ctx context.Context, buffer int) *acks {
	a := &acks{q: q, pending: make(chan ack, buffer), flushed: make(chan struct{})}
	// acks still in flight must be saved after ctx is canceled
	go a.run(context.WithoutCancel(ctx))
	return a
}

// send blocks until the job is committed.
func (a *acks) send(job *Job) error {
	done := make(chan error, 1)
	a.pending <- ack{job.ID, job.ConsumerID, done}
	return <-done
}

// close waits for the pending acks. Call it once no worker can send.
func (a *acks) close() {
	close(a.pending)
	<-a.flushed
}

func (a *acks) run(ctx context.Context) {
	defer close(a.flushed)
	var batch []ack
	for c := range a.pending {
		batch = append(batch[:0], c)
	drain:
		for {
			select {
			case c, ok := <-a.pending:
				if !ok {
					break drain
				}
				batch = append(batch, c)
			default:
				break drain
			}
		}
		// one UPDATE per grab, usually 1 or 2 for a batch
		byConsumer := map[int64][]int64{}
		for _, c := range batch {
			byConsumer[c.consumerID] = append(byConsumer[c.consumerID], c.id)
		}
		errs := map[int64]error{}
		for consumerID, ids := range byConsumer {
			// on error, the visibility timeout puts the jobs back, as with CompleteJob before
			errs[consumerID] = a.q.write(func() error {
				return a.q.CompleteJobs(ctx, CompleteJobsParams{ConsumerID: consumerID, Ids: ids})
			})
		}
		for _, c := range batch {
			c.done <- errs[c.consumerID]
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
