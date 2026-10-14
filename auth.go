package main

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// ---------- argon2id 密码哈希 ----------

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

func hashPassword(pw string) string {
	salt := randomToken(argonSaltLen / 2)
	saltBytes, _ := hex.DecodeString(salt)
	key := argon2.IDKey([]byte(pw), saltBytes, argonTime, argonMemory, argonThreads, argonKeyLen)
	return "argon2id$" + salt + "$" + hex.EncodeToString(key)
}

func verifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	saltBytes, err1 := hex.DecodeString(parts[1])
	keyBytes, err2 := hex.DecodeString(parts[2])
	if err1 != nil || err2 != nil {
		return false
	}
	key := argon2.IDKey([]byte(pw), saltBytes, argonTime, argonMemory, argonThreads, uint32(len(keyBytes)))
	return subtle.ConstantTimeCompare(key, keyBytes) == 1
}

// ---------- 会话 ----------

type Session struct {
	Token    string
	CSRF     string
	User     string
	Expires  time.Time
	LastSeen time.Time
}

type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	path     string // 持久化文件(重启不丢会话),空则不持久化
}

func NewSessions(persistPath string) *SessionManager {
	return &SessionManager{sessions: map[string]*Session{}, path: persistPath}
}

const sessionCookie = "macpanel_session"

func (sm *SessionManager) create(user string, hours int) *Session {
	if hours < 1 {
		hours = 24 // 防 config 里 session_hours 缺失/为 0 导致会话秒过期
	}
	s := &Session{
		Token:    randomToken(32),
		CSRF:     randomToken(32),
		User:     user,
		Expires:  time.Now().Add(time.Duration(hours) * time.Hour),
		LastSeen: time.Now(),
	}
	sm.mu.Lock()
	// 顺带清理过期会话
	now := time.Now()
	for k, v := range sm.sessions {
		if now.After(v.Expires) {
			delete(sm.sessions, k)
		}
	}
	sm.sessions[s.Token] = s
	sm.persistLocked()
	sm.mu.Unlock()
	return s
}

// persistLocked 落盘会话(0600,文件里是有效凭证,权限必须收紧)
func (sm *SessionManager) persistLocked() {
	if sm.path == "" {
		return
	}
	type sRec struct {
		Token, CSRF, User string
		Expires, LastSeen time.Time
	}
	out := make([]sRec, 0, len(sm.sessions))
	for _, v := range sm.sessions {
		out = append(out, sRec{v.Token, v.CSRF, v.User, v.Expires, v.LastSeen})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	tmp := sm.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, sm.path)
	}
}

// Load 启动时恢复会话(丢弃已过期)
func (sm *SessionManager) Load() {
	if sm.path == "" {
		return
	}
	b, err := os.ReadFile(sm.path)
	if err != nil {
		return
	}
	type sRec struct {
		Token, CSRF, User string
		Expires, LastSeen time.Time
	}
	var recs []sRec
	if json.Unmarshal(b, &recs) != nil {
		return
	}
	now := time.Now()
	restored := 0
	sm.mu.Lock()
	for _, r := range recs {
		if r.Token != "" && now.Before(r.Expires) {
			sm.sessions[r.Token] = &Session{Token: r.Token, CSRF: r.CSRF, User: r.User, Expires: r.Expires, LastSeen: r.LastSeen}
			restored++
		}
	}
	sm.mu.Unlock()
	if restored > 0 {
		log.Printf("已恢复 %d 个会话", restored)
	}
}

func (sm *SessionManager) get(token string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	s, ok := sm.sessions[token]
	if !ok || time.Now().After(s.Expires) {
		return nil
	}
	// 滑动续期:每分钟最多刷一次
	if time.Since(s.LastSeen) > time.Minute {
		s.Expires = time.Now().Add(time.Until(s.Expires) + time.Minute)
		s.LastSeen = time.Now()
	}
	return s
}

func (sm *SessionManager) remove(token string) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.persistLocked()
	sm.mu.Unlock()
}

func (sm *SessionManager) sessionFromRequest(r *http.Request) *Session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return sm.get(c.Value)
}

// ---------- 登录限速 ----------

type attempt struct {
	fails int
	until time.Time
}

type RateLimiter struct {
	mu      sync.Mutex
	byIP    map[string]*attempt
	global  int
	window  time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{byIP: map[string]*attempt{}, window: time.Now()}
}

// Allow 返回是否允许尝试
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if a, ok := rl.byIP[ip]; ok && now.Before(a.until) {
		return false
	}
	return true
}

