// 投资组合 HTTP 接口：返回持仓汇总，刷新证券估值并记录资产快照。

package httpapi

import (
	"database/sql"
	"math"
	"net/http"
	"time"

	"fulibu-go/internal/service"
)

// portfolioSummary 校验登录态和请求方法，返回当前用户的投资组合汇总。
func (a *app) portfolioSummary(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method != http.MethodGet {
		apiError(w, 405, "method_not_allowed", "方法不允许")
		return
	}
	v, err := a.ledger.PortfolioSummary(u.ID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	out(w, 200, v)
}

// valuationQuote 保存待更新资产、已换算为最小货币单位的估值及报价币种和日期。
type valuationQuote struct {
	Asset          asset
	Amount         int64
	Currency, Date string
}

// refreshValuations 获取证券报价后检查持仓版本，在事务中更新可用估值并返回逐项失败信息。
func (a *app) refreshValuations(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method != http.MethodPost {
		apiError(w, 405, "method_not_allowed", "方法不允许")
		return
	}
	var input /* 表示无业务字段的估值刷新请求体。 */ struct{}
	o, err := mutationInput(r, u, &input)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	saved, ok, err := a.ledger.Replay(r.Context(), o)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if ok {
		writeMutation(w, saved)
		return
	}
	assets, err := a.listAssets(u.ID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	quotes := []valuationQuote{}
	failures := []map[string]any{}
	client := &http.Client{Timeout: 12 * time.Second}
	fetch := a.quote
	if fetch == nil {
		fetch = fetchLiveQuote
	}
	for _, item := range assets {
		if (item.Category != "stock" && item.Category != "fund") || item.Code == nil || item.Quantity == nil {
			continue
		}
		if r.Context().Err() != nil {
			apiError(w, 408, "request_cancelled", "估值刷新已取消")
			return
		}
		price, currency, date, e := fetch(client, item.Category, *item.Code)
		amount := float64(0)
		if item.Quantity != nil {
			amount = *item.Quantity * price
		}
		minor, conversionErr := service.Money(amount, false)
		if e != nil || conversionErr != nil || !service.Currency(currency) || math.IsNaN(price) || price <= 0 {
			failures = append(failures, map[string]any{"id": item.ID, "code": *item.Code, "error": "无法获取有效行情，保留原估值"})
			continue
		}
		quotes = append(quotes, valuationQuote{item, minor, currency, date})
	}
	a.mutate(w, r, o /* 校验获取行情后的持仓版本，更新估值并记录快照和逐项结果。 */, func(tx *sql.Tx) (service.MutationResult, error) {
		updated := []map[string]any{}
		beforeItems := []asset{}
		afterItems := []asset{}
		for _, q := range quotes {
			current, e := service.GetAsset(tx, u.ID, q.Asset.ID, false)
			if e != nil {
				return service.MutationResult{}, &service.APIError{Status: 409, Code: "version_conflict", Message: "持仓在获取行情期间已变化，请重试"}
			}
			if current.Version != q.Asset.Version {
				return service.MutationResult{}, &service.APIError{Status: 409, Code: "version_conflict", Message: "持仓在获取行情期间已变化，请重试"}
			}
			if _, e = tx.Exec(`UPDATE assets SET amount=?,currency=? WHERE id=? AND user_id=?`, q.Amount, q.Currency, current.ID, u.ID); e != nil {
				return service.MutationResult{}, e
			}
			after, e := service.GetAsset(tx, u.ID, current.ID, false)
			if e != nil {
				return service.MutationResult{}, e
			}
			beforeItems = append(beforeItems, current)
			afterItems = append(afterItems, after)
			updated = append(updated, map[string]any{"id": current.ID, "amount": after.Amount, "currency": after.Currency, "priceDate": q.Date, "version": after.Version})
		}
		snapshot, e := service.SnapshotTx(tx, u.ID, "valuation_refresh")
		if e != nil {
			return service.MutationResult{}, e
		}
		snapshot = snapshot && !o.DryRun
		return mutationJSON(200, map[string]any{"updated": updated, "errors": failures, "complete": len(failures) == 0, "before": beforeItems, "after": afterItems, "snapshotRecorded": snapshot, "dryRun": o.DryRun, "moneyUnit": "minor"})
	})
}
