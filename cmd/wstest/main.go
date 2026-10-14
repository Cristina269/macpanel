// wstest — smoke test for the terminal WS (delete after use, or keep as a debug tool)
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: wstest <ws-url> <session-cookie>")
		os.Exit(1)
	}
	hdr := http.Header{"Cookie": []string{"macpanel_session=" + os.Args[2]}}
	ws, _, err := websocket.DefaultDialer.Dial(os.Args[1], hdr)
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer ws.Close()

	// set the terminal size first
	ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":120,"rows":30}`))
	// send the command
	ws.WriteMessage(websocket.TextMessage, []byte("echo macpanel-ws-ok-$((40+2))\r"))

	got := ""
	deadline := time.Now().Add(8 * time.Second)
	readErr := make(chan error, 1)
	go func() {
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			got += string(data)
			if len(got) > 200*1024 {
				return
			}
		}
	}()
	for time.Now().Before(deadline) {
		select {
		case err := <-readErr:
			fmt.Println("read closed:", err)
		case <-time.After(500 * time.Millisecond):
		}
		if len(got) > 200*1024 {
			break
		}
	}
	fmt.Println("---- got", len(got), "bytes ----")
	if contains(got, "macpanel-ws-ok-42") {
		fmt.Println("TERMINAL WS OK: echo round-trip correct, resize message accepted")
	} else {
		fmt.Println("TERMINAL WS FAIL, tail:", tail(got, 400))
		os.Exit(1)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
