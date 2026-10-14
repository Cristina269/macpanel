package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// GET /api/monitors
func (app *App) handleMonitorList(w http.ResponseWriter, r *http.Request, s *Session) {
	var mons []Monitor
	app.store.Read(func(c *Config) {
		mons = append([]Monitor(nil), c.Monitors...)
	})
	apiOK(w, map[string]any{"monitors": mons})
}

// POST /api/monitors {name, type, target, enabled}
func (app *App) handleMonitorAdd(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Target  string `json:"target"`
		Enabled bool   `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	switch req.Type {
	case "launchd", "port", "http", "process":
	default:
		apiErr(w, 400, "类型须为 launchd/port/http/process")
		return
	}
	if req.Name == "" || req.Target == "" {
		apiErr(w, 400, "名称和目标不能为空")
		return
	}
	m := Monitor{ID: randomToken(8), Name: req.Name, Type: req.Type, Target: req.Target, Enabled: req.Enabled}
	if err := app.store.Update(func(c *Config) { c.Monitors = append(c.Monitors, m) }); err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	app.audit("monitor_add", m.Name)
	app.checkMonitor(m.ID)
	apiOK(w, map[string]any{"monitor": m})
}

// POST /api/monitors/delete {id}
func (app *App) handleMonitorDelete(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	_ = app.store.Update(func(c *Config) {
		out := c.Monitors[:0]
		for _, m := range c.Monitors {
			if m.ID != req.ID {
				out = append(out, m)
			}
		}
		c.Monitors = out
	})
	apiOK(w, nil)
}

// POST /api/monitors/toggle {id, enabled}
func (app *App) handleMonitorToggle(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	_ = app.store.Update(func(c *Config) {
		for i := range c.Monitors {
			if c.Monitors[i].ID == req.ID {
				c.Monitors[i].Enabled = req.Enabled
			}
		}
	})
	apiOK(w, nil)
}

// POST /api/monitors/check {id}
func (app *App) handleMonitorCheck(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	app.checkMonitor(req.ID)
	var mon *Monitor
	app.store.Read(func(c *Config) {
		for i := range c.Monitors {
			if c.Monitors[i].ID == req.ID {
				mm := c.Monitors[i]
				mon = &mm
			}
		}
	})
	if mon == nil {
		apiErr(w, 404, "未找到")
		return
	}
	apiOK(w, map[string]any{"monitor": mon})
}

// POST /api/monitors/action {id, action}
func (app *App) handleMonitorAction(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	var target string
	app.store.Read(func(c *Config) {
		for _, m := range c.Monitors {
			if m.ID == req.ID && m.Type == "launchd" {
				target = m.Target
			}
		}
	})
	if target == "" {
		apiErr(w, 404, "未找到或类型不支持")
		return
	}
	label := "gui/" + fmt.Sprint(os.Getuid()) + "/" + target
	var out string
	var code int
	switch req.Action {
	case "restart":
		out, code = runCmd(0, "launchctl", "kickstart", "-k", label)
	case "start":
		out, code = runCmd(0, "launchctl", "kickstart", label)
	case "stop":
		out, code = runCmd(0, "launchctl", "bootout", label)
	default:
		apiErr(w, 400, "未知操作")
		return
	}
	app.audit("monitor_"+req.Action, target)
	if code != 0 {
		apiErr(w, 500, strings.TrimSpace(out))
		return
	}
	apiOK(w, nil)
}

// checkMonitor runs a single check and writes the status back by ID (values are copies, so no concurrency issues)
func (app *App) checkMonitor(id string) {
	var m Monitor
	found := false
	app.store.Read(func(c *Config) {
		for _, mm := range c.Monitors {
			if mm.ID == id {
				m, found = mm, true
			}
		}
	})
	if !found {
		return
	}
	start := time.Now()
	ok, errOut := false, ""
	switch m.Type {
	case "port":
		host, port, has := splitHostPort(m.Target)
		if !has {
			errOut = "目标须为 host:port"
			break
		}
		d := net.Dialer{Timeout: 3 * time.Second}
		conn, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort(host, port))
		if err == nil {
			conn.Close()
			ok = true
		} else {
			errOut = err.Error()
		}
	case "http":
		client := http.Client{Timeout: 5 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
		resp, err := client.Get(m.Target)
		if err == nil {
			resp.Body.Close()
			ok = resp.StatusCode < 500
			if !ok {
				errOut = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
		} else {
			errOut = err.Error()
		}
	case "process":
		out, code := runCmd(10*time.Second, "pgrep", "-f", m.Target)
		ok = code == 0 && strings.TrimSpace(out) != ""
		if !ok {
			errOut = "进程未找到"
		}
	case "launchd":
		out, code := runCmd(10*time.Second, "launchctl", "print", "gui/"+fmt.Sprint(os.Getuid())+"/"+m.Target)
		ok = code == 0 && strings.Contains(out, "state = running")
		if !ok {
			errOut = "服务未运行"
		}
	}
	ms := float64(time.Since(start).Microseconds()) / 1000
	_ = app.store.Update(func(c *Config) {
		for i := range c.Monitors {
			if c.Monitors[i].ID == id {
				c.Monitors[i].LastOK = ok
				c.Monitors[i].LastCheck = time.Now()
				c.Monitors[i].LatencyMs = ms
				c.Monitors[i].LastError = errOut
			}
		}
	})
}

func (app *App) monitorLoop(every time.Duration) {
	for {
		time.Sleep(every)
		var ids []string
		app.store.Read(func(c *Config) {
			for _, m := range c.Monitors {
				if m.Enabled {
					ids = append(ids, m.ID)
				}
			}
		})
		for _, id := range ids {
			app.checkMonitor(id)
		}
	}
}

func splitHostPort(s string) (host, port string, ok bool) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}
