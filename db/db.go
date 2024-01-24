package db

import (
	"fmt"
	"sync"

	"github.com/jmoiron/sqlx"
)

type ConnectionStringer interface {
	ConnectionString() string
}

var connections map[string]*sqlx.DB = make(map[string]*sqlx.DB)
var lock sync.RWMutex

func Open(connStringer ConnectionStringer) (*sqlx.DB, error) {
	lock.Lock()
	defer lock.Unlock()
	existingDb, ok := connections[connStringer.ConnectionString()]
	if ok {
		return existingDb, nil
	} else {
		db, err := sqlx.Open("postgres", connStringer.ConnectionString())
		if err != nil {
			return nil, fmt.Errorf("failed to connect to db: %w", err)
		}
		connections[connStringer.ConnectionString()] = db
		return db, nil
	}
}
