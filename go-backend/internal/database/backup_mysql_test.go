package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Each integration test owns a new database; existing databases are never cleared.
func backupMySQLTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testMySQLDSN(t)
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	admin, err := OpenBackupMySQL(cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("fulibu_backup_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + quoteIdent(name) + " CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.DBName = name
	db, err := OpenBackupMySQL(cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); admin.Exec("DROP DATABASE " + quoteIdent(name)); admin.Close() })
	return db
}

// Removing provenance checks would allow one SQLite instance to overwrite another.
func TestBackupTargetSourceProtection(t *testing.T) {
	ctx := context.Background()
	target := backupMySQLTestDB(t)
	if err := InitializeBackupTarget(ctx, target, "source-a", false); err != nil {
		t.Fatal(err)
	}
	if err := InitializeBackupTarget(ctx, target, "source-a", true); err != nil {
		t.Fatal(err)
	}
	if err := InitializeBackupTarget(ctx, target, "source-b", false); err == nil {
		t.Fatal("different source accepted")
	}
	fresh := backupMySQLTestDB(t)
	if err := InitializeBackupTarget(ctx, fresh, "source-a", true); err == nil {
		t.Fatal("changed empty target accepted")
	}
	stranger := backupMySQLTestDB(t)
	execBackupTest(t, stranger, `CREATE TABLE users(id BIGINT PRIMARY KEY, name TEXT)`)
	execBackupTest(t, stranger, `INSERT INTO users VALUES(1,'untouched')`)
	if err := InitializeBackupTarget(ctx, stranger, "source-a", false); err == nil {
		t.Fatal("unowned target accepted")
	}
	var name string
	if err := stranger.QueryRow("SELECT name FROM users WHERE id=1").Scan(&name); err != nil || name != "untouched" {
		t.Fatalf("changed foreign data: %s %v", name, err)
	}
}

func TestBackupTargetSchemaAndDSN(t *testing.T) {
	if db, err := OpenBackupMySQL("bad-secret-dsn"); err == nil {
		db.Close()
		t.Fatal("invalid DSN accepted")
	} else if strings.Contains(err.Error(), "secret") {
		t.Fatal("error exposes DSN")
	}
	ctx := context.Background()
	target := backupMySQLTestDB(t)
	if err := InitializeBackupTarget(ctx, target, "source", false); err != nil {
		t.Fatal(err)
	}
	for _, table := range backupTables {
		rows, err := target.Query("SELECT " + joinQuoted(table.columns) + " FROM " + quoteIdent(table.name) + " LIMIT 0")
		if err != nil {
			t.Fatalf("%s: %v", table.name, err)
		}
		rows.Close()
	}
	var count int
	if err := target.QueryRow("SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE()").Scan(&count); err != nil || count != 0 {
		t.Fatalf("triggers=%d err=%v", count, err)
	}
	execBackupTest(t, target, "ALTER TABLE assets DROP COLUMN note")
	if err := InitializeBackupTarget(ctx, target, "source", true); err == nil {
		t.Fatal("incompatible schema accepted")
	}
}

func testMySQLDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is unset; real MySQL test not run")
	}
	return dsn
}

func TestBackupTargetResumesEmptyCheckpointInitialization(t *testing.T) {
	ctx := context.Background()
	target := backupMySQLTestDB(t)
	execBackupTest(t, target, `CREATE TABLE backup_checkpoint(id BIGINT PRIMARY KEY,source_id VARBINARY(64) NOT NULL,sequence BIGINT NOT NULL DEFAULT 0,initialized TINYINT NOT NULL DEFAULT 0, CHECK(id=1)) ENGINE=InnoDB`)
	if err := InitializeBackupTarget(ctx, target, "source", false); err != nil {
		t.Fatalf("interrupted marker initialization did not recover: %v", err)
	}
	if err := InitializeBackupTarget(ctx, target, "source", true); err != nil {
		t.Fatal(err)
	}
}

func TestBackupTargetRejectsIncompatibleStructure(t *testing.T) {
	changes := map[string]string{
		"integer-as-double":      "ALTER TABLE assets MODIFY amount DOUBLE NOT NULL",
		"nontransactional-table": "ALTER TABLE income_settings ENGINE=MyISAM",
		"extra-version-trigger":  "CREATE TRIGGER assets_version BEFORE UPDATE ON assets FOR EACH ROW SET NEW.version=OLD.version+1",
		"missing-unique-key":     "ALTER TABLE users DROP INDEX users_email",
		"missing-foreign-key":    "ALTER TABLE assets DROP FOREIGN KEY assets_ibfk_1",
		"narrow-key":             "ALTER TABLE exchange_rates MODIFY currency VARBINARY(8) NOT NULL",
	}
	for name, statement := range changes {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			target := backupMySQLTestDB(t)
			if err := InitializeBackupTarget(ctx, target, "source", false); err != nil {
				t.Fatal(err)
			}
			execBackupTest(t, target, statement)
			if err := InitializeBackupTarget(ctx, target, "source", true); err == nil {
				t.Fatal("unsafe target structure accepted")
			}
		})
	}
}

func TestBackupTargetAddsMarketTablesToOwnedLegacyTarget(t *testing.T) {
	ctx := context.Background()
	target := backupMySQLTestDB(t)
	if err := InitializeBackupTarget(ctx, target, "source-market-upgrade", false); err != nil {
		t.Fatal(err)
	}
	execBackupTest(t, target, `INSERT INTO exchange_rates(currency,cny_rate,rate_date,updated_at) VALUES('USD',7,'2026-10-04','before')`)
	for _, table := range backupTables {
		if isMarketCacheTable(table.name) {
			execBackupTest(t, target, "DROP TABLE "+quoteIdent(table.name))
		}
	}
	if err := InitializeBackupTarget(ctx, target, "source-market-upgrade", true); err != nil {
		t.Fatal(err)
	}
	var rate float64
	if err := target.QueryRow(`SELECT cny_rate FROM exchange_rates WHERE currency='USD'`).Scan(&rate); err != nil || rate != 7 {
		t.Fatalf("legacy backup changed %v %v", rate, err)
	}
	for _, table := range backupTables {
		if isMarketCacheTable(table.name) {
			var n int
			if err := target.QueryRow("SELECT COUNT(*) FROM " + quoteIdent(table.name)).Scan(&n); err != nil {
				t.Fatal(err)
			}
		}
	}
}
