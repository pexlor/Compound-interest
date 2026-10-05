package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type dailyObservation struct {
	Date                                   string
	Price, ReturnPrice, Income, AnnualRate float64
}
type marketSeries struct {
	Rows             []dailyObservation
	Currency, Source string
	InceptionKnown   bool
	Quote            float64
	QuoteDate        string
}

type contextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t contextTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(r.Clone(t.ctx))
}
func contextClient(ctx context.Context, client *http.Client) *http.Client {
	copy := *client
	base := copy.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = contextTransport{ctx, base}
	return &copy
}
func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func sortObservations(rows []dailyObservation) []dailyObservation {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Date < rows[j].Date })
	out := rows[:0]
	for _, row := range rows {
		if len(out) > 0 && out[len(out)-1].Date == row.Date {
			out[len(out)-1] = row
		} else {
			out = append(out, row)
		}
	}
	return out
}

// Fund paging follows the provider's actual counts. A partial page or a
// repeated/non-progressing page is an error, never proof of fund inception.
func fetchFundSeries(ctx context.Context, client *http.Client, code string, from time.Time) (marketSeries, error) {
	return fetchFundNAVSeries(ctx, client, code, from, false)
}

func fetchFundNAVSeries(ctx context.Context, client *http.Client, code string, from time.Time, money bool) (marketSeries, error) {
	client = contextClient(ctx, client)
	series := marketSeries{Currency: "CNY", Source: "东方财富历史净值"}
	lastOldest := ""
	seen := 0
	for index := 1; index <= 600; index++ {
		page, err := fetchFundHistoryPage(client, code, "", index)
		if err != nil {
			return series, err
		}
		if page.PageSize <= 0 || page.TotalCount <= 0 || len(page.Data.List) == 0 {
			return series, fmt.Errorf("基金历史分页不完整")
		}
		expected := page.PageSize
		if remaining := page.TotalCount - seen; remaining < expected {
			expected = remaining
		}
		if len(page.Data.List) != expected {
			return series, fmt.Errorf("基金历史分页被截断")
		}
		oldest := "9999"
		newest := ""
		for _, r := range page.Data.List {
			at, e := time.Parse("2006-01-02", r.Date)
			if e != nil {
				return series, e
			}
			price, e := strconv.ParseFloat(r.NAV, 64)
			if e != nil {
				return series, fmt.Errorf("基金历史价格无效")
			}
			income, publishedRate := 0.0, 0.0
			ret, e := strconv.ParseFloat(firstNonEmpty(r.TotalNAV, r.NAV), 64)
			if money {
				income = price
				publishedRate, _ = strconv.ParseFloat(r.TotalNAV, 64)
				price = 1
				ret = 1
				series.Source = "东方财富货币基金每万份收益"
			}
			if e != nil || !finitePositive(price) || !finitePositive(ret) {
				return series, fmt.Errorf("基金历史净值无效")
			}
			series.Rows = append(series.Rows, dailyObservation{Date: at.Format("2006-01-02"), Price: price, ReturnPrice: ret, Income: income, AnnualRate: publishedRate})
			if r.Date < oldest {
				oldest = r.Date
			}
			if r.Date > newest {
				newest = r.Date
			}
		}
		if lastOldest != "" && newest >= lastOldest {
			return series, fmt.Errorf("基金分页日期重复")
		}
		lastOldest = oldest
		seen += len(page.Data.List)
		if seen >= page.TotalCount {
			series.InceptionKnown = true
			break
		}
		if oldest <= from.Format("2006-01-02") {
			break
		}
		if index == 600 {
			return series, fmt.Errorf("基金历史超过分页上限")
		}
	}
	series.Rows = sortObservations(series.Rows)
	if len(series.Rows) < 2 {
		return series, fmt.Errorf("基金历史不足两个有效数据点")
	}
	latest := series.Rows[len(series.Rows)-1]
	series.Quote = latest.Price
	series.QuoteDate = latest.Date
	return series, nil
}

