package internal_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/khepin/liteq/internal"
	"github.com/matryer/is"
	_ "modernc.org/sqlite"
)

func TestMain(m *testing.M) {
	e := m.Run()

	removeFiles("jobs.db", "jobs.db-journal", "jobs.db-wal", "jobs.db-shm")
	os.Exit(e)
}

func removeFiles(files ...string) error {
	for _, file := range files {
		err := os.Remove(file)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func getDb(file string) (*sql.DB, error) {
	if file != ":memory:" {
		err := removeFiles(file, file+"-journal", file+"-wal", file+"-shm")
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	dsn := file + "?_pragma=busy_timeout(5000)"
	if file == ":memory:" {
		dsn = ":memory:?_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	schema, err := os.ReadFile("../db/schema.sql")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(string(schema))
	if err != nil {
		return nil, err
	}
	return db, nil
}

func getJqueue(file string) (*internal.Queries, error) {
	sqlcdb, err := getDb(file)
	if err != nil {
		return nil, err
	}
	return internal.New(sqlcdb), nil
}

func Test_QueueJob(t *testing.T) {
	is := is.New(t)
	sqlcdb, err := getDb("jobs.db")
	is.NoErr(err) // error opening sqlite3 database

	jqueue := internal.New(sqlcdb)
	jobPaylod := `{"type": "slack", "channel": "C01B2PZQZ3D", "text": "Hello world"}`
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue: "",
		Job:   jobPaylod,
	})
	is.NoErr(err) // error queuing job

	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 1) // expected 1 job
	is.Equal(jobs[0].Job, jobPaylod)
}

func Test_FetchTwice(t *testing.T) {
	is := is.New(t)
	sqlcdb, err := getDb("jobs.db")
	is.NoErr(err) // error opening sqlite3 database

	jqueue := internal.New(sqlcdb)
	jobPaylod := `{"type": "slack", "channel": "C01B2PZQZ3D", "text": "Hello world"}`
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue: "",
		Job:   jobPaylod,
	})
	is.NoErr(err) // error queuing job
	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 1) // expected 1 job
	is.Equal(jobs[0].Job, jobPaylod)

	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 0) // expected 0 job
}

// delay jobs, fetch jobs, check that we get no jobs, then check that we get the job after the delay
func Test_DelayedJob(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:        "",
		Job:          `{"type": "slack", "channel": "C01B2PZQZ3D", "text": "Hello world"}`,
		ExecuteAfter: time.Now().Unix() + 1,
	})
	is.NoErr(err) // error queuing job

	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 0) // expected 0 job

	time.Sleep(1 * time.Second)
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 1) // expected 1 job
}

func Test_PrefetchJobs(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue(":memory:")
	is.NoErr(err) // error getting job queue
	for i := 0; i < 10; i++ {

		err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
			Queue: "",
			Job:   `{"type": "slack", "channel": "C01B2PZQZ3D", "text": "Hello world"}`,
		})
		is.NoErr(err) // error queuing job
	}

	// grab the first 2
	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 2,
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 2) // expected 2 jobs

	// the next 6
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 6,
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 6) // expected 6 jobs

	// try for 5 but only 2 are left
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 5,
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 2) // expected 2 jobs
}

