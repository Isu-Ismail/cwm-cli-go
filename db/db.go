package db

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// AppVersion is the global metadata version string for CWM database and CLI.
const AppVersion = "v2.0.0"

// getBuildSignature computes a unique hash for schema & build verification
func getBuildSignature() string {
	raw := AppVersion + ":cwm_schema_v6_type_column_migration_2026_07_24"
	return fmt.Sprintf("%x", md5.Sum([]byte(raw)))
}

// GetScriptsDir returns the global scripts directory path ~/.cwm/scripts
func GetScriptsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	scriptsFolder := filepath.Join(home, ".cwm", "scripts")
	if err := os.MkdirAll(scriptsFolder, 0755); err != nil {
		return "", err
	}
	return scriptsFolder, nil
}

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

// checkAndRepairDB detects malformed SQLite b-tree pages and runs VACUUM/REINDEX to repair
func checkAndRepairDB(db *sql.DB) {
	var integrity string
	err := db.QueryRow("PRAGMA integrity_check;").Scan(&integrity)
	if err != nil || integrity != "ok" {
		_, _ = db.Exec("VACUUM;")
		_, _ = db.Exec("REINDEX;")
	}
}

// migrateLegacyTables checks and upgrades tables that used AUTOINCREMENT so sqlite_sequence can be safely removed
func migrateLegacyTables(db *sql.DB) {
	var createSql string
	_ = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='trashed_commands'").Scan(&createSql)

	if strings.Contains(strings.ToUpper(createSql), "AUTOINCREMENT") {
		// Re-create table without AUTOINCREMENT keyword
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS trashed_commands_new (
			id INTEGER PRIMARY KEY,
			variable TEXT NOT NULL,
			command TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			deleted_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`)
		_, _ = db.Exec(`INSERT INTO trashed_commands_new (id, variable, command, tags, description, deleted_at)
			SELECT id, variable, command, tags, description, deleted_at FROM trashed_commands;`)
		_, _ = db.Exec(`DROP TABLE trashed_commands;`)
		_, _ = db.Exec(`ALTER TABLE trashed_commands_new RENAME TO trashed_commands;`)
	}
}

// cleanupUnwantedTables safely drops sqlite_sequence and any non-schema tables
func cleanupUnwantedTables(db *sql.DB) {
	allowed := map[string]bool{
		"saved_commands":   true,
		"config":           true,
		"history_logs":     true,
		"trashed_commands": true,
		"db_metadata":      true,
	}

	// 1. Safe standard drop of sqlite_sequence table
	_, _ = db.Exec("DROP TABLE IF EXISTS sqlite_sequence;")

	// 2. Query all tables in database and drop non-schema tables
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err == nil {
		var toDrop []string
		for rows.Next() {
			var name string
			if errScan := rows.Scan(&name); errScan == nil {
				if !allowed[name] && !strings.HasPrefix(name, "sqlite_") {
					toDrop = append(toDrop, name)
				}
			}
		}
		_ = rows.Err()
		rows.Close()

		for _, tbl := range toDrop {
			_, _ = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s;", tbl))
		}
	}
}

// InitDB initializes connection, repairs integrity, cleans unwanted tables, checks build signature, and runs schema migrations
func InitDB() (*sql.DB, error) {
	_, _ = GetScriptsDir()

	path, err := GetDBPath()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// 1. Check database integrity and rebuild/vacuum if malformed
	checkAndRepairDB(db)

	// 2. Upgrade legacy AUTOINCREMENT tables so sqlite_sequence can be safely dropped
	migrateLegacyTables(db)

	// 3. Forcibly drop sqlite_sequence and non-schema tables on every DB connection
	cleanupUnwantedTables(db)

	// 4. Ensure db_metadata table exists
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS db_metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		db.Close()
		return nil, err
	}

	buildSig := getBuildSignature()

	// 5. Check if stored build_signature matches current build
	var currentBuildSig string
	_ = db.QueryRow("SELECT value FROM db_metadata WHERE key = 'build_signature'").Scan(&currentBuildSig)

	if currentBuildSig == buildSig {
		// Schema & build signature matches, skip running re-migrations
		return db, nil
	}

	// 6. Run schema creation without AUTOINCREMENT keyword
	queries := []string{
		`CREATE TABLE IF NOT EXISTS saved_commands (
			variable TEXT PRIMARY KEY,
			command TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'command',
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
		`CREATE TABLE IF NOT EXISTS trashed_commands (
			id INTEGER PRIMARY KEY,
			variable TEXT NOT NULL,
			command TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'command',
			deleted_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_history_context ON history_logs (context_dir, logged_at);`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			db.Close()
			return nil, err
		}
	}

	// Re-run cleanup to ensure no sequence table was produced
	cleanupUnwantedTables(db)

	// Safe column migration for pre-existing older databases
	_, _ = db.Exec("ALTER TABLE saved_commands ADD COLUMN description TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE saved_commands ADD COLUMN type TEXT NOT NULL DEFAULT 'command'")
	_, _ = db.Exec("ALTER TABLE trashed_commands ADD COLUMN type TEXT NOT NULL DEFAULT 'command'")

	// 7. Update build_signature and db_version in db_metadata
	_, _ = db.Exec(`INSERT INTO db_metadata (key, value, updated_at) VALUES ('build_signature', ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, buildSig)

	_, _ = db.Exec(`INSERT INTO db_metadata (key, value, updated_at) VALUES ('db_version', ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, AppVersion)

	return db, nil
}

// TrashSavedCommands archives saved commands into trashed_commands table and caps total at 100 records
func TrashSavedCommands(db *sql.DB, variables []string) error {
	if len(variables) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT INTO trashed_commands (variable, command, tags, description, type) SELECT variable, command, tags, description, COALESCE(type, 'command') FROM saved_commands WHERE variable = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, v := range variables {
		if _, errExec := stmt.Exec(v); errExec != nil {
			return errExec
		}
	}

	// Keep max 100 items in trashed_commands
	_, _ = tx.Exec(`DELETE FROM trashed_commands WHERE id NOT IN (
		SELECT id FROM trashed_commands ORDER BY id DESC LIMIT 100
	)`)

	return tx.Commit()
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

	// Drop sqlite_sequence on copy bank cleanly
	_, _ = dbConn.Exec("DROP TABLE IF EXISTS copy_bank.sqlite_sequence;")

	// Ensure copy bank has db_metadata table
	_, _ = dbConn.Exec(`
		CREATE TABLE IF NOT EXISTS copy_bank.db_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)

	// 1. Two-way merge of saved_commands based on updated_at timestamp
	_, err = dbConn.Exec(`
		INSERT INTO copy_bank.saved_commands (variable, command, tags, description, created_at, updated_at)
		SELECT variable, command, tags, description, created_at, updated_at FROM main.saved_commands
		ON CONFLICT(variable) DO UPDATE SET
			command = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.command ELSE saved_commands.command END,
			tags = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.tags ELSE saved_commands.tags END,
			description = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.description ELSE saved_commands.description END,
			updated_at = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.updated_at ELSE saved_commands.updated_at END
	`)
	if err != nil {
		return err
	}

	_, err = dbConn.Exec(`
		INSERT INTO main.saved_commands (variable, command, tags, description, created_at, updated_at)
		SELECT variable, command, tags, description, created_at, updated_at FROM copy_bank.saved_commands
		ON CONFLICT(variable) DO UPDATE SET
			command = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.command ELSE saved_commands.command END,
			tags = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.tags ELSE saved_commands.tags END,
			description = CASE WHEN excluded.updated_at > saved_commands.updated_at THEN excluded.description ELSE saved_commands.description END,
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
	if err != nil {
		return err
	}

	// 3. Merge db_metadata
	_, _ = dbConn.Exec(`
		INSERT INTO copy_bank.db_metadata (key, value, updated_at)
		SELECT key, value, updated_at FROM main.db_metadata
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`)

	return nil
}