func fetchUSSeries(ctx context.Context, client *http.Client, code string, from time.Time) (marketSeries, error) {
	ticker := strings.TrimPrefix(strings.ToUpper(code), "US")
	endpoint := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?period1=%d&period2=%d&interval=1d&events=div,splits&includeAdjustedClose=true", url.PathEscape(strings.ReplaceAll(ticker, ".", "-")), from.Unix(), time.Now().Unix()+86400)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return marketSeries{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, err := client.Do(req)
	if err != nil {
		return marketSeries{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return marketSeries{}, fmt.Errorf("Yahoo Finance HTTP %d", res.StatusCode)
	}
	var payload struct {
		Chart struct {
			Result []struct {
				Meta struct {
					FirstTradeDate int64 `json:"firstTradeDate"`
				}
				Timestamp  []int64
				Indicators struct {
					AdjClose []struct {
						Values []*float64 `json:"adjclose"`
					} `json:"adjclose"`
					Quote []struct {
						Close []*float64 `json:"close"`
					} `json:"quote"`
				}
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		} `json:"chart"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&payload); err != nil {
		return marketSeries{}, err
	}
	if len(payload.Chart.Result) != 1 {
		return marketSeries{}, fmt.Errorf("美股历史不可用")
	}
	r := payload.Chart.Result[0]
	if len(r.Indicators.AdjClose) == 0 {
		return marketSeries{}, fmt.Errorf("美股复权历史不可用")
	}
	series := marketSeries{Currency: "USD", Source: "Yahoo Finance 复权收盘价（人民币汇率调整）", InceptionKnown: r.Meta.FirstTradeDate > 0 && !time.Unix(r.Meta.FirstTradeDate, 0).Before(from)}
	for i, stamp := range r.Timestamp {
		adj := r.Indicators.AdjClose[0].Values
		if i >= len(adj) || adj[i] == nil || !finitePositive(*adj[i]) {
			continue
		}
		price := *adj[i]
		if len(r.Indicators.Quote) > 0 && i < len(r.Indicators.Quote[0].Close) && r.Indicators.Quote[0].Close[i] != nil && finitePositive(*r.Indicators.Quote[0].Close[i]) {
			price = *r.Indicators.Quote[0].Close[i]
		}
		series.Rows = append(series.Rows, dailyObservation{Date: time.Unix(stamp, 0).UTC().Format("2006-01-02"), Price: price, ReturnPrice: *adj[i]})
	}
	series.Rows = sortObservations(series.Rows)
	if len(series.Rows) < 2 {
		return series, fmt.Errorf("美股有效历史不足")
	}
	latest := series.Rows[len(series.Rows)-1]
	series.Quote, series.QuoteDate = latest.Price, latest.Date
	if price, _, date, e := fetchLiveQuote(contextClient(ctx, client), "stock", code); e == nil {
		series.Quote, series.QuoteDate = price, date
	}
	return series, nil
}

func fetchTencentRange(ctx context.Context, client *http.Client, symbol string, start, stop time.Time, adjustment string) ([]marketPoint, error) {
	param := fmt.Sprintf("%s,day,%s,%s,640,%s", symbol, start.Format("2006-01-02"), stop.Format("2006-01-02"), adjustment)
	req, err := http.NewRequestWithContext(ctx, "GET", "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?"+url.Values{"param": {param}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("腾讯历史 HTTP %d", res.StatusCode)
	}
	var payload tencentKlineResponse
	if err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("腾讯历史错误: %s", payload.Msg)
	}
	raw, ok := payload.Data[symbol]
	if !ok {
		return nil, fmt.Errorf("腾讯历史缺少证券")
	}
	var data tencentKlineData
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	rows := data.Day
	if adjustment == "qfq" && len(data.QfqDay) > 0 {
		rows = data.QfqDay
	}
	if len(rows) >= 640 {
		return nil, fmt.Errorf("腾讯历史被截断")
	}
	points := tencentHistoryPoints(rows, time.Time{})
	if len(points) != len(rows) {
		return nil, fmt.Errorf("腾讯历史含无效日线")
	}
	for _, point := range points {
		date := point.at.Format("2006-01-02")
		if date < start.Format("2006-01-02") || date > stop.Format("2006-01-02") {
			return nil, fmt.Errorf("腾讯历史未遵守日期范围")
		}
	}
	return points, nil
}
func fetchTencentSeries(ctx context.Context, client *http.Client, category, code string, from time.Time) (marketSeries, error) {
	symbol, currency, err := tencentSymbol(category, code)
	if err != nil {
		return marketSeries{}, err
	}
	series := marketSeries{Currency: currency, Source: "腾讯财经前复权日线"}
	end := time.Now().UTC()
	for start := from; !start.After(end); {
		stop := time.Date(start.Year()+1, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
		if stop.After(end) {
			stop = end
		}
		adjusted, err := fetchTencentRange(ctx, client, symbol, start, stop, "qfq")
		if err != nil {
			return series, err
		}
		raw, err := fetchTencentRange(ctx, client, symbol, start, stop, "")
		if err != nil {
			return series, err
		}
		prices := map[string]float64{}
		for _, p := range raw {
			prices[p.at.Format("2006-01-02")] = p.price
		}
		for _, p := range adjusted {
			date := p.at.Format("2006-01-02")
			price, ok := prices[date]
			if !ok {
				return series, fmt.Errorf("腾讯原始日线和复权日线不一致")
			}
			series.Rows = append(series.Rows, dailyObservation{Date: date, Price: price, ReturnPrice: p.price})
		}
		start = stop.AddDate(0, 0, 1)
	}
	series.Rows = sortObservations(series.Rows)
	if len(series.Rows) < 2 {
		return series, fmt.Errorf("证券有效历史不足")
	}
	latest := series.Rows[len(series.Rows)-1]
	series.Quote, series.QuoteDate = latest.Price, latest.Date
	// Only a full range plus matching provider first-trade metadata confirms
	// a young security. Empty earlier chunks alone could be source truncation.
	if series.Rows[0].Date > from.AddDate(0, 0, 7).Format("2006-01-02") {
		if first, err := fetchSecurityFirstTrade(ctx, client, symbol); err == nil {
			oldest, _ := time.Parse("2006-01-02", series.Rows[0].Date)
			series.InceptionKnown = !first.Before(from) && oldest.Sub(first) >= 0 && oldest.Sub(first) <= 7*24*time.Hour
		}
	}
	if price, date, err := fetchTencentQuote(contextClient(ctx, client), symbol); err == nil {
		series.Quote, series.QuoteDate = price, date
	}
	return series, nil
}

func fetchMarketSeries(ctx context.Context, client *http.Client, category, code string, from time.Time) (marketSeries, error) {
	if isUSSecurity(code) {
		return fetchUSSeries(ctx, client, code, from)
	}
	if (category == "fund" || category == "money") && !isExchangeFund(code) {
		return fetchFundNAVSeries(ctx, client, code, from, category == "money")
	}
	return fetchTencentSeries(ctx, client, category, code, from)
}

func fetchSecurityFirstTrade(ctx context.Context, client *http.Client, symbol string) (time.Time, error) {
	ticker := ""
	switch {
	case strings.HasPrefix(symbol, "sh"):
		ticker = symbol[2:] + ".SS"
	case strings.HasPrefix(symbol, "sz"):
		ticker = symbol[2:] + ".SZ"
	case strings.HasPrefix(symbol, "hk"):
		n, err := strconv.Atoi(symbol[2:])
		if err != nil {
			return time.Time{}, err
		}
		ticker = fmt.Sprintf("%04d.HK", n)
	default:
		return time.Time{}, fmt.Errorf("不支持的上市信息市场")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://query1.finance.yahoo.com/v8/finance/chart/"+url.PathEscape(ticker)+"?range=1mo&interval=1d", nil)
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, err := client.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return time.Time{}, fmt.Errorf("上市信息 HTTP %d", res.StatusCode)
	}
	var payload struct {
		Chart struct {
			Result []struct {
				Meta struct {
					FirstTradeDate int64 `json:"firstTradeDate"`
				}
			} `json:"result"`
		} `json:"chart"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&payload); err != nil {
		return time.Time{}, err
	}
	if len(payload.Chart.Result) != 1 || payload.Chart.Result[0].Meta.FirstTradeDate <= 0 {
		return time.Time{}, fmt.Errorf("没有可验证的最早交易日期")
	}
	date := time.Unix(payload.Chart.Result[0].Meta.FirstTradeDate, 0).UTC().Format("2006-01-02")
	return time.Parse("2006-01-02", date)
}
