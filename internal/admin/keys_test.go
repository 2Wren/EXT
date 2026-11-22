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
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"weekly":123,"total":456}`, id))
	if w.Code != 200 {
		t.Fatal("id-only update", w.Code, w.Body.String())
	}
	w = request("PUT", "/api/keys", `{"id":"bad"}`)
	if w.Code != 400 {
		t.Fatal("invalid id accepted", w.Code)
	}
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q}`, strings.Repeat("f", 64)))
	if w.Code != 404 {
		t.Fatal("unknown key should not be created", w.Code)
	}
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"secret":"must-not-accept"}`, id))
	if w.Code != 400 {
		t.Fatal("raw key input accepted", w.Code)
	}
	for _, removed := range []string{`"name":"Alice"`, `"enabled":true`, `"daily":1`} {
		if w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,%s}`, id, removed)); w.Code != 400 {
			t.Fatal("removed field accepted", removed, w.Code)
		}
	}
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"weekly":123,"total":456,"models":["claude-opus-*"," gpt-5 ","GPT-5",""]}`, id))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"models":["claude-opus-*","gpt-5"]`) {
		t.Fatal("model restriction", w.Code, w.Body.String())
	}
	if got := ledger.Models(id); len(got) != 2 || !billing.AllowsModel(got, "devin/Claude-Opus-5") || billing.AllowsModel(got, "claude-fable-5-2") {
		t.Fatal("restriction not applied", got)
	}
	w = request("GET", "/api/keys", "")
	if !strings.Contains(w.Body.String(), `"models":["claude-opus-*","gpt-5"]`) {
		t.Fatal("restriction not listed", w.Body.String())
	}
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"weekly":123,"total":456}`, id))
	if w.Code != 200 || len(ledger.Models(id)) != 0 {
		t.Fatal("restriction not cleared", w.Code, ledger.Models(id))
	}
	w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"weekly":123,"total":456,"week_reset":"0 18:00"}`, id))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"week_reset":"0 18:00"`) {
		t.Fatal("reset update", w.Code, w.Body.String())
	}
	for _, bad := range []string{`"7 00:00"`, `"monday"`, `"1 8:00"`} {
		if w = request("PUT", "/api/keys", fmt.Sprintf(`{"id":%q,"week_reset":%s}`, id, bad)); w.Code != 400 {
			t.Fatal("invalid reset accepted", bad, w.Code)
		}
	}
	w = request("GET", "/api/keys", "")
	if !strings.Contains(w.Body.String(), `"week_reset":"0 18:00"`) || !strings.Contains(w.Body.String(), `"week_start":`) {
		t.Fatal("reset not listed", w.Body.String())
	}
	if _, err = db.Exec(`INSERT INTO events(id,key_id,trace,request_id,session,provider,model,billed_model,auth_index,input,output,cached,cache_write,total,latency,ttft,failed,happened,body,cost)
 VALUES("a",?,"","","","p","m","m","",1,1,0,0,2,5,1,0,1000,"{}",7),("b",?,"","","","p","m","m","",1,1,0,0,2,5,1,1,2000,"{}",NULL),("c",?,"","","","p","m","m","",1,1,0,0,2,5,1,0,3000,"{}",9)`, id, id, id); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Total, Failed, Cost, Page, Size int64
		Rows                            []map[string]any
	}
	for _, c := range []struct {
		query               string
		total, failed, cost int64
		ids                 string
	}{
		{"", 3, 1, 16, "cba"},
		{"?size=2", 3, 1, 16, "cb"},
		{"?size=2&page=2", 3, 1, 16, "a"},
		{"?from=2000", 2, 1, 9, "cb"},
		{"?from=1000&to=3000", 2, 1, 7, "ba"},
		{"?page=9", 3, 1, 16, ""},
	} {
		w = request("GET", "/api/events"+c.query, "")
		if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 {
			t.Fatal(c.query, w.Code, err, w.Body.String())
		}
		ids := ""
		for _, row := range list.Rows {
			ids += row["id"].(string)
		}
		if list.Total != c.total || list.Failed != c.failed || list.Cost != c.cost || ids != c.ids {
			t.Fatal("request list", c.query, w.Body.String())
		}
	}
	w = request("GET", "/api/keys", "")
	json.Unmarshal(w.Body.Bytes(), &keys)
	// Events alone are not spend; the fixture rows above were never charged.
	if keys[0]["spent"] != float64(0) || keys[0]["spent_week"] != float64(0) {
		t.Fatal("spend columns", keys)
	}
	if !strings.Contains(w.Body.String(), `"name":"`) {
		t.Fatal("name missing", w.Body.String())
	}
	w = request("GET", "/api/events?page=9", "")
	if !strings.Contains(w.Body.String(), `"rows":[]`) {
		t.Fatal("empty page must be an array", w.Body.String())
	}
	w = request("GET", "/api/overview?from=2000", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"requests":2`) {
		t.Fatal("overview period", w.Code, w.Body.String())
	}
	if w = request("GET", "/api/overview?to=1000", ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal("overview outside the period", w.Body.String())
	}
	for query, bucket := range map[string]string{"": `"bucket":"1970-01-01"`, "?unit=hour&from=1000": `"bucket":"1970-01-01 08:00"`} {
		w = request("GET", "/api/series"+query, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), bucket) || !strings.Contains(w.Body.String(), `"requests":3`) {
			t.Fatal("series", query, w.Code, w.Body.String())
		}
	}
	if _, err = db.Exec(`INSERT INTO turns(id,key_id,day,started,protocol,model,session,parent_response,response_id,trace,price_id,status,audit_state,latency,ttfb)
 VALUES('t1',?,'1970-01-01',1000,'p','m','s1','','','',0,200,'stored',1,1),('t2',?,'1970-01-01',5000,'p','m','s1','','','',0,200,'stored',1,1),
 ('t3',?,'1970-01-01',2000,'p','m','s2','','','',0,200,'stored',1,1)`, id, id, id); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		query string
		total int64
		rows  string
	}{
		{"", 2, "s1:2 s2:1 "},
		{"?size=1&page=2", 2, "s2:1 "},
		{"?from=1500&to=3000", 1, "s2:1 "},
		// A session appears in every period it was active in, with all its turns.
		{"?to=1500", 1, "s1:2 "},
		{"?from=6000", 0, ""},
		{"?key=" + id + "&session=s1", 2, "t2 t1 "},
		{"?key=" + id + "&session=s1&size=1&page=2", 2, "t1 "},
	} {
		w = request("GET", "/api/sessions"+c.query, "")
		if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 {
			t.Fatal(c.query, w.Code, err, w.Body.String())
		}
		rows := ""
		for _, row := range list.Rows {
			if row["id"] != nil {
				rows += row["id"].(string) + " "
			} else {
				rows += fmt.Sprintf("%s:%v ", row["session"], row["turns"])
			}
		}
		if list.Total != c.total || rows != c.rows {
			t.Fatal("session list", c.query, w.Body.String())
		}
	}
	names := fmt.Sprintf(`{"base":"https://cpa.example","names":{%q:" Mine ",%q:"stranger"}}`, cpa.NameID("https://cpa.example", "sk-contract-test-secret"), strings.Repeat("0", 64))
	w = request("POST", "/api/keys/names", names)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != fmt.Sprintf(`{"named":{%q:"Mine"}}`, id) {
		t.Fatal("CPAMC names", w.Code, w.Body.String())
	}
	w = request("POST", "/api/keys/names", `{"base":"https://other.example","names":{"x":"y"}}`)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"named":{}}` {
		t.Fatal("foreign names", w.Code, w.Body.String())
	}
	w = request("POST", "/api/keys/names", `{"base":"","names":{}}`)
	if w.Code != 400 {
		t.Fatal("empty names accepted", w.Code)
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
	if len(keys) != 1 || keys[0]["cpa_present"] != false || keys[0]["name"] != "Mine" || keys[0]["weekly"] != float64(123) || keys[0]["total"] != float64(456) {
		t.Fatal("deleted policy must remain", keys)
	}
	if ledger.Enabled(id) {
		t.Fatal("CPA deleted key not blocked")
	}

	const first, second = "sk-alpha-fixture-key", "sk-zulu-fixture-key"
	for _, order := range [][2]string{{second, first}, {first, second}} {
		body = fmt.Sprintf(`[%q,%q,%q]`, order[0], order[1], order[0])
		if w = request("POST", "/api/keys/sync", ""); w.Code != 200 {
			t.Fatal("sync reordered keys", w.Code, w.Body.String())
		}
		w = request("GET", "/api/keys", "")
		if err = json.Unmarshal(w.Body.Bytes(), &keys); err != nil || w.Code != 200 || len(keys) != 3 {
			t.Fatal("list reordered keys", w.Code, err, w.Body.String())
		}
		for i, key := range order {
			if keys[i]["id"] != cpa.KeyID(key) || keys[i]["cpa_present"] != true {
				t.Fatalf("position %d does not match the CPA list: %v", i, keys)
			}
		}
		if keys[2]["id"] != id || keys[2]["cpa_present"] != false {
			t.Fatal("deleted key should follow current keys", keys)
		}
	}
}
