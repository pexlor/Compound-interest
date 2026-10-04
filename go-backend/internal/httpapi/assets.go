// 资产 HTTP 接口：查询、新增、部分更新和归档资产，并记录变更快照。

package httpapi

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"fulibu-go/internal/service"
)

// listAssets 读取当前用户尚未归档的资产列表。
func (a *app) listAssets(id int64) ([]asset, error) { return service.ListAssets(a.db, id, false) }

// assets 处理资产列表查询、新增和部分更新请求，并统一执行事务变更。
func (a *app) assets(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		include := r.URL.Query().Get("includeArchived")
		if include != "" && include != "true" && include != "false" {
			writeAPIError(w, badRequest("includeArchived 必须为 true 或 false"))
			return
		}
		if raw := r.URL.Query().Get("id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id < 1 {
				writeAPIError(w, badRequest("无效资产 ID"))
				return
			}
			v, err := service.GetAsset(a.db, u.ID, id, include == "true")
			if err != nil {
				writeAPIError(w, err)
				return
			}
			out(w, 200, map[string]any{"asset": v, "moneyUnit": "minor"})
			return
		}
		v, err := service.ListAssets(a.db, u.ID, include == "true")
		if err != nil {
			writeAPIError(w, err)
			return
		}
		result := []asset{}
		for _, item := range v {
			q := r.URL.Query()
			if name := q.Get("name"); name != "" && !strings.Contains(strings.ToLower(item.Name), strings.ToLower(name)) {
				continue
			}
			if code := q.Get("code"); code != "" && (item.Code == nil || !strings.EqualFold(*item.Code, code)) {
				continue
			}
			if category := q.Get("category"); category != "" && category != item.Category {
				continue
			}
			result = append(result, item)
		}
		out(w, 200, map[string]any{"assets": result, "moneyUnit": "minor"})
	case http.MethodPost:
		var x service.AssetCreate
		o, err := mutationInput(r, u, &x)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		a.mutate(w, r, o /* 在事务中创建资产并记录当天快照，返回新增结果或预览。 */, func(tx *sql.Tx) (service.MutationResult, error) {
			after, err := service.CreateAsset(tx, u.ID, x)
			if err != nil {
				return service.MutationResult{}, err
			}
			snapshot, err := service.SnapshotTx(tx, u.ID, "asset_change")
			if err != nil {
				return service.MutationResult{}, err
			}
			snapshot = snapshot && !o.DryRun
			return mutationJSON(201, map[string]any{"asset": after, "before": nil, "after": after, "snapshot": snapshot, "dryRun": o.DryRun, "moneyUnit": "minor"})
		})
	case http.MethodPatch:
		var x service.AssetPatch
		o, err := mutationInput(r, u, &x)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if x.ID < 1 {
			writeAPIError(w, badRequest("无效资产 ID"))
			return
		}
		a.mutate(w, r, o /* 检查当前资产版本，保存部分更新并记录快照。 */, func(tx *sql.Tx) (service.MutationResult, error) {
			before, err := service.GetAsset(tx, u.ID, x.ID, false)
			if err != nil {
				return service.MutationResult{}, err
			}
			if err = checkVersion(x.Version, before.Version); err != nil {
				return service.MutationResult{}, err
			}
			after, err := service.PatchAsset(tx, u.ID, before, x)
			if err != nil {
				return service.MutationResult{}, err
			}
			snapshot, err := service.SnapshotTx(tx, u.ID, "asset_change")
			if err != nil {
				return service.MutationResult{}, err
			}
			snapshot = snapshot && !o.DryRun
			return mutationJSON(200, map[string]any{"ok": true, "before": before, "after": after, "snapshot": snapshot, "dryRun": o.DryRun, "moneyUnit": "minor"})
		})
	default:
		apiError(w, 405, "method_not_allowed", "方法不允许")
	}
}

// archiveAsset 校验资产编号与版本，将资产归档并记录相应快照。
func (a *app) archiveAsset(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method != http.MethodPost {
		apiError(w, 405, "method_not_allowed", "方法不允许")
		return
	}
	var x /* 承载归档请求的资产编号和预期版本。 */ struct {
		ID      int64
		Version *int64
	}
	o, err := mutationInput(r, u, &x)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if x.ID < 1 {
		writeAPIError(w, badRequest("无效资产 ID"))
		return
	}
	a.mutate(w, r, o /* 检查资产版本后归档记录，并保存变更后的快照。 */, func(tx *sql.Tx) (service.MutationResult, error) {
		before, err := service.GetAsset(tx, u.ID, x.ID, false)
		if err != nil {
			return service.MutationResult{}, err
		}
		if err = checkVersion(x.Version, before.Version); err != nil {
			return service.MutationResult{}, err
		}
		after, err := service.ArchiveAsset(tx, u.ID, x.ID)
		if err != nil {
			return service.MutationResult{}, err
		}
		snapshot, err := service.SnapshotTx(tx, u.ID, "asset_change")
		if err != nil {
			return service.MutationResult{}, err
		}
		snapshot = snapshot && !o.DryRun
		return mutationJSON(200, map[string]any{"ok": true, "before": before, "after": after, "snapshot": snapshot, "dryRun": o.DryRun, "moneyUnit": "minor"})
	})
}
