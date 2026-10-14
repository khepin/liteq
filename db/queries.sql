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

-- name: CompleteJobs :exec
UPDATE
    jobs
SET
    job_status = 'completed',
    finished_at = unixepoch(),
    updated_at = unixepoch(),
    consumer_fetched_at = 0,
    remaining_attempts = 0
WHERE
    jobs.job_status = 'fetched'
    AND jobs.consumer_id = sqlc.arg('consumer_id')
    AND jobs.id IN (sqlc.slice('ids'));

-- name: FailJob :exec
UPDATE
    jobs
SET
    job_status = CASE
        WHEN remaining_attempts <= 1 THEN 'failed'
        -- if we can't put the job back on the queue due to the unique index
        -- on deduping key, fail this job. the next one is already queued and
        -- will pick up
        WHEN deduping_key != '' AND EXISTS (
            SELECT 1
            FROM jobs sib
            WHERE sib.deduping_key = jobs.deduping_key
            AND sib.deduping_key != ''
            AND sib.job_status = 'queued'
            AND sib.id != jobs.id
        ) THEN 'failed'
        ELSE 'queued'
    END,
    finished_at = 0,
    updated_at = unixepoch(),
    consumer_fetched_at = 0,
    remaining_attempts = MAX(remaining_attempts - 1, 0),
    errors = ?
WHERE
    jobs.id = ?
    AND jobs.job_status = 'fetched'
    AND jobs.consumer_id = ?;

-- name: MarkJobsForConsumer :many
UPDATE
    jobs
SET
    consumer_id = ?,
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
            AND (
                js.deduping_key = ''
                OR NOT EXISTS (
                    SELECT 1
                    FROM jobs jd
                    WHERE jd.deduping_key = js.deduping_key
                    AND jd.deduping_key != '' -- ensure we match the index
                    AND jd.job_status = 'fetched'
                )
            )
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
        -- if we can't put the job back on the queue due to the unique index
        -- on deduping key, fail this job. the next one is already queued and
        -- will pick up
        WHEN deduping_key != '' AND EXISTS (
            SELECT 1
            FROM jobs sibling
            WHERE sibling.deduping_key = jobs.deduping_key
            AND sibling.deduping_key != ''
            AND sibling.job_status = 'queued'
            AND sibling.id != jobs.id
        ) THEN 'failed'
        ELSE 'queued'
    END,
    updated_at = unixepoch(),
    consumer_fetched_at = 0,
    remaining_attempts = MAX(remaining_attempts - 1, 0),
    errors = json_insert(errors, '$[#]', 'visibility timeout expired')
WHERE
    jobs.job_status = 'fetched'
    AND jobs.queue = ?
    AND jobs.consumer_fetched_at < ?;

-- name: RenewJobs :many
UPDATE
    jobs
SET
    consumer_fetched_at = sqlc.arg('renewed_at'),
    updated_at = unixepoch()
WHERE
    jobs.job_status = 'fetched'
    AND jobs.consumer_id = sqlc.arg('consumer_id')
    AND jobs.id IN (sqlc.slice('ids')) RETURNING id;

-- name: FindJob :one
SELECT
    *
FROM
    jobs
WHERE
    id = ?;
