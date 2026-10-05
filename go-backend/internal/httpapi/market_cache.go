package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"fulibu-go/internal/service"
)

var marketIntervals = []int{365, 1095, 1825, 3650}
var marketCaches sync.Map

// marketCache 按数据库共享采集并发、请求合并及汇率和调度互斥状态。
type marketCache struct {
	db          *sql.DB
	mu          sync.Mutex
	flights     map[string]chan struct{}
	slots       chan struct{}
	ctx         context.Context
	client      *http.Client
	fetch       func(context.Context, *http.Client, string, string, time.Time) (marketSeries, error)
	retryDelays []time.Duration
	fxMu        sync.Mutex
	jobsMu      sync.Mutex
}

// marketCacheFor 按数据库取得共享缓存协调器，使接口和定时任务复用并发请求。
func marketCacheFor(db *sql.DB) *marketCache {
	v, _ := marketCaches.LoadOrStore(db, &marketCache{db: db, flights: map[string]chan struct{}{}, slots: make(chan struct{}, 2), ctx: context.Background(), client: &http.Client{Timeout: 12 * time.Second}, fetch: fetchMarketSeries, retryDelays: []time.Duration{2 * time.Second, 5 * time.Second}})
	return v.(*marketCache)
}

// marketIdentity 校验证券类别和代码，返回跨账户复用的规范化行情标识。
func marketIdentity(category, code string) (string, string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if category != "stock" && category != "fund" && category != "money" {
		return "", "", fmt.Errorf("不支持的资产类别")
	}
	if len(code) == 0 || len(code) > 32 || strings.ContainsAny(code, " \t\r\n/?&=") {
		return "", "", fmt.Errorf("不支持的证券代码")
	}
	if (category == "fund" || category == "money") && len(code) == 6 && allDigits(code) && !isExchangeFund(code) {
		return category, code, nil
	}
	if category == "money" {
		return "", "", fmt.Errorf("该货币基金暂无历史数据源，请保留手动利率")
	}
	symbol, _, err := tencentSymbol(category, code)
	if err != nil {
		return "", "", err
	}
	switch {
	case strings.HasPrefix(symbol, "us"):
		return "stock", strings.TrimPrefix(symbol, "us"), nil
	case strings.HasPrefix(symbol, "hk"):
		return "stock", strings.TrimPrefix(symbol, "hk") + ".HK", nil
	default:
		return "stock", strings.ToUpper(symbol), nil
	}
}

// marketDate 返回上海时区的当前计算日期。
func marketDate() string { return time.Now().In(shanghai).Format("2006-01-02") }

// cacheTime 返回带时区的 UTC 获取时间，供新鲜度检查和重启恢复使用。
func cacheTime() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// lastError 读取指定证券最近一次同步错误，成功时返回空字符串。
func (c *marketCache) lastError(cat, code string) string {
	var e string
	c.db.QueryRow(`SELECT last_error FROM market_sync_state WHERE category=? AND code=?`, cat, code).Scan(&e)
	return e
}

// busy 在并发锁保护下检查指定证券是否正在同步。
func (c *marketCache) busy(cat, code string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flights[cat+":"+code] != nil
}

// read 仅从数据库读取最近成功的年化及报价，并标记旧版本或同步失败状态。
func (c *marketCache) read(cat, code string, days int) (marketResult, bool) {
	var payload string
	var version, current int
	var failure string
	err := c.db.QueryRow(`SELECT r.payload,r.input_version,s.input_version,s.last_error FROM market_return_cache r JOIN market_sync_state s ON s.category=r.category AND s.code=r.code WHERE r.category=? AND r.code=? AND r.lookback_days=? ORDER BY r.calculation_date DESC LIMIT 1`, cat, code, days).Scan(&payload, &version, &current, &failure)
	var result marketResult
	if err != nil || json.Unmarshal([]byte(payload), &result) != nil || !result.AnnualReady || (cat == "fund" && result.ReturnMethod != "total-return-v2") {
		return result, false
	}
	result.Stale = result.CalculationDate != marketDate() || version != current || failure != ""
	result.Error = failure
	c.db.QueryRow(`SELECT price,currency,price_date,fetched_at FROM market_quotes WHERE category=? AND code=?`, cat, code).Scan(&result.CurrentPrice, &result.PriceCurrency, &result.PriceDate, &result.FetchedAt)
	return result, true
}

