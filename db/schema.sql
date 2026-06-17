PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS jobs (
    id INTEGER NOT NULL,
    queue TEXT NOT NULL,
    job TEXT NOT NULL,
    job_status TEXT NOT NULL DEFAULT 'queued',
    execute_after INTEGER NOT NULL DEFAULT 0,
    remaining_attempts INTEGER NOT NULL DEFAULT 1,
    consumer_fetched_at INTEGER NOT NULL DEFAULT 0,
    finished_at INTEGER NOT NULL DEFAULT 0,
    deduping_key TEXT NOT NULL DEFAULT '',
    errors TEXT NOT NULL DEFAULT "[]",
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS todo ON jobs (queue, job_status, execute_after)
WHERE
    job_status = 'queued'
    OR job_status = 'fetched';

CREATE UNIQUE INDEX IF NOT EXISTS dedupe ON jobs (deduping_key, job_status)
WHERE
    deduping_key != ''
    AND job_status = 'queued';
