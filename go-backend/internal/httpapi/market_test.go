package httpapi

import (
	"testing"
)

func TestTencentSymbol(t *testing.T) {
	tests := []struct {
		category, code, wantSymbol, wantCurrency string
	}{
		{"stock", "600519", "sh600519", "CNY"},
		{"stock", "000001", "sz000001", "CNY"},
		{"stock", "0700.HK", "hk00700", "HKD"},
		{"stock", "QQQ", "usQQQ", "USD"},
		{"fund", "510300", "sh510300", "CNY"},
	}
	for _, test := range tests {
		symbol, currency, err := tencentSymbol(test.category, test.code)
		if err != nil || symbol != test.wantSymbol || currency != test.wantCurrency {
			t.Errorf("tencentSymbol(%q, %q) = (%q, %q, %v), want (%q, %q, nil)", test.category, test.code, symbol, currency, err, test.wantSymbol, test.wantCurrency)
		}
	}
}

func TestTencentSymbolRejectsOffExchangeFund(t *testing.T) {
	if _, _, err := tencentSymbol("fund", "110022"); err == nil {
		t.Fatal("expected off-exchange fund to be rejected")
	}
}

func TestQuoteDate(t *testing.T) {
	for input, want := range map[string]string{
		"20260831161431":      "2026-08-31",
		"2026/08/31 16:08:50": "2026-08-31",
		"2026-08-31 11:01:40": "2026-08-31",
	} {
		if got := quoteDate(input); got != want {
			t.Errorf("quoteDate(%q) = %q, want %q", input, got, want)
		}
	}
}
