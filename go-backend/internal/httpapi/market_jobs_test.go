package httpapi

import (
	"context"
	"errors"
	"fulibu-go/internal/service"
	"testing"
	"time"
)

// TestNextMarketRunShanghai 验证上海时间两个固定时段及跨日边界的计算。
func TestNextMarketRunShanghai(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"2026-10-05T01:29:59Z", "2026-10-05T09:30:00+08:00"},
		{"2026-10-05T01:30:00Z", "2026-10-05T17:00:00+08:00"},
		{"2026-10-05T09:00:00Z", "2026-10-06T09:30:00+08:00"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.in)
		if got := nextMarketRun(now).Format(time.RFC3339); got != tc.want {
			t.Fatalf("%s => %s want %s", tc.in, got, tc.want)
		}
	}
}

// TestMarketJobsRestartCoalescesAndPersists 验证重启后遗漏多个时段只执行一次，成功记录持久化防重。
func TestMarketJobsRestartCoalescesAndPersists(t *testing.T) {
	db, _, _ := apiFixture(t)
	a := &app{db: db}
	now := time.Date(2026, 10, 5, 18, 0, 0, 0, shanghai)
	calls := 0
	refresh := func(context.Context) (int, int, error) { calls++; return 2, 0, nil }
	if err := a.runMarketSlots(context.Background(), now, refresh); err != nil {
		t.Fatal(err)
	}
	if err := a.runMarketSlots(context.Background(), now, refresh); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM market_refresh_runs WHERE job_state='complete'`).Scan(&n)
	if calls != 1 || n != 2 {
		t.Fatalf("calls=%d complete=%d", calls, n)
	}
}

// TestMarketJobsFailureIsRetryable 验证失败批次经过冷却后能再次执行并更新为成功。
func TestMarketJobsFailureIsRetryable(t *testing.T) {
	db, _, _ := apiFixture(t)
	a := &app{db: db}
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, shanghai)
	if err := a.runMarketSlots(context.Background(), now, func(context.Context) (int, int, error) { return 0, 1, errors.New("offline") }); err == nil {
		t.Fatal("expected failure")
	}
	// Retry only after a five-minute cooldown; real persisted finish time is used.
	db.Exec(`UPDATE market_refresh_runs SET finished_at=?`, now.Add(-6*time.Minute).UTC().Format(time.RFC3339Nano))
	if err := a.runMarketSlots(context.Background(), now, func(context.Context) (int, int, error) { return 1, 0, nil }); err != nil {
		t.Fatal(err)
	}
	var state string
	db.QueryRow(`SELECT job_state FROM market_refresh_runs`).Scan(&state)
	if state != "complete" {
		t.Fatal(state)
	}
}

// TestDailyAssetSnapshotsArePrivateAndDoNotInventHistory 验证个人日快照隔离账户，且不虚构录入前余额。
func TestDailyAssetSnapshotsArePrivateAndDoNotInventHistory(t *testing.T) {
	db, _, _ := apiFixture(t)
	a := &app{db: db, ledger: service.NewLedger(db)}
	db.Exec(`INSERT INTO users(id,email,display_name,password_hash,password_salt) VALUES(2,'second@example.com','second','h','s')`)
	db.Exec(`INSERT INTO assets(id,user_id,name,category,amount,currency) VALUES(1,1,'cash','deposit',10000,'CNY'),(2,2,'cash','deposit',20000,'CNY')`)
	if err := a.recordCachedAssetSnapshots(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM asset_daily_snapshots`).Scan(&count)
	if count != 2 {
		t.Fatal(count)
	}
	var amount int
	db.QueryRow(`SELECT amount FROM asset_daily_snapshots WHERE user_id=1`).Scan(&amount)
	if amount != 10000 {
		t.Fatal(amount)
	}
	db.QueryRow(`SELECT COUNT(*) FROM asset_daily_snapshots WHERE snapshot_date<>?`, marketDate()).Scan(&count)
	if count != 0 {
		t.Fatal("invented history")
	}
}

// TestMarketWakeAlignsToScheduledBoundary 验证临近刷新时段时准确唤醒，不受进程启动秒数影响。
func TestMarketWakeAlignsToScheduledBoundary(t *testing.T) {
	now := time.Date(2026, 10, 5, 9, 29, 53, 0, shanghai)
	want := time.Date(2026, 10, 5, 9, 30, 0, 0, shanghai)
	if got := nextMarketWake(now); !got.Equal(want) {
		t.Fatalf("wake %v want %v", got, want)
	}
	now = time.Date(2026, 10, 5, 16, 59, 53, 0, shanghai)
	want = time.Date(2026, 10, 5, 17, 0, 0, 0, shanghai)
	if got := nextMarketWake(now); !got.Equal(want) {
		t.Fatalf("wake %v want %v", got, want)
	}
}
