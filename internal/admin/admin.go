package admin

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"math"
	"net/http"
	"strconv"
	"time"

	"cpa-ext/internal/audit"
	"cpa-ext/internal/billing"
	"cpa-ext/internal/store"
	"cpa-ext/internal/usage"
)

type Service struct {
	DB        *sql.DB
	Ledger    *billing.Ledger
	Audit     *audit.Archive
	Collector *usage.Collector
	Password  string
	Assets    fs.FS
}

func (s Service) Handler(path string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		status := s.Collector.Status()
		status["prices"] = s.Ledger.CatalogStatus()
		reply(w, 200, status)
	})
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/series", s.series)
	mux.HandleFunc("GET /api/keys", s.keys)
	mux.HandleFunc("GET /api/keys/sync", s.keySyncStatus)
	mux.HandleFunc("POST /api/keys/sync", s.syncKeys)
	mux.HandleFunc("PUT /api/keys", s.updateKey)
	mux.HandleFunc("POST /api/keys/names", s.nameKeys)
	mux.HandleFunc("GET /api/prices", func(w http.ResponseWriter, r *http.Request) {
		s.query(w, `SELECT p.*, CASE WHEN p.id=(SELECT q.id FROM prices q WHERE q.protocol=p.protocol AND q.model=p.model ORDER BY q.is_reference ASC,q.id DESC LIMIT 1) THEN 1 ELSE 0 END active FROM prices p ORDER BY is_reference ASC,id DESC LIMIT 1000`)
	})
	mux.HandleFunc("POST /api/prices", s.addPrice)
	mux.HandleFunc("GET /api/models", s.models)
	mux.HandleFunc("GET /api/sessions", s.sessions)
	mux.HandleFunc("GET /api/turns/{id}", s.turn)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/credentials", func(w http.ResponseWriter, r *http.Request) {
		credentials, err := s.Collector.Credentials(r.Context())
		if err != nil {
			reply(w, 502, map[string]string{"error": err.Error()})
			return
		}
		reply(w, 200, credentials)
	})
	mux.Handle("GET /", http.FileServerFS(s.Assets))
	sessions := sessions{digest: sha256.Sum256([]byte(s.Password)), path: path}
	static := http.FileServerFS(s.Assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		frames := "'none'"
		if path == PanelPath {
			frames = "'self'"
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors "+frames+"; base-uri 'none'; form-action 'self'")
		if (r.Method == "PUT" || r.Method == "POST") && r.Header.Get("X-CPA-Ext-Admin") != "1" {
			reply(w, 403, map[string]string{"error": "需要后台请求标识"})
			return
		}
		if r.Method == "GET" && (r.URL.Path == "/app.js" || r.URL.Path == "/app.css") {
			static.ServeHTTP(w, r)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/session" {
			sessions.login(w, r)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/logout" {
			sessions.logout(w, r)
			return
		}
		if !sessions.authorized(r) {
			if r.Method == "GET" && r.URL.Path == "/" {
				body, err := fs.ReadFile(s.Assets, "login.html")
				if err != nil {
					http.Error(w, "Login unavailable", 500)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(body)
				return
			}
			if r.Header.Get("Authorization") != "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="cpa-ext", charset="UTF-8"`)
			}
			reply(w, 401, map[string]string{"error": "登录已失效"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func reply(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s Service) query(w http.ResponseWriter, stmt string, args ...any) {
	rows, err := store.Query(s.DB, stmt, args...)
	if err != nil {
		reply(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	reply(w, 200, rows)
}

// selection reads what a list request asks for: the period [from,to) in Unix
// milliseconds, unbounded where a side is missing, and the page to return.
func selection(r *http.Request) (from, to, page, size int64) {
	q := r.URL.Query()
	number := func(name string, fallback int64) int64 {
		n, err := strconv.ParseInt(q.Get(name), 10, 64)
		if err != nil || n < 0 {
			return fallback
		}
		return n
	}
	return number("from", 0), number("to", math.MaxInt64), max(number("page", 1), 1), min(max(number("size", 50), 1), 200)
}

func (s Service) overview(w http.ResponseWriter, r *http.Request) {
	from, to, _, _ := selection(r)
	s.query(w, `SELECT model,key_id,count(*) requests,sum(input) input,sum(output) output,sum(cached) cached,
 sum(cache_write) cache_write, sum(total) total, sum(cost) cost, sum(failed) failures,
 round(avg(latency),1) latency, round(avg(CASE WHEN ttft>0 THEN ttft END),1) ttft,
 round(CASE WHEN sum(input+cached+cache_write)>0 THEN 100.0*sum(cached)/sum(input+cached+cache_write) END,2) cache_pct,
 sum(CASE WHEN cost IS NULL THEN 1 ELSE 0 END) pending
 FROM events WHERE happened>=? AND happened<? GROUP BY model,key_id ORDER BY requests DESC,model,key_id`, from, to)
}

// series totals the period per Beijing day, or per hour with unit=hour.
func (s Service) series(w http.ResponseWriter, r *http.Request) {
	from, to, _, _ := selection(r)
	bucket := "date(happened/1000,'unixepoch','+8 hours')"
	if r.URL.Query().Get("unit") == "hour" {
		bucket = "strftime('%Y-%m-%d %H:00',happened/1000,'unixepoch','+8 hours')"
	}
	s.query(w, `SELECT `+bucket+` bucket,count(*) requests,sum(total) tokens,sum(cost) cost,
 sum(input+cached+cache_write) input_tokens,sum(cached) cached,
 sum(failed) failures,sum(CASE WHEN cost IS NULL THEN 1 ELSE 0 END) pending
 FROM events WHERE happened>=? AND happened<? GROUP BY bucket ORDER BY bucket`, from, to)
}

func (s Service) keys(w http.ResponseWriter, r *http.Request) {
	rows, err := store.Query(s.DB, `SELECT k.*,coalesce(c.masked_key,'') masked_key,coalesce(c.cpa_present,0) cpa_present,
 (SELECT coalesce(sum(amount),0) FROM spend WHERE key_id=k.id) spent,
 (SELECT coalesce(sum(amount),0) FROM spend WHERE key_id=k.id AND day>=?) spent_week
 FROM keys k LEFT JOIN cpa_keys c ON c.id=k.id ORDER BY k.name,k.id`, billing.Week(time.Now()))
	if err != nil {
		reply(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	for _, row := range rows {
		row["models"] = billing.SplitModels(row["models"].(string))
		row["cpa_present"] = row["cpa_present"].(int64) != 0
	}
	reply(w, 200, rows)
}

func (s Service) updateKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID     string   `json:"id"`
		Weekly int64    `json:"weekly"`
		Total  int64    `json:"total"`
		Models []string `json:"models"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.ID) != 64 {
		reply(w, 400, map[string]string{"error": "invalid key id"})
		return
	}
	if _, err := hex.DecodeString(input.ID); err != nil {
		reply(w, 400, map[string]string{"error": "invalid key id"})
		return
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM keys WHERE id=?", input.ID).Scan(&count); err != nil {
		reply(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	if count == 0 {
		reply(w, 404, map[string]string{"error": "key not synchronized from CPA"})
		return
	}
	if err := s.Ledger.UpdateKey(billing.Key{ID: input.ID, Weekly: input.Weekly, Total: input.Total, Models: input.Models}); err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	input.Models = s.Ledger.Models(input.ID)
	reply(w, 200, input)
}

// nameKeys receives the key names CPAMC stores in the browser for one API base.
func (s Service) nameKeys(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Base  string            `json:"base"`
		Names map[string]string `json:"names"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Base == "" || len(input.Names) == 0 {
		reply(w, 400, map[string]string{"error": "base and names are required"})
		return
	}
	named, err := s.Collector.NameKeys(r.Context(), input.Base, input.Names)
	if err != nil {
		reply(w, 502, map[string]string{"error": "CPA keys unavailable"})
		return
	}
	reply(w, 200, map[string]any{"named": named})
}
func (s Service) keySyncStatus(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, s.Collector.KeySyncStatus())
}
func (s Service) syncKeys(w http.ResponseWriter, r *http.Request) {
	if err := s.Collector.SyncKeys(r.Context()); err != nil {
		reply(w, 502, s.Collector.KeySyncStatus())
		return
	}
	reply(w, 200, s.Collector.KeySyncStatus())
}

func (s Service) addPrice(w http.ResponseWriter, r *http.Request) {
	var p billing.Price
	if !decode(w, r, &p) {
		return
	}
	p.IsReference = false
	p.Source = "manual"
	p.SourceDate = time.Now().In(billing.Shanghai).Format("2006-01-02")
	p.SourceURL = ""
	if err := s.Ledger.AddPrice(p); err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 201, map[string]string{"status": "saved"})
}

func decode(w http.ResponseWriter, r *http.Request, input any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	dec.DisallowUnknownFields()
	if dec.Decode(input) != nil || dec.Decode(new(any)) != io.EOF {
		reply(w, 400, map[string]string{"error": "invalid JSON input"})
		return false
	}
	return true
}

// paged answers a list request with one page of rows next to the totals of the
// whole selection.
func (s Service) paged(w http.ResponseWriter, totals map[string]any, page, size int64, stmt string, args ...any) {
	rows, err := store.Query(s.DB, stmt+" LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		reply(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	totals["page"], totals["size"], totals["rows"] = page, size, rows
	reply(w, 200, totals)
}

// sessions lists the sessions that had a request in the period, most recently
// active first, each with the figures of the whole session. With key and
// session it lists the turns of that one session instead.
func (s Service) sessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, page, size := selection(r)
	key, session := q.Get("key"), q.Get("session")
	var total int64
	if session != "" && key != "" {
		if err := s.DB.QueryRow("SELECT count(*) FROM turns WHERE key_id=? AND session=?", key, session).Scan(&total); err != nil {
			reply(w, 500, map[string]string{"error": "database query failed"})
			return
		}
		s.paged(w, map[string]any{"total": total}, page, size, `SELECT t.*,e.cost,e.input,e.output,e.cached,e.cache_write,e.total,e.blocked
 FROM turns t LEFT JOIN events e ON e.turn_id=t.id WHERE t.key_id=? AND t.session=? ORDER BY t.started DESC,t.id DESC`, key, session)
		return
	}
	const active = `SELECT key_id,session FROM turns WHERE started>=? AND started<? AND (?='' OR key_id=?) GROUP BY key_id,session`
	args := []any{from, to, key, key}
	if err := s.DB.QueryRow("SELECT count(*) FROM ("+active+")", args...).Scan(&total); err != nil {
		reply(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	s.paged(w, map[string]any{"total": total}, page, size, `SELECT t.key_id,t.session,max(t.started) started,count(*) turns,min(t.settled) all_settled,
 sum(e.cost) cost, sum(e.total) tokens FROM turns t LEFT JOIN events e ON e.turn_id=t.id
 WHERE (t.key_id,t.session) IN (`+active+`) GROUP BY t.key_id,t.session ORDER BY started DESC,t.key_id,t.session`, args...)
}

func (s Service) turn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, err := store.Query(s.DB, `SELECT t.*,e.cost,e.input,e.output,e.cached,e.cache_write,e.total,e.blocked
 FROM turns t LEFT JOIN events e ON e.turn_id=t.id WHERE t.id=?`, id)
	if err != nil {
		reply(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	if len(rows) == 0 {
		reply(w, 404, map[string]string{"error": "turn not found"})
		return
	}
	var body json.RawMessage
	if data, err := s.Audit.Read(id); err == nil {
		body = data
	}
	reply(w, 200, map[string]any{"turn": rows[0], "body": body})
}

// events lists requests newest first, one page at a time, within the period,
// together with the totals of the whole selection.
func (s Service) events(w http.ResponseWriter, r *http.Request) {
	from, to, page, size := selection(r)
	key := r.URL.Query().Get("key")
	args := []any{from, to, key, key}
	const chosen = ` FROM events e LEFT JOIN turns t ON t.id=e.turn_id WHERE e.happened>=? AND e.happened<? AND (?='' OR e.key_id=?)`
	var total, failed, cost int64
	if err := s.DB.QueryRow(`SELECT count(*),coalesce(sum(e.failed),0),coalesce(sum(e.cost),0)`+chosen, args...).Scan(&total, &failed, &cost); err != nil {
		reply(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	s.paged(w, map[string]any{"total": total, "failed": failed, "cost": cost}, page, size, `SELECT e.id,e.happened,e.key_id,e.trace,e.request_id,e.model,e.billed_model,e.provider,e.input,e.output,e.cached,e.cache_write,e.total,
 e.cost,e.failed,e.turn_id,e.blocked,e.latency,e.ttft,e.price_id,e.approx,t.status http_status`+chosen+` ORDER BY e.happened DESC,e.id DESC`, args...)
}
