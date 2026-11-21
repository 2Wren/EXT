package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cpa-ext/internal/audit"
	"cpa-ext/internal/billing"
	"cpa-ext/internal/cpa"
)

type Gateway struct {
	upstream    *url.URL
	proxy       *httputil.ReverseProxy
	ledger      *billing.Ledger
	audit       *audit.Archive
	connections atomic.Int64
	draining    chan struct{}
	drainOnce   sync.Once
}

type turnKey struct{}

func New(upstream *url.URL, ledger *billing.Ledger, archive *audit.Archive) *Gateway {
	g := &Gateway{upstream: upstream, ledger: ledger, audit: archive, draining: make(chan struct{})}
	g.proxy = &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(upstream)
			p.Out.Header.Del("X-Management-Key")
			p.Out.Header.Set("Accept-Encoding", "identity")
		},
		FlushInterval: -1,
		ModifyResponse: func(r *http.Response) error {
			t, _ := r.Request.Context().Value(turnKey{}).(*audit.Turn)
			if t == nil {
				return nil
			}
			t.Trace = r.Header.Get("X-CPA-Trace-ID")
			t.Status = r.StatusCode
			r.Body = &tap{ReadCloser: r.Body, turn: t}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			if t, _ := r.Context().Value(turnKey{}).(*audit.Turn); t != nil {
				t.Status = 502
				t.Interrupted = true
			}
			Failure(w, 502, "upstream_unavailable", "CPA connection failed")
		},
	}
	return g
}

type tap struct {
	io.ReadCloser
	turn *audit.Turn
}

func (b *tap) Read(buf []byte) (int, error) {
	n, err := b.ReadCloser.Read(buf)
	if n > 0 {
		if len(b.turn.Response) == 0 {
			b.turn.TTFB = time.Since(b.turn.Started).Microseconds()
		}
		capture(b.turn, buf[:n])
	}
	if err != nil && !errors.Is(err, io.EOF) {
		b.turn.Interrupted = true
	}
	return n, err
}

func capture(t *audit.Turn, data []byte) {
	n := min(len(data), audit.ResponseCap-len(t.Response))
	if n < len(data) {
		t.Truncated = true
	}
	t.Response = append(t.Response, data[:n]...)
}

func Failure(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message, "type": code, "code": code}})
}

func quotaError(err error) (int, string) {
	switch {
	case errors.Is(err, billing.ErrDisabled):
		return 403, "key_disabled"
	case errors.Is(err, billing.ErrExceeded):
		return 429, "quota_exhausted"
	case errors.Is(err, billing.ErrUnavailable):
		return 422, "quota_bound_unavailable"
	default:
		return 503, "quota_temporarily_unavailable"
	}
}

func identity(r *http.Request) (string, bool) {
	var key string
	auth := r.Header.Get("Authorization")
	if kind, value, ok := strings.Cut(auth, " "); ok && strings.EqualFold(kind, "Bearer") {
		auth = strings.TrimSpace(value)
	}
	q := r.URL.Query()
	candidates := append([]string{auth}, r.Header.Values("X-Goog-Api-Key")...)
	candidates = append(candidates, r.Header.Values("X-Api-Key")...)
	candidates = append(candidates, q["key"]...)
	candidates = append(candidates, q["auth_token"]...)
	if len(r.Header.Values("Authorization")) > 1 {
		return "", false
	}
	for _, v := range candidates {
		if v == "" {
			continue
		}
		if key != "" && v != key {
			return "", false
		}
		key = v
	}
	if key == "" {
		return "", false
	}
	return cpa.KeyID(key), true
}

