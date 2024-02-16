# liteq

Library to have a persistent job queue in Go backed by a SQLite DB.

## Motivation

I needed a way to have a persistent queue for a small app so it would survive restarts and allow me to schedule jobs in the future.
Since I already had SQLite as a dependency, I didn't want to add another dependency to make the app work. Especially not an external one like RabbitMQ, Redis, SQS or others.

liteq allows to run tens of thousands of jobs per second if needed. It can also be made to use more than a single DB file to keep growing the concurrency should you need it.

## Usage

### Install

```sh
go get github.com/khepin/liteq
```

### Setup and DB creation

```go
import (
    "github.com/khepin/liteq"
    _ "github.com/mattn/go-sqlite3"
)

func main() {
    // Open the sqlite3 DB in the file "liteq.db"
    liteqDb, err := sql.Open("sqlite3", "liteq.db")
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
    // Create the DB table if it doesn't exist
    liteq.Setup(liteqDb)
    // create a job queue
    jqueue := liteq.New(liteqDb)
}
```

### Queuing a job

```go
jqueue.QueueJob(context.Background(), liteq.QueueJobParams{
    Queue:             "notify.email",
    Job:               `{"email_address": "bob@example.com", "content": "..."}`,
})
```

This will send the job with the given payload on a queue called `notify.email`.

### Consuming

To consume jobs from the queue, you call the consume method:

```go
jqueue.Consume(context.Background(), liteq.ConsumeParams{
    Queue:             "notify.email",
    PoolSize:          3,
    VisibilityTimeout: 20,
    Worker:            func (ctx context.Context, job *liteq.Job) error {
        return sendEmail(job)
    },
})
```

- `context.Background()` You can pass in a cancellable context and the queue will stop processing when the context is canceled
- `Queue` this is the name of the queue we want this consumer to consume from
- `PoolSize` this is the number of concurrent consumer we want for this queue
- `VisibilityTimeout` is the time that a job will remain reserved to a consumer. After that time has elapsed, if the job hasn't been marked either failed
or successful, it will be put back in the queue for others to consume. The error will be added to the job's error list and the number of remaining attempts will be
decreased.
- `Worker` A callback to process the job. When an error is returned, the job is either returned to the queue for processing with a decreased number of remaining attempts or marked as failed if no more attempts remain.

### Multiple attempts

When queueing a job, it is possible to decide how many times this job can be attempted in case of failures:

```go
jqueue.QueueJob(context.Background(), liteq.QueueJobParams{
    Queue:             "notify.email",
    RemainingAttempts: 3,
    Job:               `{"email_address": "bob@example.com", "content": "..."}`,
})
```

### Delayed jobs

When queueing a job, you can decide to execute it at a later point in time:

```go
jqueue.QueueJob(context.Background(), liteq.QueueJobParams{
    Queue:             "notify.email",
    ExecuteAfter:      time.Now().Add(6*time.Minute).Unix(),
    Job:               `{"email_address": "bob@example.com", "content": "..."}`,
})
```

In this case, the job won't run until the given time.

### Deduplication

Sometimes it can be useful to prevent the queueing of multiple messages that would essentially be performing the same task to avoid un-necessary work.

This is possible in `liteq` via the `DedupingKey` job parameter. There are 2 types of deduping keys:

- `IgnoreDuplicate` will ignore the new job that was sent and keep the one that was already on the queue
- `ReplaceDuplicate` will instead remove the job currently on the queue and use the new one instead

Assuming we have the following consumer:

```go
jqueue.Consume(context.Background(), liteq.ConsumeParams{
    Queue:             "print",
    VisibilityTimeout: 20,
    Worker:            func (ctx context.Context, job *liteq.Job) error {
        fmt.Println(job.Payload)
        return nil
    },
})
```

And we send the following jobs:

```go
jqueue.QueueJob(context.Background(), liteq.QueueJobParams{
    Queue:             "print",
    Job:               `first`,
    DedupingKey:       liteq.IgnoreDuplicate("print.job")
})
jqueue.QueueJob(context.Background(), liteq.QueueJobParams{
    Queue:             "print",
    Job:               `second`,
    DedupingKey:       liteq.IgnoreDuplicate("print.job")
})
```

Then the result would be a single output line:

```
first
```

If instead we use `liteq.ReplaceDuplicate`

```go
jqueue.QueueJob(context.Background(), liteq.QueueJobParams{
    Queue:             "print",
    Job:               `third`,
    DedupingKey:       liteq.ReplaceDuplicate("print.job")
})
jqueue.QueueJob(context.Background(), liteq.QueueJobParams{
    Queue:             "print",
    Job:               `fourth`,
    DedupingKey:       liteq.ReplaceDuplicate("print.job")
})
```

We will output `fourth`

If we think for example of the scenario of sending email or text notifications about an order to a customer, we could construct a deduping key like:

```go
jqueue.QueueJob(context.Background(), liteq.QueueJobParams{
    Queue:             "email.notify",
    Job:               `{"order_id": 123, "customer_id": "abc"}`,
    DedupingKey:       liteq.ReplaceDuplicate("email.notify:%s:%s", customer.ID, order.ID)
})
```

That way if the `Order Prepared` status update email hasn't been sent yet by the time we're ready to send the `Order Shipped` email, we can skip the `Order Prepared` one and only send the most recent update to the customer.
