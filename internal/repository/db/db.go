package db

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

type ConnectionStringer interface {
	ConnectionString() string
}

var (
	readConnections  = make(map[string]*sql.DB)
	writeConnections = make(map[string]*sql.DB)
	lock             sync.Mutex
)

// Open returns the read pool, which can use multiple SQLite connections.
func Open(connStringer ConnectionStringer) (*sql.DB, error) {
	return open(connStringer, readConnections, 0)
}

// OpenWriter returns the single-connection write pool. All application writes
// must use it so SQLite writes from this process are serialized.
func OpenWriter(connStringer ConnectionStringer) (*sql.DB, error) {
	return open(connStringer, writeConnections, 1)
}

func open(connStringer ConnectionStringer, connections map[string]*sql.DB, maxOpenConnections int) (*sql.DB, error) {
	lock.Lock()
	defer lock.Unlock()
	connectionString := connStringer.ConnectionString()
	if existingDB, ok := connections[connectionString]; ok {
		return existingDB, nil
	}

	database, err := sql.Open("sqlite", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to db: %w", err)
	}
	if maxOpenConnections > 0 {
		database.SetMaxOpenConns(maxOpenConnections)
	}
	connections[connectionString] = database
	return database, nil
}