func route(r *http.Request) (string, string) {
	path := r.URL.Path
	if r.Method == "GET" {
		switch path {
		case "/v1/models", "/v1beta/models", "/openai/v1/models":
			return "read", ""
		case "/v1/responses", "/backend-api/codex/responses":
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				return "responses-ws", ""
			}
		}
	}
	if r.Method != "POST" {
		return "", ""
	}
	switch path {
	case "/v1/chat/completions", "/openai/v1/chat/completions":
		return "chat", ""
	case "/v1/responses", "/backend-api/codex/responses":
		return "responses", ""
	case "/v1/messages":
		return "messages", ""
	case "/v1/messages/count_tokens":
		return "read", ""
	}
	if r.URL.Query().Get("alt") == "sse" || strings.HasPrefix(path, "/v1beta/models/") {
		if prefix, ok := strings.CutPrefix(path, "/v1beta/models/"); ok {
			model, action, ok := strings.Cut(prefix, ":")
			if ok && model != "" && !strings.Contains(model, "/") {
				switch action {
				case "generateContent", "streamGenerateContent":
					return "gemini", model
				case "countTokens":
					return "read", ""
				}
			}
		}
	}
	return "", ""
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	protocol, pathModel := route(r)
	if protocol == "" {
		http.NotFound(w, r)
		return
	}
	key, ok := identity(r)
	if !ok {
		Failure(w, 401, "invalid_key", "provide one valid client key")
		return
	}
	if !g.ledger.Enabled(key) {
		Failure(w, 403, "key_disabled", "key is disabled or unregistered")
		return
	}
	if protocol == "read" {
		g.proxy.ServeHTTP(w, r)
		return
	}
	if protocol == "responses-ws" {
		g.websocket(w, r, key)
		return
	}
	started := time.Now()
	if enc := r.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		Failure(w, 415, "unsupported_content_encoding", "send an uncompressed request")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, audit.RequestCap))
	if err != nil {
		var maxErr *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
		}
		Failure(w, status, "request_body_unreadable", "cannot read the request body")
		return
	}
	t, err := g.start(r, key, protocol, pathModel, started, body)
	if err != nil {
		status, code := quotaError(err)
		Failure(w, status, code, err.Error())
		return
	}
	// Counted until the turn is handed to the archive, so shutdown waits for it.
	g.connections.Add(1)
	defer g.connections.Add(-1)
	defer func() {
		t.Duration = time.Since(t.Started).Microseconds()
		if r.Context().Err() != nil {
			t.Interrupted = true
		}
		g.audit.Submit(t)
	}()
	ctx := context.WithValue(r.Context(), turnKey{}, t)
	r = r.WithContext(ctx)
	r.Body = io.NopCloser(bytes.NewReader(body))
	w.Header().Set("X-CPA-Ext-Turn", t.ID)
	g.proxy.ServeHTTP(w, r)
}

func (g *Gateway) start(r *http.Request, key, protocol, pathModel string, started time.Time, body []byte) (*audit.Turn, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, billing.ErrUnavailable
	}
	model := field(fields, "model")
	if pathModel != "" {
		model = pathModel
	}
	var output int64
	capField := map[string]string{"chat": "max_tokens", "responses": "max_output_tokens", "messages": "max_tokens", "responses-ws": "max_output_tokens"}[protocol]
	if protocol == "chat" && fields["max_completion_tokens"] != nil {
		capField = "max_completion_tokens"
	}
	strict := true
	if protocol == "gemini" {
		var config map[string]json.RawMessage
		if len(fields["generationConfig"]) > 0 && json.Unmarshal(fields["generationConfig"], &config) != nil {
			strict = false
		}
		if v := config["maxOutputTokens"]; len(v) > 0 && (json.Unmarshal(v, &output) != nil || output <= 0) {
			strict = false
		}
	} else if v := fields[capField]; len(v) > 0 && (json.Unmarshal(v, &output) != nil || output <= 0) {
		strict = false
	}
	if !g.audit.Acquire() {
		return nil, billing.ErrWarming
	}
	// The certified envelope is deliberately narrow for each protocol.
	strict = strict && protocol != "responses-ws" && safeEnvelope(protocol, fields)
	grant, err := g.ledger.Admit(key, protocol, model, output, strict, billing.Day(started))
	if err != nil {
		g.audit.Release()
		return nil, err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		panic(err)
	}
	session := ""
	for _, name := range []string{"X-Session-ID", "Session-ID", "X-Claude-Code-Session-ID", "X-Codex-Session-ID"} {
		if s := strings.TrimSpace(r.Header.Get(name)); s != "" && len(s) <= 256 {
			session = s
			break
		}
	}
	if session == "" {
		session = field(fields, "conversation_id")
	}
	if session == "" {
		session = field(fields, "prompt_cache_key")
	}
	if session == "" && protocol == "messages" {
		var m map[string]json.RawMessage
		if json.Unmarshal(fields["metadata"], &m) == nil {
			if user := field(m, "user_id"); strings.HasPrefix(user, "{") {
				var meta map[string]json.RawMessage
				if json.Unmarshal([]byte(user), &meta) == nil {
					session = field(meta, "session_id")
				}
			}
		}
	}
	return &audit.Turn{ID: hex.EncodeToString(random[:]), KeyID: key, Protocol: protocol, Model: model, Parent: field(fields, "previous_response_id"), Session: session, Started: started, Request: body, Grant: grant}, nil
}

