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
	_ "github.com/mattn/go-sqlite3"
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

	db, err := sql.Open("sqlite3", file)
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
