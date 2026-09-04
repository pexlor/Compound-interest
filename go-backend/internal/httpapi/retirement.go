package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"strings"
)

// retirement 处理退休目标资产的查询、新增和删除请求。
func (a *app) retirement(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method == http.MethodGet {
		result, err := a.ledger.Retirement(u.ID)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		out(w, 200, result)
		return
	}
	if r.Method == http.MethodPost {
		var request struct {
			Name, Category, Currency string
			Amount                   float64
		}
		if body(r, &request) != nil || strings.TrimSpace(request.Name) == "" || request.Amount <= 0 || request.Amount > maxMoney {
			fail(w, 400, "请输入有效的目标资产")
			return
		}
		currency := strings.ToUpper(strings.TrimSpace(request.Currency))
		if currency == "" {
			currency = "CNY"
		}
		result, err := a.ledger.AddRetirementItem(u.ID, strings.TrimSpace(request.Name), strings.TrimSpace(request.Category), int64(math.Round(request.Amount*100)), currency)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		out(w, 201, result)
		return
	}
	if r.Method == http.MethodDelete {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id < 1 {
			fail(w, 400, "无效目标资产")
			return
		}
		result, err := a.ledger.DeleteRetirementItem(u.ID, id)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		out(w, 200, result)
		return
	}
	if r.Method != http.MethodPut {
		fail(w, 405, "方法不允许")
		return
	}
	var request struct {
		TargetCNY float64 `json:"targetCny"`
	}
	if body(r, &request) != nil || request.TargetCNY <= 0 || request.TargetCNY > maxMoney {
		fail(w, 400, "请输入有效的退休目标金额")
		return
	}
	result, err := a.ledger.SaveRetirementTarget(u.ID, int64(math.Round(request.TargetCNY*100)))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out(w, 200, result)
}
