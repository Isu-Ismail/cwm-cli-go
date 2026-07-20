package db

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// GetDBPath returns the global path ~/.cwm/cwm.db
func GetDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dbFolder := filepath.Join(home, ".cwm")
	if err := os.MkdirAll(dbFolder, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dbFolder, "cwm.db"), nil
}

// InitDB initializes connection and runs schema migrations
func InitDB() (*sql.DB, error) {
	path, err := GetDBPath()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	queries := []string{
		`CREATE TABLE IF NOT EXISTS saved_commands (
			variable TEXT PRIMARY KEY,
			command TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS history_logs (
			command TEXT NOT NULL,
			context_dir TEXT NOT NULL,
			logged_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_history_context ON history_logs (context_dir, logged_at);`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			db.Close()
			return nil, err
		}
	}

	return db, nil
}

// GetDBConn returns the active database connection (optionally targeting copy bank)
func GetDBConn(useCopy bool) (*sql.DB, error) {
	if !useCopy {
		return InitDB()
	}

	db, err := InitDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	copyPath, err := GetConfigValue(db, "copy_bank_path")
	if err != nil || copyPath == "" {
		return nil, fmt.Errorf("copy bank path is not configured in config settings")
	}

	copyDBPath := filepath.Join(copyPath, "cwm.db")
	if _, err := os.Stat(copyDBPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("copy bank database file does not exist at: %s", copyDBPath)
	}

	return sql.Open("sqlite", copyDBPath)
}

// GetConfigValue reads config key
func GetConfigValue(db *sql.DB, key string) (string, error) {
	var val string
	err := db.QueryRow("SELECT value FROM config WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// SetConfigValue updates config key and updates copy bank
func SetConfigValue(db *sql.DB, key string, val string) error {
	_, err := db.Exec("INSERT INTO config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?", key, val, val)
	if err != nil {
		return err
	}
	return SyncToCopyBank(db)
}

// ClearConfig resets configurations
func ClearConfig(db *sql.DB) error {
	_, err := db.Exec("DELETE FROM config")
	if err != nil {
		return err
	}
	return SyncToCopyBank(db)
}

// SyncToCopyBank performs a two-way SQL merge between main database and copy bank if configured
func SyncToCopyBank(dbConn *sql.DB) error {
	copyPath, err := GetConfigValue(dbConn, "copy_bank_path")
	if err != nil || copyPath == "" {
		return nil
	}

	err = os.MkdirAll(copyPath, 0755)
	if err != nil {
		return err
	}

	destPath := filepath.Join(copyPath, "cwm.db")

	// If remote database doesn't exist, simply copy the file to initialize it
	if _, errStat := os.Stat(destPath); os.IsNotExist(errStat) {
		srcPath, err := GetDBPath()
		if err != nil {
			return err
		}
		srcFile, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		destFile, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, srcFile)
		return err
	}

	// If it already exists, perform a two-way SQL merge
	_, err = dbConn.Exec("ATTACH DATABASE ? AS copy_bank", destPath)
	if err != nil {
		// Fallback to direct file copy if attach is blocked
		srcPath, errPath := GetDBPath()
		if errPath != nil {
			return errPath
		}
		srcFile, errSrc := os.Open(srcPath)
		if errSrc != nil {
			return errSrc
		}
		defer srcFile.Close()

		destFile, errDest := os.Create(destPath)
		if errDest != nil {
			return errDest
		}
		defer destFile.Close()

		_, errCopy := io.Copy(destFile, srcFile)
		return errCopy
	}
	defer func() {
		_, _ = dbConn.Exec("DETACH DATABASE copy_bank")
	}()

	// 1. Two-way merge of saved_commands based on updated_at timestamp
	_, err = dbConn.Exec(`
		INSERT INTO copy_bank.saved_commands (variable, command, tags, created_at, updated_at)
		SELECT variable, command, tags, created_at, updated_at FROM main.saved_commands
		ON CONFLICT(variable) DO UPDATE SET
			command = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.command ELSE saved_commands.command END,
			tags = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.tags ELSE saved_commands.tags END,
			updated_at = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.updated_at ELSE saved_commands.updated_at END
	`)
	if err != nil {
		return err
	}

	_, err = dbConn.Exec(`
		INSERT INTO main.saved_commands (variable, command, tags, created_at, updated_at)
		SELECT variable, command, tags, created_at, updated_at FROM copy_bank.saved_commands
		ON CONFLICT(variable) DO UPDATE SET
			command = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.command ELSE saved_commands.command END,
			tags = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.tags ELSE saved_commands.tags END,
			updated_at = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.updated_at ELSE saved_commands.updated_at END
	`)
	if err != nil {
		return err
	}

	// 2. Two-way merge of history_logs to prevent duplicate records
	_, err = dbConn.Exec(`
		INSERT INTO copy_bank.history_logs (command, context_dir, logged_at)
		SELECT command, context_dir, logged_at FROM main.history_logs
		WHERE NOT EXISTS (
			SELECT 1 FROM copy_bank.history_logs
			WHERE copy_bank.history_logs.command = main.history_logs.command
			  AND copy_bank.history_logs.context_dir = main.history_logs.context_dir
			  AND copy_bank.history_logs.logged_at = main.history_logs.logged_at
		)
	`)
	if err != nil {
		return err
	}

	_, err = dbConn.Exec(`
		INSERT INTO main.history_logs (command, context_dir, logged_at)
		SELECT command, context_dir, logged_at FROM copy_bank.history_logs
		WHERE NOT EXISTS (
			SELECT 1 FROM main.history_logs
			WHERE main.history_logs.command = copy_bank.history_logs.command
			  AND main.history_logs.context_dir = copy_bank.history_logs.context_dir
			  AND main.history_logs.logged_at = copy_bank.history_logs.logged_at
		)
	`)
	return err
}
