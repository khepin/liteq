package main

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"

	"github.com/khepin/liteq"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	howMany := 100_000

	go func() {
		db, err := sql.Open("sqlite3", "bench.db")
		if err != nil {
			panic(err)
		}
		liteq.Setup(db)

		jq := liteq.New(db)

		for i := 0; i < howMany; i++ {
			jq.QueueJob(context.Background(), liteq.QueueJobParams{
				Queue: fmt.Sprintf("default-%d", i%30),
				Job:   "SendEmail",
			})
		}
	}()

	c := make(chan struct{})
	for i := 0; i < 30; i++ {
		db, err := sql.Open("sqlite3", "bench.db")
		if err != nil {
			panic(err)
		}
		liteq.Setup(db)

		jq := liteq.New(db)

		go func(i int) {
			err := jq.Consume(context.Background(), liteq.ConsumeParams{
				Queue:    fmt.Sprintf("default-%d", i),
				PoolSize: 10,
				Worker: func(ctx context.Context, job *liteq.Job) error {
					// random sleep
					n := rand.Intn(50)

					time.Sleep(time.Duration(n) * time.Millisecond)
					c <- struct{}{}
					return nil
				},
			})
			fmt.Println(err)
		}(i)
	}

	rec := 0
	for range c {
		rec++
		if rec%1000 == 0 {
			fmt.Println(rec)
		}
		if rec == howMany {
			return
		}
	}
}