func Test_Consume(t *testing.T) {
	is := is.New(t)
	// This somehow doesn't work well with in memory db
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue

	fillq1 := make(chan struct{}, 1)
	go func() {
		for i := 0; i < 100; i++ {
			jobPayload := fmt.Sprintf("q1-%d", i)
			err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
				Queue: "q1",
				Job:   jobPayload,
			})

			is.NoErr(err) // error queuing job
		}
		fillq1 <- struct{}{}
		close(fillq1)
	}()

	fillq2 := make(chan struct{}, 1)
	go func() {
		for i := 0; i < 100; i++ {
			jobPayload := fmt.Sprintf("q2-%d", i)
			err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
				Queue: "q2",
				Job:   jobPayload,
			})

			is.NoErr(err) // error queuing job
		}
		fillq2 <- struct{}{}
		close(fillq2)
	}()

	q1 := make(chan string, 100)
	q1done := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		jqueue.Consume(ctx, internal.ConsumeParams{
			Queue:    "q1",
			PoolSize: 1,
			Worker: func(ctx context.Context, job *internal.Job) error {
				q1 <- job.Job
				if job.Job == "q1-99" {
					q1done <- struct{}{}
					close(q1done)
					close(q1)
				}
				return nil
			},
		})
	}()

	q2 := make(chan string, 100)
	q2done := make(chan struct{}, 1)
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() {
		jqueue.Consume(ctx2, internal.ConsumeParams{
			Queue:    "q2",
			PoolSize: 1,
			Worker: func(ctx context.Context, job *internal.Job) error {
				q2 <- job.Job
				if job.Job == "q2-99" {
					q2done <- struct{}{}
					close(q2done)
					close(q2)
				}
				return nil
			},
		})
	}()

	<-fillq1
	<-q1done
	cancel()
	<-fillq2
	<-q2done
	cancel2()

	i := 0
	for pl := range q1 {
		is.True(strings.HasPrefix(pl, "q1")) // expected q1-*
		i++
	}
	is.Equal(i, 100) // expected 100 jobs

	j := 0
	for pl := range q2 {
		is.True(strings.HasPrefix(pl, "q2")) // expected q2-*
		j++
	}
	is.Equal(j, 100) // expected 100 jobs
}

func Test_MultipleAttempts(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue

	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:             "",
		Job:               `{"type": "slack", "channel": "C01B2PZQZ3D", "text": "Hello world"}`,
		RemainingAttempts: 3,
	})
	is.NoErr(err) // error queuing job

	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})

	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 1) // expected 1 job

	thejob := jobs[0]
	is.Equal(thejob.RemainingAttempts, int64(3)) // expected 3 attempts

	// Fail and verify
	err = jqueue.FailJob(context.Background(), internal.FailJobParams{
		ID:     thejob.ID,
		Errors: internal.ErrorList{"error1"},
	})
	is.NoErr(err) // error failing job
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})
	is.NoErr(err)                                 // error fetching job for consumer
	is.Equal(len(jobs), 1)                        // expected 1 job
	is.Equal(jobs[0].RemainingAttempts, int64(2)) // expected 2 attempts
	is.Equal(jobs[0].Errors, internal.ErrorList{"error1"})

	// Fail again and verify
	err = jqueue.FailJob(context.Background(), internal.FailJobParams{
		ID:     thejob.ID,
		Errors: internal.ErrorList{"error1", "error2"},
	})
	is.NoErr(err) // error failing job
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})
	is.NoErr(err)                                 // error fetching job for consumer
	is.Equal(len(jobs), 1)                        // expected 1 job
	is.Equal(jobs[0].RemainingAttempts, int64(1)) // expected 1 attempts
	is.Equal(jobs[0].Errors, internal.ErrorList{"error1", "error2"})

	// Fail again and verify
	err = jqueue.FailJob(context.Background(), internal.FailJobParams{
		ID:     thejob.ID,
		Errors: internal.ErrorList{"error1", "error2", "error3"},
	})
	is.NoErr(err) // error failing job
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
	})
	is.NoErr(err)          // error fetching job for consumer
	is.Equal(len(jobs), 0) // expected no job since no more attempts
}

func Test_VisibilityTimeout(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue

	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue: "",
		Job:   `{"type": "slack", "channel": "C01B2PZQZ3D", "text": "Hello world"}`,
	})
	is.NoErr(err) // error queuing job

	ctx, cancel := context.WithCancel(context.Background())
	id := int64(0)
	go func() {
		jqueue.Consume(ctx, internal.ConsumeParams{
			Queue:             "",
			PoolSize:          1,
			VisibilityTimeout: 1,
			OnEmptySleep:      100 * time.Millisecond,
			Worker: func(ctx context.Context, job *internal.Job) error {
				id = job.ID
				time.Sleep(3 * time.Second)
				return nil
			},
		})
	}()

	time.Sleep(2 * time.Second)
	cancel()
	job, err := jqueue.FindJob(context.Background(), id)

	is.NoErr(err)                     // error fetching job for consumer
	is.Equal(job.JobStatus, "failed") // expected fetched
	is.Equal(job.Errors, internal.ErrorList{"visibility timeout expired"})
	// Sleep to ensure enough time for the job to finish and avoid panics
	time.Sleep(2 * time.Second)
}

