package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestMarketCachePersistsAllIntervalsAndDeduplicates(t *testing.T) {
	db, _, _ := apiFixture(t)
	c := marketCacheFor(db)
	var calls atomic.Int32
	c.fetch = func(ctx context.Context, _ *http.Client, category, code string, from time.Time) (marketSeries, error) {
		calls.Add(1)
		return marketSeries{Rows: []dailyObservation{{Date: "2015-01-01", Price: 1, ReturnPrice: 1}, {Date: "2026-10-04", Price: 2, ReturnPrice: 2}}, Currency: "CNY", Source: "test", InceptionKnown: true, Quote: 2, QuoteDate: "2026-10-04"}, nil
	}
	done := c.startSync("fund", "021000", false)
	same := c.startSync("fund", "021000", false)
	if done != same {
		t.Fatal("concurrent duplicate fetch")
	}
	<-done
	if calls.Load() != 1 {
		t.Fatalf("calls %d", calls.Load())
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM market_returns`).Scan(&n)
	if n != 4 {
		t.Fatalf("returns %d, error %s", n, c.lastError("fund", "021000"))
	}
	for _, days := range []int{365, 1095, 1825, 3650} {
		r, ok := c.read("fund", "021000", days)
		if !ok || !r.AnnualReady || r.AnnualRate <= 0 {
			t.Fatalf("missing interval %d %+v", days, r)
		}
	}
	r, err := c.get("fund", "021000", 1095, false)
	if err != nil || r.Pending || r.Stale {
		t.Fatalf("not a local hit %+v %v", r, err)
	}
	if calls.Load() != 1 {
		t.Fatal("cache hit fetched upstream")
	}
	// New HTTP/scheduler app instances share this coordinator for the database.
	if marketCacheFor(db) != c {
		t.Fatal("cache coordinator not shared")
	}
}

func TestMarketCacheFailurePreservesLastSuccess(t *testing.T) {
	db, _, _ := apiFixture(t)
	c := marketCacheFor(db)
	c.fetch = func(context.Context, *http.Client, string, string, time.Time) (marketSeries, error) {
		return marketSeries{Rows: []dailyObservation{{Date: "2024-01-01", Price: 1, ReturnPrice: 1}, {Date: "2026-10-04", Price: 2, ReturnPrice: 2}}, Currency: "CNY", Source: "test", InceptionKnown: true, Quote: 2, QuoteDate: "2026-10-04"}, nil
	}
	<-c.startSync("fund", "021000", false)
	old, _ := c.read("fund", "021000", 1095)
	c.fetch = func(context.Context, *http.Client, string, string, time.Time) (marketSeries, error) {
		return marketSeries{}, errors.New("upstream offline")
	}
	c.retryDelays = []time.Duration{0, 0}
	<-c.startSync("fund", "021000", true)
	got, err := c.get("fund", "021000", 1095, false)
	if err != nil || !got.Stale || got.AnnualRate != old.AnnualRate || !got.AnnualReady {
		t.Fatalf("lost old rate %+v %v", got, err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM market_daily_prices`).Scan(&n)
	if n != 2 {
		t.Fatal("failed sync modified observations")
	}
}

