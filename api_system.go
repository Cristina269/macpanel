package main

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// GET /api/system/status — 当前快照
func (app *App) handleStatus(w http.ResponseWriter, r *http.Request, s *Session) {
	out := map[string]any{}
	if v, err := cpu.Percent(0, false); err == nil && len(v) > 0 {
		out["cpu"] = v[0]
	}
	if v, err := cpu.Percent(0, true); err == nil {
		out["cpu_cores"] = v
	}
	if n, err := cpu.Counts(true); err == nil {
		out["cpu_count"] = n
	}
	if vm, err := memGet(); err == nil {
		out["mem"] = vm
	}
	if l, err := load.Avg(); err == nil {
		out["load"] = map[string]float64{"one": l.Load1, "five": l.Load5, "fifteen": l.Load15}
	}
	up, _ := host.Uptime()
	out["uptime"] = up
	out["host"] = hostInfo()
	if du, err := disk.Usage("/"); err == nil {
		out["root_disk"] = du
	}
	out["disks"] = diskList()
	out["top_procs"] = app.sampler.topProcs
	out["time"] = time.Now().Format(time.RFC3339)
	apiOK(w, out)
}

// GET /api/system/history — 30 分钟历史曲线
func (app *App) handleHistory(w http.ResponseWriter, r *http.Request, s *Session) {
	sp := app.sampler
	apiOK(w, map[string]any{
		"interval": 5,
		"cpu":      sp.cpu.snapshot(),
		"mem_used": sp.memUsed.snapshot(),
		"net_rx":   sp.netRx.snapshot(),
		"net_tx":   sp.netTx.snapshot(),
		"disk_r":   sp.diskR.snapshot(),
		"disk_w":   sp.diskW.snapshot(),
		"load1":    sp.load1.snapshot(),
	})
}

func hostInfo() map[string]string {
	hi, _ := host.Info()
	return map[string]string{
		"hostname": hi.Hostname,
		"os":       hi.Platform + " " + hi.PlatformVersion,
		"kernel":   hi.KernelArch,
	}
}

func diskList() []map[string]any {
	out := []map[string]any{}
	parts, err := disk.Partitions(false)
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true
		du, err := disk.Usage(p.Mountpoint)
		if err != nil || du.Total == 0 {
			continue
		}
		out = append(out, map[string]any{
			"mount":     p.Mountpoint,
			"fstype":    p.Fstype,
			"device":    p.Device,
			"total":     du.Total,
			"used":      du.Used,
			"free":      du.Free,
			"used_pct":  du.UsedPercent,
		})
	}
	return out
}

// GET /api/system/ports — 监听端口 + 进程名
func (app *App) handlePorts(w http.ResponseWriter, r *http.Request, s *Session) {
	cons, err := gnet.Connections("inet")
	if err != nil {
		apiErr(w, 500, "获取连接失败: "+err.Error())
		return
	}
	type portInfo struct {
		Addr    string `json:"addr"`
		Port    int    `json:"port"`
		PID     int32  `json:"pid"`
		Process string `json:"process"`
		Fav     bool   `json:"fav"`
		Known   string `json:"known"`
	}
	fav := map[int]bool{}
	for _, p := range app.store.Cfg.FavoritePorts {
		fav[p] = true
	}
	known := map[int]string{}
	for _, k := range app.store.Cfg.KnownServices {
		known[k.Port] = k.Name
	}
	out := []portInfo{}
	seen := map[string]bool{}
	for _, c := range cons {
		if c.Status != "LISTEN" {
			continue
		}
		addr := c.Laddr.IP
		if addr == "::" || addr == "0.0.0.0" {
			addr = "*"
		}
		uniq := strconv.Itoa(int(c.Laddr.Port)) + "/" + addr
		if seen[uniq] {
			continue
		}
		seen[uniq] = true
		pi := portInfo{
			Addr:  addr,
			Port:  int(c.Laddr.Port),
			PID:   c.Pid,
			Fav:   fav[int(c.Laddr.Port)],
			Known: known[int(c.Laddr.Port)],
		}
		if c.Pid > 0 {
			if p, err := process.NewProcess(c.Pid); err == nil {
				pi.Process, _ = p.Name()
			}
		}
		out = append(out, pi)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Port < out[b].Port })
	apiOK(w, map[string]any{"ports": out})
}

// POST /api/system/ports/favorite {port, fav}
func (app *App) handlePortFavorite(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Port int  `json:"port"`
		Fav  bool `json:"fav"`
	}
	if err := readJSON(r, &req); err != nil || req.Port <= 0 {
		apiErr(w, 400, "参数错误")
		return
	}
	err := app.store.Update(func(c *Config) {
		out := c.FavoritePorts[:0]
		for _, p := range c.FavoritePorts {
			if p != req.Port {
				out = append(out, p)
			}
		}
		if req.Fav {
			out = append(out, req.Port)
		}
		c.FavoritePorts = out
	})
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	apiOK(w, nil)
}

// GET /api/system/probe?port=8080 — 健康探测
func (app *App) handleProbe(w http.ResponseWriter, r *http.Request, s *Session) {
	portStr := r.URL.Query().Get("port")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		apiErr(w, 400, "端口无效")
		return
	}
	start := time.Now()
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + portStr + "/")
	ms := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		apiOK(w, map[string]any{"http_ok": false, "detail": err.Error(), "ms": ms})
		return
	}
	resp.Body.Close()
	apiOK(w, map[string]any{"http_ok": true, "status": resp.StatusCode, "ms": ms})
}
