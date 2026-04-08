package db

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open creates a GORM database connection for the given SQLite DSN path.
// The parent directory is created if it does not exist.
func Open(dsn string) (*gorm.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Warn),
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", dsn, err)
	}

	// Enable WAL mode for better concurrent read performance.
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	slog.Info("database opened", "dsn", dsn)
	return db, nil
}

// Close cleanly shuts down the underlying database connection pool.
// Important for SQLite WAL mode: a clean close triggers a final checkpoint.
func Close(database *gorm.DB) error {
	sqlDB, err := database.DB()
	if err != nil {
		return fmt.Errorf("getting underlying sql.DB: %w", err)
	}
	return sqlDB.Close()
}

// AutoMigrate creates or updates all application tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.Account{},
		&models.Region{},
		&models.Instance{},
		&models.Change{},
		&models.ChangeInstance{},
		&models.Tool{},
		&models.Script{},
		&models.ExecutionSession{},
		&models.ExecutionBatch{},
		&models.Execution{},
	)
}
