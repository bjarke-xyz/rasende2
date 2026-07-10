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

var connections map[string]*sql.DB = make(map[string]*sql.DB)
var lock sync.RWMutex

func Open(connStringer ConnectionStringer) (*sql.DB, error) {
	lock.Lock()
	defer lock.Unlock()
	existingDb, ok := connections[connStringer.ConnectionString()]
	if ok {
		return existingDb, nil
	} else {
		db, err := sql.Open("sqlite", connStringer.ConnectionString())
		if err != nil {
			return nil, fmt.Errorf("failed to connect to db: %w", err)
		}
		connections[connStringer.ConnectionString()] = db
		return db, nil
	}
}

func OpenQueries(connStringer ConnectionStringer) (*Queries, error) {
	db, err := Open(connStringer)
	if err != nil {
		return nil, err
	}
	return NewQueries(db), nil
}
