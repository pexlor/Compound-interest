package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fulibu-go/internal/service"
)

const maxExportBytes = 64 << 20

// exportOptions 描述导出类别、闭区间日期、资产选择和归档范围。
type exportOptions struct {
	datasets         []string
	from, to, format string
	ids              []int64
	includeArchived  bool
}

// exportQuery 描述固定导出查询及参数，避免将用户输入拼入 SQL。
type exportQuery struct {
	sql  string
	args []any
}

// parseExportOptions 严格检查导出类型、日期和资产编号，默认单类 CSV、多类 ZIP。
func parseExportOptions(q url.Values) (exportOptions, error) {
	o := exportOptions{from: q.Get("from"), to: q.Get("to"), format: q.Get("format")}
	seen := map[string]bool{}
	for _, name := range strings.Split(q.Get("datasets"), ",") {
		switch name {
		case "assets", "prices", "returns", "snapshots", "rates":
		default:
			return o, fmt.Errorf("请选择有效的导出数据")
		}
		if !seen[name] {
			o.datasets = append(o.datasets, name)
			seen[name] = true
		}
	}
	for _, date := range []string{o.from, o.to} {
		if date != "" {
			if _, e := time.Parse("2006-01-02", date); e != nil || len(date) != 10 {
				return o, fmt.Errorf("日期必须为有效的 YYYY-MM-DD")
			}
		}
	}
	if o.from != "" && o.to != "" && o.from > o.to {
		return o, fmt.Errorf("开始日期不能晚于结束日期")
	}
	if o.format == "" {
		o.format = "csv"
		if len(o.datasets) > 1 {
			o.format = "zip"
		}
	}
	if o.format != "csv" && o.format != "zip" {
		return o, fmt.Errorf("格式必须为 csv 或 zip")
	}
	if o.format == "csv" && len(o.datasets) != 1 {
		return o, fmt.Errorf("多个数据类别请使用 ZIP 格式")
	}
	if v := q.Get("includeArchived"); v != "" {
		if v != "true" && v != "false" {
			return o, fmt.Errorf("includeArchived 必须为 true 或 false")
		}
		o.includeArchived = v == "true"
	}
	if raw, ok := q["assetIds"]; ok {
		if len(raw) != 1 || raw[0] == "" {
			return o, fmt.Errorf("请选择资产")
		}
		parts := strings.Split(raw[0], ",")
		if len(parts) > 500 {
			return o, fmt.Errorf("一次最多选择 500 个资产")
		}
		ids := map[int64]bool{}
		for _, s := range parts {
			id, e := strconv.ParseInt(s, 10, 64)
			if e != nil || id < 1 {
				return o, fmt.Errorf("无效的资产编号")
			}
			if !ids[id] {
				o.ids = append(o.ids, id)
				ids[id] = true
			}
		}
	}
	return o, nil
}

// minorExportAmount 将整数分精确转换为原币种主单位字符串，不经过浮点舍入。
func minorExportAmount(n int64) string {
	sign := ""
	v := uint64(n)
	if n < 0 {
		sign = "-"
		v = uint64(-(n + 1)) + 1
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// exportCell 格式化数据库值，对文本中可能被表格执行的公式前缀加单引号。
func exportCell(v any, minor bool) string {
	if v == nil {
		return ""
	}
	switch n := v.(type) {
	case int64:
		if minor {
			return minorExportAmount(n)
		}
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	}
	s := fmt.Sprint(v)
	if b, ok := v.([]byte); ok {
		s = string(b)
	}
	trimmed := strings.TrimLeft(s, " \t\r\n\ufeff")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") || strings.HasPrefix(s, "\n") {
		return "'" + s
	}
	return s
}

// writeExportQuery 读取固定查询并写入 CSV，按列名处理金额并限制导出大小。
func writeExportQuery(ctx context.Context, tx *sql.Tx, cw *csv.Writer, buf *bytes.Buffer, q exportQuery) (int, error) {
	rows, e := tx.QueryContext(ctx, q.sql, q.args...)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return 0, e
	}
	values := make([]any, len(cols))
	dest := make([]any, len(cols))
	record := make([]string, len(cols))
	for i := range values {
		dest[i] = &values[i]
	}
	count := 0
	for rows.Next() {
		if e = rows.Scan(dest...); e != nil {
			return count, e
		}
		for i, v := range values {
			record[i] = exportCell(v, cols[i] == "amount")
		}
		if e = cw.Write(record); e != nil {
			return count, e
		}
		count++
		if buf.Len() > maxExportBytes {
			return count, fmt.Errorf("导出文件过大，请缩小日期或资产范围")
		}
	}
	cw.Flush()
	if e = cw.Error(); e != nil {
		return count, e
	}
	return count, rows.Err()
}

