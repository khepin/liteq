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

	queue := newQueue[testJobItem](jqeue, "t1", JSONMarshaler[testJobItem]{})

	inChan := make(chan testJobItem, 1)
	queue.Consume(ctx, inChan)

	err = queue.Put(ctx, testJobItem{ID: 5})
	require.NoError(t, err)

	receivedItem := <-inChan
	assert.Equal(t, 5, receivedItem.ID)
}
