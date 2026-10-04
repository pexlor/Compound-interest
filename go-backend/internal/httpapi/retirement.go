// 退休计划 HTTP 接口：读取目标进度，并创建、更新和删除目标明细。

package httpapi

import (
	"database/sql"
	"net/http"

	"fulibu-go/internal/service"
)

// retirementResponse 在当前事务中重新计算退休计划，并返回变更前后数据和预览标记。
func retirementResponse(tx *sql.Tx, userID int64, status int, before, after any, dry bool) (service.MutationResult, error) {
	result, err := service.RetirementFor(tx, userID)
	if err != nil {
		return service.MutationResult{}, err
	}
	raw := map[string]any{"target_cny": result.TargetCNY, "current_cny": result.CurrentCNY, "progress": result.Progress, "projected_years": result.ProjectedYears, "projected_date": result.ProjectedDate, "missing_currencies": result.MissingCurrencies, "annual_rate": result.AnnualRate, "items": result.Items, "before": before, "after": after, "dryRun": dry, "moneyUnit": "minor"}
	return mutationJSON(status, raw)
}

// retirement 处理退休计划查询和目标明细新增请求。
func (a *app) retirement(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		v, err := a.ledger.Retirement(u.ID)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		out(w, 200, v)
	case http.MethodPost:
		a.writeGoalItem(w, r, u, true)
	default:
		apiError(w, 405, "method_not_allowed", "方法不允许")
	}
}

// retirementItems 提供退休目标明细列表查询、部分更新和删除接口。
func (a *app) retirementItems(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method == http.MethodGet {
		v, err := a.ledger.Retirement(u.ID)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		out(w, 200, map[string]any{"items": v.Items, "moneyUnit": "minor"})
		return
	}
	if r.Method == http.MethodDelete {
		a.deleteGoalItem(w, r, u)
		return
	}
	if r.Method != http.MethodPatch {
		apiError(w, 405, "method_not_allowed", "方法不允许")
		return
	}
	a.writeGoalItem(w, r, u, false)
}

// writeGoalItem 校验请求与版本，在统一变更事务中创建或更新退休目标明细。
func (a *app) writeGoalItem(w http.ResponseWriter, r *http.Request, u *user, create bool) {
	var x service.GoalItemInput
	o, err := mutationInput(r, u, &x)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if create && x.ID != 0 {
		writeAPIError(w, badRequest("新增不能指定 ID"))
		return
	}
	if !create && x.ID < 1 {
		writeAPIError(w, badRequest("无效目标明细 ID"))
		return
	}
	a.mutate(w, r, o /* 校验目标明细版本，创建或更新记录后返回最新计划。 */, func(tx *sql.Tx) (service.MutationResult, error) {
		var before service.RetirementItem
		var err error
		if !create {
			before, err = service.GetGoalItem(tx, u.ID, x.ID)
			if err != nil {
				return service.MutationResult{}, err
			}
			if err = checkVersion(x.Version, before.Version); err != nil {
				return service.MutationResult{}, err
			}
		}
		after, err := service.WriteGoalItem(tx, u.ID, before, x, create)
		if err != nil {
			return service.MutationResult{}, err
		}
		status := 200
		var old any = before
		if create {
			status = 201
			old = nil
		}
		return retirementResponse(tx, u.ID, status, old, after, o.DryRun)
	})
}

// deleteGoalItem 校验目标明细编号与版本，在事务中删除并返回重新计算的计划。
func (a *app) deleteGoalItem(w http.ResponseWriter, r *http.Request, u *user) {
	var x /* 承载目标明细删除请求的编号与预期版本。 */ struct {
		ID      int64
		Version *int64
	}
	o, err := mutationInput(r, u, &x)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if x.ID < 1 {
		writeAPIError(w, badRequest("无效目标明细 ID"))
		return
	}
	a.mutate(w, r, o /* 检查明细版本后删除记录，并返回最新退休计划或预览。 */, func(tx *sql.Tx) (service.MutationResult, error) {
		before, err := service.GetGoalItem(tx, u.ID, x.ID)
		if err != nil {
			return service.MutationResult{}, err
		}
		if err = checkVersion(x.Version, before.Version); err != nil {
			return service.MutationResult{}, err
		}
		if _, err = tx.Exec(`DELETE FROM retirement_goal_items WHERE id=? AND user_id=?`, x.ID, u.ID); err != nil {
			return service.MutationResult{}, err
		}
		return retirementResponse(tx, u.ID, 200, before, nil, o.DryRun)
	})
}
