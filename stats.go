package main

import (
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

const ringSize = 900 // 5s × 900 = 75 分钟历史

type ring struct {
	mu   sync.Mutex
	buf  []float64
	idx  int
	full bool
}

func newRing() *ring { return &ring{buf: make([]float64, ringSize)} }

func (r *ring) push(v float64) {
	r.mu.Lock()
	r.buf[r.idx] = v
	r.idx = (r.idx + 1) % ringSize
	if r.idx == 0 {
		r.full = true
	}
	r.mu.Unlock()
}

func (r *ring) snapshot() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := ringSize
	if !r.full {
		n = r.idx
	}
	out := make([]float64, n)
	if r.full {
		// 已回绕:从 idx(最旧)拼接到 idx-1(最新),两段拷贝避免越界
		copy(out, r.buf[r.idx:])
		copy(out[ringSize-r.idx:], r.buf[:r.idx])
	} else {
		copy(out, r.buf[:n])
	}
	return out
}

type Sampler struct {
	cpu, memUsed, memAvail, netRx, netTx, diskR, diskW, load1 *ring

	procMu    sync.Mutex
	procTimes map[int32]float64
	topProcs  []ProcInfo
	procCount int
	lastProc  time.Time
}

type ProcInfo struct {
	PID    int32   `json:"pid"`
	Name   string  `json:"name"`
	CPUPct float64 `json:"cpu"`
	MemMB  float64 `json:"mem_mb"`
}

func NewSampler() *Sampler {
	return &Sampler{
		cpu: newRing(), memUsed: newRing(), memAvail: newRing(),
		netRx: newRing(), netTx: newRing(),
		diskR: newRing(), diskW: newRing(), load1: newRing(),
		procTimes: map[int32]float64{},
	}
}

func (s *Sampler) Start(every time.Duration) {
	s.sampleOnce() // 先采一次,cpu.Percent(0) 首调无意义但初始化内部状态
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for range t.C {
			s.sampleOnce()
		}
	}()
}

func (s *Sampler) sampleOnce() {
	if v, err := cpu.Percent(0, false); err == nil && len(v) > 0 {
		s.cpu.push(v[0])
	}
	if vm, err := memGet(); err == nil {
		s.memUsed.push(float64(vm.Used))
		s.memAvail.push(float64(vm.Available))
	}
	if rx, tx, err := netRates(); err == nil {
		s.netRx.push(rx)
		s.netTx.push(tx)
	}
	if r, w, err := diskRates(); err == nil {
		s.diskR.push(r)
		s.diskW.push(w)
	}
	if l, err := load.Avg(); err == nil {
		s.load1.push(l.Load1)
	}
	// 进程级采样 syscall 开销大,每 2 个 tick(≈10s)跑一次
	s.procCount++
	if s.lastProc.IsZero() || s.procCount%2 == 0 {
		iv := time.Since(s.lastProc).Seconds()
		if iv < 1 {
			iv = 5
		}
		s.sampleProcs(iv)
		s.lastProc = time.Now()
	}
}

func (s *Sampler) sampleProcs(interval float64) {
	procs, err := process.Processes()
	if err != nil {
		return
	}
	now := map[int32]float64{}
	type cand struct {
		ProcInfo
	}
	list := make([]ProcInfo, 0, len(procs))
	for _, p := range procs {
		t, err := p.Times()
		if err != nil {
			continue
		}
		total := t.User + t.System
		now[p.Pid] = total
		name, _ := p.Name()
		memMB := 0.0
		if mi, err := p.MemoryInfo(); err == nil {
			memMB = float64(mi.RSS) / 1024 / 1024
		}
		list = append(list, ProcInfo{PID: p.Pid, Name: name, MemMB: memMB})
	}
	s.procMu.Lock()
	prev := s.procTimes
	s.procTimes = now
	top := make([]ProcInfo, 0, 32)
	for i := range list {
		pid := list[i].PID
		if pt, ok := prev[pid]; ok {
			list[i].CPUPct = (now[pid] - pt) / interval * 100
		}
		if list[i].CPUPct > 0.1 || list[i].MemMB > 50 {
			top = append(top, list[i])
		}
	}
	s.procMu.Unlock()
	sort.Slice(top, func(a, b int) bool { return top[a].CPUPct > top[b].CPUPct })
	if len(top) > 15 {
		top = top[:15]
	}
	s.topProcs = top
}

// ---------- 采集辅助(gopsutil 的 macOS 兼容封装) ----------

type memInfo struct {
	Total     uint64
	Used      uint64
	Available uint64
	SwapTotal uint64
	SwapUsed  uint64
}

func memGet() (memInfo, error) {
	vm, err := mem.VirtualMemory()
	out := memInfo{}
	if err != nil {
		return out, err
	}
	out.Total, out.Used, out.Available = vm.Total, vm.Used, vm.Available
	if sw, err := mem.SwapMemory(); err == nil {
		out.SwapTotal, out.SwapUsed = sw.Total, sw.Used
	}
	return out, nil
}

var lastNet gnet.IOCountersStat
var lastNetTime time.Time
var netMu sync.Mutex

func netRates() (rx, tx float64, err error) {
	netMu.Lock()
	defer netMu.Unlock()
	counters, err := gnet.IOCounters(true)
	if err != nil {
		return 0, 0, err
	}
	var agg gnet.IOCountersStat
	for _, c := range counters {
		if c.Name == "lo0" || c.Name == "lo" {
			continue
		}
		agg.BytesRecv += c.BytesRecv
		agg.BytesSent += c.BytesSent
	}
	if lastNetTime.IsZero() {
		lastNet, lastNetTime = agg, time.Now()
		return 0, 0, nil
	}
	dt := time.Since(lastNetTime).Seconds()
	if dt <= 0 || agg.BytesRecv < lastNet.BytesRecv {
		lastNet, lastNetTime = agg, time.Now()
		return 0, 0, nil
	}
	rx = float64(agg.BytesRecv-lastNet.BytesRecv) / dt
	tx = float64(agg.BytesSent-lastNet.BytesSent) / dt
	lastNet, lastNetTime = agg, time.Now()
	return rx, tx, nil
}

var lastDisk disk.IOCountersStat
var lastDiskTime time.Time
var diskMu sync.Mutex

func diskRates() (r, w float64, err error) {
	diskMu.Lock()
	defer diskMu.Unlock()
	counters, err := disk.IOCounters()
	if err != nil {
		return 0, 0, err
	}
	var agg disk.IOCountersStat
	for name, c := range counters {
		_ = name
		agg.ReadBytes += c.ReadBytes
		agg.WriteBytes += c.WriteBytes
	}
	if lastDiskTime.IsZero() {
		lastDisk, lastDiskTime = agg, time.Now()
		return 0, 0, nil
	}
	dt := time.Since(lastDiskTime).Seconds()
	if dt <= 0 || agg.ReadBytes < lastDisk.ReadBytes {
		lastDisk, lastDiskTime = agg, time.Now()
		return 0, 0, nil
	}
	r = float64(agg.ReadBytes-lastDisk.ReadBytes) / dt
	w = float64(agg.WriteBytes-lastDisk.WriteBytes) / dt
	lastDisk, lastDiskTime = agg, time.Now()
	return r, w, nil
}
