// HTTP 接口基础设施：注册路由，统一处理请求解析、响应格式与健康检查。
// Package httpapi contains HTTP concerns only: routing, cookies, validation
// and JSON request/response handling. Database creation lives in internal/database.
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"fulibu-go/internal/service"
)

const sessionName = "fulibu_session"

// app 持有 HTTP 应用的数据库、账本服务和可注入的行情查询函数。
type app struct {
	db     *sql.DB
	ledger *service.Ledger
	quote  func(*http.Client, string, string) (float64, string, string, error)
}

// user 表示认证用户的编号、邮箱和显示名称。
type user struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// asset 复用业务层资产结构，保持 HTTP 响应与数据库资产模型一致。
type asset = service.Asset

// New 创建 API 应用并注册所有路由。
func New(db *sql.DB) http.Handler {
	a := &app{db: db, ledger: service.NewLedger(db)}
	return a.routes()
}

// routes 注册认证、资产、收入、退休、行情与汇率路由，并包裹统一中间件。
func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.health)
	mux.HandleFunc("/api/auth/register", a.register)
	mux.HandleFunc("/api/auth/login", a.login)
	mux.HandleFunc("/api/auth/logout", a.logout)
	mux.HandleFunc("/api/auth/me", a.me)
	mux.HandleFunc("/api/auth/tokens", a.apiTokens)
	mux.HandleFunc("/api/assets", a.assets)
	mux.HandleFunc("/api/assets/archive", a.archiveAsset)
	mux.HandleFunc("/api/income", a.income)
	mux.HandleFunc("/api/retirement", a.retirement)
	mux.HandleFunc("/api/retirement/items", a.retirementItems)
	mux.HandleFunc("/api/history", a.history)
	mux.HandleFunc("/api/dashboard", a.dashboard)
	mux.HandleFunc("/api/exchange-rates", a.rates)
	mux.HandleFunc("/api/market", a.market)
	mux.HandleFunc("/api/portfolio/summary", a.portfolioSummary)
	mux.HandleFunc("/api/valuations/refresh", a.refreshValuations)
	return a.headers(a.authorize(mux))
}

// headers 为所有 API 响应补充统一的 JSON 和禁止缓存头。
func (a *app) headers(next http.Handler) http.Handler {
	return http.HandlerFunc( /* 为响应设置 JSON 类型与禁止缓存头，再调用后续处理器。 */ func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// out 以指定状态码输出 JSON 响应。
func out(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail 以统一结构输出错误响应。
func fail(w http.ResponseWriter, status int, msg string) {
	out(w, status, map[string]string{"error": msg})
}

// body 限制请求体大小并严格解析 JSON 数据。
func body(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("请求只能包含一个 JSON 对象")
	}
	return nil
}

// health 返回服务健康检查结果。
func (a *app) health(w http.ResponseWriter, r *http.Request) {
	out(w, 200, map[string]bool{"ok": true})
}
