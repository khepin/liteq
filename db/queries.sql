-- name: doQueueJobIgnoreDupe :exec
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
    AND job_status = 'queued' DO NOTHING;

-- name: doQueueJobReplaceDupe :exec
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
    remaining_attempts = EXCLUDED.remaining_attempts;

-- name: CompleteJob :exec
UPDATE
    jobs
SET
    job_status = 'completed',
    finished_at = unixepoch(),
    updated_at = unixepoch(),
    consumer_fetched_at = 0,
    remaining_attempts = 0
WHERE
    id = ?;

-- name: FailJob :exec
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
    id = ?;

-- name: MarkJobsForConsumer :many
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
    ) RETURNING *;

-- name: ResetJobs :execrows
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
    AND consumer_fetched_at < ?;

-- name: FindJob :one
SELECT
    *
FROM
    jobs
WHERE
    id = ?;
