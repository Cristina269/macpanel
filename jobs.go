package main

import (
	"bufio"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Job 后台任务(brew install、容器创建、证书签发等)
type Job struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Status  string   `json:"status"` // running | done | failed
	Cmdline string   `json:"cmdline"`
	Exit    int      `json:"exit"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended"`

	mu   sync.Mutex
	logs []string
}

const jobLogMax = 1000

func (j *Job) appendLog(line string) {
	j.mu.Lock()
	if len(j.logs) >= jobLogMax {
		j.logs = j.logs[len(j.logs)-jobLogMax+1:]
	}
	j.logs = append(j.logs, line)
	j.mu.Unlock()
}

func (j *Job) tail(n int) []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if n <= 0 || n > len(j.logs) {
		n = len(j.logs)
	}
	out := make([]string, n)
	copy(out, j.logs[len(j.logs)-n:])
	return out
}

type JobManager struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func NewJobs() *JobManager { return &JobManager{jobs: map[string]*Job{}} }

// Start 启动后台命令,返回 job
func (jm *JobManager) Start(name string, argv ...string) *Job {
	j := &Job{
		ID: randomToken(8), Name: name,
		Status:  "running",
		Cmdline: strings.Join(argv, " "),
		Started: time.Now(),
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		j.appendLog("pipe error: " + err.Error())
		j.Status = "failed"
		return j
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		j.appendLog("start error: " + err.Error())
		j.Status = "failed"
		return j
	}
	jm.mu.Lock()
	jm.jobs[j.ID] = j
	jm.mu.Unlock()
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			j.appendLog(sc.Text())
		}
		err := cmd.Wait()
		j.Exit = 0
		if ee, ok := err.(*exec.ExitError); ok {
			j.Exit = ee.ExitCode()
		} else if err != nil {
			j.Exit = -1
		}
		j.Ended = time.Now()
		if j.Exit == 0 {
			j.Status = "done"
		} else {
			j.Status = "failed"
		}
	}()
	return j
}

// GET /api/jobs — 列表(简要)
func (app *App) handleJobList(w http.ResponseWriter, r *http.Request, s *Session) {
	type jobBrief struct {
		ID      string    `json:"id"`
		Name    string    `json:"name"`
		Status  string    `json:"status"`
		Exit    int       `json:"exit"`
		Started time.Time `json:"started"`
		Cmdline string    `json:"cmdline"`
	}
	app.jobs.mu.Lock()
	defer app.jobs.mu.Unlock()
	out := []jobBrief{}
	for _, j := range app.jobs.jobs {
		out = append(out, jobBrief{ID: j.ID, Name: j.Name, Status: j.Status, Exit: j.Exit, Started: j.Started, Cmdline: j.Cmdline})
	}
	apiOK(w, map[string]any{"jobs": out})
}

// GET /api/jobs/{id} — 详情含日志
func (app *App) handleJobGet(w http.ResponseWriter, r *http.Request, s *Session) {
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	app.jobs.mu.Lock()
	j := app.jobs.jobs[id]
	app.jobs.mu.Unlock()
	if j == nil {
		apiErr(w, 404, "任务不存在")
		return
	}
	since := 0
	if v := r.URL.Query().Get("since"); v != "" {
		since, _ = strconv.Atoi(v)
	}
	logs := j.tail(0)
	if since > 0 && since <= len(logs) {
		logs = logs[since:]
	} else if since > len(logs) {
		logs = nil
	}
	apiOK(w, map[string]any{
		"id": j.ID, "name": j.Name, "status": j.Status, "exit": j.Exit,
		"started": j.Started, "ended": j.Ended, "log": logs, "total": len(j.tail(0)),
	})
}
