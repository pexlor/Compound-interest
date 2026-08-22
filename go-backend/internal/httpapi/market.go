package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
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
}
type yahooResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency           string  `json:"currency"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []*float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
	} `json:"chart"`
}

func yahooSymbol(code string) string {
	if strings.EqualFold(code, "BRK.B") {
		return "BRK-B"
	}
	return strings.ToUpper(strings.TrimSpace(code))
}

// fetchFundQuote uses the public fund estimate endpoint for mainland fund and
// money-fund codes. Those instruments are priced in CNY and do not expose a
// Yahoo-compatible ticker. The endpoint offers the current estimate but not a
// consistent long history, so callers are told that history is limited.
func fetchFundQuote(code string, days int) (marketResult, error) {
	if len(code) != 6 {
		return marketResult{}, fmt.Errorf("暂不支持该基金代码")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get("https://fundgz.1234567.com.cn/js/" + url.PathEscape(code) + ".js")
	if err != nil {
		return marketResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return marketResult{}, fmt.Errorf("基金服务返回 HTTP %d", response.StatusCode)
	}
	var body struct {
		Gszzl  string `json:"gszzl"`
		Gsz    string `json:"gsz"`
		Gztime string `json:"gztime"`
	}
	text := new(strings.Builder)
	if _, err := io.Copy(text, response.Body); err != nil {
		return marketResult{}, err
	}
	start, end := strings.Index(text.String(), "{"), strings.LastIndex(text.String(), "}")
	if start < 0 || end <= start {
		return marketResult{}, fmt.Errorf("基金响应无效")
	}
	if err := json.Unmarshal([]byte(text.String()[start:end+1]), &body); err != nil {
		return marketResult{}, err
	}
	var price, change float64
	if _, err := fmt.Sscanf(body.Gsz, "%f", &price); err != nil || price <= 0 {
		return marketResult{}, fmt.Errorf("未取得基金净值")
	}
	_, _ = fmt.Sscanf(body.Gszzl, "%f", &change)
	date := body.Gztime
	if len(date) >= 10 {
		date = date[:10]
	}
	if date == "" {
		date = time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	}
	return marketResult{Code: code, AnnualRate: change, RequestedDays: days, ActualDays: 1, HistoryLimited: true, StartDate: date, EndDate: date, CalculationDate: date, CurrentPrice: price, PriceCurrency: "CNY", PriceDate: date, Source: "天天基金"}, nil
}

func fetchStooq(code string, days int) (marketResult, error) {
	symbol := strings.ToLower(strings.ReplaceAll(yahooSymbol(code), "-", ".")) + ".us"
	client := &http.Client{Timeout: 12 * time.Second}
	response, err := client.Get("https://stooq.com/q/d/l/?" + url.Values{"s": {symbol}, "i": {"d"}}.Encode())
	if err != nil {
		return marketResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return marketResult{}, fmt.Errorf("备用行情服务返回 HTTP %d", response.StatusCode)
	}
	rows, err := csv.NewReader(response.Body).ReadAll()
	if err != nil || len(rows) < 3 {
		return marketResult{}, fmt.Errorf("备用行情数据不足")
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	points := []struct {
		price float64
		at    time.Time
	}{}
	for index, row := range rows {
		if index == 0 || len(row) < 5 {
			continue
		}
		at, parseDateErr := time.Parse("2006-01-02", row[0])
		var close float64
		_, parsePriceErr := fmt.Sscanf(row[4], "%f", &close)
		if parseDateErr == nil && parsePriceErr == nil && close > 0 && at.After(cutoff) {
			points = append(points, struct {
				price float64
				at    time.Time
			}{close, at})
		}
	}
	if len(points) < 2 {
		return marketResult{}, fmt.Errorf("备用历史行情不足")
	}
	first, last := points[0], points[len(points)-1]
	years := math.Max(1/365.25, float64(last.at.Sub(first.at).Hours())/(24*365.25))
	annual := (math.Pow(last.price/first.price, 1/years) - 1) * 100
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	return marketResult{Code: strings.ToUpper(code), AnnualRate: annual, RequestedDays: days, ActualDays: int(last.at.Sub(first.at).Hours() / 24), HistoryLimited: len(points) < days/2, StartDate: first.at.Format("2006-01-02"), EndDate: last.at.Format("2006-01-02"), CalculationDate: today, CurrentPrice: last.price, PriceCurrency: "USD", PriceDate: last.at.Format("2006-01-02"), Source: "Stooq"}, nil
}

func fetchMarket(category, code string, days int) (marketResult, error) {
	if days < 1 || days > 3650 {
		return marketResult{}, fmt.Errorf("不支持的历史区间")
	}
	if (category == "fund" || category == "money") && len(code) == 6 {
		return fetchFundQuote(code, days)
	}
	symbol := yahooSymbol(code)
	endpoint := "https://query1.finance.yahoo.com/v8/finance/chart/" + url.PathEscape(symbol) + "?" + url.Values{"range": {fmt.Sprintf("%dd", days+10)}, "interval": {"1d"}, "events": {"history"}}.Encode()
	client := &http.Client{Timeout: 12 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		return fetchStooq(code, days)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fetchStooq(code, days)
	}
	var payload yahooResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fetchStooq(code, days)
	}
	if len(payload.Chart.Result) == 0 {
		return marketResult{}, fmt.Errorf("未找到 %s 行情", code)
	}
	chart := payload.Chart.Result[0]
	if len(chart.Indicators.Quote) == 0 {
		return marketResult{}, fmt.Errorf("行情数据不完整")
	}
	closes := chart.Indicators.Quote[0].Close
	points := make([]struct {
		price float64
		at    time.Time
	}, 0, len(closes))
	for i, price := range closes {
		if price == nil || *price <= 0 || i >= len(chart.Timestamp) {
			continue
		}
		points = append(points, struct {
			price float64
			at    time.Time
		}{*price, time.Unix(chart.Timestamp[i], 0)})
	}
	if len(points) < 2 {
		return marketResult{}, fmt.Errorf("历史行情不足")
	}
	first, last := points[0], points[len(points)-1]
	years := math.Max(1/365.25, float64(last.at.Sub(first.at).Hours())/(24*365.25))
	annual := (math.Pow(last.price/first.price, 1/years) - 1) * 100
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	current := chart.Meta.RegularMarketPrice
	if current <= 0 {
		current = last.price
	}
	currency := chart.Meta.Currency
	if currency == "" {
		currency = "USD"
	}
	return marketResult{Code: strings.ToUpper(code), AnnualRate: annual, RequestedDays: days, ActualDays: int(last.at.Sub(first.at).Hours() / 24), HistoryLimited: len(points) < days/2, StartDate: first.at.Format("2006-01-02"), EndDate: last.at.Format("2006-01-02"), CalculationDate: today, CurrentPrice: current, PriceCurrency: currency, PriceDate: last.at.Format("2006-01-02"), Source: "Yahoo Finance"}, nil
}

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
		if _, err := fmt.Sscanf(raw, "%d", &days); err != nil {
			fail(w, 400, "不支持的历史区间")
			return
		}
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if code != "" {
		result, err := fetchMarket(category, code, days)
		if err != nil {
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
			errors = append(errors, map[string]string{"code": *asset.Code, "error": err.Error()})
			continue
		}
		result.Category = asset.Category
		results = append(results, result)
	}
	out(w, 200, map[string]any{"results": results, "errors": errors})
}