func Test_DedupeIgnore(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "",
		Job:         `job:1`,
		DedupingKey: internal.IgnoreDuplicate("dedupe"),
	})
	is.NoErr(err) // error queuing job

	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "",
		Job:         `job:2`,
		DedupingKey: internal.IgnoreDuplicate("dedupe"),
	})
	is.NoErr(err) // error queuing job

	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)                  // error fetching job for consumer
	is.Equal(len(jobs), 1)         // expected only 1 job due to dedupe
	is.Equal(jobs[0].Job, `job:1`) // expected job:1
}

// A job whose deduping key matches an already-fetched (in-flight) job must not
// be grabbed until that in-flight job finishes. The key doubles as a concurrency key.
func Test_DedupeConcurrencyKey(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue

	// Queue and grab job:1 — it is now 'fetched' (in flight).
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "",
		Job:         `job:1`,
		DedupingKey: internal.IgnoreDuplicate("dedupe"),
	})
	is.NoErr(err) // error queuing job:1

	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)                  // error grabbing job:1
	is.Equal(len(jobs), 1)         // expected job:1 fetched
	is.Equal(jobs[0].Job, `job:1`) // expected job:1
	job1 := jobs[0]

	// job:1 is 'fetched' (not 'queued'), so the queued-uniqueness index lets job:2
	// in with the same key.
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "",
		Job:         `job:2`,
		DedupingKey: internal.IgnoreDuplicate("dedupe"),
	})
	is.NoErr(err) // error queuing job:2

	// job:2 must stay invisible while job:1 is in flight.
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)          // error grabbing while in flight
	is.Equal(len(jobs), 0) // expected no job: same key still in flight

	// Once job:1 completes, job:2 becomes grabbable.
	err = jqueue.CompleteJob(context.Background(), job1.ID)
	is.NoErr(err) // error completing job:1

	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)                  // error grabbing job:2
	is.Equal(len(jobs), 1)         // expected job:2 now available
	is.Equal(jobs[0].Job, `job:2`) // expected job:2
}

// A job that fails with retries left while a same-key sibling is already queued
// must not collide with the dedupe unique index. It yields to the sibling
// (goes terminal) instead of re-queuing, and the sibling stays grabbable.
func Test_DedupeRetryYieldsToSibling(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue

	// Queue and grab job:1 with retries left — now in flight.
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:             "",
		Job:               `job:1`,
		DedupingKey:       internal.IgnoreDuplicate("dedupe"),
		RemainingAttempts: 3,
	})
	is.NoErr(err) // error queuing job:1

	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)          // error grabbing job:1
	is.Equal(len(jobs), 1) // expected job:1 fetched
	job1 := jobs[0]

	// Queue job:2 with the same key while job:1 is in flight.
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "",
		Job:         `job:2`,
		DedupingKey: internal.IgnoreDuplicate("dedupe"),
	})
	is.NoErr(err) // error queuing job:2

	// Fail job:1. It has attempts left, but a queued sibling exists, so it must
	// yield (go to 'failed') rather than re-queue and violate the dedupe index.
	err = jqueue.FailJob(context.Background(), internal.FailJobParams{
		ID:     job1.ID,
		Errors: internal.ErrorList{"boom"},
	})
	is.NoErr(err) // FailJob must not hit a unique constraint violation

	failed, err := jqueue.FindJob(context.Background(), job1.ID)
	is.NoErr(err)                        // error finding job:1
	is.Equal(failed.JobStatus, "failed") // job:1 yielded to the sibling
	is.Equal(failed.Errors, internal.ErrorList{"boom"})

	// job:1 is no longer fetched, so the sibling job:2 is now grabbable.
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)                  // error grabbing job:2
	is.Equal(len(jobs), 1)         // expected job:2 available
	is.Equal(jobs[0].Job, `job:2`) // expected job:2
}

