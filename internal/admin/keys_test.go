package admin

import (
	"cpa-ext/internal/billing"
	"cpa-ext/internal/cpa"
	"cpa-ext/internal/store"
	"cpa-ext/internal/usage"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestKeysAPIContract(t *testing.T) {
	body := `["sk-contract-test-secret"]`
	status := 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v8/management/config/access/api-keys" {
			t.Errorf("wrong CPA endpoint %s", r.URL.Path)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer upstream.Close()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, err := billing.New(db)
	if err != nil {
		t.Fatal(err)
	}
	collector := usage.New(db, cpa.Client{Base: upstream.URL, HTTP: upstream.Client()}, ledger)
	handler := (Service{DB: db, Ledger: ledger, Collector: collector, Password: "test-password", Assets: fstest.MapFS{}}).Handler("")
	request := func(method, path, payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(payload))
		r.SetBasicAuth("admin", "test-password")
		r.Header.Set("X-CPA-Ext-Admin", "1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/keys", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal("empty list must be array", w.Code, w.Body.String())
	}
	w = request("POST", "/api/keys/sync", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/api/keys", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var keys []map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &keys); err != nil || len(keys) != 1 {
		t.Fatal("array contract", err, w.Body.String())
	}
	id := cpa.KeyID("sk-contract-test-secret")
	if keys[0]["id"] != id || keys[0]["masked_key"] != cpa.MaskKey("sk-contract-test-secret") || keys[0]["cpa_present"] != true {
		t.Fatal("key fields", keys)
	}
	if strings.Contains(w.Body.String(), "sk-contract-test-secret") {
		t.Fatal("raw secret exposed")
	}
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"name":"Alice","enabled":true,"daily":123,"total":456}`, id))
	if w.Code != 200 {
		t.Fatal("id-only update", w.Code, w.Body.String())
	}
	w = request("PUT", "/api/keys", `{"id":"bad","enabled":true}`)
	if w.Code != 400 {
		t.Fatal("invalid id accepted", w.Code)
	}
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"enabled":true}`, strings.Repeat("f", 64)))
	if w.Code != 404 {
		t.Fatal("unknown key should not be created", w.Code)
	}
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"secret":"must-not-accept","enabled":true}`, id))
	if w.Code != 400 {
		t.Fatal("raw key input accepted", w.Code)
	}
	status = 503
	w = request("POST", "/api/keys/sync", "")
	if w.Code != 502 {
		t.Fatal("upstream failure status", w.Code)
	}
	w = request("GET", "/api/keys/sync", "")
	var syncStatus usage.KeySyncStatus
	json.Unmarshal(w.Body.Bytes(), &syncStatus)
	if w.Code != 200 || syncStatus.Count != 1 || syncStatus.Error == "" {
		t.Fatal("sync status", w.Code, syncStatus)
	}
	status = 200
	body = `[]`
	w = request("POST", "/api/keys/sync", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/api/keys", "")
	json.Unmarshal(w.Body.Bytes(), &keys)
	if len(keys) != 1 || keys[0]["cpa_present"] != false || keys[0]["name"] != "Alice" || keys[0]["daily"] != float64(123) {
		t.Fatal("deleted policy must remain", keys)
	}
	if ledger.Enabled(id) {
		t.Fatal("CPA deleted key not blocked")
	}
}
