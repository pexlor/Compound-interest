package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type marketResult struct {
	Category        string  `json:"category"`
	Code            string  `json:"code"`
	AnnualRate      float64 `json:"annualRate"`
	RequestedDays   int     `json:"requestedDays"`
	ActualDays      int     `json:"actualDays"`
	HistoryLimited  bool    `json:"historyLimited"`
	StartDate       string  `json:"startDate"`
	EndDate         string  `json:"endDate"`
	CalculationDate string  `json:"calculationDate"`
	CurrentPrice    float64 `json:"currentPrice,omitempty"`
	PriceCurrency   string  `json:"priceCurrency,omitempty"`
	PriceDate       string  `json:"priceDate,omitempty"`
	Source          string  `json:"source"`
	Stale           bool    `json:"stale,omitempty"`
}

type tencentKlineResponse struct {
	Code int                        `json:"code"`
	Msg  string                     `json:"msg"`
	Data map[string]json.RawMessage `json:"data"`
}

type tencentKlineData struct {
	QfqDay []json.RawMessage `json:"qfqday"`
	Day    []json.RawMessage `json:"day"`
}

type marketPoint struct {
	price float64
	at    time.Time
}

// tencentSymbol converts UI codes to Tencent Finance symbols. Plain six-digit
// mainland codes are resolved by the usual exchange-code convention.
func tencentSymbol(category, code string) (symbol, currency string, err error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", "", fmt.Errorf("缺少证券代码")
	}
	if strings.HasPrefix(code, "SH") && len(code) == 8 && allDigits(code[2:]) {
		return strings.ToLower(code), "CNY", nil
	}
	if strings.HasPrefix(code, "SZ") && len(code) == 8 && allDigits(code[2:]) {
		return strings.ToLower(code), "CNY", nil
	}
	if strings.HasSuffix(code, ".SS") && len(code) == 9 && allDigits(code[:6]) {
		return "sh" + code[:6], "CNY", nil
	}
	if strings.HasSuffix(code, ".SZ") && len(code) == 9 && allDigits(code[:6]) {
		return "sz" + code[:6], "CNY", nil
	}
	if strings.HasPrefix(code, "HK") && len(code) == 7 && allDigits(code[2:]) {
		return strings.ToLower(code), "HKD", nil
	}
	if strings.HasSuffix(code, ".HK") && allDigits(strings.TrimSuffix(code, ".HK")) {
		return "hk" + fmt.Sprintf("%05s", strings.TrimSuffix(code, ".HK")), "HKD", nil
	}
	if len(code) == 6 && allDigits(code) {
		isExchangeFund := strings.HasPrefix(code, "51") || strings.HasPrefix(code, "52") || strings.HasPrefix(code, "56") || strings.HasPrefix(code, "58") || strings.HasPrefix(code, "15") || strings.HasPrefix(code, "16")
		if category == "fund" && !isExchangeFund {
			return "", "", fmt.Errorf("腾讯财经不提供场外基金 %s 的实时估值；请输入 ETF 代码", code)
		}
		if strings.HasPrefix(code, "5") || strings.HasPrefix(code, "6") || strings.HasPrefix(code, "9") {
			return "sh" + code, "CNY", nil
		}
		return "sz" + code, "CNY", nil
	}
	if strings.HasPrefix(code, "US") && len(code) > 2 {
		return "us" + code[2:], "USD", nil
	}
	return "us" + code, "USD", nil
}

func allDigits(value string) bool { return value != "" && strings.Trim(value, "0123456789") == "" }

// fetchTencentQuote reads the live quote endpoint and rejects any non-quote
// response (such as an error HTML page) before parsing it.
func fetchTencentQuote(client *http.Client, symbol string) (float64, string, error) {
	response, err := client.Get("https://qt.gtimg.cn/q=" + url.QueryEscape(symbol))
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("腾讯财经返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, "", err
	}
	quote := string(body)
	start, end := strings.Index(quote, "\""), strings.LastIndex(quote, "\"")
	if start < 0 || end <= start {
		return 0, "", fmt.Errorf("未找到 %s 行情", symbol)
	}
	fields := strings.Split(quote[start+1:end], "~")
	if len(fields) < 31 {
		return 0, "", fmt.Errorf("腾讯财经行情数据不完整")
	}
	price, err := strconv.ParseFloat(fields[3], 64)
	if err != nil || price <= 0 {
		return 0, "", fmt.Errorf("未取得 %s 最新价格", symbol)
	}
	date := quoteDate(fields[30])
	if date == "" {
		date = time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	}
	return price, date, nil
}