// exportCSV 根据用户持仓生成固定查询，导出公共行情时仅选择本人关联证券与币种。
func exportCSV(ctx context.Context, tx *sql.Tx, userID int64, assets []service.Asset, o exportOptions, name string) ([]byte, int, error) {
	filter := "user_id=?"
	args := []any{userID}
	if !o.includeArchived {
		filter += " AND archived_at IS NULL"
	}
	if len(o.ids) > 0 {
		filter += " AND id IN (" + strings.TrimRight(strings.Repeat("?,", len(o.ids)), ",") + ")"
		for _, id := range o.ids {
			args = append(args, id)
		}
	}
	queries := []exportQuery{}
	headers := []string{}
	switch name {
	case "assets":
		headers = []string{"资产编号", "资产名称", "类别", "证券代码", "金额(原币种主单位)", "持有数量", "币种", "年化假设(%)", "备注", "创建时间", "归档时间"}
		queries = append(queries, exportQuery{`SELECT id,name,category,code,amount,quantity,currency,annual_rate,note,created_at,archived_at FROM assets WHERE ` + filter + ` ORDER BY id`, args})
	case "snapshots":
		headers = []string{"资产编号", "资产名称", "日期", "金额(原币种主单位)", "持有数量", "币种", "年化(%)", "行情日期", "来源", "获取时间"}
		queries = append(queries, exportQuery{`SELECT s.asset_id,a.name,s.snapshot_date,s.amount,s.quantity,s.currency,s.annual_rate,s.price_date,s.source,s.fetched_at FROM asset_daily_snapshots s JOIN assets a ON a.id=s.asset_id AND a.user_id=s.user_id WHERE s.user_id=? AND a.id IN (SELECT id FROM assets WHERE ` + filter + `)`, append([]any{userID}, args...)})
	case "prices", "returns":
		if name == "prices" {
			headers = []string{"类别", "证券代码", "行情日期", "原始价格", "收益计算价格(复权或累计净值)", "万份收益", "七日年化(%)", "币种", "来源", "获取时间"}
		} else {
			headers = []string{"类别", "证券代码", "测试区间(天)", "计算日期", "年化(%)", "区间收益(%)", "请求天数", "实际天数", "历史不足", "起始日期", "结束日期", "来源", "计算时间"}
		}
		seen := map[string]bool{}
		for _, a := range assets {
			if a.Code == nil {
				continue
			}
			cat, code, e := marketIdentity(a.Category, *a.Code)
			if e != nil {
				continue
			}
			key := cat + ":" + code
			if seen[key] {
				continue
			}
			seen[key] = true
			query := `SELECT category,code,price_date,price,return_price,income,annual_rate,currency,source,fetched_at FROM market_daily_prices WHERE category=? AND code=?`
			if name == "returns" {
				query = `SELECT category,code,lookback_days,calculation_date,annual_rate,period_return,requested_days,actual_days,history_limited,start_date,end_date,source,calculated_at FROM market_returns WHERE category=? AND code=?`
			}
			queries = append(queries, exportQuery{query, []any{cat, code}})
		}
	case "rates":
		headers = []string{"币种", "兑人民币汇率", "汇率日期", "来源", "获取时间"}
		seen := map[string]bool{}
		for _, a := range assets {
			if !seen[a.Currency] {
				seen[a.Currency] = true
				queries = append(queries, exportQuery{`SELECT currency,cny_rate,rate_date,source,fetched_at FROM exchange_rate_history WHERE currency=?`, []any{a.Currency}})
			}
		}
	}
	dateColumn := map[string]string{"prices": "price_date", "returns": "calculation_date", "snapshots": "s.snapshot_date", "rates": "rate_date"}[name]
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	cw := csv.NewWriter(&buf)
	if e := cw.Write(headers); e != nil {
		return nil, 0, e
	}
	count := 0
	for _, q := range queries {
		if dateColumn != "" {
			if o.from != "" {
				q.sql += " AND " + dateColumn + ">=?"
				q.args = append(q.args, o.from)
			}
			if o.to != "" {
				q.sql += " AND " + dateColumn + "<=?"
				q.args = append(q.args, o.to)
			}
			q.sql += " ORDER BY " + dateColumn
			if name == "returns" {
				q.sql += ",lookback_days"
			}
			if name == "snapshots" {
				q.sql += ",s.asset_id"
			}
		}
		n, e := writeExportQuery(ctx, tx, cw, &buf, q)
		if e != nil {
			return nil, count, e
		}
		count += n
	}
	cw.Flush()
	if e := cw.Error(); e != nil {
		return nil, count, e
	}
	return buf.Bytes(), count, nil
}

