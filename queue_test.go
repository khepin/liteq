package liteq

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/khepin/liteq/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

type testJobItem struct {
	ID int
}

func TestBasicQueue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sqlite.db")
	defer os.Remove(dbPath)
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(internal.Schema)
	require.NoError(t, err)

	jqeue, err := New(db)
	require.NoError(t, err)

	ctx := context.Background()

	queue := NewQueue[testJobItem](jqeue, "t1", JSONMarshaler[testJobItem]{})

	inChan := make(chan testJobItem, 1)
	queue.ConsumeChan(ctx, inChan)

	err = queue.Put(ctx, testJobItem{ID: 5})
	require.NoError(t, err)

	receivedItem := <-inChan
	assert.Equal(t, 5, receivedItem.ID)
}

func TestGOBMarshaler(t *testing.T) {
	marshaler := GOBMarshaler[testJobItem]{}

	dataBytes, err := marshaler.Marshal(testJobItem{ID: 12})
	require.NoError(t, err)

	jobItem, err := marshaler.Unmarshal(dataBytes)
	require.NoError(t, err)
	assert.Equal(t, 12, jobItem.ID)
}

func TestAccessContextVars(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sqlite.db")
	defer os.Remove(dbPath)
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(internal.Schema)
	require.NoError(t, err)

	jqeue, err := New(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	queue := NewQueue(jqeue, "t1", JSONMarshaler[testJobItem]{})
	queue.Put(ctx, testJobItem{}, Retries(2))

	remainingAttemptsChan := make(chan interface{})

	go func() {
		queue.Consume(ctx, func(ctx context.Context, job testJobItem) error {
			remainingAttemptsChan <- ctx.Value(CtxJobRemainingAttempts).(int64)
			return nil
		})
	}()
	remainingAttemptsVal := <-remainingAttemptsChan
	remainingAttempts, ok := remainingAttemptsVal.(int64)
	require.True(t, ok)
	assert.Equal(t, int64(2), remainingAttempts)
}

func TestDelayFailedJobRetry(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sqlite.db")
	defer os.Remove(dbPath)
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(internal.Schema)
	require.NoError(t, err)

	jqeue, err := New(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	queue := NewQueue(jqeue, "t1", JSONMarshaler[testJobItem]{})
	queue.Put(ctx, testJobItem{}, Retries(2))

	execTimeChan := make(chan time.Time, 2)

	go func() {
		queue.Consume(ctx, func(ctx context.Context, job testJobItem) error {
			execTimeChan <- time.Now()
			return NewWorkerError(errors.New("test failure"), WithRetryDelay(time.Millisecond*500))
		})
	}()

	firstExecutionTime := <-execTimeChan
	secondExecutionTime := <-execTimeChan
	executionDelay := secondExecutionTime.Sub(firstExecutionTime)
	assert.GreaterOrEqual(t, executionDelay, time.Millisecond*500)
}

func BenchmarkJSONQueue(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "sqlite.db")
	defer os.Remove(dbPath)
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(b, err)
	_, err = db.Exec(internal.Schema)
	require.NoError(b, err)

	jqeue, err := New(db)
	require.NoError(b, err)
	queue := NewQueue[*testJobItem](jqeue, "t1", JSONMarshaler[*testJobItem]{})
	b.ReportAllocs()
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		maxItems := 10000
		go func(queue *Queue[*testJobItem]) {
			for i := 0; i < maxItems; i++ {
				ctx := context.Background()
				queue.Put(ctx, &testJobItem{
					ID: i,
				})
			}
		}(queue)
		queue.Consume(ctx, func(ctx context.Context, job *testJobItem) error {
			if job.ID == maxItems-1 {
				cancel()
			}
			return nil
		}, PoolSize(5), OnEmptySleep(time.Millisecond*10))
	}
}

func BenchmarkGOBQueue(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "sqlite.db")
	defer os.Remove(dbPath)
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(b, err)
	_, err = db.Exec(internal.Schema)
	require.NoError(b, err)

	jqeue, err := New(db)
	require.NoError(b, err)
	queue := NewQueue[*testJobItem](jqeue, "t1", GOBMarshaler[*testJobItem]{})
	b.ReportAllocs()
	for b.Loop() {

		ctx, cancel := context.WithCancel(context.Background())
		maxItems := 10000
		go func(queue *Queue[*testJobItem]) {
			for i := 0; i < maxItems; i++ {
				ctx := context.Background()
				queue.Put(ctx, &testJobItem{
					ID: i,
				})
			}
		}(queue)
		queue.Consume(ctx, func(ctx context.Context, job *testJobItem) error {
			if job.ID == maxItems-1 {
				cancel()
			}
			return nil
		}, PoolSize(5), OnEmptySleep(time.Millisecond*10))
	}
}
