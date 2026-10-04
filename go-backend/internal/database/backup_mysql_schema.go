package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

var checkpointBackupTable = backupTable{"backup_checkpoint", fields("id,source_id,sequence,initialized"), fields("id")}

// Names alone are insufficient: a DOUBLE amount or nontransactional table would
// acknowledge corrupt data. Check storage, types, keys, cascades and triggers.
func validateMySQLBackupSchema(ctx context.Context, db *sql.DB) error {
	for _, table := range backupTables {
		if err := validateMySQLBackupTable(ctx, db, table); err != nil {
			return err
		}
	}
	return nil
}

func validateMySQLBackupTable(ctx context.Context, db *sql.DB, table backupTable) (err error) {
	stage := "engine"
	defer func() {
		if errors.Is(err, ErrBackupSchema) {
			err = fmt.Errorf("%w: %s %s", err, table.name, stage)
		}
	}()
	var engine, kind string
	if err := db.QueryRowContext(ctx, `SELECT ENGINE,TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table.name).Scan(&engine, &kind); err != nil {
		return ErrBackupSchema
	}
	if !strings.EqualFold(engine, "InnoDB") || kind != "BASE TABLE" {
		return ErrBackupSchema
	}
	rows, err := db.QueryContext(ctx, `SELECT COLUMN_NAME,DATA_TYPE,COLUMN_TYPE,IS_NULLABLE,CHARACTER_MAXIMUM_LENGTH,COLLATION_NAME,EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? ORDER BY ORDINAL_POSITION`, table.name)
	if err != nil {
		return err
	}
	count := 0
	stage = "columns"
	for rows.Next() {
		var column, dataType, columnType, nullable, extra string
		var length sql.NullInt64
		var collation sql.NullString
		if err := rows.Scan(&column, &dataType, &columnType, &nullable, &length, &collation, &extra); err != nil {
			rows.Close()
			return err
		}
		if count >= len(table.columns) || column != table.columns[count] {
			rows.Close()
			return ErrBackupSchema
		}
		count++
		stage = "column " + column
		wantType := mysqlBackupType(column)
		if table.name == "backup_checkpoint" {
			switch column {
			case "source_id":
				wantType = "VARBINARY(64)"
			case "sequence":
				wantType = "BIGINT"
			case "initialized":
				wantType = "TINYINT"
			}
		}
		base := strings.ToLower(strings.Split(wantType, "(")[0])
		wantNullable := mysqlBackupNullable(table, column)
		if dataType != base || strings.Contains(strings.ToLower(columnType), "unsigned") || (nullable == "YES") != wantNullable || extra != "" {
			rows.Close()
			return ErrBackupSchema
		}
		if base == "varbinary" {
			wantLength, _ := strconv.ParseInt(strings.TrimSuffix(strings.Split(wantType, "(")[1], ")"), 10, 64)
			if !length.Valid || length.Int64 != wantLength {
				rows.Close()
				return ErrBackupSchema
			}
		}
		if base == "longtext" && (!collation.Valid || collation.String != "utf8mb4_bin") {
			rows.Close()
			return ErrBackupSchema
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != len(table.columns) {
		return ErrBackupSchema
	}
	var triggers int
	stage = "triggers"
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() AND EVENT_OBJECT_TABLE=?`, table.name).Scan(&triggers); err != nil {
		return err
	}
	if triggers != 0 {
		return ErrBackupSchema
	}
	stage = "indexes"
	if err := validateMySQLBackupIndexes(ctx, db, table); err != nil {
		return err
	}
	stage = "foreign keys"
	if err := validateMySQLBackupForeignKeys(ctx, db, table); err != nil {
		return err
	}
	stage = "checks"
	return validateMySQLBackupChecks(ctx, db, table)
}

