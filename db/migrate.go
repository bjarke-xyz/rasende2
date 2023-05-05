package db

import (
	"errors"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func Migrate(direction string, dbConnStr string) error {
	log.Println(dbConnStr)
	m, err := migrate.New("file://migrations", dbConnStr)
	if err != nil {
		return fmt.Errorf("failed to load migration files: %w", err)
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