// get 优先返回持久化缓存，发现缺失或过期时合并后台补齐请求。
func (c *marketCache) get(category, code string, days int, force bool) (marketResult, error) {
	if days < 1 || days > 3650 {
		return marketResult{}, fmt.Errorf("不支持的历史区间")
	}
	cat, key, err := marketIdentity(category, code)
	if err != nil {
		return marketResult{}, err
	}
	result, ok := c.read(cat, key, days)
	var expectedCount, actualCount int
	c.db.QueryRow(`SELECT observation_count FROM market_sync_state WHERE category=? AND code=?`, cat, key).Scan(&expectedCount)
	c.db.QueryRow(`SELECT COUNT(*) FROM market_daily_prices WHERE category=? AND code=?`, cat, key).Scan(&actualCount)
	damaged := expectedCount > 0 && actualCount != expectedCount
	if damaged {
		result.Stale = true
	}
	// Missing derived intervals can be calculated from local observations alone.
	if !ok && !damaged && !c.busy(cat, key) {
		_ = c.calculate(context.Background(), cat, key, []int{days})
		result, ok = c.read(cat, key, days)
	}
	var checked, retry, covered string
	c.db.QueryRow(`SELECT checked_at,retry_after,covered_from FROM market_sync_state WHERE category=? AND code=?`, cat, key).Scan(&checked, &retry, &covered)
	checkAt, _ := time.Parse(time.RFC3339Nano, checked)
	retryAt, _ := time.Parse(time.RFC3339Nano, retry)
	due := force || (!time.Now().Before(retryAt) && (covered == "" || result.Stale || time.Since(checkAt) >= 30*time.Minute))
	if due {
		c.startSync(cat, key, force || damaged)
	}
	if !ok {
		result = marketResult{RequestedDays: days, CalculationDate: marketDate(), Error: c.lastError(cat, key)}
		c.db.QueryRow(`SELECT price,currency,price_date,fetched_at FROM market_quotes WHERE category=? AND code=?`, cat, key).Scan(&result.CurrentPrice, &result.PriceCurrency, &result.PriceDate, &result.FetchedAt)
	}
	result.Category = category
	result.Code = strings.ToUpper(strings.TrimSpace(code))
	result.Pending = c.busy(cat, key)
	if !ok && !result.Pending && result.Error == "" {
		result.Error = "请求的历史区间未完整覆盖，暂无法计算年化"
	}
	return result, nil
}

// startSync 合并同证券的同步任务，限制并发及重试，并返回任务完成信号。
func (c *marketCache) startSync(cat, code string, force bool) <-chan struct{} {
	key := cat + ":" + code
	c.mu.Lock()
	if done := c.flights[key]; done != nil {
		c.mu.Unlock()
		return done
	}
	done := make(chan struct{})
	c.flights[key] = done
	parent := c.ctx
	c.mu.Unlock()
	go func() {
		defer func() { c.mu.Lock(); delete(c.flights, key); close(done); c.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
		defer cancel()
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-c.slots }()
		var err error
		for attempt := 0; attempt <= len(c.retryDelays); attempt++ {
			if attempt > 0 {
				timer := time.NewTimer(c.retryDelays[attempt-1])
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			err = c.sync(ctx, cat, code, force)
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				break
			}
		}
		if err != nil {
			_, writeErr := c.db.Exec(`INSERT INTO market_sync_state(category,code,checked_at,last_error,retry_after) VALUES(?,?,?,?,?) ON CONFLICT(category,code) DO UPDATE SET checked_at=excluded.checked_at,last_error=excluded.last_error,retry_after=excluded.retry_after`, cat, code, cacheTime(), err.Error(), time.Now().UTC().Add(5*time.Minute).Format(time.RFC3339Nano))
			if writeErr != nil {
				log.Printf("[market-cache] persist failure %s: %v", key, writeErr)
			}
			log.Printf("[market-cache] sync_failed key=%s error=%v", key, err)
		} else {
			log.Printf("[market-cache] sync_success key=%s", key)
		}
	}()
	return done
}

