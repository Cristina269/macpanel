package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

var dataDir string

type App struct {
	store    *Store
	sessions *SessionManager
	limiter  *RateLimiter
	sampler  *Sampler
	jobs     *JobManager
}

func main() {
	defaultDir := filepath.Join(homeDir(), "macpanel", "data")
	var cfgDir = flag.String("data", defaultDir, "数据目录")
	flag.Parse()
	dataDir = *cfgDir
	_ = os.MkdirAll(dataDir, 0o700)

	store, err := LoadStore(dataDir)
	if err != nil {
		log.Fatal("加载配置失败: ", err)
	}
	app := &App{
		store:    store,
		sessions: NewSessions(filepath.Join(dataDir, "sessions.json")),
		limiter:  NewRateLimiter(),
		sampler:  NewSampler(),
		jobs:     NewJobs(),
	}
	app.sessions.Load()
	app.sampler.Start(5 * time.Second)
	go app.monitorLoop(60 * time.Second)

	mux := http.NewServeMux()

	// 公开:初始化状态 + 初始化 + 登录
	mux.HandleFunc("/api/setup-status", app.handleSetupStatus)
	mux.HandleFunc("/api/setup", app.handleSetup)
	mux.HandleFunc("/api/login", app.handleLogin)

	// 需登录
	mux.HandleFunc("/api/me", app.authWrap(app.handleMe))
	mux.HandleFunc("/api/logout", app.authWrap(app.handleLogout))
	mux.HandleFunc("/api/system/status", app.authWrap(app.handleStatus))
	mux.HandleFunc("/api/system/history", app.authWrap(app.handleHistory))
	mux.HandleFunc("/api/system/ports", app.authWrap(app.handlePorts))
	mux.HandleFunc("/api/system/ports/favorite", app.authWrap(app.handlePortFavorite))
	mux.HandleFunc("/api/system/probe", app.authWrap(app.handleProbe))
	mux.HandleFunc("/api/services/launchd", app.authWrap(app.handleLaunchdList))
	mux.HandleFunc("/api/services/launchd/action", app.authWrap(app.handleLaunchdAction))
	mux.HandleFunc("/api/services/brew", app.authWrap(app.handleBrewServices))
	mux.HandleFunc("/api/services/brew/action", app.authWrap(app.handleBrewServiceAction))
	mux.HandleFunc("/api/monitors", app.authWrap(app.handleMonitorList))
	mux.HandleFunc("/api/monitors/add", app.authWrap(app.handleMonitorAdd))
	mux.HandleFunc("/api/monitors/delete", app.authWrap(app.handleMonitorDelete))
	mux.HandleFunc("/api/monitors/toggle", app.authWrap(app.handleMonitorToggle))
	mux.HandleFunc("/api/monitors/check", app.authWrap(app.handleMonitorCheck))
	mux.HandleFunc("/api/monitors/action", app.authWrap(app.handleMonitorAction))
	mux.HandleFunc("/api/cron", app.authWrap(app.handleCronList))
	mux.HandleFunc("/api/cron/toggle", app.authWrap(app.handleCronToggle))
	mux.HandleFunc("/api/cron/save", app.authWrap(app.handleCronSave))
	mux.HandleFunc("/api/cron/delete", app.authWrap(app.handleCronDelete))
	mux.HandleFunc("/api/brew/installed", app.authWrap(app.handleBrewInstalled))
	mux.HandleFunc("/api/brew/search", app.authWrap(app.handleBrewSearch))
	mux.HandleFunc("/api/brew/info", app.authWrap(app.handleBrewInfo))
	mux.HandleFunc("/api/brew/action", app.authWrap(app.handleBrewAction))
	mux.HandleFunc("/api/container/list", app.authWrap(app.handleContainerList))
	mux.HandleFunc("/api/container/action", app.authWrap(app.handleContainerAction))
	mux.HandleFunc("/api/container/logs", app.authWrap(app.handleContainerLogs))
	mux.HandleFunc("/api/container/images", app.authWrap(app.handleContainerImages))
	mux.HandleFunc("/api/container/run", app.authWrap(app.handleContainerRun))
	mux.HandleFunc("/api/sites", app.authWrap(app.handleSiteList))
	mux.HandleFunc("/api/sites/save", app.authWrap(app.handleSiteSave))
	mux.HandleFunc("/api/sites/delete", app.authWrap(app.handleSiteDelete))
	mux.HandleFunc("/api/sites/caddy", app.authWrap(app.handleCaddyAction))
	mux.HandleFunc("/api/apps", app.authWrap(app.handleAppList))
	mux.HandleFunc("/api/apps/install-brew", app.authWrap(app.handleAppInstallBrew))
	mux.HandleFunc("/api/apps/install-container", app.authWrap(app.handleAppInstallContainer))
	mux.HandleFunc("/api/jobs", app.authWrap(app.handleJobList))
	mux.HandleFunc("/api/jobs/", app.authWrap(app.handleJobGet))
	mux.HandleFunc("/api/ai/settings", app.authWrap(app.handleAIGet))
	mux.HandleFunc("/api/ai/settings/save", app.authWrap(app.handleAISet))
	mux.HandleFunc("/api/ai/chat", app.authWrap(app.handleAIChat))
	mux.HandleFunc("/api/audit", app.authWrap(app.handleAudit))
	mux.HandleFunc("/api/settings", app.authWrap(app.handleSettingsGet))
	mux.HandleFunc("/api/settings/save", app.authWrap(app.handleSettingsSave))
	mux.HandleFunc("/api/password", app.authWrap(app.handlePasswordChange))
	mux.HandleFunc("/api/terminal", app.authWrap(app.handleTerminal))

	// 静态资源(公开,SPA 壳由 entrance 中间件守卫);URL /assets/* ↔ web/assets/*
	assets, _ := fs.Sub(webFS, "web/assets")
	fileServer := http.FileServer(http.FS(assets))
	mux.Handle("/assets/", http.StripPrefix("/assets/", fileServer))

	// SPA 入口(含 entrance 门卫)
	mux.HandleFunc("/", app.spaHandler(fileServer))

	handler := securityHeaders(cacheControl(mux))

	listen := store.Cfg.Listen
	if listen == "" {
		listen = ":9000"
	}
	tlsOn := store.Cfg.TLSCert != "" && store.Cfg.TLSKey != ""
	if tlsOn && store.Cfg.HTTPPort != "" {
		go func() {
			redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host := r.Host
				target := "https://" + host + r.RequestURI
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			})
			log.Println("HTTP 跳转监听", store.Cfg.HTTPPort)
			log.Println(http.ListenAndServe(store.Cfg.HTTPPort, securityHeaders(redirect)))
		}()
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("MacPanel 启动于 %s (TLS=%v)", listen, tlsOn)
	if tlsOn {
		log.Fatal(srv.ListenAndServeTLS(store.Cfg.TLSCert, store.Cfg.TLSKey))
	} else {
		log.Fatal(srv.ListenAndServe())
	}
}

