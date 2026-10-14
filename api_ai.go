package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// GET /api/ai/settings — key masked
func (app *App) handleAIGet(w http.ResponseWriter, r *http.Request, s *Session) {
	var ai AISettings
	app.store.Read(func(c *Config) { ai = c.AI })
	masked := ""
	if len(ai.Key) > 8 {
		masked = ai.Key[:4] + "****" + ai.Key[len(ai.Key)-4:]
	}
	apiOK(w, map[string]any{
		"base_url": ai.BaseURL, "model": ai.Model, "key_set": ai.Key != "", "key_masked": masked,
	})
}

// POST /api/ai/settings {base_url, model, key (empty = keep unchanged)}
func (app *App) handleAISet(w http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		Key     string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	err := app.store.Update(func(c *Config) {
		if strings.TrimSpace(req.BaseURL) != "" {
			c.AI.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
		}
		if strings.TrimSpace(req.Model) != "" {
			c.AI.Model = strings.TrimSpace(req.Model)
		}
		if req.Key != "" {
			c.AI.Key = strings.TrimSpace(req.Key)
		}
	})
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	app.audit("ai_settings", "更新 AI 配置")
	apiOK(w, nil)
}

// POST /api/ai/chat {messages:[{role, content}], diagnose:bool} — SSE streaming response
func (app *App) handleAIChat(w http.ResponseWriter, r *http.Request, s *Session) {
	var ai AISettings
	app.store.Read(func(c *Config) { ai = c.AI })
	if ai.Key == "" {
		apiErr(w, 400, "未配置 AI API Key,请到 设置 → AI 配置")
		return
	}
	var req struct {
		Messages []map[string]string `json:"messages"`
		Diagnose bool                `json:"diagnose"`
	}
	if err := readJSON(r, &req); err != nil {
		apiErr(w, 400, "参数错误")
		return
	}
	msgs := req.Messages
	if req.Diagnose {
		snapshot := app.aiSystemSnapshot()
		sys := map[string]string{
			"role":    "system",
			"content": "你是 MacPanel 内置的系统运维助手。以下是这台 macOS 服务器的实时状态快照(JSON),请据此回答用户的运维问题,用中文:\n" + snapshot,
		}
		msgs = append([]map[string]string{sys}, msgs...)
	}
	if len(msgs) == 0 {
		apiErr(w, 400, "消息为空")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"model":    ai.Model,
		"messages": msgs,
		"stream":   true,
	})
	base := ai.BaseURL
	if base == "" {
		base = "https://api.deepseek.com"
	}
	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Authorization", "Bearer "+ai.Key)
	client := &http.Client{Timeout: 5 * time.Minute}
	upstream, err := client.Do(proxyReq)
	if err != nil {
		apiErr(w, 502, "上游请求失败: "+err.Error())
		return
	}
	defer upstream.Body.Close()
	if upstream.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(upstream.Body, 4096))
		apiErr(w, upstream.StatusCode, "上游错误: "+string(b))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 8192)
	for {
		n, err := upstream.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// aiSystemSnapshot builds a system status summary for the AI
func (app *App) aiSystemSnapshot() string {
	var b strings.Builder
	b.WriteString("{")
	if v, err := cpuPercentQuick(); err == nil {
		b.WriteString("\"cpu_percent\":" + fmt_F(v) + ",")
	}
	if vm, err := memGet(); err == nil {
		b.WriteString("\"mem\":{\"total_mb\":" + itoa(int(vm.Total/1024/1024)) +
			",\"used_mb\":" + itoa(int(vm.Used/1024/1024)) + "},")
	}
	if l, err := loadAvgQuick(); err == nil {
		b.WriteString("\"load1\":" + fmt_F(l) + ",")
	}
	up, _ := hostUptime()
	b.WriteString("\"uptime_sec\":" + itoa(int(up)) + ",")
	b.WriteString("\"sites\":")
	app.store.Read(func(c *Config) {
		sb, _ := json.Marshal(c.Sites)
		b.Write(sb)
		b.WriteString(",")
		b.WriteString("\"monitors\":")
		mb, _ := json.Marshal(c.Monitors)
		b.Write(mb)
	})
	b.WriteString("}")
	return b.String()
}