// sync 补取历史和必要汇率，原子写入日数据，再重算各档年化。
func (c *marketCache) sync(ctx context.Context, cat, code string, force bool) error {
	from := time.Now().UTC().AddDate(0, 0, -3695)
	var covered, full string
	var inception bool
	c.db.QueryRow(`SELECT covered_from,full_checked_at,inception_known FROM market_sync_state WHERE category=? AND code=?`, cat, code).Scan(&covered, &full, &inception)
	fullAt, _ := time.Parse(time.RFC3339Nano, full)
	fullSync := force || covered == "" || time.Since(fullAt) >= 7*24*time.Hour
	if !fullSync {
		from = time.Now().UTC().AddDate(0, 0, -30)
	}
	series, err := c.fetch(ctx, c.client, cat, code, from)
	if err != nil {
		return err
	}
	if !fullSync && len(series.Rows) > 0 {
		// A historical adjustment change in the overlap revises the basis of
		// older prices too. Fetch the retained range before publishing returns.
		newest := ""
		for _, row := range series.Rows {
			if row.Date > newest {
				newest = row.Date
			}
		}
		revised := false
		for _, row := range series.Rows {
			if row.Date >= newest {
				continue
			} // Today's unfinished close may fluctuate.
			var previous float64
			if c.db.QueryRowContext(ctx, `SELECT return_price FROM market_daily_prices WHERE category=? AND code=? AND price_date=?`, cat, code, row.Date).Scan(&previous) == nil && math.Abs(previous-row.ReturnPrice) > 1e-9*math.Max(1, math.Abs(previous)) {
				revised = true
				break
			}
		}
		if revised {
			fullSync = true
			from = time.Now().UTC().AddDate(0, 0, -3695)
			series, err = c.fetch(ctx, c.client, cat, code, from)
			if err != nil {
				return err
			}
		}
	}
	if len(series.Rows) < 2 || !finitePositive(series.Quote) || !serviceCurrency(series.Currency) {
		return fmt.Errorf("行情序列或报价无效")
	}
	series.Rows = sortObservations(series.Rows)
	for _, row := range series.Rows {
		if _, err := time.Parse("2006-01-02", row.Date); err != nil || !finitePositive(row.Price) || !finitePositive(row.ReturnPrice) || math.IsNaN(row.Income) || math.IsInf(row.Income, 0) {
			return fmt.Errorf("每日行情无效")
		}
	}
	// Do all external work before beginning SQLite's single-connection transaction.
	if series.Currency == "USD" {
		if err = c.ensureUSDFX(ctx, time.Now().UTC().AddDate(0, 0, -3702).Format("2006-01-02"), series.Rows[len(series.Rows)-1].Date, force); err != nil {
			return err
		}
	}
	now := cacheTime()
	if fullSync {
		full = now
		covered = from.Format("2006-01-02")
	}
	if series.InceptionKnown {
		inception = true
		if fullSync {
			covered = series.Rows[0].Date
		}
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The complete returned interval replaces its old observations atomically.
	if _, err = tx.ExecContext(ctx, `DELETE FROM market_daily_prices WHERE category=? AND code=? AND price_date>=? AND price_date<=?`, cat, code, series.Rows[0].Date, series.Rows[len(series.Rows)-1].Date); err != nil {
		return err
	}
	for _, row := range series.Rows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO market_daily_prices(category,code,price_date,price,return_price,income,annual_rate,currency,source,fetched_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(category,code,price_date) DO UPDATE SET price=excluded.price,return_price=excluded.return_price,income=excluded.income,annual_rate=excluded.annual_rate,currency=excluded.currency,source=excluded.source,fetched_at=excluded.fetched_at`, cat, code, row.Date, row.Price, row.ReturnPrice, row.Income, row.AnnualRate, series.Currency, series.Source, now); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO market_quotes(category,code,price,currency,price_date,source,fetched_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(category,code) DO UPDATE SET price=excluded.price,currency=excluded.currency,price_date=excluded.price_date,source=excluded.source,fetched_at=excluded.fetched_at`, cat, code, series.Quote, series.Currency, series.QuoteDate, series.Source, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO market_sync_state(category,code,covered_from,covered_to,inception_known,input_version,checked_at,full_checked_at,last_success) VALUES(?,?,?,?,?,1,?,?,?) ON CONFLICT(category,code) DO UPDATE SET covered_from=excluded.covered_from,covered_to=excluded.covered_to,inception_known=excluded.inception_known,input_version=input_version+1,checked_at=excluded.checked_at,full_checked_at=excluded.full_checked_at,last_success=excluded.last_success,last_error='',retry_after=''`, cat, code, covered, series.Rows[len(series.Rows)-1].Date, inception, now, full, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE market_sync_state SET observation_count=(SELECT COUNT(*) FROM market_daily_prices WHERE category=? AND code=?) WHERE category=? AND code=?`, cat, code, cat, code); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return c.calculate(ctx, cat, code, marketIntervals)
}

// serviceCurrency 判断行情源返回的币种是否为当前采集器支持的币种。
func serviceCurrency(s string) bool {
	switch s {
	case "CNY", "USD", "HKD":
		return true
	}
	return false
}

// calculate 在一致的数据库事务中，根据日序列计算并保存指定区间年化。
func (c *marketCache) calculate(ctx context.Context, cat, code string, intervals []int) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT price_date,price,return_price,income,annual_rate FROM market_daily_prices WHERE category=? AND code=? ORDER BY price_date`, cat, code)
	if err != nil {
		return err
	}
	points := []dailyObservation{}
	for rows.Next() {
		var p dailyObservation
		if err = rows.Scan(&p.Date, &p.Price, &p.ReturnPrice, &p.Income, &p.AnnualRate); err != nil {
			rows.Close()
			return err
		}
		points = append(points, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(points) < 2 {
		return fmt.Errorf("历史不足两个数据点")
	}
	if cat == "fund" {
		raw := make([]service.PriceObservation, len(points))
		for i, p := range points {
			raw[i] = service.PriceObservation{Date: p.Date, Price: p.Price, TotalPrice: p.ReturnPrice}
		}
		adjusted, e := service.FundTotalReturn(raw)
		if e != nil {
			return e
		}
		for i, p := range adjusted {
			points[i].ReturnPrice = p.TotalPrice
		}
	}
	var inception bool
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT inception_known,input_version FROM market_sync_state WHERE category=? AND code=?`, cat, code).Scan(&inception, &version); err != nil {
		return err
	}
	var price float64
	var currency, priceDate, fetched, source string
	if err = tx.QueryRowContext(ctx, `SELECT price,currency,price_date,fetched_at,source FROM market_quotes WHERE category=? AND code=?`, cat, code).Scan(&price, &currency, &priceDate, &fetched, &source); err != nil {
		return err
	}
	latest := points[len(points)-1]
	lastAt, _ := time.Parse("2006-01-02", latest.Date)
	for _, days := range intervals {
		cutoff := lastAt.AddDate(0, 0, -days).Format("2006-01-02")
		first := points[0]
		found := false
		for _, point := range points {
			if point.Date <= cutoff {
				first = point
				found = true
			} else {
				break
			}
		}
		if !found && !inception && cat != "money" {
			continue
		} // Unknown provider coverage is not confirmed inception.
		firstAt, _ := time.Parse("2006-01-02", first.Date)
		startPrice, endPrice := first.ReturnPrice, latest.ReturnPrice
		if currency == "USD" {
			r, e := historicalUSDFrom(tx, first.Date)
			if e != nil {
				return e
			}
			startPrice *= r
			r, e = historicalUSDFrom(tx, latest.Date)
			if e != nil {
				return e
			}
			endPrice *= r
		}
		actual := int(lastAt.Sub(firstAt).Hours() / 24)
		if actual < 1 {
			return fmt.Errorf("历史日期间隔无效")
		}
		rate := annualized(marketPoint{price: startPrice, at: firstAt}, marketPoint{price: endPrice, at: lastAt})
		if cat == "money" {
			sum := 0.0
			count := 0
			for i := len(points) - 1; i >= 0 && count < 7; i-- {
				sum += points[i].Income
				count++
			}
			rate = sum / float64(count) * 365 / 100
			// Published seven-day annual yield is authoritative when available.
			if latest.AnnualRate != 0 {
				rate = latest.AnnualRate
			}
		}
		if math.IsNaN(rate) || math.IsInf(rate, 0) {
			return fmt.Errorf("年化结果无效")
		}
		result := marketResult{ReturnMethod: "total-return-v2", Category: cat, Code: code, AnnualRate: rate, AnnualReady: true, RequestedDays: days, ActualDays: actual, HistoryLimited: !found && cat != "money", StartDate: first.Date, EndDate: latest.Date, CalculationDate: marketDate(), CurrentPrice: price, PriceCurrency: currency, PriceDate: priceDate, Source: source, FetchedAt: fetched}
		payload, _ := json.Marshal(result)
		now := cacheTime()
		_, e := tx.ExecContext(ctx, `INSERT INTO market_returns(category,code,lookback_days,calculation_date,annual_rate,period_return,requested_days,actual_days,history_limited,start_date,end_date,source,calculated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(category,code,lookback_days,calculation_date) DO UPDATE SET annual_rate=excluded.annual_rate,period_return=excluded.period_return,actual_days=excluded.actual_days,history_limited=excluded.history_limited,start_date=excluded.start_date,end_date=excluded.end_date,source=excluded.source,calculated_at=excluded.calculated_at`, cat, code, days, result.CalculationDate, rate, (endPrice/startPrice-1)*100, days, actual, result.HistoryLimited, first.Date, latest.Date, source, now)
		if e == nil {
			_, e = tx.ExecContext(ctx, `INSERT INTO market_return_cache(category,code,lookback_days,calculation_date,input_version,payload,calculated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(category,code,lookback_days,calculation_date) DO UPDATE SET input_version=excluded.input_version,payload=excluded.payload,calculated_at=excluded.calculated_at`, cat, code, days, result.CalculationDate, version, string(payload), now)
		}
		if e != nil {
			tx.Rollback()
			return e
		}
	}
	return tx.Commit()
}

