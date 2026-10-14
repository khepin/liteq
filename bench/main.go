package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/khepin/liteq"
	_ "modernc.org/sqlite"
)

func main() {
	howMany := flag.Int("jobs", 100_000, "number of jobs")
	queues := flag.Int("queues", 30, "number of queues")
	poolSize := flag.Int("pool", 10, "workers per queue")
	maxSleep := flag.Int("sleep", 0, "max random worker sleep in ms")
	sync := flag.String("sync", "NORMAL", "PRAGMA synchronous")
	maxConns := flag.Int("conns", 1, "max open connections, 0 for unlimited")
	prefill := flag.Bool("prefill", false, "enqueue all jobs before consuming")
	flag.Parse()

	db, err := sql.Open("sqlite", "file:bench.db?_pragma=busy_timeout(10000)&_pragma=synchronous("+*sync+")")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(*maxConns)
	if err := liteq.Setup(db); err != nil {
		panic(err)
	}
	jq := liteq.New(db)

	start := time.Now()
	enqueued := make(chan struct{})
	go func() {
		for i := 0; i < *howMany; i++ {
			err := jq.QueueJob(context.Background(), liteq.QueueJobParams{
				Queue: fmt.Sprintf("default-%d", i%*queues),
				Job:   "SendEmail",
			})
			if err != nil {
				panic(err)
			}
		}
		fmt.Printf("enqueued %d in %s (%.0f jobs/s)\n", *howMany, time.Since(start), float64(*howMany)/time.Since(start).Seconds())
		close(enqueued)
	}()

	if *prefill {
		<-enqueued
		start = time.Now()
	}

	var done atomic.Int64
	for i := 0; i < *queues; i++ {
		go func(i int) {
			err := jq.Consume(context.Background(), liteq.ConsumeParams{
				Queue:        fmt.Sprintf("default-%d", i),
				PoolSize:     *poolSize,
				OnEmptySleep: 10 * time.Millisecond,
				Worker: func(ctx context.Context, job *liteq.Job) error {
					if *maxSleep > 0 {
						time.Sleep(time.Duration(rand.Intn(*maxSleep)) * time.Millisecond)
					}
					done.Add(1)
					return nil
				},
			})
			panic(err)
		}(i)
	}

	for done.Load() < int64(*howMany) {
		time.Sleep(time.Millisecond)
	}
	fmt.Printf("consumed %d in %s (%.0f jobs/s)\n", *howMany, time.Since(start), float64(*howMany)/time.Since(start).Seconds())
}
