package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	header := http.Header{}
	header.Set("Authorization", "Bearer fixture-client-secret-local-only")
	header.Set("X-Session-ID", "fixture-websocket")
	ws, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8580/v1/responses", header)
	if err != nil {
		panic(err)
	}
	defer ws.Close()
	strict := os.Getenv("EXPECT_STRICT_BLOCK") == "1"
	for turn := 1; turn <= 2; turn++ {
		req := map[string]any{
			"type":  "response.create",
			"model": "fixture-chat",
			"input": []any{map[string]any{"role": "user", "content": "bench"}},
		}
		if err := ws.WriteJSON(req); err != nil {
			panic(err)
		}
		if err := ws.SetReadDeadline(time.Now().Add(7 * time.Second)); err != nil {
			panic(err)
		}
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				panic(err)
			}
			var m map[string]any
			if err = json.Unmarshal(raw, &m); err != nil {
				panic(err)
			}
			if m["type"] == "error" {
				details, _ := m["error"].(map[string]any)
				if strict && details["code"] == "quota_bound_unavailable" {
					fmt.Println("WebSocket generation with strict key was refused before forwarding")
					return
				}
				panic(string(raw))
			}
			if m["type"] == "response.completed" {
				if strict {
					panic("strict WebSocket request unexpectedly reached upstream")
				}
				fmt.Printf("WebSocket turn %d completed\n", turn)
				break
			}
		}
	}
}