// cacheControl 防"升级后浏览器用旧缓存"问题:API 与 SPA 壳禁缓存,静态资源要求回源验证
func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler:entrance 门卫 + SPA 壳
func (app *App) spaHandler(assets http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		entrance := app.store.Cfg.Entrance
		if entrance != "" {
			// API 路径由各自 auth 守卫,不在这里拦
			if !strings.HasPrefix(p, "/api/") && !strings.HasPrefix(p, "/assets/") {
				if p != "/"+entrance && !strings.HasPrefix(p, "/"+entrance+"/") {
					// 404 而非 302:不向扫描器暴露入口的存在
					http.NotFound(w, r)
					return
				}
			}
		}
		// 其余路径一律回 SPA
		b, err := webFS.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, "panel shell missing", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}
}

// handleSettingsGet 读取当前安全设置(不含密码哈希)
func (app *App) handleSettingsGet(w http.ResponseWriter, r *http.Request, s *Session) {
	out := map[string]any{}
	app.store.Read(func(c *Config) {
		out["entrance"] = c.Entrance
		out["listen"] = c.Listen
		out["tls_cert"] = c.TLSCert
		out["tls_key"] = c.TLSKey
		out["http_port"] = c.HTTPPort
		out["ip_allowlist"] = c.IPAllowlist
		out["known_services"] = c.KnownServices
		out["session_hours"] = c.SessionHours
	})
	apiOK(w, out)
}

// handleSettingsSave 安全设置:入口路径、监听、TLS、IP 白名单、已知服务
func (app *App) handleSettingsSave(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Entrance      *string         `json:"entrance"`
		Listen        *string         `json:"listen"`
		TLSCert       *string         `json:"tls_cert"`
		TLSKey        *string         `json:"tls_key"`
		HTTPPort      *string         `json:"http_port"`
		IPAllowlist   *[]string       `json:"ip_allowlist"`
		KnownServices *[]KnownService `json:"known_services"`
		SessionHours  *int            `json:"session_hours"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	err := app.store.Update(func(c *Config) {
		if req.Entrance != nil {
			c.Entrance = strings.TrimSpace(*req.Entrance)
		}
		if req.Listen != nil {
			c.Listen = strings.TrimSpace(*req.Listen)
		}
		if req.TLSCert != nil {
			c.TLSCert = strings.TrimSpace(*req.TLSCert)
		}
		if req.TLSKey != nil {
			c.TLSKey = strings.TrimSpace(*req.TLSKey)
		}
		if req.HTTPPort != nil {
			c.HTTPPort = strings.TrimSpace(*req.HTTPPort)
		}
		if req.IPAllowlist != nil {
			c.IPAllowlist = *req.IPAllowlist
		}
		if req.KnownServices != nil {
			c.KnownServices = *req.KnownServices
		}
		if req.SessionHours != nil && *req.SessionHours >= 1 && *req.SessionHours <= 720 {
			c.SessionHours = *req.SessionHours
		}
	})
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	app.audit("settings_save", "")
	apiOK(w, map[string]any{"note": "listen/TLS 修改需重启面板生效"})
}

// handlePasswordChange 修改自己的密码
func (app *App) handlePasswordChange(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	if len(req.New) < 10 {
		apiErr(w, 400, "新密码至少 10 位")
		return
	}
	ok := false
	app.store.Read(func(c *Config) {
		for _, u := range c.Users {
			if u.Name == s.User && verifyPassword(req.Old, u.Hash) {
				ok = true
			}
		}
	})
	if !ok {
		apiErr(w, 401, "旧密码错误")
		return
	}
	err := app.store.Update(func(c *Config) {
		for i := range c.Users {
			if c.Users[i].Name == s.User {
				c.Users[i].Hash = hashPassword(req.New)
			}
		}
	})
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	app.audit("password_change", s.User)
	apiOK(w, nil)
}
