package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func seedBackupApplication(t *testing.T, db *sql.DB) {
	t.Helper()
	execBackupTest(t, db, `INSERT INTO users(id,email,display_name,password_hash,password_salt) VALUES(1,'用户@example.com','用户','hash','salt')`)
	execBackupTest(t, db, `INSERT INTO sessions VALUES('session',1,1900000000,'2026-10-04 00:00:00')`)
	execBackupTest(t, db, `INSERT INTO api_tokens(id,user_id,name,token_hash,scope,expires_at) VALUES(1,1,'CLI','api','write',1900000000)`)
	execBackupTest(t, db, `INSERT INTO mutation_requests(user_id,operation,request_key,fingerprint,status,response) VALUES(1,'asset','key','fp',201,?)`, `{"text":"`+strings.Repeat("长", 30000)+`"}`)
	execBackupTest(t, db, `INSERT INTO operation_logs(id,user_id,operation,request_key,response) VALUES(1,1,'asset','key','{}')`)
	execBackupTest(t, db, `INSERT INTO assets(id,user_id,name,category,amount,quantity,code,note) VALUES(1,1,'中文资产','deposit',9007199254740993,NULL,NULL,'')`)
	execBackupTest(t, db, `INSERT INTO income_settings(user_id,monthly_salary,compensation) VALUES(1,200000,'{"options":[]}')`)
	execBackupTest(t, db, `INSERT INTO retirement_goal_items(id,user_id,name,category,amount) VALUES(1,1,'住房','deposit',9007199254740993)`)
	execBackupTest(t, db, `INSERT INTO exchange_rates VALUES('USD',7.25,'2026-10-04','2026-10-04 00:00:00'),('usd',8,'2026-10-04','2026-10-04 00:00:00'),('USD ',9,'2026-10-04','2026-10-04 00:00:00')`)
	execBackupTest(t, db, `INSERT INTO exchange_rate_history VALUES(1,'USD',7.25,'2026-10-04','provider','2026-10-04T00:00:00Z')`)
	execBackupTest(t, db, `INSERT INTO market_returns(id,category,code,lookback_days,calculation_date,annual_rate,period_return,requested_days,actual_days,history_limited,start_date,end_date,source) VALUES(1,'stock','ABC',365,'2026-10-04',0.1,0.1,365,350,1,'2025-10-04','2026-10-04','provider')`)
	execBackupTest(t, db, "INSERT INTO asset_history(id,user_id,snapshot_date,total_cny,`trigger`,rate_date) VALUES(1,1,'2026-10-04',9007199254740993,'manual',NULL)")
}

