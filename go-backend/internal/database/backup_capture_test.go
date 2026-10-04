package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func backupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func execBackupTest(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// Missing triggers or a non-transactional bootstrap would lose existing or new rows.
func TestBackupCaptureBootstrapAndChanges(t *testing.T) {
	ctx := context.Background()
	db := backupTestDB(t)
	execBackupTest(t, db, `INSERT INTO users(id,email,display_name,password_hash,password_salt) VALUES(1,'one','用户','hash','salt')`)
	execBackupTest(t, db, `INSERT INTO assets(id,user_id,name,category,amount,note) VALUES(1,1,'资产','deposit',9007199254740993,'')`)
	source, err := EnableBackup(ctx, db)
	if err != nil || source == "" {
		t.Fatalf("source=%q err=%v", source, err)
	}
	events, err := ReadBackupEvents(ctx, db, 100)
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	if events[0].Table != "users" || events[1].Table != "assets" {
		t.Fatal("bootstrap must put parents first")
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal(events[1].Row, &row); err != nil {
		t.Fatal(err)
	}
	if string(row["amount"]) != "9007199254740993" || string(row["quantity"]) != "null" {
		t.Fatalf("lost values: %s", events[1].Row)
	}
	again, err := EnableBackup(ctx, db)
	if err != nil || again != source {
		t.Fatalf("reopen source=%q err=%v", again, err)
	}
	events, _ = ReadBackupEvents(ctx, db, 100)
	if len(events) != 2 {
		t.Fatalf("duplicate bootstrap: %d", len(events))
	}
	if err := AckBackupEvents(ctx, db, events[1].Sequence); err != nil {
		t.Fatal(err)
	}
	execBackupTest(t, db, `UPDATE assets SET archived_at='2026-10-04' WHERE id=1`)
	execBackupTest(t, db, `DELETE FROM users WHERE id=1`)
	events, err = ReadBackupEvents(ctx, db, 100)
	if err != nil {
		t.Fatal(err)
	}
	deleted := map[string]bool{}
	archived := false
	for _, e := range events {
		if e.Operation == "delete" {
			deleted[e.Table] = true
		}
		if e.Table == "assets" && e.Operation == "upsert" {
			json.Unmarshal(e.Row, &row)
			archived = archived || string(row["archived_at"]) == `"2026-10-04"`
		}
	}
	if !archived || !deleted["assets"] || !deleted["users"] {
		t.Fatalf("missing change: %#v", events)
	}
}

// Capturing NEW instead of the live row can replay version 1 after version 2.
func TestBackupCaptureRollbackAndVersions(t *testing.T) {
	for _, recreate := range []bool{false, true} {
		t.Run(map[bool]string{false: "original-order", true: "reversed-order"}[recreate], func(t *testing.T) {
			db := backupTestDB(t)
			ctx := context.Background()
			execBackupTest(t, db, `INSERT INTO users(id,email,display_name,password_hash,password_salt) VALUES(1,'a','a','h','s')`)
			execBackupTest(t, db, `INSERT INTO assets(id,user_id,name,category,amount) VALUES(1,1,'cash','deposit',100)`)
			if _, err := EnableBackup(ctx, db); err != nil {
				t.Fatal(err)
			}
			if recreate {
				execBackupTest(t, db, `DROP TRIGGER assets_version`)
				execBackupTest(t, db, `CREATE TRIGGER assets_version AFTER UPDATE ON assets WHEN NEW.version=OLD.version BEGIN UPDATE assets SET version=OLD.version+1 WHERE id=NEW.id; END`)
			}
			before, _ := ReadBackupEvents(ctx, db, 100)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`UPDATE assets SET amount=999 WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			tx.Rollback()
			after, _ := ReadBackupEvents(ctx, db, 100)
			if len(after) != len(before) {
				t.Fatal("rollback queued events")
			}
			execBackupTest(t, db, `UPDATE assets SET amount=200 WHERE id=1`)
			after, _ = ReadBackupEvents(ctx, db, 100)
			var last struct {
				Version int64
				Amount  int64
			}
			for _, e := range after {
				if e.Table == "assets" && e.Operation == "upsert" {
					if err := json.Unmarshal(e.Row, &last); err != nil {
						t.Fatal(err)
					}
				}
			}
			if last.Version != 2 || last.Amount != 200 {
				t.Fatalf("last=%+v", last)
			}
		})
	}
}

func TestBackupCapturePersistsWhenWorkerDisabled(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = EnableBackup(ctx, db); err != nil {
		t.Fatal(err)
	}
	execBackupTest(t, db, `INSERT INTO exchange_rates VALUES('USD',7,'2026-10-04','2026-10-04 00:00:00')`)
	events, _ := ReadBackupEvents(ctx, db, 100)
	seq := events[0].Sequence
	if err := AckBackupEvents(ctx, db, seq); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	execBackupTest(t, db, `UPDATE exchange_rates SET cny_rate=8 WHERE currency='USD'`)
	events, _ = ReadBackupEvents(ctx, db, 100)
	if len(events) != 1 || events[0].Sequence <= seq {
		t.Fatalf("nonpersistent capture: %#v", events)
	}
	if err := AckBackupEvents(ctx, db, seq); err != nil {
		t.Fatal(err)
	}
	events, _ = ReadBackupEvents(ctx, db, 100)
	if len(events) != 1 {
		t.Fatal("ack deleted newer event")
	}
}