// A visibility-timeout reset of a job whose same-key sibling is already queued
// must yield (go terminal) instead of re-queuing into a dedupe collision.
func Test_DedupeResetYieldsToSibling(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue

	// Queue and grab job:1 with retries left — now in flight (fetched).
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:             "",
		Job:               `job:1`,
		DedupingKey:       internal.IgnoreDuplicate("dedupe"),
		RemainingAttempts: 3,
	})
	is.NoErr(err) // error queuing job:1

	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)          // error grabbing job:1
	is.Equal(len(jobs), 1) // expected job:1 fetched
	job1 := jobs[0]

	// Queue job:2 with the same key while job:1 is in flight.
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "",
		Job:         `job:2`,
		DedupingKey: internal.IgnoreDuplicate("dedupe"),
	})
	is.NoErr(err) // error queuing job:2

	// Reset with a cutoff in the future so job:1 counts as timed out. With a
	// queued sibling present, it must yield to 'failed' rather than collide.
	rows, err := jqueue.ResetJobs(context.Background(), internal.ResetJobsParams{
		Queue:             "",
		ConsumerFetchedAt: time.Now().Unix() + 100,
	})
	is.NoErr(err)            // ResetJobs must not hit a unique constraint violation
	is.Equal(rows, int64(1)) // expected job:1 reset

	timedOut, err := jqueue.FindJob(context.Background(), job1.ID)
	is.NoErr(err)                          // error finding job:1
	is.Equal(timedOut.JobStatus, "failed") // job:1 yielded to the sibling
	is.Equal(timedOut.Errors, internal.ErrorList{"visibility timeout expired"})

	// The sibling job:2 is now grabbable.
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)                  // error grabbing job:2
	is.Equal(len(jobs), 1)         // expected job:2 available
	is.Equal(jobs[0].Job, `job:2`) // expected job:2
}

// The deduping/concurrency key is scoped per queue: the same key in two
// different queues must not dedupe against each other, and an in-flight job in
// one queue must not block a same-key job in another queue.
func Test_DedupeScopedToQueue(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue

	// Same key, different queues — both must be accepted (no cross-queue dedupe).
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "emails",
		Job:         `email`,
		DedupingKey: internal.IgnoreDuplicate("user:42"),
	})
	is.NoErr(err) // error queuing emails job
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "billing",
		Job:         `bill`,
		DedupingKey: internal.IgnoreDuplicate("user:42"),
	})
	is.NoErr(err) // error queuing billing job

	// Grab the emails job — it is now in flight with key user:42.
	emails, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "emails",
		Count: 10,
	})
	is.NoErr(err)            // error grabbing emails
	is.Equal(len(emails), 1) // expected the emails job
	is.Equal(emails[0].Job, `email`)

	// The billing job has the same key but a different queue, so the in-flight
	// emails job must not block it.
	billing, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "billing",
		Count: 10,
	})
	is.NoErr(err)             // error grabbing billing
	is.Equal(len(billing), 1) // expected the billing job, not blocked cross-queue
	is.Equal(billing[0].Job, `bill`)
}

// Jobs without a deduping key have no concurrency relationship: one being in
// flight must not block grabbing the others.
func Test_ConcurrencyEmptyKeyNotBlocked(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue

	for i := 0; i < 2; i++ {
		err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
			Queue: "",
			Job:   fmt.Sprintf("job:%d", i),
		})
		is.NoErr(err) // error queuing job
	}

	// Grab one — it is now in flight with an empty key.
	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 1,
	})
	is.NoErr(err)          // error grabbing first job
	is.Equal(len(jobs), 1) // expected 1 job

	// The other empty-key job must still be grabbable.
	jobs, err = jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)          // error grabbing second job
	is.Equal(len(jobs), 1) // expected the other job, not blocked by the in-flight one
}

func Test_DedupeReplace(t *testing.T) {
	is := is.New(t)
	jqueue, err := getJqueue("jobs.db")
	is.NoErr(err) // error getting job queue
	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "",
		Job:         `job:1`,
		DedupingKey: internal.ReplaceDuplicate("dedupe"),
	})
	is.NoErr(err) // error queuing job

	err = jqueue.QueueJob(context.Background(), internal.QueueJobParams{
		Queue:       "",
		Job:         `job:2`,
		DedupingKey: internal.ReplaceDuplicate("dedupe"),
	})
	is.NoErr(err) // error queuing job

	jobs, err := jqueue.GrabJobs(context.Background(), internal.GrabJobsParams{
		Queue: "",
		Count: 10,
	})
	is.NoErr(err)                  // error fetching job for consumer
	is.Equal(len(jobs), 1)         // expected only 1 job due to dedupe
	is.Equal(jobs[0].Job, `job:2`) // expected job:1
}
