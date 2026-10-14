package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var auditMu sync.Mutex

// audit 追加审计日志(JSONL)
func (app *App) audit(event, detail string) {
	entry := map[string]any{
		"time":   time.Now().Format(time.RFC3339),
		"event":  event,
		"detail": detail,
	}
	b, _ := json.Marshal(entry)
	auditMu.Lock()
	defer auditMu.Unlock()
	f, err := os.OpenFile(filepath.Join(dataDir, "audit.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, string(b))
}

// GET /api/audit — 最近审计日志(倒序,限 500 条)
func (app *App) handleAudit(w http.ResponseWriter, r *http.Request, s *Session) {
	b, err := os.ReadFile(filepath.Join(dataDir, "audit.log"))
	if err != nil {
		apiOK(w, map[string]any{"entries": []string{}})
		return
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 500 {
		lines = lines[len(lines)-500:]
	}
	// 倒序
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	apiOK(w, map[string]any{"entries": lines})
}
