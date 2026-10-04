// API 令牌认证与管理：校验 Bearer 凭据、限制操作权限并支持令牌创建和撤销。

package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// principalKey 作为请求上下文的身份键，避免与其他上下文键发生冲突。
type principalKey struct{}

// principal 保存 API 令牌认证后的用户身份与访问权限。
type principal struct {
	User  user
	Scope string
}

// isBearer 判断请求上下文中是否存在已经认证的 API 令牌身份。
func isBearer(r *http.Request) bool {
	_, ok := r.Context().Value(principalKey{}).(principal)
	return ok
}

// apiError 输出带有稳定错误代码和中文提示的 JSON 错误响应。
func apiError(w http.ResponseWriter, status int, code, message string) {
	out(w, status, map[string]string{"code": code, "error": message})
}

// authorize treats an explicitly provided credential as authoritative: it
// never falls back to a cookie after an invalid Authorization header.
// authorize 优先验证显式 Authorization 凭据并限制操作权限，无效凭据不回退到 Cookie。
func (a *app) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc( /* 校验显式令牌、用户权限和接口限制，再把认证身份写入请求上下文。 */ func(w http.ResponseWriter, r *http.Request) {
		if _, supplied := r.Header["Authorization"]; !supplied {
			next.ServeHTTP(w, r)
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			apiError(w, 401, "unauthorized", "无效的访问令牌")
			return
		}
		var p principal
		err := a.db.QueryRow(`SELECT u.id,u.email,u.display_name,t.scope FROM api_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=? AND t.expires_at>?`, digest(parts[1]), time.Now().Unix()).Scan(&p.User.ID, &p.User.Email, &p.User.DisplayName, &p.Scope)
		if errors.Is(err, sql.ErrNoRows) {
			apiError(w, 401, "unauthorized", "访问令牌无效或已过期")
			return
		}
		if err != nil {
			apiError(w, 500, "internal_error", "认证失败")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/auth/") && r.URL.Path != "/api/auth/me" {
			apiError(w, 403, "forbidden", "此接口仅允许网页登录会话访问")
			return
		}
		if r.URL.Path == "/api/dashboard" || (p.Scope == "read" && r.Method != http.MethodGet && r.Method != http.MethodHead) || (r.Method == http.MethodDelete && r.URL.Path == "/api/retirement/items") {
			apiError(w, 403, "forbidden", "访问令牌没有此操作权限")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// apiTokens 通过网页登录会话列出、创建或撤销当前用户的 API 访问令牌。
func (a *app) apiTokens(w http.ResponseWriter, r *http.Request) {
	if isBearer(r) {
		apiError(w, 403, "forbidden", "令牌管理需要网页登录会话")
		return
	}
	u := a.need(w, r)
	if u == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query(`SELECT id,name,scope,expires_at,created_at FROM api_tokens WHERE user_id=? ORDER BY id`, u.ID)
		if err != nil {
			apiError(w, 500, "internal_error", "无法读取令牌")
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, expires int64
			var name, scope, created string
			if rows.Scan(&id, &name, &scope, &expires, &created) != nil {
				apiError(w, 500, "internal_error", "无法读取令牌")
				return
			}
			items = append(items, map[string]any{"id": id, "name": name, "scope": scope, "expiresAt": expires, "createdAt": created})
		}
		if rows.Err() != nil {
			apiError(w, 500, "internal_error", "无法读取令牌")
			return
		}
		out(w, 200, map[string]any{"tokens": items})
	case http.MethodPost:
		var x /* 承载令牌创建请求的名称、权限与原始有效期字段。 */ struct {
			Name, Scope   string
			ExpiresInDays json.RawMessage
		}
		if body(r, &x) != nil {
			apiError(w, 400, "invalid_request", "请求无效")
			return
		}
		days := 30
		if len(x.ExpiresInDays) > 0 {
			if strings.TrimSpace(string(x.ExpiresInDays)) == "null" || json.Unmarshal(x.ExpiresInDays, &days) != nil {
				apiError(w, 400, "invalid_request", "有效期必须为整数")
				return
			}
		}
		x.Name = strings.TrimSpace(x.Name)
		if x.Name == "" || len([]rune(x.Name)) > 120 || (x.Scope != "read" && x.Scope != "write") || days < 1 || days > 365 {
			apiError(w, 400, "invalid_request", "请输入名称、read/write 权限和 1 至 365 天有效期")
			return
		}
		plain := token()
		expires := time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()
		res, err := a.db.Exec(`INSERT INTO api_tokens(user_id,name,token_hash,scope,expires_at) VALUES(?,?,?,?,?)`, u.ID, x.Name, digest(plain), x.Scope, expires)
		if err != nil {
			apiError(w, 500, "internal_error", "无法创建令牌")
			return
		}
		id, _ := res.LastInsertId()
		out(w, 201, map[string]any{"id": id, "name": x.Name, "scope": x.Scope, "expiresAt": expires, "token": plain})
	case http.MethodDelete:
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id < 1 {
			apiError(w, 400, "invalid_request", "无效令牌 ID")
			return
		}
		res, err := a.db.Exec(`DELETE FROM api_tokens WHERE id=? AND user_id=?`, id, u.ID)
		if err != nil {
			apiError(w, 500, "internal_error", "无法撤销令牌")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			apiError(w, 404, "not_found", "令牌不存在")
			return
		}
		out(w, 200, map[string]bool{"ok": true})
	default:
		apiError(w, 405, "method_not_allowed", "方法不允许")
	}
}