func backupRows(t *testing.T, db *sql.DB, table backupTable) [][]any {
	t.Helper()
	rows, err := db.Query("SELECT " + joinQuoted(table.columns) + " FROM " + quoteIdent(table.name) + " ORDER BY " + joinQuoted(table.keys))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := [][]any{}
	for rows.Next() {
		values := make([]any, len(table.columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = string(b)
			}
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func compareBackupTables(t *testing.T, primary, target *sql.DB) {
	t.Helper()
	for _, table := range backupTables {
		want := backupRows(t, primary, table)
		got := backupRows(t, target, table)
		if !reflect.DeepEqual(want, got) {
			// MySQL may return numeric values as bytes in text protocol; preserve decimal values.
			if fmt.Sprint(want) != fmt.Sprint(got) {
				t.Fatalf("%s diverged (source rows=%d, backup rows=%d)", table.name, len(want), len(got))
			}
		}
		t.Logf("%s: %d identical rows", table.name, len(want))
	}
}

func applyAllTestEvents(t *testing.T, primary, target *sql.DB, source string) {
	t.Helper()
	ctx := context.Background()
	for {
		events, err := ReadBackupEvents(ctx, primary, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 {
			return
		}
		seq, err := ApplyBackupEvents(ctx, target, source, events)
		if err != nil {
			t.Fatal(err)
		}
		if err := AckBackupEvents(ctx, primary, seq); err != nil {
			t.Fatal(err)
		}
	}
}

// Changing integer decoding to float64 or truncating a text/key must fail this test.
func TestBackupReplicationAllTables(t *testing.T) {
	primary := backupTestDB(t)
	target := backupMySQLTestDB(t)
	seedBackupApplication(t, primary)
	source, err := EnableBackup(context.Background(), primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := InitializeBackupTarget(context.Background(), target, source, false); err != nil {
		t.Fatal(err)
	}
	applyAllTestEvents(t, primary, target, source)
	compareBackupTables(t, primary, target)
}

func TestBackupReplicationReplayAndFailure(t *testing.T) {
	ctx := context.Background()
	primary := backupTestDB(t)
	target := backupMySQLTestDB(t)
	seedBackupApplication(t, primary)
	source, err := EnableBackup(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := InitializeBackupTarget(ctx, target, source, false); err != nil {
		t.Fatal(err)
	}
	events, err := ReadBackupEvents(ctx, primary, 100)
	if err != nil {
		t.Fatal(err)
	}
	through, err := ApplyBackupEvents(ctx, target, source, events)
	if err != nil {
		t.Fatal(err)
	}
	// Crash before local acknowledgement: the same persisted events are sent again.
	replay, err := ApplyBackupEvents(ctx, target, source, events)
	if err != nil || replay != through {
		t.Fatalf("replay seq=%d err=%v", replay, err)
	}
	compareBackupTables(t, primary, target)
	if err := AckBackupEvents(ctx, primary, through); err != nil {
		t.Fatal(err)
	}
	execBackupTest(t, primary, `UPDATE exchange_rates SET cny_rate=10 WHERE currency='USD'`)
	next, _ := ReadBackupEvents(ctx, primary, 100)
	next = append(next, BackupEvent{Sequence: next[0].Sequence + 1, Table: "unknown", Operation: "delete", Key: json.RawMessage(`[1]`), Row: json.RawMessage(`{}`)})
	if _, err := ApplyBackupEvents(ctx, target, source, next); err == nil {
		t.Fatal("invalid batch accepted")
	}
	var rate float64
	var checkpoint int64
	target.QueryRow(`SELECT cny_rate FROM exchange_rates WHERE currency='USD'`).Scan(&rate)
	target.QueryRow(`SELECT sequence FROM backup_checkpoint WHERE id=1`).Scan(&checkpoint)
	if rate != 7.25 || checkpoint != through {
		t.Fatalf("partial batch committed rate=%v checkpoint=%d", rate, checkpoint)
	}
	applyAllTestEvents(t, primary, target, source)
	compareBackupTables(t, primary, target)
	// A second unique key collision must never overwrite the row with another ID.
	execBackupTest(t, target, `UPDATE users SET email='occupied' WHERE id=1`)
	execBackupTest(t, primary, `INSERT INTO users(id,email,display_name,password_hash,password_salt) VALUES(2,'occupied','other','h','s')`)
	collision, _ := ReadBackupEvents(ctx, primary, 100)
	if _, err := ApplyBackupEvents(ctx, target, source, collision); err == nil {
		t.Fatal("unique collision accepted")
	}
	var id int64
	if err := target.QueryRow(`SELECT id FROM users WHERE email='occupied'`).Scan(&id); err != nil || id != 1 {
		t.Fatalf("wrong row overwritten: id=%d err=%v", id, err)
	}
}

func TestBackupReplicationForeignKeysAndVersions(t *testing.T) {
	ctx := context.Background()
	primary := backupTestDB(t)
	target := backupMySQLTestDB(t)
	seedBackupApplication(t, primary)
	source, err := EnableBackup(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := InitializeBackupTarget(ctx, target, source, false); err != nil {
		t.Fatal(err)
	}
	applyAllTestEvents(t, primary, target, source)
	for _, table := range []string{"assets", "income_settings", "retirement_goal_items"} {
		execBackupTest(t, primary, "UPDATE "+table+" SET version=version")
	}
	execBackupTest(t, primary, `UPDATE assets SET archived_at='2026-10-04T00:00:00Z' WHERE id=1`)
	applyAllTestEvents(t, primary, target, source)
	compareBackupTables(t, primary, target)
	execBackupTest(t, primary, `DELETE FROM users WHERE id=1`)
	applyAllTestEvents(t, primary, target, source)
	compareBackupTables(t, primary, target)
}
