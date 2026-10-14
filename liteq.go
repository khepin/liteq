package liteq

import (
	"context"
	"database/sql"

	"github.com/khepin/liteq/internal"
)

// Creates the db file with the tables and indexes
func Setup(db *sql.DB) error {
	_, err := db.Exec(internal.Schema)
	if err != nil {
		return err
	}

	// tables created before consumer_id existed
	var hasConsumerID bool
	err = db.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('jobs') WHERE name = 'consumer_id'`).Scan(&hasConsumerID)
	if err != nil || hasConsumerID {
		return err
	}
	_, err = db.Exec(`ALTER TABLE jobs ADD COLUMN consumer_id INTEGER NOT NULL DEFAULT 0`)
	return err
}

func New(db *sql.DB) *JobQueue {
	queries, err := internal.Prepare(context.Background(), db)
	if err != nil {
		// the jobs table does not exist yet, run the queries unprepared
		queries = internal.New(db)
	}
	return &JobQueue{internal.NewQueue(queries)}
}

// Releases the prepared statements
func (jq *JobQueue) Close() error {
	return jq.queries.Close()
}

type JobQueue struct {
	queries *internal.Queue
}

type QueueJobParams = internal.QueueJobParams
type DedupingKey = internal.DedupingKey
type IgnoreDuplicate = internal.IgnoreDuplicate
type ReplaceDuplicate = internal.ReplaceDuplicate

func (jq *JobQueue) QueueJob(ctx context.Context, params QueueJobParams) error {
	return jq.queries.QueueJob(ctx, params)
}

type ConsumeParams = internal.ConsumeParams

func (jq *JobQueue) Consume(ctx context.Context, params ConsumeParams) error {
	return jq.queries.Consume(ctx, params)
}

type ErrorList = internal.ErrorList
type Job = internal.Job
