package db

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var fs embed.FS

func Migrate(direction string, db *sql.DB) error {
	goose.SetBaseFS(fs)
	if err := goose.SetDialect("sqlite"); err != nil {
		return fmt.Errorf("error setting dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("error doing up migration: %w", err)
	}
	return nil
}
