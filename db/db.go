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
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			command TEXT NOT NULL,
			context_dir TEXT NOT NULL,
			logged_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_history_context ON history_logs(context_dir);`,
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

// SyncToCopyBank copies global cwm.db directly to copy_bank_path if configured
func SyncToCopyBank(db *sql.DB) error {
	copyPath, err := GetConfigValue(db, "copy_bank_path")
	if err != nil || copyPath == "" {
		return nil
	}

	srcPath, err := GetDBPath()
	if err != nil {
		return err
	}

	err = os.MkdirAll(copyPath, 0755)
	if err != nil {
		return err
	}

	destPath := filepath.Join(copyPath, "cwm.db")

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
