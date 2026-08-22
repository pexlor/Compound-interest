// Package httpapi contains HTTP concerns only: routing, cookies, validation
// and JSON request/response handling. Database creation lives in internal/database.
package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"fulibu-go/internal/service"
)

const sessionName = "fulibu_session"
const maxMoney = float64(1<<53-1) / 100

type app struct {
	db     *sql.DB
	ledger *service.Ledger
}
type user struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}
type asset struct {
	ID                 int64    `json:"id"`
	UserID             int64    `json:"user_id"`
	Name               string   `json:"name"`
	Category           string   `json:"category"`
	Code               *string  `json:"code"`
	Amount             int64    `json:"amount"`
	Quantity           *float64 `json:"quantity"`
	Currency           string   `json:"currency"`
	AnnualRate         float64  `json:"annual_rate"`
	InvestmentStrategy string   `json:"investment_strategy"`
	InvestmentAmount   *int64   `json:"investment_amount"`
	Note               string   `json:"note"`
	CreatedAt          string   `json:"created_at"`
}

// New wires the API routes to an already-initialized database.
func New(db *sql.DB) http.Handler {
	a := &app{db: db, ledger: service.NewLedger(db)}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.health)
	mux.HandleFunc("/api/auth/register", a.register)
	mux.HandleFunc("/api/auth/login", a.login)
	mux.HandleFunc("/api/auth/logout", a.logout)
	mux.HandleFunc("/api/auth/me", a.me)
	mux.HandleFunc("/api/assets", a.assets)
	mux.HandleFunc("/api/income", a.income)
	mux.HandleFunc("/api/retirement", a.retirement)
	mux.HandleFunc("/api/history", a.history)
	mux.HandleFunc("/api/dashboard", a.dashboard)
	mux.HandleFunc("/api/exchange-rates", a.rates)
	mux.HandleFunc("/api/market", a.market)
	return a.headers(mux)
}
func (a *app) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
func out(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	out(w, status, map[string]string{"error": msg})
}
func body(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func (a *app) health(w http.ResponseWriter, r *http.Request) {
	out(w, 200, map[string]bool{"ok": true})
}
