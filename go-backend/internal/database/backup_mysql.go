package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

var (
	ErrBackupSourceConflict      = errors.New("backup target belongs to another source")
	ErrBackupUnownedTarget       = errors.New("backup target contains unowned tables; use an empty dedicated database")
	ErrBackupReinitialize        = errors.New("backup target changed; stop the server and explicitly reinitialize an empty target")
	ErrBackupSchema              = errors.New("backup target schema is incompatible")
	ErrBackupCheckpointRegressed = errors.New("backup checkpoint regressed; stop the server and reinitialize a new empty target")
)

func OpenBackupMySQL(dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("invalid MYSQL_DSN")
	}
	if cfg.Timeout == 0 || cfg.Timeout > 5*time.Second {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 || cfg.ReadTimeout > 10*time.Second {
		cfg.ReadTimeout = 10 * time.Second
	}
	if cfg.WriteTimeout == 0 || cfg.WriteTimeout > 10*time.Second {
		cfg.WriteTimeout = 10 * time.Second
	}
	cfg.MultiStatements = false
	cfg.ParseTime = false
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	if err := cfg.Apply(mysql.Charset("utf8mb4", "utf8mb4_bin")); err != nil {
		return nil, errors.New("invalid MySQL backup charset")
	}
	// Truncation of a backup is never acceptable, even if the server default is permissive.
	cfg.Params["sql_mode"] = "'STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'"
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, errors.New("invalid MySQL backup configuration")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(3 * time.Minute)
	return db, nil
}

// Bootstrap ownership is persisted before DDL so interrupted initialization can resume.
// An application database without our marker is never modified, even if currently empty.
func InitializeBackupTarget(ctx context.Context, target *sql.DB, sourceID string, requireExisting bool) error {
	if sourceID == "" {
		return errors.New("empty backup source")
	}
	if err := target.PingContext(ctx); err != nil {
		return err
	}
	rows, err := target.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()`)
	if err != nil {
		return err
	}
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !tables["backup_checkpoint"] {
		if requireExisting {
			return ErrBackupReinitialize
		}
		if len(tables) > 0 {
			return ErrBackupUnownedTarget
		}
		if _, err := target.ExecContext(ctx, `CREATE TABLE backup_checkpoint(id BIGINT PRIMARY KEY,source_id VARBINARY(64) NOT NULL,sequence BIGINT NOT NULL DEFAULT 0,initialized TINYINT NOT NULL DEFAULT 0, CHECK(id=1)) ENGINE=InnoDB`); err != nil {
			return err
		}
		if _, err := target.ExecContext(ctx, `INSERT INTO backup_checkpoint(id,source_id) VALUES(1,?)`, sourceID); err != nil {
			return err
		}
	}
	if err := validateMySQLBackupTable(ctx, target, checkpointBackupTable); err != nil {
		return err
	}
	var source string
	var initialized int
	err = target.QueryRowContext(ctx, `SELECT source_id,initialized FROM backup_checkpoint WHERE id=1`).Scan(&source, &initialized)
	if errors.Is(err, sql.ErrNoRows) && !requireExisting && len(tables) == 1 {
		// A crash can leave the newly created, compatible marker table without its row.
		if _, err := target.ExecContext(ctx, `INSERT INTO backup_checkpoint(id,source_id) VALUES(1,?)`, sourceID); err != nil {
			return err
		}
		source, initialized, err = sourceID, 0, nil
	}
	if err != nil {
		return ErrBackupSchema
	}
	if source != sourceID {
		return ErrBackupSourceConflict
	}
	if initialized == 0 {
		for _, table := range backupTables {
			if _, err := target.ExecContext(ctx, mysqlBackupDDL(table)); err != nil {
				return err
			}
		}
	}
	if err := validateMySQLBackupSchema(ctx, target); err != nil {
		return err
	}
	if initialized == 0 {
		_, err = target.ExecContext(ctx, `UPDATE backup_checkpoint SET initialized=1 WHERE id=1 AND source_id=?`, sourceID)
	}
	return err
}

// Backup tables accept complete source rows; IDs and timestamps have no generated defaults.
func mysqlBackupDDL(table backupTable) string {
	parts := make([]string, 0, len(table.columns)+5)
	for _, column := range table.columns {
		definition := quoteIdent(column) + " " + mysqlBackupType(column)
		if !mysqlBackupNullable(table, column) {
			definition += " NOT NULL"
		}
		parts = append(parts, definition)
	}
	parts = append(parts, "PRIMARY KEY ("+joinQuoted(table.keys)+")")
	switch table.name {
	case "users":
		parts = append(parts, "UNIQUE KEY users_email (email)")
	case "api_tokens":
		parts = append(parts, "UNIQUE KEY api_tokens_hash (token_hash)", "CHECK(scope IN ('read','write'))")
	case "exchange_rate_history":
		parts = append(parts, "UNIQUE KEY rate_day (currency,rate_date)")
	case "market_returns":
		parts = append(parts, "UNIQUE KEY market_lookup (category,code,lookback_days,calculation_date)")
	case "asset_history":
		parts = append(parts, "UNIQUE KEY user_day (user_id,snapshot_date)")
	}
	switch table.name {
	case "sessions", "api_tokens", "mutation_requests", "operation_logs", "assets", "retirement_goal_items":
		parts = append(parts, "FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE")
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin", quoteIdent(table.name), strings.Join(parts, ","))
}

func mysqlBackupNullable(table backupTable, column string) bool {
	return column == "code" && table.name == "assets" || column == "quantity" || column == "investment_amount" || column == "archived_at" || column == "user_id" && table.name == "assets" || column == "rate_date" && table.name == "asset_history"
}

func mysqlBackupType(column string) string {
	switch column {
	case "id", "user_id", "password_iterations", "expires_at", "amount", "investment_amount", "version", "monthly_salary", "monthly_savings", "annual_bonus", "status", "total_cny", "lookback_days", "requested_days", "actual_days", "history_limited", "asset_id", "inception_known", "input_version", "success_count", "failure_count":
		return "BIGINT"
	case "quantity", "annual_rate", "cny_rate", "period_return", "price", "return_price", "income":
		return "DOUBLE"
	case "email":
		return "VARBINARY(2048)"
	case "token_hash", "operation", "request_key", "category":
		return "VARBINARY(512)"
	case "code":
		return "VARBINARY(1024)"
	case "currency", "rate_date", "snapshot_date", "calculation_date", "price_date", "run_date", "slot":
		return "VARBINARY(64)"
	default:
		return "LONGTEXT"
	}
}
