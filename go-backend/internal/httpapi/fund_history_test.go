package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// 东方财富会将pageSize=100限制为20，历史起点不能被第一页的长度截断。
func TestFundHistoryUsesRequestedBoundaryAndActualInception(t *testing.T) {
	for _, tc := range []struct {
		name, start string
		limited     bool
		actual      int
		annual      float64
	}{
		{"full_three_years", "2023-09-28", false, 1097, 25.958939},
		{"young_share_class", "2024-06-19", true, 832, 35.566659},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				q := r.URL.Query()
				if q.Get("fundCode") != "016452" || r.Header.Get("Referer") != "https://fundf10.eastmoney.com/" {
					t.Error("基金查询缺少代码或来源请求头")
				}
				row := func(date, nav string) map[string]string {
					return map[string]string{"FSRQ": date, "DWJZ": nav, "LJJZ": nav}
				}
				rows := []map[string]string{row("2026-09-29", "2"), row("2026-09-01", "1.9")}
				total := 41
				if q.Get("endDate") != "" {
					if q.Get("endDate") != "2023-09-30" {
						t.Errorf("查询历史边界错误: %s", q.Get("endDate"))
					}
					rows = []map[string]string{}
					if !tc.limited {
						rows = []map[string]string{row(tc.start, "1")}
					}
					total = len(rows)
				} else if q.Get("pageIndex") == "3" {
					rows = []map[string]string{row(tc.start, "1")}
				}
				json.NewEncoder(w).Encode(map[string]any{"Data": map[string]any{"LSJZList": rows}, "TotalCount": total, "PageSize": 20, "ErrCode": 0})
			}))
			defer provider.Close()
			target, _ := url.Parse(provider.URL)
			client := &http.Client{Transport: localQuoteTransport(func(r *http.Request) (*http.Response, error) {
				local := r.Clone(r.Context())
				u := *r.URL
				u.Scheme, u.Host = target.Scheme, target.Host
				local.URL = &u
				return http.DefaultTransport.RoundTrip(local)
			})}
			got, err := fetchFundMarket(client, "016452", 1095, 2, "2026-09-29")
			if err != nil {
				t.Fatal(err)
			}
			if got.StartDate != tc.start || got.EndDate != "2026-09-29" || got.ActualDays != tc.actual || got.HistoryLimited != tc.limited {
				t.Fatalf("历史被第一页截断或成立日期判断错误: %+v", got)
			}
			if math.Abs(got.AnnualRate-tc.annual) > 0.001 {
				t.Fatalf("年化未按完整区间计算: %f", got.AnnualRate)
			}
		})
	}
}
