package liteq_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/khepin/liteq"
	"github.com/matryer/is"
	_ "modernc.org/sqlite"
)

// A jobs table created before consumer_id existed gets the column on Setup.
func Test_SetupAddsConsumerID(t *testing.T) {
	is := is.New(t)
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	is.NoErr(err) // error opening db

	_, err = db.Exec(`CREATE TABLE jobs (
    id INTEGER NOT NULL,
    queue TEXT NOT NULL,
    job TEXT NOT NULL,
    job_status TEXT NOT NULL DEFAULT 'queued',
    execute_after INTEGER NOT NULL DEFAULT 0,
    remaining_attempts INTEGER NOT NULL DEFAULT 1,
    consumer_fetched_at INTEGER NOT NULL DEFAULT 0,
    finished_at INTEGER NOT NULL DEFAULT 0,
    deduping_key TEXT NOT NULL DEFAULT '',
    errors TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (id)
)`)
	is.NoErr(err) // error creating old table

	is.NoErr(liteq.Setup(db)) // error migrating
	is.NoErr(liteq.Setup(db)) // Setup must be idempotent

	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name = 'consumer_id'`).Scan(&n)
	is.NoErr(err)  // error reading columns
	is.Equal(n, 1) // expected consumer_id column
}
