package cpa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// Keys uses the v8 YAML-tree configuration endpoint. Raw keys stay transient.
func (c Client) Keys(ctx context.Context) ([]string, error) {
	var keys []string
	if err := c.Get(ctx, "config/access/api-keys", &keys); err != nil {
		return nil, err
	}
	if keys == nil {
		return nil, errors.New("CPA returned an unexpected API key payload")
	}
	for _, key := range keys {
		if key == "" || strings.TrimSpace(key) != key {
			return nil, errors.New("CPA returned an invalid API key")
		}
	}
	return keys, nil
}

// NameID is the identifier CPAMC files a client key's display name under. CPAMC
// keeps those names only in the browser, keyed by the SHA-256 of
// JSON.stringify(["api-key-name", apiBase, key]).
func NameID(base, key string) string {
	var text bytes.Buffer
	enc := json.NewEncoder(&text)
	enc.SetEscapeHTML(false)
	_ = enc.Encode([]string{"api-key-name", base, key})
	sum := sha256.Sum256(bytes.TrimSuffix(text.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])
}
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "********"
	}
	return key[:4] + "…" + key[len(key)-4:]
}
