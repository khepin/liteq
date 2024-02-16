// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0
// source: queries.sql

package internal

import (
	"context"
)

const completeJob = `-- name: CompleteJob :exec
UPDATE
    jobs
SET
    job_status = 'completed',
    finished_at = unixepoch(),
    updated_at = unixepoch(),
    consumer_fetched_at = 0,
    remaining_attempts = 0
WHERE
    id = ?
`

func (q *Queries) CompleteJob(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx, completeJob, id)
	return err
}

const failJob = `-- name: FailJob :exec
UPDATE
    jobs
SET
    job_status = CASE
        WHEN remaining_attempts <= 1 THEN 'failed'
        ELSE 'queued'
    END,
    finished_at = 0,
    updated_at = unixepoch(),
    consumer_fetched_at = 0,
    remaining_attempts = MAX(remaining_attempts - 1, 0),
    errors = ?
WHERE
    id = ?
`

type FailJobParams struct {
	Errors ErrorList
	ID     int64
}

func (q *Queries) FailJob(ctx context.Context, arg FailJobParams) error {
	_, err := q.db.ExecContext(ctx, failJob, arg.Errors, arg.ID)
	return err
}

const findJob = `-- name: FindJob :one
SELECT
    id, queue, job, job_status, execute_after, remaining_attempts, consumer_fetched_at, finished_at, deduping_key, errors, created_at, updated_at
FROM
    jobs
WHERE
    id = ?
`

func (q *Queries) FindJob(ctx context.Context, id int64) (*Job, error) {
	row := q.db.QueryRowContext(ctx, findJob, id)
	var i Job
	err := row.Scan(
		&i.ID,
		&i.Queue,
		&i.Job,
		&i.JobStatus,
		&i.ExecuteAfter,
		&i.RemainingAttempts,
		&i.ConsumerFetchedAt,
		&i.FinishedAt,
		&i.DedupingKey,
		&i.Errors,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return &i, err
}

const markJobsForConsumer = `-- name: MarkJobsForConsumer :many
UPDATE
    jobs
SET
    consumer_fetched_at = unixepoch(),
    updated_at = unixepoch(),
    job_status = 'fetched'
WHERE
    jobs.job_status = 'queued'
    AND jobs.remaining_attempts > 0
    AND jobs.id IN (
        SELECT
            id
        FROM
            jobs js
        WHERE
            js.queue = ?
            AND js.job_status = 'queued'
            AND js.execute_after <= ?
            AND js.remaining_attempts > 0
        ORDER BY
            execute_after ASC
        LIMIT
            ?
    ) RETURNING id, queue, job, job_status, execute_after, remaining_attempts, consumer_fetched_at, finished_at, deduping_key, errors, created_at, updated_at
`

type MarkJobsForConsumerParams struct {
	Queue        string
	ExecuteAfter int64
	Limit        int64
}

func (q *Queries) MarkJobsForConsumer(ctx context.Context, arg MarkJobsForConsumerParams) ([]*Job, error) {
	rows, err := q.db.QueryContext(ctx, markJobsForConsumer, arg.Queue, arg.ExecuteAfter, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*Job
	for rows.Next() {
		var i Job
		if err := rows.Scan(
			&i.ID,
			&i.Queue,
			&i.Job,
			&i.JobStatus,
			&i.ExecuteAfter,
			&i.RemainingAttempts,
			&i.ConsumerFetchedAt,
			&i.FinishedAt,
			&i.DedupingKey,
			&i.Errors,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, &i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const resetJobs = `-- name: ResetJobs :execrows
UPDATE
    jobs
SET
    job_status = CASE
        WHEN remaining_attempts <= 1 THEN 'failed'
        ELSE 'queued'
    END,
    updated_at = unixepoch(),
    consumer_fetched_at = 0,
    remaining_attempts = MAX(remaining_attempts - 1, 0),
    errors = json_insert(errors, '$[#]', 'visibility timeout expired')
WHERE
    job_status = 'fetched'
    AND queue = ?
    AND consumer_fetched_at < ?
`

type ResetJobsParams struct {
	Queue             string
	ConsumerFetchedAt int64
}

func (q *Queries) ResetJobs(ctx context.Context, arg ResetJobsParams) (int64, error) {
	result, err := q.db.ExecContext(ctx, resetJobs, arg.Queue, arg.ConsumerFetchedAt)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

const doQueueJobIgnoreDupe = `-- name: doQueueJobIgnoreDupe :exec
INSERT INTO
    jobs (
        queue,
        job,
        execute_after,
        job_status,
        created_at,
        updated_at,
        remaining_attempts,
        deduping_key
    )
VALUES
    (
        ?,
        ?,
        ?,
        'queued',
        unixepoch(),
        unixepoch(),
        ?,
        ?
    ) ON CONFLICT (deduping_key, job_status)
WHERE
    deduping_key != ''
    AND job_status = 'queued' DO NOTHING
`

type doQueueJobIgnoreDupeParams struct {
	Queue             string
	Job               string
	ExecuteAfter      int64
	RemainingAttempts int64
	DedupingKey       string
}

func (q *Queries) doQueueJobIgnoreDupe(ctx context.Context, arg doQueueJobIgnoreDupeParams) error {
	_, err := q.db.ExecContext(ctx, doQueueJobIgnoreDupe,
		arg.Queue,
		arg.Job,
		arg.ExecuteAfter,
		arg.RemainingAttempts,
		arg.DedupingKey,
	)
	return err
}

const doQueueJobReplaceDupe = `-- name: doQueueJobReplaceDupe :exec
INSERT INTO
    jobs (
        queue,
        job,
        execute_after,
        job_status,
        created_at,
        updated_at,
        remaining_attempts,
        deduping_key
    )
VALUES
    (
        ?,
        ?,
        ?,
        'queued',
        unixepoch(),
        unixepoch(),
        ?,
        ?
    ) ON CONFLICT (deduping_key, job_status)
WHERE
    deduping_key != ''
    AND job_status = 'queued' DO
UPDATE
SET
    job = EXCLUDED.job,
    execute_after = EXCLUDED.execute_after,
    updated_at = unixepoch(),
    remaining_attempts = EXCLUDED.remaining_attempts
`

type doQueueJobReplaceDupeParams struct {
	Queue             string
	Job               string
	ExecuteAfter      int64
	RemainingAttempts int64
	DedupingKey       string
}

func (q *Queries) doQueueJobReplaceDupe(ctx context.Context, arg doQueueJobReplaceDupeParams) error {
	_, err := q.db.ExecContext(ctx, doQueueJobReplaceDupe,
		arg.Queue,
		arg.Job,
		arg.ExecuteAfter,
		arg.RemainingAttempts,
		arg.DedupingKey,
	)
	return err
}
