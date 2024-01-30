package db

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var fs embed.FS

func Migrate(direction string, dbConnStr string) error {
	d, err := iofs.New(fs, "migrations")
	if err != nil {
		return fmt.Errorf("failed to load migration files: %w", err)
	}

	dbConnStr = fmt.Sprintf("sqlite3://%v", dbConnStr)
	m, err := migrate.NewWithSourceInstance("iofs", d, dbConnStr)
	if err != nil {
		return fmt.Errorf("failed create new source instance: %w", err)
	}

	migrateMethod := m.Up

	if direction == "down" {
		migrateMethod = m.Down
	}
	if err := migrateMethod(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to migrate %v: %w", direction, err)
	}
	return nil
}
