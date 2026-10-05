package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFundSeriesReadsAllPagesAndRejectsIncomplete(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("pageIndex") == "1" {
					fmt.Fprint(w, `{"TotalCount":4,"PageSize":2,"Data":{"LSJZList":[{"FSRQ":"2026-10-04","DWJZ":"2.4","LJJZ":"3.4"},{"FSRQ":"2026-10-03","DWJZ":"2.3","LJJZ":"3.3"}]}}`)
					return
				}
				if broken {
					fmt.Fprint(w, `{"TotalCount":4,"PageSize":2,"Data":{"LSJZList":[]}}`)
					return
				}
				fmt.Fprint(w, `{"TotalCount":4,"PageSize":2,"Data":{"LSJZList":[{"FSRQ":"2024-01-02","DWJZ":"1.2","LJJZ":"2.2"},{"FSRQ":"2024-01-01","DWJZ":"1.1","LJJZ":"2.1"}]}}`)
			}))
			defer server.Close()
			client := &http.Client{Transport: quoteTransportForCache{server.URL}}
			from, _ := time.Parse("2006-01-02", "2020-01-01")
			series, err := fetchFundSeries(context.Background(), client, "021000", from)
			if broken {
				if err == nil {
					t.Fatal("partial history accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(series.Rows) != 4 || !series.InceptionKnown || series.Rows[0].ReturnPrice != 2.1 || calls != 2 {
				t.Fatalf("bad series %+v, calls=%d", series, calls)
			}
		})
	}
}

type quoteTransportForCache struct{ url string }

func (tr quoteTransportForCache) RoundTrip(r *http.Request) (*http.Response, error) {
	cloned := r.Clone(r.Context())
	u := *r.URL
	cloned.URL = &u
	target, _ := http.NewRequest("GET", tr.url, nil)
	cloned.URL.Scheme = target.URL.Scheme
	cloned.URL.Host = target.URL.Host
	return http.DefaultTransport.RoundTrip(cloned)
}

func TestYahooSeriesRetainsRawAndAdjustedPrices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/q=" || r.URL.Query().Get("q") != "" {
			w.WriteHeader(502)
			return
		}
		fmt.Fprint(w, `{"chart":{"result":[{"meta":{"firstTradeDate":1704067200},"timestamp":[1704067200,1704153600],"indicators":{"adjclose":[{"adjclose":[9,10]}],"quote":[{"close":[19,20]}]}}]}}`)
	}))
	defer server.Close()
	from, _ := time.Parse("2006-01-02", "2020-01-01")
	series, err := fetchUSSeries(context.Background(), &http.Client{Transport: quoteTransportForCache{server.URL}}, "QQQM", from)
	if err != nil || len(series.Rows) != 2 || series.Rows[0].Price != 19 || series.Rows[0].ReturnPrice != 9 || !series.InceptionKnown {
		t.Fatalf("%+v %v", series, err)
	}
}
func TestMoneySeriesStoresPublishedSevenDayAnnualRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"TotalCount":2,"PageSize":20,"Data":{"LSJZList":[{"FSRQ":"2026-10-04","DWJZ":"0.3512","LJJZ":"1.3630"},{"FSRQ":"2026-10-03","DWJZ":"0.0000","LJJZ":"1.3620"}]}}`)
	}))
	defer server.Close()
	from, _ := time.Parse("2006-01-02", "2020-01-01")
	series, err := fetchFundNAVSeries(context.Background(), &http.Client{Transport: quoteTransportForCache{server.URL}}, "202308", from, true)
	if err != nil {
		t.Fatal(err)
	}
	if series.Rows[1].AnnualRate != 1.363 || series.Rows[1].Income != 0.3512 || series.Quote != 1 {
		t.Fatalf("%+v", series)
	}
}

func TestTencentSeriesSeparatesRawAndAdjustedPrices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "" {
			w.WriteHeader(502)
			return
		}
		if strings.HasSuffix(r.URL.Query().Get("param"), ",qfq") {
			fmt.Fprint(w, `{"code":0,"data":{"sh600519":{"qfqday":[["2026-01-02","8","9"],["2026-01-05","9","10"]]}}}`)
		} else {
			fmt.Fprint(w, `{"code":0,"data":{"sh600519":{"day":[["2026-01-02","18","19"],["2026-01-05","19","20"]]}}}`)
		}
	}))
	defer server.Close()
	from, _ := time.Parse("2006-01-02", "2026-01-01")
	series, err := fetchTencentSeries(context.Background(), &http.Client{Transport: quoteTransportForCache{server.URL}}, "stock", "SH600519", from)
	if err != nil || series.Rows[0].Price != 19 || series.Rows[0].ReturnPrice != 9 {
		t.Fatalf("%+v %v", series, err)
	}
}

func TestTencentYoungSecurityConfirmsAvailableHistoryStart(t *testing.T) {
	first := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/finance/chart/") {
			fmt.Fprintf(w, `{"chart":{"result":[{"meta":{"firstTradeDate":%d}}]}}`, first)
			return
		}
		if r.URL.Query().Get("q") != "" {
			w.WriteHeader(502)
			return
		}
		param := r.URL.Query().Get("param")
		data := `[]`
		if strings.Contains(param, "day,2026-") {
			data = `[["2026-01-02","10","10"],["2026-10-02","12","12"]]`
		}
		if strings.HasSuffix(param, ",qfq") {
			fmt.Fprintf(w, `{"code":0,"data":{"sh588999":{"qfqday":%s}}}`, data)
		} else {
			fmt.Fprintf(w, `{"code":0,"data":{"sh588999":{"day":%s}}}`, data)
		}
	}))
	defer server.Close()
	from, _ := time.Parse("2006-01-02", "2020-01-01")
	series, err := fetchTencentSeries(context.Background(), &http.Client{Transport: quoteTransportForCache{server.URL}}, "fund", "SH588999", from)
	if err != nil || !series.InceptionKnown {
		t.Fatalf("young security lacks confirmed start: %+v %v", series, err)
	}
}
