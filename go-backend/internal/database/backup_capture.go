package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type BackupEvent struct {
	Sequence         int64
	Table, Operation string
	Key, Row         json.RawMessage
}

const backupSchema = `
CREATE TABLE IF NOT EXISTS backup_metadata (
 id INTEGER PRIMARY KEY CHECK(id=1), source_id TEXT NOT NULL,
 acknowledged INTEGER NOT NULL DEFAULT 0, last_success TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS backup_outbox (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 table_name TEXT NOT NULL, operation TEXT NOT NULL CHECK(operation IN ('upsert','delete')),
 row_key TEXT NOT NULL, row_data TEXT NOT NULL
);`

// EnableBackup atomically installs capture and seeds all existing application rows.
// Capture remains installed when MySQL is temporarily disabled.
func EnableBackup(ctx context.Context, db *sql.DB) (string, error) {
	return initializeCapture(ctx, db, false)
}

// ReinitializeBackup is a maintenance operation; stop the application beforehand.
func ReinitializeBackup(ctx context.Context, db *sql.DB) (string, error) {
	return initializeCapture(ctx, db, true)
}

func initializeCapture(ctx context.Context, db *sql.DB, reset bool) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, backupSchema); err != nil {
		return "", err
	}
	var source string
	err = tx.QueryRowContext(ctx, `SELECT source_id FROM backup_metadata WHERE id=1`).Scan(&source)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil && !reset {
		// An existing source keeps its identity and checkpoint while additive
		// application tables acquire capture and an initial row seed.
		for _, table := range backupTables {
			if !isMarketCacheTable(table.name) {
				continue
			}
			var triggers int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name IN (?,?,?)`, "backup_capture_"+table.name+"_insert", "backup_capture_"+table.name+"_update", "backup_capture_"+table.name+"_delete").Scan(&triggers); err != nil {
				return "", err
			}
			if triggers == 3 {
				continue
			}
			if err = installCapture(ctx, tx, table); err != nil {
				return "", err
			}
			query := fmt.Sprintf(`INSERT INTO backup_outbox(table_name,operation,row_key,row_data) SELECT '%s','upsert',%s,%s FROM %s ORDER BY %s`, table.name, keyJSON(table, ""), rowJSON(table, ""), quoteIdent(table.name), joinQuoted(table.keys))
			if _, err = tx.ExecContext(ctx, query); err != nil {
				return "", err
			}
		}
		return source, tx.Commit()
	}
	var entropy [16]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return "", err
	}
	source = hex.EncodeToString(entropy[:])
	if reset {
		if _, err = tx.ExecContext(ctx, `DELETE FROM backup_outbox; DELETE FROM backup_metadata;`); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO backup_metadata(id,source_id) VALUES(1,?)`, source); err != nil {
		return "", err
	}
	for _, table := range backupTables {
		if err = installCapture(ctx, tx, table); err != nil {
			return "", fmt.Errorf("capture %s: %w", table.name, err)
		}
		query := fmt.Sprintf(`INSERT INTO backup_outbox(table_name,operation,row_key,row_data) SELECT '%s','upsert',%s,%s FROM %s ORDER BY %s`, table.name, keyJSON(table, ""), rowJSON(table, ""), quoteIdent(table.name), joinQuoted(table.keys))
		if _, err = tx.ExecContext(ctx, query); err != nil {
			return "", err
		}
	}
	return source, tx.Commit()
}

func joinQuoted(cols []string) string {
	result := make([]string, len(cols))
	for i, col := range cols {
		result[i] = quoteIdent(col)
	}
	return strings.Join(result, ",")
}

func keyJSON(table backupTable, prefix string) string {
	parts := make([]string, len(table.keys))
	for i, col := range table.keys {
		parts[i] = prefix + quoteIdent(col)
	}
	return "json_array(" + strings.Join(parts, ",") + ")"
}

func rowJSON(table backupTable, prefix string) string {
	parts := make([]string, 0, len(table.columns)*2)
	for _, col := range table.columns {
		parts = append(parts, "'"+col+"'", prefix+quoteIdent(col))
	}
	return "json_object(" + strings.Join(parts, ",") + ")"
}

func keyPredicate(table backupTable, prefix string) string {
	parts := make([]string, len(table.keys))
	for i, col := range table.keys {
		parts[i] = quoteIdent(col) + "=" + prefix + quoteIdent(col)
	}
	return strings.Join(parts, " AND ")
}

func installCapture(ctx context.Context, tx *sql.Tx, table backupTable) error {
	for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
		name := "backup_capture_" + table.name + "_" + strings.ToLower(op)
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+quoteIdent(name)); err != nil {
			return err
		}
		var body string
		if op == "DELETE" {
			body = fmt.Sprintf(`INSERT INTO backup_outbox(table_name,operation,row_key,row_data) VALUES('%s','delete',%s,'{}');`, table.name, keyJSON(table, "OLD."))
		} else {
			// Read the live row: nested version triggers may already have changed NEW.
			body = fmt.Sprintf(`INSERT INTO backup_outbox(table_name,operation,row_key,row_data) SELECT '%s','upsert',%s,%s FROM %s WHERE %s;`, table.name, keyJSON(table, ""), rowJSON(table, ""), quoteIdent(table.name), keyPredicate(table, "NEW."))
			if op == "UPDATE" {
				body = fmt.Sprintf(`INSERT INTO backup_outbox(table_name,operation,row_key,row_data) SELECT '%s','delete',%s,'{}' WHERE %s != %s;`, table.name, keyJSON(table, "OLD."), keyJSON(table, "OLD."), keyJSON(table, "NEW.")) + body
			}
		}
		query := fmt.Sprintf("CREATE TRIGGER %s AFTER %s ON %s BEGIN %s END", quoteIdent(name), op, quoteIdent(table.name), body)
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func ReadBackupEvents(ctx context.Context, db *sql.DB, limit int) ([]BackupEvent, error) {
	if limit <= 0 {
		return nil, errors.New("backup batch limit must be positive")
	}
	rows, err := db.QueryContext(ctx, `SELECT sequence,table_name,operation,row_key,row_data FROM backup_outbox ORDER BY sequence LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []BackupEvent
	for rows.Next() {
		var event BackupEvent
		var key, row string
		if err := rows.Scan(&event.Sequence, &event.Table, &event.Operation, &key, &row); err != nil {
			return nil, err
		}
		event.Key, event.Row = json.RawMessage(key), json.RawMessage(row)
		result = append(result, event)
	}
	return result, rows.Err()
}

func AckBackupEvents(ctx context.Context, db *sql.DB, through int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE backup_metadata SET acknowledged=MAX(acknowledged,?),last_success=strftime('%Y-%m-%dT%H:%M:%SZ','now'),last_error='' WHERE id=1`, through); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM backup_outbox WHERE sequence<=?`, through); err != nil {
		return err
	}
	return tx.Commit()
}
