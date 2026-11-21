package audit

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type message struct{ Role, Text string }

func plainContent(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []any:
		var b strings.Builder
		for _, item := range x {
			part := obj(item)
			kind := str(part["type"])
			if kind != "" && kind != "text" && kind != "input_text" && kind != "output_text" {
				return "", false
			}
			text, ok := part["text"].(string)
			if !ok {
				return "", false
			}
			b.WriteString(text)
		}
		return b.String(), true
	default:
		return "", false
	}
}

func history(protocol string, request []byte) []message {
	var body Object
	if json.Unmarshal(request, &body) != nil {
		return nil
	}
	items := arr(body["messages"])
	content := "content"
	switch protocol {
	case "responses", "responses-ws":
		items = arr(body["input"])
	case "gemini":
		items = arr(body["contents"])
		content = "parts"
	}
	if len(items) > 2048 {
		return nil
	}
	result := make([]message, 0, len(items))
	for _, item := range items {
		m := obj(item)
		role := str(m["role"])
		if role == "model" {
			role = "assistant"
		}
		if role != "user" && role != "assistant" && role != "developer" && role != "system" {
			return nil
		}
		if m["tool_calls"] != nil {
			return nil
		}
		text, ok := plainContent(m[content])
		if !ok {
			return nil
		}
		result = append(result, message{role, text})
	}
	return result
}

func historyDigest(items []message) string {
	raw, _ := json.Marshal(items)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (a *Archive) resolveHistory(t *Turn, items []message) (string, error) {
	// A matching prefix must include a previous assistant response.
	if len(items) < 3 || items[len(items)-1].Role != "user" || items[len(items)-2].Role != "assistant" {
		return "", nil
	}
	hash := historyDigest(items[:len(items)-1])
	var count int
	var session sql.NullString
	err := a.db.QueryRow("SELECT count(DISTINCT session),min(session) FROM conversation_history WHERE key_id=? AND protocol=? AND digest=?", t.KeyID, t.Protocol, hash).Scan(&count, &session)
	if err != nil || count != 1 {
		return "", err
	}
	return session.String, nil
}
