package main

import (
	"net/http"
	"strings"
	"time"
)

type BrewPkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// GET /api/brew/installed
func (app *App) handleBrewInstalled(w http.ResponseWriter, r *http.Request, s *Session) {
	out, code := runCmd(60*time.Second, brewBin(), "list", "--versions")
	if code != 0 {
		apiErr(w, 500, "brew list 失败: "+out)
		return
	}
	pkgs := []BrewPkg{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			pkgs = append(pkgs, BrewPkg{Name: f[0], Version: f[1]})
		}
	}
	apiOK(w, map[string]any{"packages": pkgs})
}

// GET /api/brew/search?q=xxx
func (app *App) handleBrewSearch(w http.ResponseWriter, r *http.Request, s *Session) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || !safeName(q) {
		apiErr(w, 400, "搜索词无效")
		return
	}
	out, code := runCmd(60*time.Second, brewBin(), "search", q)
	if code != 0 {
		apiErr(w, 500, "brew search 失败: "+out)
		return
	}
	names := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "==> ") || strings.HasSuffix(line, "formulae") || strings.HasSuffix(line, "casks") {
			continue
		}
		if !strings.Contains(line, " ") {
			names = append(names, line)
		}
	}
	apiOK(w, map[string]any{"results": names})
}

// GET /api/brew/info?name=xxx
func (app *App) handleBrewInfo(w http.ResponseWriter, r *http.Request, s *Session) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if !safeName(name) {
		apiErr(w, 400, "名称无效")
		return
	}
	out, code := runCmd(60*time.Second, brewBin(), "info", name)
	if code != 0 {
		apiErr(w, 404, strings.TrimSpace(out))
		return
	}
	apiOK(w, map[string]any{"info": strings.TrimSpace(out)})
}

// POST /api/brew/action {name, action: install|uninstall|upgrade}
func (app *App) handleBrewAction(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Name   string `json:"name"`
		Action string `json:"action"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	if !safeName(req.Name) {
		apiErr(w, 400, "名称无效")
		return
	}
	switch req.Action {
	case "install", "uninstall", "upgrade":
	default:
		apiErr(w, 400, "未知操作")
		return
	}
	// uninstall requires a confirmation header
	if req.Action == "uninstall" && r.Header.Get("X-Panel-Confirm") != "yes" {
		apiErr(w, 400, "缺少确认头")
		return
	}
	j := app.jobs.Start("brew "+req.Action+" "+req.Name, brewBin(), req.Action, req.Name)
	app.audit("brew_"+req.Action, req.Name)
	apiOK(w, map[string]any{"job_id": j.ID})
}