func TestMissingObservationForcesHistoryRepair(t *testing.T) {
	db, _, _ := apiFixture(t)
	c := marketCacheFor(db)
	var calls atomic.Int32
	c.fetch = func(context.Context, *http.Client, string, string, time.Time) (marketSeries, error) {
		calls.Add(1)
		return marketSeries{Rows: []dailyObservation{{Date: "2015-01-01", Price: 1, ReturnPrice: 1}, {Date: "2020-01-01", Price: 1.5, ReturnPrice: 1.5}, {Date: "2026-10-04", Price: 2, ReturnPrice: 2}}, Currency: "CNY", Source: "test", InceptionKnown: true, Quote: 2, QuoteDate: "2026-10-04"}, nil
	}
	<-c.startSync("fund", "021000", false)
	db.Exec(`DELETE FROM market_daily_prices WHERE price_date='2020-01-01'`)
	r, err := c.get("fund", "021000", 1095, false)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Pending {
		t.Fatal("missing stored date not detected")
	}
	c.mu.Lock()
	done := c.flights["fund:021000"]
	c.mu.Unlock()
	if done != nil {
		<-done
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM market_daily_prices`).Scan(&n)
	if n != 3 || calls.Load() != 2 {
		t.Fatalf("repair n=%d calls=%d", n, calls.Load())
	}
}

func TestDashboardIncludesCachedRatesWithoutExternalFetch(t *testing.T) {
	db, h, cookie := apiFixture(t)
	db.Exec(`INSERT INTO assets(user_id,name,category,code,amount,quantity,currency) VALUES(1,'fund','fund','021000',10000,100,'CNY')`)
	c := marketCacheFor(db)
	c.fetch = func(context.Context, *http.Client, string, string, time.Time) (marketSeries, error) {
		return marketSeries{Rows: []dailyObservation{{Date: "2024-01-01", Price: 1, ReturnPrice: 1}, {Date: "2026-10-04", Price: 2, ReturnPrice: 2}}, Currency: "CNY", Source: "test", InceptionKnown: true, Quote: 2, QuoteDate: "2026-10-04"}, nil
	}
	<-c.startSync("fund", "021000", false)
	status, v := requestAPI(t, h, cookie, "", "GET", "/api/dashboard", "", "")
	if status != 200 || len(v["marketResults"].([]any)) != 1 {
		t.Fatalf("%d %v", status, v)
	}
	r := v["marketResults"].([]any)[0].(map[string]any)
	if r["annualReady"] != true || r["requestedDays"] != float64(1095) {
		t.Fatal(r)
	}
}

func TestChangedHistoryRecomputesEveryInterval(t *testing.T) {
	db, _, _ := apiFixture(t)
	c := marketCacheFor(db)
	last := 2.0
	c.fetch = func(context.Context, *http.Client, string, string, time.Time) (marketSeries, error) {
		return marketSeries{Rows: []dailyObservation{{Date: "2015-01-01", Price: 1, ReturnPrice: 1}, {Date: "2026-10-04", Price: last, ReturnPrice: last}}, Currency: "CNY", Source: "test", InceptionKnown: true, Quote: last, QuoteDate: "2026-10-04"}, nil
	}
	<-c.startSync("fund", "021000", false)
	old, _ := c.read("fund", "021000", 1095)
	last = 3
	<-c.startSync("fund", "021000", true)
	for _, days := range marketIntervals {
		r, _ := c.read("fund", "021000", days)
		if r.Stale || r.AnnualRate <= old.AnnualRate || r.CurrentPrice != 3 {
			t.Fatalf("interval %d: %+v", days, r)
		}
	}
}

func TestMarketCacheStatusReportsOnlyCurrentUserSecurities(t *testing.T) {
	db, h, cookie := apiFixture(t)
	db.Exec(`INSERT INTO assets(user_id,name,category,code,amount,currency) VALUES(1,'fund','fund','021000',10000,'CNY')`)
	db.Exec(`INSERT INTO market_daily_prices(category,code,price_date,price,return_price,currency,source,fetched_at) VALUES('fund','021000','2026-10-04',2,2,'CNY','test','now'),('stock','PRIVATE', '2026-10-04',3,3,'CNY','test','now')`)
	status, v := requestAPI(t, h, cookie, "", "GET", "/api/market/cache-status", "", "")
	if status != 200 {
		t.Fatalf("status %d %v", status, v)
	}
	securities := v["securities"].([]any)
	if len(securities) != 1 || securities[0].(map[string]any)["dailyRows"] != float64(1) {
		t.Fatal(v)
	}
}

func TestIncrementalAdjustmentChangeRefreshesFullHistory(t *testing.T) {
	db, _, _ := apiFixture(t)
	c := marketCacheFor(db)
	phase := 0
	fullCalls := 0
	recent := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	latest := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	c.fetch = func(_ context.Context, _ *http.Client, _, _ string, from time.Time) (marketSeries, error) {
		points := []dailyObservation{{Date: recent, Price: 100, ReturnPrice: 100}, {Date: latest, Price: 100, ReturnPrice: 100}}
		if phase > 0 {
			points[0].ReturnPrice = 50
			points[1].ReturnPrice = 50
		}
		if from.Before(time.Now().AddDate(0, 0, -100)) {
			fullCalls++
			old := 100.0
			if phase > 0 {
				old = 50
			}
			points = append([]dailyObservation{{Date: "2015-01-01", Price: 100, ReturnPrice: old}}, points...)
		}
		return marketSeries{Rows: points, Currency: "CNY", Source: "test", InceptionKnown: true, Quote: 100, QuoteDate: latest}, nil
	}
	<-c.startSync("fund", "021000", false)
	phase = 1
	<-c.startSync("fund", "021000", false)
	var oldest float64
	db.QueryRow(`SELECT return_price FROM market_daily_prices WHERE price_date='2015-01-01'`).Scan(&oldest)
	if fullCalls != 2 || oldest != 50 {
		t.Fatalf("mixed adjustment basis: fullCalls=%d oldest=%v", fullCalls, oldest)
	}
	r, _ := c.read("fund", "021000", 1095)
	if r.AnnualRate != 0 || r.Stale {
		t.Fatalf("wrong revised return %+v", r)
	}
}
