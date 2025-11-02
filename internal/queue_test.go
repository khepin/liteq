package internal

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testJobItem struct {
	ID int
}

func TestBasicQueue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sqlite.db")
	defer os.Remove(dbPath)
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(Schema)
	require.NoError(t, err)

	jqeue := New(db)

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