func quoteDate(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "/", "-"))
	if len(value) >= 10 && value[4:5] == "-" && value[7:8] == "-" {
		return value[:10]
	}
	if len(value) >= 8 && allDigits(value[:8]) {
		return value[:4] + "-" + value[4:6] + "-" + value[6:8]
	}
	return ""
}

func isExchangeFund(code string) bool {
	return len(code) == 6 && allDigits(code) && (strings.HasPrefix(code, "51") || strings.HasPrefix(code, "52") || strings.HasPrefix(code, "56") || strings.HasPrefix(code, "58") || strings.HasPrefix(code, "15") || strings.HasPrefix(code, "16"))
}

func isUSSecurity(code string) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	if strings.HasPrefix(code, "US") && len(code) > 2 {
		return true
	}
	return code != "" && !allDigits(code) && !strings.HasPrefix(code, "SH") && !strings.HasPrefix(code, "SZ") && !strings.HasSuffix(code, ".HK")
}

// fetchFundQuote restores the old Eastmoney source for off-exchange funds.
func fetchFundQuote(client *http.Client, code string) (float64, string, error) {
	endpoint := "https://api.fund.eastmoney.com/f10/lsjz?" + url.Values{"fundCode": {code}, "pageIndex": {"1"}, "pageSize": {"1"}}.Encode()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Referer", "https://fundf10.eastmoney.com/")
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("东方财富基金服务返回 HTTP %d", response.StatusCode)
	}
	var payload struct {
		Data struct {
			List []struct {
				Date string `json:"FSRQ"`
				NAV  string `json:"DWJZ"`
			} `json:"LSJZList"`
		} `json:"Data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return 0, "", err
	}
	if len(payload.Data.List) == 0 {
		return 0, "", fmt.Errorf("没有找到基金 %s 的最新净值", code)
	}
	price, err := strconv.ParseFloat(payload.Data.List[0].NAV, 64)
	if err != nil || price <= 0 {
		return 0, "", fmt.Errorf("基金 %s 的最新净值无效", code)
	}
	return price, payload.Data.List[0].Date, nil
}

func fetchLiveQuote(client *http.Client, category, code string) (float64, string, string, error) {
	if category == "fund" && !isExchangeFund(code) && !isUSSecurity(code) {
		price, date, err := fetchFundQuote(client, code)
		return price, "CNY", date, err
	}
	symbol, currency, err := tencentSymbol(category, code)
	if err != nil {
		return 0, "", "", err
	}
	price, date, err := fetchTencentQuote(client, symbol)
	return price, currency, date, err
}

// refreshMarketAssetValues writes the latest quote into every quantity-based
// asset before a portfolio snapshot is calculated. A quote failure leaves its
// most recently saved valuation intact, so one unavailable symbol cannot
// prevent the rest of the portfolio from being recorded.
func (a *app) refreshMarketAssetValues(userID int64) error {
	assets, err := a.listAssets(userID)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 12 * time.Second}
	for _, asset := range assets {
		if (asset.Category != "stock" && asset.Category != "fund") || asset.Code == nil || asset.Quantity == nil || *asset.Quantity <= 0 {
			continue
		}
		price, quoteCurrency, _, err := fetchLiveQuote(client, asset.Category, *asset.Code)
		if err != nil {
			continue
		}
		amount := int64(math.Round(*asset.Quantity * price * 100))
		if amount <= 0 {
			continue
		}
		if _, err := a.db.Exec("UPDATE assets SET amount=?,currency=? WHERE id=? AND user_id=?", amount, quoteCurrency, asset.ID, userID); err != nil {
			return err
		}
	}
	return nil
}

func fetchTencentHistory(client *http.Client, symbol string, days int) ([]json.RawMessage, error) {
	parameter := fmt.Sprintf("%s,day,,,%d,qfq", symbol, days+10)
	endpoint := "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?" + url.Values{"param": {parameter}}.Encode()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("腾讯财经历史服务返回 HTTP %d", response.StatusCode)
	}
	var payload tencentKlineResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("腾讯财经历史响应无效: %w", err)
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("腾讯财经历史服务错误: %s", payload.Msg)
	}
	rawData, ok := payload.Data[symbol]
	if !ok {
		return nil, fmt.Errorf("未找到 %s 历史行情", symbol)
	}
	var data tencentKlineData
	if err := json.Unmarshal(rawData, &data); err != nil {
		return nil, fmt.Errorf("腾讯财经历史数据无效: %w", err)
	}
	if len(data.QfqDay) > 0 {
		return data.QfqDay, nil
	}
	return data.Day, nil
}

func tencentHistoryPoints(rows []json.RawMessage, cutoff time.Time) []marketPoint {
	points := make([]marketPoint, 0, len(rows))
	for _, rawRow := range rows {
		var row []string
		if err := json.Unmarshal(rawRow, &row); err != nil {
			continue
		}
		if len(row) < 3 {
			continue
		}
		at, dateErr := time.Parse("2006-01-02", row[0])
		price, priceErr := strconv.ParseFloat(row[2], 64)
		if dateErr == nil && priceErr == nil && price > 0 && !at.Before(cutoff) {
			points = append(points, marketPoint{price: price, at: at})
		}
	}
	return points
}

func annualized(first, last marketPoint) float64 {
	years := math.Max(1/365.25, float64(last.at.Sub(first.at).Hours())/(24*365.25))
	return (math.Pow(last.price/first.price, 1/years) - 1) * 100
}

// fetchUSMarket restores Yahoo Finance adjusted closes for US historical
// returns, and converts both endpoints with Frankfurter USD/CNY history.
func fetchUSMarket(client *http.Client, code string, days int, current float64, priceDate string) (marketResult, error) {
	ticker := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(code)), "US")
	end := time.Now().Unix() + 86400
	start := end - int64(days+45)*86400
	endpoint := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?period1=%d&period2=%d&interval=1d&events=div,splits&includeAdjustedClose=true", url.PathEscape(strings.ReplaceAll(ticker, ".", "-")), start, end)
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return marketResult{}, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response, err := client.Do(request)
	if err != nil {
		return marketResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return marketResult{}, fmt.Errorf("Yahoo Finance 返回 HTTP %d", response.StatusCode)
	}
	var payload struct {
		Chart struct {
			Result []struct {
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					AdjClose []struct {
						Values []*float64 `json:"adjclose"`
					} `json:"adjclose"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return marketResult{}, err
	}
	if len(payload.Chart.Result) == 0 || len(payload.Chart.Result[0].Indicators.AdjClose) == 0 {
		return marketResult{}, fmt.Errorf("没有找到美股 %s 的复权历史行情", ticker)
	}
	result := payload.Chart.Result[0]
	points := []marketPoint{}
	for i, timestamp := range result.Timestamp {
		if i >= len(result.Indicators.AdjClose[0].Values) || result.Indicators.AdjClose[0].Values[i] == nil || *result.Indicators.AdjClose[0].Values[i] <= 0 {
			continue
		}
		points = append(points, marketPoint{price: *result.Indicators.AdjClose[0].Values[i], at: time.Unix(timestamp, 0).UTC()})
	}
	if len(points) < 2 {
		return marketResult{}, fmt.Errorf("美股 %s 的复权历史行情不足", ticker)
	}
	last := points[len(points)-1]
	cutoff := last.at.AddDate(0, 0, -days)
	first := points[0]
	limited := true
	for _, point := range points {
		if !point.at.After(cutoff) {
			first, limited = point, false
		}
	}
	startRate, err := fetchHistoricalUSDCNY(client, first.at.Format("2006-01-02"))
	if err != nil {
		return marketResult{}, err
	}
	endRate, err := fetchHistoricalUSDCNY(client, last.at.Format("2006-01-02"))
	if err != nil {
		return marketResult{}, err
	}
	first.price *= startRate
	last.price *= endRate
	actualDays := int(last.at.Sub(first.at).Hours() / 24)
	return marketResult{Code: strings.ToUpper(code), AnnualRate: annualized(first, last), RequestedDays: days, ActualDays: actualDays, HistoryLimited: limited, StartDate: first.at.Format("2006-01-02"), EndDate: last.at.Format("2006-01-02"), CalculationDate: time.Now().In(shanghai).Format("2006-01-02"), CurrentPrice: current, PriceCurrency: "USD", PriceDate: priceDate, Source: "Yahoo Finance 复权收盘价（人民币汇率调整）"}, nil
}