// exportData 通过认证会话或只读令牌生成一致性数据库快照的下载文件，不抓行情或修改数据。
func (a *app) exportData(w http.ResponseWriter, r *http.Request) {
	u := a.need(w, r)
	if u == nil {
		return
	}
	if r.Method != http.MethodGet {
		fail(w, 405, "方法不允许")
		return
	}
	o, e := parseExportOptions(r.URL.Query())
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	tx, e := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if e != nil {
		fail(w, 500, "无法读取导出数据")
		return
	}
	defer tx.Rollback()
	assets, e := service.ListAssets(tx, u.ID, o.includeArchived)
	if e != nil {
		fail(w, 500, "无法读取资产")
		return
	}
	if len(o.ids) > 0 {
		requested := map[int64]bool{}
		for _, id := range o.ids {
			requested[id] = true
		}
		selected := []service.Asset{}
		for _, a := range assets {
			if requested[a.ID] {
				selected = append(selected, a)
				delete(requested, a.ID)
			}
		}
		if len(requested) > 0 {
			fail(w, 404, "所选资产不存在或已归档")
			return
		}
		assets = selected
	}
	files := map[string][]byte{}
	total := 0
	summary := []string{}
	for _, name := range o.datasets {
		data, n, err := exportCSV(r.Context(), tx, u.ID, assets, o, name)
		if err != nil {
			fail(w, 500, "导出失败，请重试或缩小日期和资产范围")
			return
		}
		total += len(data)
		if total > maxExportBytes {
			fail(w, 413, "导出文件过大，请缩小日期或资产范围")
			return
		}
		files[name] = data
		summary = append(summary, fmt.Sprintf("%s.csv：%d 条数据", name, n))
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "无法完成导出")
		return
	}
	data := files[o.datasets[0]]
	fileName := o.datasets[0] + "-" + marketDate() + ".csv"
	contentType := "text/csv; charset=utf-8"
	if o.format == "zip" {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, name := range o.datasets {
			file, err := zw.Create(name + ".csv")
			if err == nil {
				_, err = file.Write(files[name])
			}
			if err != nil {
				fail(w, 500, "无法打包导出文件")
				return
			}
		}
		readme, err := zw.Create("导出说明.txt")
		if err == nil {
			_, err = readme.Write([]byte("资产星图数据导出\n" + strings.Join(summary, "\n") + "\n\n金额为原币种主单位，年化和区间收益为百分数。\n历史筛选日期包含两端，不影响当前资产清单。\n行情仅包含所选资产已缓存的实际数据日期，不自动补齐未获取数据。\n收益计算价格可能为复权价格或基金累计净值。\n货币基金万份收益和公布七日年化单独记录。\n文本公式前缀已加单引号；导入表格时将证券代码列设为文本以保留前导零。\n"))
		}
		if err != nil {
			fail(w, 500, "无法生成导出说明")
			return
		}
		if e = zw.Close(); e != nil {
			fail(w, 500, "无法打包导出文件")
			return
		}
		data = buf.Bytes()
		fileName = "asset-data-" + marketDate() + ".zip"
		contentType = "application/zip"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+fileName+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
