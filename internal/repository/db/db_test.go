package db

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

type testConnectionString string

func (s testConnectionString) ConnectionString() string { return string(s) }

func TestOpenWriterSerializesConcurrentWrites(t *testing.T) {
	connString := testConnectionString("file:" + filepath.Join(t.TempDir(), "test.db"))
	database, err := OpenWriter(connString)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if got := database.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("writer max open connections = %d, want 1", got)
	}
	readDB, err := Open(connString)
	if err != nil {
		t.Fatal(err)
	}
	defer readDB.Close()
	if readDB == database {
		t.Fatal("read and write pools should be separate")
	}
	if got := readDB.Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("read pool max open connections = %d, want unlimited (0)", got)
	}
	firstRead, err := readDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer firstRead.Rollback()
	secondRead, err := readDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer secondRead.Rollback()
	if got := readDB.Stats().OpenConnections; got < 2 {
		t.Fatalf("read pool open connections = %d, want at least 2 concurrent connections", got)
	}
	if _, err := database.Exec("CREATE TABLE writes (value INTEGER)"); err != nil {
		t.Fatal(err)
	}

	const writes = 32
	var wg sync.WaitGroup
	errs := make(chan error, writes)
	for i := 0; i < writes; i++ {
		wg.Add(1)
		go func(value int) {
			defer wg.Done()
			_, err := database.Exec("INSERT INTO writes (value) VALUES (?)", value)
			if err != nil {
				errs <- fmt.Errorf("insert %d: %w", value, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM writes").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != writes {
		t.Fatalf("inserted %d rows, want %d", count, writes)
	}
}
