package cpa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNameIDMatchesCPAMC(t *testing.T) {
	if got := NameID("https://cpa.example", "sk-contract-test-secret"); got != "d215d5f037ee8efda6808a68ecb569faa7cdd3fe5852386bdddc09b833530327" {
		t.Fatal(got)
	}
}

func TestKeysV8Contract(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{{`[]`, true}, {`["sk-example-key"]`, true}, {`null`, false}, {`{"api-keys":[]}`, false}, {`[""]`, false}, {`[" key "]`, false}} {
		t.Run(tc.body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v8/management/config/access/api-keys" {
					t.Errorf("wrong path %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer admin-test" {
					t.Error("missing authorization")
				}
				w.Write([]byte(tc.body))
			}))
			defer s.Close()
			keys, err := (Client{Base: s.URL, Secret: "admin-test", HTTP: s.Client()}).Keys(context.Background())
			if (err == nil) != tc.ok {
				t.Fatalf("keys=%v err=%v", keys, err)
			}
		})
	}
	if MaskKey("short") != "********" || MaskKey("sk-example-key") != "sk-e…-key" {
		t.Fatal("masking")
	}
}
