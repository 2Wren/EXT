package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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
				panic(string(raw))
			}
			if m["type"] == "response.completed" {
				fmt.Printf("WebSocket turn %d completed\n", turn)
				break
			}
		}
	}
}