func field(m map[string]json.RawMessage, name string) string {
	var v string
	_ = json.Unmarshal(m[name], &v)
	if len(v) > 256 {
		return ""
	}
	return v
}

func safeEnvelope(protocol string, fields map[string]json.RawMessage) bool {
	allowed := map[string]bool{"model": true, "stream": true, "temperature": true, "top_p": true}
	specific := map[string][]string{
		"chat":      {"messages", "max_tokens", "max_completion_tokens", "n", "stream_options", "stop", "tools", "tool_choice", "parallel_tool_calls", "user", "seed"},
		"responses": {"input", "instructions", "max_output_tokens", "previous_response_id", "tools", "tool_choice", "parallel_tool_calls", "store", "prompt_cache_key", "reasoning"},
		"messages":  {"messages", "system", "max_tokens", "stop_sequences", "tools", "tool_choice", "metadata", "top_k"},
		"gemini":    {"contents", "systemInstruction", "generationConfig", "tools", "toolConfig"},
	}
	for _, k := range specific[protocol] {
		allowed[k] = true
	}
	for k := range fields {
		if !allowed[k] {
			return false
		}
	}
	var n int
	if v := fields["n"]; v != nil && (json.Unmarshal(v, &n) != nil || n != 1) {
		return false
	}
	if fields["max_tokens"] != nil && fields["max_completion_tokens"] != nil {
		return false
	}
	var config map[string]json.RawMessage
	if fields["generationConfig"] != nil {
		if json.Unmarshal(fields["generationConfig"], &config) != nil {
			return false
		}
		for k := range config {
			switch k {
			case "maxOutputTokens", "temperature", "topP", "topK", "stopSequences":
			default:
				return false
			}
		}
	}
	var tools []map[string]json.RawMessage
	if v := fields["tools"]; v != nil && json.Unmarshal(v, &tools) != nil {
		return false
	}
	for _, tool := range tools {
		switch protocol {
		case "chat", "responses":
			if field(tool, "type") != "function" {
				return false
			}
		case "messages":
			if field(tool, "name") == "" || tool["input_schema"] == nil || field(tool, "type") != "" {
				return false
			}
		case "gemini":
			if tool["functionDeclarations"] == nil || len(tool) != 1 {
				return false
			}
		}
	}
	for _, key := range []string{"messages", "input", "contents", "system", "systemInstruction"} {
		var v any
		if json.Unmarshal(fields[key], &v) == nil && !textOnly(v) {
			return false
		}
	}
	return true
}

func textOnly(v any) bool {
	switch x := v.(type) {
	case []any:
		for _, part := range x {
			if !textOnly(part) {
				return false
			}
		}
	case map[string]any:
		for k, part := range x {
			switch k {
			case "image_url", "image", "audio", "input_audio", "file", "inlineData", "inline_data", "fileData", "file_data", "document", "video":
				return false
			}
			if k == "type" {
				if s, ok := part.(string); ok && strings.Contains(s, "image") {
					return false
				}
			}
			if !textOnly(part) {
				return false
			}
		}
	}
	return true
}

// Drain asks idle WebSocket sessions to end and waits for active generations.
func (g *Gateway) Drain(ctx context.Context) error {
	g.drainOnce.Do(func() { close(g.draining) })
	wait := time.NewTicker(10 * time.Millisecond)
	defer wait.Stop()
	for g.connections.Load() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait.C:
		}
	}
	return nil
}
