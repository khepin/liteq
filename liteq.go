package liteq

import (
	"context"
	"database/sql"

	"github.com/khepin/liteq/internal"
)

// Creates the db file with the tables and indexes
func Setup(db *sql.DB) error {
	_, err := db.Exec(internal.Schema)
	return err
}

func New(db *sql.DB) *JobQueue {
	queries := internal.New(db)
	return &JobQueue{queries}
}

type JobQueue struct {
	queries *internal.Queries
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
