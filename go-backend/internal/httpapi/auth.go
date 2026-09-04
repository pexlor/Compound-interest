package httpapi

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

// token 生成安全的随机会话令牌。
func token() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// digest 计算会话令牌的 SHA-256 摘要以便安全存储。
func digest(s string) string {
	x := sha256.Sum256([]byte(s))
	return base64.StdEncoding.EncodeToString(x[:])
}

// password 使用 PBKDF2 派生密码哈希。
func password(p, salt string, n int) string {
	key, err := pbkdf2.Key(sha256.New, p, must64(salt), n, 32)
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// must64 解码 Base64 字符串；仅用于已验证的内部盐值。
func must64(s string) []byte { v, _ := base64.StdEncoding.DecodeString(s); return v }

// current 根据会话 Cookie 查询当前登录用户。
func (a *app) current(r *http.Request) (*user, error) {
	c, e := r.Cookie(sessionName)
	if e != nil {
		return nil, nil
	}
	var u user
	e = a.db.QueryRow(`SELECT u.id,u.email,u.display_name FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, digest(c.Value), time.Now().Unix()).Scan(&u.ID, &u.Email, &u.DisplayName)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return &u, e
}

// need 获取当前用户；未登录时直接返回认证错误。
func (a *app) need(w http.ResponseWriter, r *http.Request) *user {
	u, e := a.current(r)
	if e != nil {
		fail(w, 500, e.Error())
		return nil
	}
	if u == nil {
		fail(w, 401, "请先登录")
	}
	return u
}

// setSession 创建 30 天有效的会话并写入安全 Cookie。
func (a *app) setSession(w http.ResponseWriter, id int64) {
	t := token()
	_, _ = a.db.Exec("DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix())
	_, _ = a.db.Exec("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)", digest(t), id, time.Now().Add(30*24*time.Hour).Unix())
	http.SetCookie(w, &http.Cookie{Name: sessionName, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 2592000})
}

// register 处理用户注册请求。
func (a *app) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "方法不允许")
		return
	}
	var x struct{ DisplayName, Email, Password string }
	if body(r, &x) != nil || len([]rune(strings.TrimSpace(x.DisplayName))) < 2 || len([]rune(x.DisplayName)) > 40 || !strings.Contains(x.Email, "@") || len(x.Password) < 8 || len(x.Password) > 128 {
		fail(w, 400, "请输入有效的注册信息")
		return
	}
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	s := base64.StdEncoding.EncodeToString(b)
	res, e := a.db.Exec("INSERT INTO users(email,display_name,password_hash,password_salt,password_iterations) VALUES(?,?,?,?,210000)", strings.ToLower(strings.TrimSpace(x.Email)), strings.TrimSpace(x.DisplayName), password(x.Password, s, 210000), s)
	if e != nil {
		fail(w, 409, "该邮箱已经注册，请直接登录")
		return
	}
	id, _ := res.LastInsertId()
	a.setSession(w, id)
	out(w, 201, map[string]any{"user": user{id, strings.ToLower(strings.TrimSpace(x.Email)), strings.TrimSpace(x.DisplayName)}})
}

// login 校验凭据并创建登录会话。
func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "方法不允许")
		return
	}
	var x struct{ Email, Password string }
	if body(r, &x) != nil {
		fail(w, 400, "请求无效")
		return
	}
	var id int64
	var email, name, hash, salt string
	var n int
	e := a.db.QueryRow("SELECT id,email,display_name,password_hash,password_salt,password_iterations FROM users WHERE email=?", strings.ToLower(strings.TrimSpace(x.Email))).Scan(&id, &email, &name, &hash, &salt, &n)
	if e != nil || subtle.ConstantTimeCompare([]byte(password(x.Password, salt, n)), []byte(hash)) != 1 {
		fail(w, 401, "邮箱或密码不正确")
		return
	}
	a.setSession(w, id)
	out(w, 200, map[string]any{"user": user{id, email, name}})
}

// logout 删除当前会话并清除浏览器 Cookie。
func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "方法不允许")
		return
	}
	if c, e := r.Cookie(sessionName); e == nil {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE token_hash=?", digest(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	out(w, 200, map[string]bool{"ok": true})
}

// me 返回当前登录用户资料。
func (a *app) me(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fail(w, 405, "方法不允许")
		return
	}
	u := a.need(w, r)
	if u != nil {
		out(w, 200, map[string]any{"user": u})
	}
}
