package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// Same host, different port = same-site: the browser sends the session cookie, so the Origin check must reject it
func TestTerminalUpgraderRejectsOtherOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := wsUpgrader.Upgrade(w, r, nil); err == nil {
			c.Close()
		}
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	if c, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://127.0.0.1:1"}}); err == nil {
		c.Close()
		t.Fatal("cross-origin handshake accepted")
	}
	c, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {srv.URL}})
	if err != nil {
		t.Fatal("same-origin handshake rejected:", err)
	}
	c.Close()
}

func TestSetupRequiresToken(t *testing.T) {
	dataDir = t.TempDir()
	store, err := LoadStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, setupToken: newSetupToken(dataDir)}
	post := func(user, token string) int {
		w := httptest.NewRecorder()
		body := `{"username":"` + user + `","password":"0123456789","token":"` + token + `"}`
		app.handleSetup(w, httptest.NewRequest("POST", "/api/setup", strings.NewReader(body)))
		return w.Code
	}
	if c := post("admin", ""); c != 403 {
		t.Fatalf("missing token: got %d, want 403", c)
	}
	if c := post("admin", "wrong"); c != 403 {
		t.Fatalf("wrong token: got %d, want 403", c)
	}
	if c := post("admin", app.setupToken); c != 200 {
		t.Fatalf("valid token: got %d, want 200", c)
	}
	if c := post("evil", app.setupToken); c != 403 {
		t.Fatalf("second setup: got %d, want 403", c)
	}
	if fileExists(dataDir + "/setup-token") {
		t.Fatal("setup-token file not removed after setup")
	}
}
