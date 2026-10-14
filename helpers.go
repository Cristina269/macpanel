package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func apiOK(w http.ResponseWriter, v map[string]any) {
	if v == nil {
		v = map[string]any{}
	}
	v["ok"] = true
	writeJSON(w, http.StatusOK, v)
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// runCmd 执行命令并返回合并输出;timeout<=0 用默认 30s
func runCmd(timeout time.Duration, name string, args ...string) (string, int) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

// clientIP 提取客户端 IP(不含端口)
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	return host
}

// ---------- AI 快照用的轻量封装 ----------

func cpuPercentQuick() (float64, error) {
	v, err := cpu.Percent(0, false)
	if err != nil || len(v) == 0 {
		return 0, err
	}
	return v[0], nil
}

func loadAvgQuick() (float64, error) {
	l, err := load.Avg()
	if err != nil {
		return 0, err
	}
	return l.Load1, nil
}

func hostUptime() (uint64, error) {
	return host.Uptime()
}

func fmt_F(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64)
}
