package usage

import (
	"context"
	"cpa-ext/internal/cpa"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsOnlyUsesReadEndpoints(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Errorf("unexpected write %s", r.Method)
		}
		switch r.URL.Path {
		case "/v8/management/config/access/api-keys":
			if r.Header.Get("Authorization") != "Bearer management" {
				t.Error("management auth")
			}
			w.Write([]byte(`["client-secret"]`))
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer client-secret" {
				t.Error("client auth")
			}
			w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o"},{"id":"unknown"}]}`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &Collector{cpa: cpa.Client{Base: srv.URL, Secret: "management", HTTP: srv.Client()}}
	models, err := c.Models(context.Background())
	if err != nil || len(models) != 2 || models[0] != "gpt-4o" || calls != 2 {
		t.Fatalf("%v %v calls=%d", models, err, calls)
	}
}
func TestModelsNoKeyIsExplicit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) }))
	defer srv.Close()
	c := &Collector{cpa: cpa.Client{Base: srv.URL, HTTP: srv.Client()}}
	if _, err := c.Models(context.Background()); err == nil {
		t.Fatal("empty keys accepted")
	}
}
