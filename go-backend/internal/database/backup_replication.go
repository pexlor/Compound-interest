package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// ApplyBackupEvents commits backup rows and their checkpoint in one MySQL transaction.
// Source IDs, versions and timestamps are copied verbatim, never regenerated.
func ApplyBackupEvents(ctx context.Context, target *sql.DB, sourceID string, events []BackupEvent) (int64, error) {
	return applyBackupEventsAtCheckpoint(ctx, target, sourceID, events, 0)
}

func applyBackupEventsAtCheckpoint(ctx context.Context, target *sql.DB, sourceID string, events []BackupEvent, minimum int64) (int64, error) {
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var source string
	var checkpoint int64
	var initialized int
	if err := tx.QueryRowContext(ctx, `SELECT source_id,sequence,initialized FROM backup_checkpoint WHERE id=1 FOR UPDATE`).Scan(&source, &checkpoint, &initialized); err != nil {
		return 0, err
	}
	if source != sourceID {
		return 0, ErrBackupSourceConflict
	}
	if initialized != 1 {
		return 0, ErrBackupSchema
	}
	if checkpoint < minimum {
		return 0, ErrBackupCheckpointRegressed
	}
	if len(events) > 0 && checkpoint > events[len(events)-1].Sequence {
		return 0, ErrBackupSourceConflict
	}
	var previous int64
	for _, event := range events {
		if event.Sequence <= previous {
			return 0, errors.New("backup events are not ordered")
		}
		previous = event.Sequence
		if event.Sequence <= checkpoint {
			continue
		}
		if err := applyBackupEvent(ctx, tx, event); err != nil {
			return 0, err
		}
		checkpoint = event.Sequence
	}
	if _, err := tx.ExecContext(ctx, `UPDATE backup_checkpoint SET sequence=? WHERE id=1 AND source_id=?`, checkpoint, sourceID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return checkpoint, nil
}

func applyBackupEvent(ctx context.Context, tx *sql.Tx, event BackupEvent) error {
	table, ok := findBackupTable(event.Table)
	if !ok {
		return errors.New("unknown backup table")
	}
	var key []any
	if err := decodeBackupJSON(event.Key, &key); err != nil {
		return err
	}
	if len(key) != len(table.keys) {
		return errors.New("invalid backup key")
	}
	predicates := make([]string, len(key))
	for i, column := range table.keys {
		value, err := backupValue(column, key[i])
		if err != nil || value == nil {
			return errors.New("invalid backup key value")
		}
		key[i] = value
		predicates[i] = quoteIdent(column) + "=?"
	}
	where := strings.Join(predicates, " AND ")
	if event.Operation == "delete" {
		_, err := tx.ExecContext(ctx, "DELETE FROM "+quoteIdent(table.name)+" WHERE "+where, key...)
		return err
	}
	if event.Operation != "upsert" {
		return errors.New("unknown backup operation")
	}
	var row map[string]any
	if err := decodeBackupJSON(event.Row, &row); err != nil {
		return err
	}
	if len(row) != len(table.columns) {
		return errors.New("incomplete backup row")
	}
	values := make([]any, len(table.columns))
	for i, column := range table.columns {
		value, exists := row[column]
		if !exists {
			return errors.New("missing backup column")
		}
		converted, err := backupValue(column, value)
		if err != nil {
			return err
		}
		row[column] = converted
		values[i] = converted
	}
	for i, column := range table.keys {
		if !reflect.DeepEqual(row[column], key[i]) {
			return errors.New("backup row key mismatch")
		}
	}
	var exists int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM "+quoteIdent(table.name)+" WHERE "+where+" FOR UPDATE", key...).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
		_, err = tx.ExecContext(ctx, "INSERT INTO "+quoteIdent(table.name)+" ("+joinQuoted(table.columns)+") VALUES ("+placeholders+")", values...)
		return err
	}
	if err != nil {
		return err
	}
	assignments := make([]string, len(table.columns))
	for i, column := range table.columns {
		assignments[i] = quoteIdent(column) + "=?"
	}
	// UPDATE by primary key cannot silently overwrite a different row on a secondary unique key collision.
	_, err = tx.ExecContext(ctx, "UPDATE "+quoteIdent(table.name)+" SET "+strings.Join(assignments, ",")+" WHERE "+where, append(values, key...)...)
	return err
}

func decodeBackupJSON(data json.RawMessage, dest any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(dest); err != nil {
		return errors.New("invalid backup JSON")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing backup JSON")
	}
	return nil
}

func backupValue(column string, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch mysqlBackupType(column) {
	case "BIGINT":
		n, ok := value.(json.Number)
		if !ok {
			return nil, fmt.Errorf("backup %s requires an integer", column)
		}
		parsed, err := n.Int64()
		if err != nil {
			return nil, fmt.Errorf("invalid backup integer in %s", column)
		}
		return parsed, nil
	case "DOUBLE":
		n, ok := value.(json.Number)
		if !ok {
			return nil, fmt.Errorf("backup %s requires a number", column)
		}
		parsed, err := strconv.ParseFloat(string(n), 64)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return nil, fmt.Errorf("invalid backup number in %s", column)
		}
		return parsed, nil
	default:
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("backup %s requires text", column)
		}
		return s, nil
	}
}