// rateQuerier 统一数据库与事务的汇率查询接口。
type rateQuerier interface{ QueryRow(string, ...any) *sql.Row }

// historicalUSD 从数据库读取指定交易日及此前七天内最近有效的美元汇率。
func (c *marketCache) historicalUSD(date string) (float64, error) {
	return historicalUSDFrom(c.db, date)
}

// historicalUSDFrom 通过数据库或计算事务查询有效历史汇率，缺失时明确返回错误。
func historicalUSDFrom(q rateQuerier, date string) (float64, error) {
	at, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, err
	}
	var rate float64
	err = q.QueryRow(`SELECT cny_rate FROM exchange_rate_history WHERE currency='USD' AND rate_date<=? AND rate_date>=? ORDER BY rate_date DESC LIMIT 1`, date, at.AddDate(0, 0, -7).Format("2006-01-02")).Scan(&rate)
	if err != nil || !finitePositive(rate) {
		return 0, fmt.Errorf("缺少 %s 的历史 USD/CNY 汇率", date)
	}
	return rate, nil
}

// ensureUSDFX 串行补齐美元历史汇率范围，成功后才记录查询覆盖状态。
func (c *marketCache) ensureUSDFX(ctx context.Context, from, to string, force bool) error {
	c.fxMu.Lock()
	defer c.fxMu.Unlock()
	var oldFrom, oldTo, checked string
	c.db.QueryRowContext(ctx, `SELECT covered_from,covered_to,checked_at FROM market_sync_state WHERE category='fx' AND code='USD'`).Scan(&oldFrom, &oldTo, &checked)
	last, _ := time.Parse(time.RFC3339Nano, checked)
	if oldFrom != "" && oldFrom <= from && oldTo >= to && ((!force && time.Since(last) < 7*24*time.Hour) || time.Since(last) < time.Minute) {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.frankfurter.dev/v2/rates?"+url.Values{"base": {"USD"}, "quotes": {"CNY"}, "from": {from}, "to": {to}}.Encode(), nil)
	if err != nil {
		return err
	}
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("历史汇率 HTTP %d", res.StatusCode)
	}
	var rates []frankfurterRate
	if err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&rates); err != nil {
		return err
	}
	if len(rates) == 0 {
		return errors.New("历史汇率为空")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := cacheTime()
	valid := 0
	for _, r := range rates {
		if r.Base != "USD" || r.Quote != "CNY" || !finitePositive(r.Rate) {
			return errors.New("历史汇率无效")
		}
		if _, err = time.Parse("2006-01-02", r.Date); err != nil {
			return err
		}
		valid++
		if _, err = tx.ExecContext(ctx, `INSERT INTO exchange_rate_history(currency,cny_rate,rate_date,source,fetched_at) VALUES('USD',?,?,?,?) ON CONFLICT(currency,rate_date) DO UPDATE SET cny_rate=excluded.cny_rate,source=excluded.source,fetched_at=excluded.fetched_at`, r.Rate, r.Date, "Frankfurter", now); err != nil {
			return err
		}
	}
	if valid == 0 {
		return errors.New("没有有效历史汇率")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO market_sync_state(category,code,covered_from,covered_to,checked_at,last_success) VALUES('fx','USD',?,?,?,?) ON CONFLICT(category,code) DO UPDATE SET covered_from=excluded.covered_from,covered_to=excluded.covered_to,checked_at=excluded.checked_at,last_success=excluded.last_success`, from, to, now, now); err != nil {
		return err
	}
	return tx.Commit()
}
