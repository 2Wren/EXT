package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"cpa-ext/internal/audit"
	"github.com/gorilla/websocket"
)

type frame struct {
	kind int
	data []byte
	err  error
}

func readFrames(c *websocket.Conn, done <-chan struct{}) <-chan frame {
	frames := make(chan frame)
	go func() {
		defer close(frames)
		for {
			kind, data, err := c.ReadMessage()
			select {
			case frames <- frame{kind, data, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return frames
}

func (g *Gateway) websocket(w http.ResponseWriter, r *http.Request, key string) {
	g.connections.Add(1)
	defer g.connections.Add(-1)
	scheme := "ws"
	if g.upstream.Scheme == "https" {
		scheme = "wss"
	}
	target := *g.upstream
	target.Scheme = scheme
	target.Path = r.URL.Path
	target.RawQuery = r.URL.RawQuery
	header := http.Header{}
	for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "OpenAI-Beta", "X-Session-ID", "Session-ID", "X-Codex-Session-ID", "User-Agent"} {
		for _, v := range r.Header.Values(name) {
			header.Add(name, v)
		}
	}
	dial := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: websocket.Subprotocols(r)}
	up, reply, err := dial.DialContext(r.Context(), target.String(), header)
	if err != nil {
		if reply != nil && reply.Body != nil {
			reply.Body.Close()
		}
		Failure(w, 502, "upstream_unavailable", "CPA WebSocket handshake failed")
		return
	}
	defer up.Close()
	protocols := []string{}
	if p := up.Subprotocol(); p != "" {
		protocols = append(protocols, p)
	}
	upgrader := websocket.Upgrader{Subprotocols: protocols}
	down, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer down.Close()
	done := make(chan struct{})
	defer close(done)
	client := readFrames(down, done)
	server := readFrames(up, done)
	var t *audit.Turn
	session := ""
	finish := func(interrupted bool) {
		if t == nil {
			return
		}
		t.Duration = time.Since(t.Started).Microseconds()
		t.Interrupted = interrupted
		g.audit.Submit(t)
		t = nil
	}
	defer finish(true)
	draining := g.draining
	for {
		select {
		case <-draining:
			// On shutdown, leave between generations; an active one finishes first.
			if t == nil {
				return
			}
			draining = nil
		case f, ok := <-client:
			if !ok || f.err != nil {
				return
			}
			var m map[string]json.RawMessage
			if f.kind != websocket.TextMessage || json.Unmarshal(f.data, &m) != nil {
				return
			}
			kind := field(m, "type")
			switch kind {
			case "response.create", "response.append":
				if t != nil {
					_ = down.WriteJSON(map[string]any{"type": "error", "status": 409, "error": map[string]any{"message": "another generation is active"}})
					continue
				}
				var err error
				t, err = g.start(r, key, "responses-ws", "", time.Now(), f.data)
				if err != nil {
					status, code := quotaError(err)
					_ = down.WriteJSON(map[string]any{"type": "error", "status": status, "error": map[string]any{"code": code, "message": err.Error()}})
					continue
				}
				if session == "" {
					session = t.Session
					if session == "" {
						session = t.ID
					}
				}
				t.Session = session
			case "response.cancel":
				if t == nil {
					continue
				}
			default:
				_ = down.WriteJSON(map[string]any{"type": "error", "status": 400, "error": map[string]any{"message": "unsupported client event"}})
				continue
			}
			_ = up.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if up.WriteMessage(f.kind, f.data) != nil {
				return
			}
		case f, ok := <-server:
			if !ok || f.err != nil {
				return
			}
			_ = down.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if down.WriteMessage(f.kind, f.data) != nil {
				return
			}
			if t == nil {
				continue
			}
			if len(t.Response) == 0 {
				t.TTFB = time.Since(t.Started).Microseconds()
			}
			capture(t, f.data)
			capture(t, []byte("\n"))
			var m map[string]json.RawMessage
			if json.Unmarshal(f.data, &m) != nil {
				continue
			}
			if trace := field(m, "trace_id"); strings.Count(trace, "-") >= 2 {
				t.Trace = trace
			}
			switch field(m, "type") {
			case "response.completed", "response.done":
				t.Status = 200
				finish(false)
			case "response.failed", "response.incomplete", "response.cancelled", "error":
				t.Status = 502
				finish(false)
			}
			if t == nil && draining == nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