func validateMySQLBackupIndexes(ctx context.Context, db *sql.DB, table backupTable) error {
	rows, err := db.QueryContext(ctx, `SELECT INDEX_NAME,COLUMN_NAME,SUB_PART FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND NON_UNIQUE=0 ORDER BY INDEX_NAME,SEQ_IN_INDEX`, table.name)
	if err != nil {
		return err
	}
	indexes := map[string][]string{}
	for rows.Next() {
		var name, column string
		var prefix sql.NullInt64
		if err := rows.Scan(&name, &column, &prefix); err != nil {
			rows.Close()
			return err
		}
		if prefix.Valid {
			rows.Close()
			return ErrBackupSchema
		}
		indexes[name] = append(indexes[name], column)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(indexes["PRIMARY"], table.keys) {
		return ErrBackupSchema
	}
	actual := map[string]bool{}
	for name, columns := range indexes {
		if name != "PRIMARY" {
			actual[strings.Join(columns, ",")] = true
		}
	}
	want := map[string]bool{}
	switch table.name {
	case "users":
		want["email"] = true
	case "api_tokens":
		want["token_hash"] = true
	case "exchange_rate_history":
		want["currency,rate_date"] = true
	case "market_returns":
		want["category,code,lookback_days,calculation_date"] = true
	case "asset_history":
		want["user_id,snapshot_date"] = true
	}
	if !reflect.DeepEqual(actual, want) {
		return ErrBackupSchema
	}
	return nil
}

func validateMySQLBackupForeignKeys(ctx context.Context, db *sql.DB, table backupTable) error {
	rows, err := db.QueryContext(ctx, `SELECT k.COLUMN_NAME,k.REFERENCED_TABLE_SCHEMA=DATABASE(),k.REFERENCED_TABLE_NAME,k.REFERENCED_COLUMN_NAME,r.DELETE_RULE,r.UPDATE_RULE
FROM information_schema.KEY_COLUMN_USAGE k JOIN information_schema.REFERENTIAL_CONSTRAINTS r ON r.CONSTRAINT_SCHEMA=k.CONSTRAINT_SCHEMA AND r.TABLE_NAME=k.TABLE_NAME AND r.CONSTRAINT_NAME=k.CONSTRAINT_NAME
WHERE k.TABLE_SCHEMA=DATABASE() AND k.TABLE_NAME=? AND k.REFERENCED_TABLE_NAME IS NOT NULL`, table.name)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var column, refTable, refColumn, onDelete, onUpdate string
		var sameSchema int
		if err := rows.Scan(&column, &sameSchema, &refTable, &refColumn, &onDelete, &onUpdate); err != nil {
			rows.Close()
			return err
		}
		if column != "user_id" || sameSchema != 1 || refTable != "users" || refColumn != "id" || onDelete != "CASCADE" || onUpdate != "RESTRICT" && onUpdate != "NO ACTION" {
			rows.Close()
			return ErrBackupSchema
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	want := 0
	switch table.name {
	case "sessions", "api_tokens", "mutation_requests", "operation_logs", "assets", "retirement_goal_items":
		want = 1
	}
	if count != want {
		return ErrBackupSchema
	}
	return nil
}

func validateMySQLBackupChecks(ctx context.Context, db *sql.DB, table backupTable) error {
	rows, err := db.QueryContext(ctx, `SELECT c.CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS c JOIN information_schema.TABLE_CONSTRAINTS t ON t.CONSTRAINT_SCHEMA=c.CONSTRAINT_SCHEMA AND t.CONSTRAINT_NAME=c.CONSTRAINT_NAME WHERE t.TABLE_SCHEMA=DATABASE() AND t.TABLE_NAME=? AND t.ENFORCED='YES'`, table.name)
	if err != nil {
		return err
	}
	checks := []string{}
	replacer := strings.NewReplacer("`", "", "(", "", ")", "", " ", "", "\n", "", "\t", "", "_utf8mb4", "", "_binary", "", "\\'", "'")
	for rows.Next() {
		var clause string
		if err := rows.Scan(&clause); err != nil {
			rows.Close()
			return err
		}
		checks = append(checks, replacer.Replace(strings.ToLower(clause)))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	want := []string{}
	switch table.name {
	case "api_tokens":
		want = []string{"scopein'read','write'"}
	case "backup_checkpoint":
		want = []string{"id=1"}
	}
	if !reflect.DeepEqual(checks, want) {
		return ErrBackupSchema
	}
	return nil
}
