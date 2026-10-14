package main

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

//go:embed apps_catalog.json
var appsFS embed.FS

type BrewApp struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Desc    string `json:"desc"`
	Pkg     string `json:"pkg"`
	Ports   []int  `json:"ports"`
	Service string `json:"service"` // brew services name; empty if none
}

type ContainerApp struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Desc    string   `json:"desc"`
	Image   string   `json:"image"`
	Ports   []int    `json:"ports"`
	RunArgs []string `json:"run_args"` // extra args appended to container run (-p/-v/-e listed separately)
}

type AppCatalog struct {
	Brew      []BrewApp      `json:"brew"`
	Container []ContainerApp `json:"container"`
}

func loadCatalog() *AppCatalog {
	b, err := appsFS.ReadFile("apps_catalog.json")
	if err != nil {
		return &AppCatalog{}
	}
	c := &AppCatalog{}
	if json.Unmarshal(b, c) != nil {
		return &AppCatalog{}
	}
	return c
}

// GET /api/apps — catalog + installed status
func (app *App) handleAppList(w http.ResponseWriter, r *http.Request, s *Session) {
	cat := loadCatalog()

	// set of installed brew packages
	installed := map[string]bool{}
	if out, code := runCmd(60*time.Second, brewBin(), "list", "--formula", "-1"); code == 0 {
		for _, n := range strings.Fields(out) {
			installed[n] = true
		}
	}
	// set of container images
	images := map[string]bool{}
	if out, code := runCmd(30*time.Second, containerBinPath(), "ls", "--all", "--format", "json"); code == 0 {
		var rows []ContainerRow
		if json.Unmarshal([]byte(out), &rows) == nil {
			for _, row := range rows {
				images[row.Configuration.Image.Reference] = true
				// the catalog records bare names; at runtime the image may carry a mirror prefix
				if i := strings.LastIndex(row.Configuration.Image.Reference, "/"); i >= 0 {
					images[row.Configuration.Image.Reference[i+1:]] = true
				}
			}
		}
	}

	brewOut := []map[string]any{}
	for _, a := range cat.Brew {
		brewOut = append(brewOut, map[string]any{
			"id": a.ID, "name": a.Name, "desc": a.Desc, "pkg": a.Pkg,
			"ports": a.Ports, "service": a.Service, "installed": installed[a.Pkg],
		})
	}
	ctrOut := []map[string]any{}
	for _, a := range cat.Container {
		seg := strings.Split(a.Image, "/")
		bare := seg[len(seg)-1]
		ctrOut = append(ctrOut, map[string]any{
			"id": a.ID, "name": a.Name, "desc": a.Desc, "image": a.Image,
			"ports": a.Ports, "run_args": a.RunArgs, "installed": images[a.Image] || images[bare],
		})
	}
	apiOK(w, map[string]any{"brew": brewOut, "container": ctrOut})
}

// POST /api/apps/install-brew {id}
func (app *App) handleAppInstallBrew(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "Invalid request")
		return
	}
	for _, a := range loadCatalog().Brew {
		if a.ID == req.ID {
			j := app.jobs.Start("brew install "+a.Pkg, brewBin(), "install", a.Pkg)
			app.audit("app_install_brew", a.Pkg)
			apiOK(w, map[string]any{"job_id": j.ID})
			return
		}
	}
	apiErr(w, 404, "App not found")
}

// POST /api/apps/install-container {id}
func (app *App) handleAppInstallContainer(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "Invalid request")
		return
	}
	for _, a := range loadCatalog().Container {
		if a.ID == req.ID {
			args := []string{"run", "-d", "--name", a.ID}
			for _, arg := range a.RunArgs {
				args = append(args, strings.ReplaceAll(arg, "$HOME", homeDir()))
			}
			args = append(args, a.Image)
			j := app.jobs.Start("container run "+a.ID, append([]string{containerBinPath()}, args...)...)
			app.audit("app_install_container", a.Image)
			apiOK(w, map[string]any{"job_id": j.ID})
			return
		}
	}
	apiErr(w, 404, "App not found")
}
