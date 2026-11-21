package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedAdminAuthentication(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("browser cookie reached CPA")
		}
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Password: "test-admin-password", Assets: fstest.MapFS{
		"login.html": &fstest.MapFile{Data: []byte("login")},
		"index.html": &fstest.MapFile{Data: []byte("dashboard")},
	}}
	handler := Panel(target, service.Handler(PanelPath))
	request := func(method, path, body string, adminHeader bool, cookie *http.Cookie) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "http://localhost:8482"+path, strings.NewReader(body))
		if adminHeader {
			req.Header.Set("X-CPA-Ext-Admin", "1")
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		handler.ServeHTTP(rec, req)
		return rec
	}
	rec := request("GET", "/cpa-ext/", "", false, nil)
	if rec.Body.String() != "login" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatal("embedded login unavailable")
	}
	if got := request("GET", "/cpa-ext/api/series", "", false, nil).Code; got != 401 {
		t.Fatalf("protected API status %d", got)
	}
	if got := request("POST", "/cpa-ext/api/session", `{"password":"test-admin-password"}`, false, nil).Code; got != 403 {
		t.Fatalf("missing admin header status %d", got)
	}
	if got := request("POST", "/cpa-ext/api/session", `{"password":"invalid"}`, true, nil).Code; got != 401 {
		t.Fatalf("invalid password status %d", got)
	}
	rec = request("POST", "/cpa-ext/api/session", `{"password":"test-admin-password"}`, true, nil)
	if rec.Code != 200 {
		t.Fatalf("login status %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Path != "/cpa-ext/" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("incorrect embedded cookie scope")
	}
	cookie := cookies[0]
	if body := request("GET", "/cpa-ext/", "", false, cookie).Body.String(); !strings.Contains(body, "dashboard") {
		t.Fatal("authenticated dashboard unavailable")
	}
	if body := request("GET", "/v8/management/plugins", "", false, cookie).Body.String(); body != "/v8/management/plugins" {
		t.Fatal("management routes did not reach CPA")
	}
	if got := request("GET", "/v1/models", "", false, nil).Code; got != 404 {
		t.Fatalf("published CPA inference route status %d", got)
	}
	rec = request("POST", "/cpa-ext/api/logout", "{}", true, cookie)
	if expired := rec.Result().Cookies(); len(expired) != 1 || expired[0].MaxAge >= 0 {
		t.Fatal("logout did not clear the embedded cookie")
	}
}