func (rl *RateLimiter) Fail(ip string) (lockedFor time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	a := rl.byIP[ip]
	if a == nil {
		a = &attempt{}
		rl.byIP[ip] = a
	}
	a.fails++
	if a.fails >= 5 {
		// 指数退避:15m, 30m, 60m ...
		mult := uint(a.fails-5) % 4
		a.until = now.Add((15 << mult) * time.Minute)
		a.fails = 5
		return time.Until(a.until)
	}
	return 0
}

func (rl *RateLimiter) Reset(ip string) {
	rl.mu.Lock()
	delete(rl.byIP, ip)
	rl.mu.Unlock()
}

// ---------- 认证 handlers ----------

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{2,32}$`)

func (app *App) needsSetup() bool {
	return len(app.store.Cfg.Users) == 0
}

func (app *App) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	apiOK(w, map[string]any{"needs_setup": app.needsSetup()})
}

func (app *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !app.needsSetup() {
		apiErr(w, http.StatusForbidden, "已初始化,请直接登录")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	if !usernameRe.MatchString(req.Username) {
		apiErr(w, 400, "用户名 2-32 位,仅字母数字_.-")
		return
	}
	if len(req.Password) < 10 {
		apiErr(w, 400, "密码至少 10 位")
		return
	}
	entrance := "p_" + randomToken(6)
	err := app.store.Update(func(c *Config) {
		c.Users = []User{{Name: req.Username, Hash: hashPassword(req.Password)}}
		c.Entrance = entrance
	})
	if err != nil {
		apiErr(w, 500, "保存失败: "+err.Error())
		return
	}
	app.audit("setup", "创建管理员 "+req.Username)
	apiOK(w, map[string]any{"entrance": entrance})
}

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !app.limiter.Allow(ip) {
		app.audit("login_deny", ip+" 触发限速")
		apiErr(w, http.StatusTooManyRequests, "尝试次数过多,请稍后再试")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	okUser, okPass := false, false
	for _, u := range app.store.Cfg.Users {
		if u.Name == req.Username {
			okUser = true
			okPass = verifyPassword(req.Password, u.Hash)
			break
		}
	}
	// 用户不存在也做一次哈希,拉平时间差
	if !okUser {
		verifyPassword(req.Password, hashPassword("timing-equalizer"))
	}
	if !okUser || !okPass {
		if d := app.limiter.Fail(ip); d > 0 {
			app.audit("login_fail", ip+" "+req.Username+" 已锁定")
			apiErr(w, http.StatusTooManyRequests, "失败次数过多,锁定至 "+time.Now().Add(d).Format("15:04:05"))
			return
		}
		app.audit("login_fail", ip+" "+req.Username)
		apiErr(w, 401, "用户名或密码错误")
		return
	}
	app.limiter.Reset(ip)
	s := app.sessions.create(req.Username, app.store.Cfg.SessionHours)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   app.store.Cfg.SessionHours * 3600,
	})
	app.audit("login", ip+" "+req.Username)
	apiOK(w, map[string]any{"csrf": s.CSRF, "user": s.User})
}

func (app *App) handleLogout(w http.ResponseWriter, r *http.Request, s *Session) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		app.sessions.remove(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	apiOK(w, nil)
}

func (app *App) handleMe(w http.ResponseWriter, r *http.Request, s *Session) {
	apiOK(w, map[string]any{"user": s.User, "csrf": s.CSRF})
}

// ---------- 中间件 ----------

type AuthedHandler func(w http.ResponseWriter, r *http.Request, s *Session)

func (app *App) authWrap(h AuthedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// IP 白名单
		if len(app.store.Cfg.IPAllowlist) > 0 && !ipAllowed(clientIP(r), app.store.Cfg.IPAllowlist) {
			apiErr(w, http.StatusForbidden, "IP 不在白名单")
			return
		}
		s := app.sessions.sessionFromRequest(r)
		if s == nil {
			apiErr(w, 401, "未登录")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			csrf := r.Header.Get("X-Panel-CSRF")
			if csrf == "" || !sameStr(csrf, s.CSRF) {
				apiErr(w, http.StatusForbidden, "CSRF 校验失败")
				return
			}
		}
		h(w, r, s)
	}
}

func sameStr(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func ipAllowed(ip string, allowlist []string) bool {
	for _, a := range allowlist {
		if a == ip || a == ip+"/32" {
			return true
		}
		// 简单前缀匹配,如 "192.168.31." 或 "172.22."
		if strings.HasSuffix(a, ".") && strings.HasPrefix(ip, a) {
			return true
		}
	}
	return false
}

// securityHeaders 统一安全响应头
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:")
		h.Set("X-XSS-Protection", "0")
		next.ServeHTTP(w, r)
	})
}
