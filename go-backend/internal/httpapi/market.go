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

func fetchTencentHistory(client *http.Client, symbol string, days int) ([]json.RawMessage, error) {
	parameter := fmt.Sprintf("%s,day,,,%d,qfq", symbol, days+10)
	endpoint := "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?" + url.Values{"param": {parameter}}.Encode()
	response, err := client.Get(endpoint)
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

// fetchMarket uses Tencent Finance for both live quotes and daily history.
func fetchMarket(category, code string, days int) (marketResult, error) {
	if days < 1 || days > 3650 {
		return marketResult{}, fmt.Errorf("不支持的历史区间")
	}
	symbol, currency, err := tencentSymbol(category, code)
	if err != nil {
		return marketResult{}, err
	}
	client := &http.Client{Timeout: 12 * time.Second}
	current, priceDate, err := fetchTencentQuote(client, symbol)
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
