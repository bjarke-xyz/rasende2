package db

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

type ConnectionStringer interface {
	ConnectionString() string
}

var connections map[string]*sqlx.DB = make(map[string]*sqlx.DB)

func Open(connStringer ConnectionStringer) (*sqlx.DB, error) {
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
