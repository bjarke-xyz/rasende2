package db

import (
	"fmt"
	"sync"

	"github.com/bjarke-xyz/rasende2/internal/repository/db/dao"
	"github.com/jmoiron/sqlx"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
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
		db, err := sqlx.Open("libsql", connStringer.ConnectionString())
		if err != nil {
			return nil, fmt.Errorf("failed to connect to db: %w", err)
		}
		connections[connStringer.ConnectionString()] = db
		return db, nil
	}
}

func OpenQueries(connStringer ConnectionStringer) (*dao.Queries, error) {
	db, err := Open(connStringer)
	if err != nil {
		return nil, err
	}
	return dao.New(db), nil
}
