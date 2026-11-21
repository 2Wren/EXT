package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"time"
)

const sessionLifetime = 8 * time.Hour

type sessions struct {
	digest [32]byte
	path   string
}

func (s sessions) cookieName() string {
	if s.path == "" {
		return "cpax_session"
	}
	return "cpax_panel_session"
}

func (s sessions) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, s.digest[:])
	h.Write([]byte("cpa-ext-admin-session:" + s.path))
	h.Write(payload)
	return h.Sum(nil)
}

func (s sessions) authorized(r *http.Request) bool {
	if name, password, ok := r.BasicAuth(); ok && name == "admin" {
		digest := sha256.Sum256([]byte(password))
		if hmac.Equal(digest[:], s.digest[:]) {
			return true
		}
	}
	cookie, err := r.Cookie(s.cookieName())
	if err != nil {
		return false
	}
	data, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(data) != 56 {
		return false
	}
	payload := data[:24]
	return hmac.Equal(data[24:], s.mac(payload)) && time.Now().Unix() < int64(binary.BigEndian.Uint64(payload[:8]))
}

func (s sessions) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	digest := sha256.Sum256([]byte(input.Password))
	if !hmac.Equal(digest[:], s.digest[:]) {
		reply(w, 401, map[string]string{"error": "密码不正确"})
		return
	}
	var payload [24]byte
	expiry := time.Now().Add(sessionLifetime)
	binary.BigEndian.PutUint64(payload[:8], uint64(expiry.Unix()))
	if _, err := rand.Read(payload[8:]); err != nil {
		reply(w, 500, map[string]string{"error": "无法建立会话"})
		return
	}
	s.setCookie(w, r, base64.RawURLEncoding.EncodeToString(append(payload[:], s.mac(payload[:])...)), int(sessionLifetime.Seconds()))
	reply(w, 200, map[string]bool{"authenticated": true})
}

func (s sessions) logout(w http.ResponseWriter, r *http.Request) {
	s.setCookie(w, r, "", -1)
	reply(w, 200, map[string]bool{"authenticated": false})
}
func (s sessions) setCookie(w http.ResponseWriter, r *http.Request, value string, age int) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: value, Path: s.path + "/", MaxAge: age,
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
}
