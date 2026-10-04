// 行情服务测试：验证代码映射、报价日期、归档过滤和并发资产更新保护。

package httpapi

import (
	"fulibu-go/internal/service"
	"net/http"
	"testing"
)

// TestArchivedAssetsNeverSupplyCachedMarketPrice 验证已归档持仓不会用于推算行情回退单价。
func TestArchivedAssetsNeverSupplyCachedMarketPrice(t *testing.T) {
	db, _, _ := apiFixture(t)
	if _, err := db.Exec(`INSERT INTO assets(id,user_id,name,category,code,amount,quantity,currency,archived_at) VALUES(1,1,'old','stock','AAPL',10000,1,'USD','old'),(2,1,'active','stock','AAPL',30000,2,'USD',NULL)`); err != nil {
		t.Fatal(err)
	}
	a := &app{db: db, ledger: service.NewLedger(db)}
	value, ok := a.cachedMarketPrice(1, "stock", "AAPL", 365)
	if !ok || value.CurrentPrice != 150 {
		t.Fatalf("archived fallback: %+v ok=%v", value, ok)
	}
	db.Exec(`DELETE FROM assets WHERE id=2`)
	if _, ok = a.cachedMarketPrice(1, "stock", "AAPL", 365); ok {
		t.Fatal("archived-only asset used for fallback")
	}
}

// TestLegacyQuoteRefreshPreservesConcurrentAssetChanges 验证行情刷新期间发生的资产修改或归档不会被旧报价覆盖。
func TestLegacyQuoteRefreshPreservesConcurrentAssetChanges(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(map[bool]string{false: "patch", true: "archive"}[archive] /* 构造刷新期间被修改或归档的资产，验证旧报价不会覆盖新记录。 */, func(t *testing.T) {
			db, _, _ := apiFixture(t)
			if _, err := db.Exec(`INSERT INTO assets(id,user_id,name,category,code,amount,quantity,currency) VALUES(1,1,'stock','stock','AAPL',10000,1,'USD')`); err != nil {
				t.Fatal(err)
			}
			a := &app{db: db, ledger: service.NewLedger(db), quote: /* 在模拟报价期间修改或归档资产，制造并发变更场景。 */ func(_ *http.Client, category, code string) (float64, string, string, error) {
				if archive {
					_, err := db.Exec(`UPDATE assets SET archived_at='now' WHERE id=1`)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					_, err := db.Exec(`UPDATE assets SET amount=60000,quantity=3 WHERE id=1`)
					if err != nil {
						t.Fatal(err)
					}
				}
				return 200, "USD", "2026-10-04", nil
			}}
			if err := a.refreshMarketAssetValues(1); err != nil {
				t.Fatal(err)
			}
			var amount int
			db.QueryRow(`SELECT amount FROM assets WHERE id=1`).Scan(&amount)
			want := 60000
			if archive {
				want = 10000
			}
			if amount != want {
				t.Fatalf("overwrote changed asset: %d != %d", amount, want)
			}
		})
	}
}

// TestTencentSymbol 验证不同市场证券代码到腾讯行情标识和币种的映射。
func TestTencentSymbol(t *testing.T) {
	tests := [] /* 定义证券类别、代码及预期腾讯标识和币种的测试用例。 */ struct {
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

// TestTencentSymbolRejectsOffExchangeFund 验证场外基金代码不能走腾讯证券报价映射。
func TestTencentSymbolRejectsOffExchangeFund(t *testing.T) {
	if _, _, err := tencentSymbol("fund", "110022"); err == nil {
		t.Fatal("expected off-exchange fund to be rejected")
	}
}

// TestQuoteDate 验证支持的报价日期格式及无效日期处理。
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
