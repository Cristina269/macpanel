package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type CronJob struct {
	Index    int    `json:"index"` // line number in the crontab file
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Name     string `json:"name"` // trailing "# name" comment on the line
	Enabled  bool   `json:"enabled"`
}

func loadCrontab() (lines []string, err error) {
	out, code := runCmd(10*time.Second, "crontab", "-l")
	if code != 0 && !strings.Contains(out, "no crontab") {
		return nil, fmt.Errorf("crontab -l 失败: %s", strings.TrimSpace(out))
	}
	for _, l := range strings.Split(out, "\n") {
		lines = append(lines, strings.TrimRight(l, "\r"))
	}
	return lines, nil
}

func parseCronJobs(lines []string) []CronJob {
	jobs := []CronJob{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// fields: min hour day month weekday command... (supports special schedules like @reboot, 6+ fields)
		f := strings.Fields(trimmed)
		if len(f) < 6 {
			continue
		}
		special := strings.HasPrefix(f[0], "@")
		if !special && len(f) < 6 {
			continue
		}
		fields := 6
		if special {
			fields = 1 + 5 - 5 // @reboot cmd → f[0]=@reboot, f[1:]=cmd
		}
		_ = fields
		var sched, cmd string
		if special {
			sched = f[0]
			cmd = strings.Join(f[1:], " ")
		} else {
			sched = strings.Join(f[:5], " ")
			cmd = strings.Join(f[5:], " ")
		}
		name := ""
		if idx := strings.LastIndex(cmd, " # "); idx > 0 {
			name = strings.TrimSpace(cmd[idx+3:])
			cmd = strings.TrimSpace(cmd[:idx])
		}
		jobs = append(jobs, CronJob{Index: i, Schedule: sched, Command: cmd, Name: name, Enabled: true})
	}
	// disabled (commented-out) jobs: in the form "# min hour day month weekday cmd"
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		inner := strings.TrimSpace(trimmed[1:])
		f := strings.Fields(inner)
		if len(f) < 6 {
			continue
		}
		sched := strings.Join(f[:5], " ")
		cmd := strings.Join(f[5:], " ")
		name := ""
		if idx := strings.LastIndex(cmd, " # "); idx > 0 {
			name = strings.TrimSpace(cmd[idx+3:])
			cmd = strings.TrimSpace(cmd[:idx])
		}
		jobs = append(jobs, CronJob{Index: i, Schedule: sched, Command: cmd, Name: name, Enabled: false})
	}
	return jobs
}

// GET /api/cron
func (app *App) handleCronList(w http.ResponseWriter, r *http.Request, s *Session) {
	lines, err := loadCrontab()
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	apiOK(w, map[string]any{"jobs": parseCronJobs(lines)})
}

// backupCrontab backs up the crontab before writing it back
func backupCrontab(lines []string) {
	dir := filepath.Join(dataDir, "cron-backup")
	_ = os.MkdirAll(dir, 0o700)
	name := time.Now().Format("20060102-150405") + ".crontab"
	_ = os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	// keep only the latest 20 backups
	entries, _ := os.ReadDir(dir)
	if len(entries) > 20 {
		for _, e := range entries[:len(entries)-20] {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func writeCrontab(lines []string) error {
	body := strings.Join(lines, "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	cmd := exec.Command("crontab", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, _ = stdin.Write([]byte(body))
	_ = stdin.Close()
	return cmd.Wait()
}

// POST /api/cron/toggle {index} — enable/disable that line
func (app *App) handleCronToggle(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Index int `json:"index"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	lines, err := loadCrontab()
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	if req.Index < 0 || req.Index >= len(lines) {
		apiErr(w, 404, "行号越界")
		return
	}
	line := strings.TrimSpace(lines[req.Index])
	if strings.HasPrefix(line, "#") {
		lines[req.Index] = strings.TrimSpace(line[1:])
	} else if line != "" {
		lines[req.Index] = "# " + line
	} else {
		apiErr(w, 400, "空行无法切换")
		return
	}
	backupCrontab(lines)
	if err := writeCrontab(lines); err != nil {
		apiErr(w, 500, "写回失败: "+err.Error())
		return
	}
	app.audit("cron_toggle", fmt.Sprintf("line %d", req.Index))
	apiOK(w, nil)
}

// POST /api/cron/save {index (-1 = add new), schedule, command, name}
func (app *App) handleCronSave(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Index    int    `json:"index"`
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
		Name     string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	if strings.ContainsAny(req.Schedule+req.Command, "\n\r") {
		apiErr(w, 400, "不能包含换行")
		return
	}
	line := req.Schedule + " " + req.Command
	if req.Name != "" {
		line += " # " + req.Name
	}
	lines, err := loadCrontab()
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	if req.Index < 0 {
		lines = append(lines, line)
	} else {
		if req.Index >= len(lines) {
			apiErr(w, 404, "行号越界")
			return
		}
		lines[req.Index] = line
	}
	backupCrontab(lines)
	if err := writeCrontab(lines); err != nil {
		apiErr(w, 500, "写回失败: "+err.Error())
		return
	}
	app.audit("cron_save", line)
	apiOK(w, nil)
}

// POST /api/cron/delete {index}
func (app *App) handleCronDelete(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Index int `json:"index"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	lines, err := loadCrontab()
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	if req.Index < 0 || req.Index >= len(lines) {
		apiErr(w, 404, "行号越界")
		return
	}
	lines = append(lines[:req.Index], lines[req.Index+1:]...)
	backupCrontab(lines)
	if err := writeCrontab(lines); err != nil {
		apiErr(w, 500, "写回失败: "+err.Error())
		return
	}
	app.audit("cron_delete", fmt.Sprintf("line %d", req.Index))
	apiOK(w, nil)
}