func fetchHistoricalUSDCNY(client *http.Client, date string) (float64, error) {
	at, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, err
	}
	endpoint := "https://api.frankfurter.dev/v2/rates?" + url.Values{"base": {"USD"}, "quotes": {"CNY"}, "from": {at.AddDate(0, 0, -7).Format("2006-01-02")}, "to": {date}}.Encode()
	response, err := client.Get(endpoint)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Frankfurter 返回 HTTP %d", response.StatusCode)
	}
	var rows []struct {
		Date, Base, Quote string
		Rate              float64
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&rows); err != nil {
		return 0, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Base == "USD" && rows[i].Quote == "CNY" && rows[i].Rate > 0 {
			return rows[i].Rate, nil
		}
	}
	return 0, fmt.Errorf("没有找到 %s 的 USD/CNY 历史汇率", date)
}

func fetchFundMarket(client *http.Client, code string, days int, current float64, priceDate string) (marketResult, error) {
	endpoint := "https://api.fund.eastmoney.com/f10/lsjz?" + url.Values{"fundCode": {code}, "pageIndex": {"1"}, "pageSize": {"100"}}.Encode()
	request, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	request.Header.Set("Referer", "https://fundf10.eastmoney.com/")
	response, err := client.Do(request)
	if err != nil {
		return marketResult{}, err
	}
	defer response.Body.Close()
	var payload struct {
		Data struct {
			List []struct {
				Date     string `json:"FSRQ"`
				NAV      string `json:"DWJZ"`
				TotalNAV string `json:"LJJZ"`
			} `json:"LSJZList"`
		} `json:"Data"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload) != nil || len(payload.Data.List) < 2 {
		return marketResult{}, fmt.Errorf("东方财富基金历史净值不可用")
	}
	latestRow := payload.Data.List[0]
	latestDate, err := time.Parse("2006-01-02", latestRow.Date)
	if err != nil {
		return marketResult{}, err
	}
	target := latestDate.AddDate(0, 0, -days)
	oldest := payload.Data.List[len(payload.Data.List)-1]
	limited := true
	for _, row := range payload.Data.List {
		date, e := time.Parse("2006-01-02", row.Date)
		if e == nil && !date.After(target) {
			oldest, limited = row, false
			break
		}
	}
	start, _ := strconv.ParseFloat(firstNonEmpty(oldest.TotalNAV, oldest.NAV), 64)
	end, _ := strconv.ParseFloat(firstNonEmpty(latestRow.TotalNAV, latestRow.NAV), 64)
	if start <= 0 || end <= 0 {
		return marketResult{}, fmt.Errorf("东方财富基金净值无效")
	}
	firstDate, _ := time.Parse("2006-01-02", oldest.Date)
	last := marketPoint{price: end, at: latestDate}
	first := marketPoint{price: start, at: firstDate}
	return marketResult{Code: strings.ToUpper(code), AnnualRate: annualized(first, last), RequestedDays: days, ActualDays: int(latestDate.Sub(firstDate).Hours() / 24), HistoryLimited: limited, StartDate: oldest.Date, EndDate: latestRow.Date, CalculationDate: time.Now().In(shanghai).Format("2006-01-02"), CurrentPrice: current, PriceCurrency: "CNY", PriceDate: priceDate, Source: "东方财富历史净值"}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// fetchMarket uses Tencent Finance for both live quotes and daily history.
func fetchMarket(category, code string, days int) (marketResult, error) {
	if days < 1 || days > 3650 {
		return marketResult{}, fmt.Errorf("不支持的历史区间")
	}
	client := &http.Client{Timeout: 12 * time.Second}
	current, currency, priceDate, err := fetchLiveQuote(client, category, code)
	if err != nil {
		return marketResult{}, err
	}
	if isUSSecurity(code) {
		return fetchUSMarket(client, code, days, current, priceDate)
	}
	if category == "fund" && !isExchangeFund(code) {
		return fetchFundMarket(client, code, days, current, priceDate)
	}
	symbol, _, err := tencentSymbol(category, code)
	if err != nil {
		return marketResult{}, err
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	rows, historyErr := fetchTencentHistory(client, symbol, days)
	points := []marketPoint(nil)
	if historyErr == nil {
		points = tencentHistoryPoints(rows, cutoff)
	}
	if len(points) < 2 {
		// A live quote is still useful when Tencent has not published a usable
		// daily series for a market (currently common for US symbols). Return it
		// explicitly as history-limited rather than replacing it with another
		// provider or failing the add/edit flow.
		return marketResult{Code: strings.ToUpper(code), RequestedDays: days, ActualDays: 1, HistoryLimited: true, StartDate: priceDate, EndDate: priceDate, CalculationDate: time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02"), CurrentPrice: current, PriceCurrency: currency, PriceDate: priceDate, Source: "腾讯财经"}, nil
	}
	first, last := points[0], points[len(points)-1]
	years := math.Max(1/365.25, float64(last.at.Sub(first.at).Hours())/(24*365.25))
	annual := (math.Pow(last.price/first.price, 1/years) - 1) * 100
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	return marketResult{Code: strings.ToUpper(code), AnnualRate: annual, RequestedDays: days, ActualDays: int(last.at.Sub(first.at).Hours() / 24), HistoryLimited: len(points) < days/2, StartDate: first.at.Format("2006-01-02"), EndDate: last.at.Format("2006-01-02"), CalculationDate: today, CurrentPrice: current, PriceCurrency: currency, PriceDate: priceDate, Source: "腾讯财经"}, nil
}

// cachedMarketPrice derives a last-known unit price when Tencent is temporarily unavailable.
func (a *app) cachedMarketPrice(userID int64, category, code string, days int) (marketResult, bool) {
	var amount int64
	var quantity float64
	var currency string
	err := a.db.QueryRow(`SELECT amount,quantity,currency FROM assets WHERE user_id=? AND category=? AND code=? AND quantity>0 ORDER BY id LIMIT 1`, userID, category, code).Scan(&amount, &quantity, &currency)
	if err != nil || amount <= 0 || quantity <= 0 {
		return marketResult{}, false
	}
	annualRate, actualDays := 0.0, 0
	historyLimited := true
	startDate, endDate, calculationDate, source := "", "", "", "已保存市值"
	cacheErr := a.db.QueryRow(`SELECT annual_rate,actual_days,history_limited,start_date,end_date,calculation_date,source FROM market_returns WHERE category=? AND code=? COLLATE NOCASE AND lookback_days=? ORDER BY calculation_date DESC LIMIT 1`, category, code, days).Scan(&annualRate, &actualDays, &historyLimited, &startDate, &endDate, &calculationDate, &source)
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	if cacheErr != nil {
		calculationDate = today
	}
	return marketResult{Category: category, Code: strings.ToUpper(code), AnnualRate: annualRate, RequestedDays: days, ActualDays: actualDays, HistoryLimited: historyLimited, StartDate: startDate, EndDate: endDate, CalculationDate: calculationDate, CurrentPrice: float64(amount) / 100 / quantity, PriceCurrency: currency, PriceDate: calculationDate, Source: source, Stale: true}, true
}

// market handles one or many market quote requests.
func (a *app) market(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method != http.MethodGet {
		fail(w, 405, "方法不允许")
		return
	}
	days := 365
	if raw := r.URL.Query().Get("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			fail(w, 400, "不支持的历史区间")
			return
		}
		days = value
	}
	code, category := strings.TrimSpace(r.URL.Query().Get("code")), strings.TrimSpace(r.URL.Query().Get("category"))
	if code != "" {
		result, err := fetchMarket(category, code, days)
		if err != nil {
			if cached, ok := a.cachedMarketPrice(u.ID, category, code, days); ok {
				out(w, 200, cached)
				return
			}
			fail(w, 502, "行情读取失败: "+err.Error())
			return
		}
		result.Category = category
		out(w, 200, result)
		return
	}
	assets, err := a.listAssets(u.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	results := []marketResult{}
	errors := []map[string]string{}
	for _, asset := range assets {
		if asset.Code == nil || (asset.Category != "stock" && asset.Category != "fund") {
			continue
		}
		result, err := fetchMarket(asset.Category, *asset.Code, days)
		if err != nil {
			if cached, ok := a.cachedMarketPrice(u.ID, asset.Category, *asset.Code, days); ok {
				results = append(results, cached)
				continue
			}
			errors = append(errors, map[string]string{"code": *asset.Code, "error": err.Error()})
			continue
		}
		result.Category = asset.Category
		results = append(results, result)
	}
	out(w, 200, map[string]any{"results": results, "errors": errors})
}
