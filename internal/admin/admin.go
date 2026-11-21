package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"cpa-ext/internal/audit"
	"cpa-ext/internal/billing"
	"cpa-ext/internal/cpa"
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

func (s Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, s.Collector.Status()) })
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/keys", s.keys)
	mux.HandleFunc("PUT /api/keys", s.updateKey)
	mux.HandleFunc("GET /api/prices", func(w http.ResponseWriter, r *http.Request) {
		s.query(w, "SELECT * FROM prices ORDER BY id DESC LIMIT 200")
	})
	mux.HandleFunc("POST /api/prices", s.addPrice)
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
	digest := sha256.Sum256([]byte(s.Password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		name, password, ok := r.BasicAuth()
		sent := sha256.Sum256([]byte(password))
		if !ok || name != "admin" || subtle.ConstantTimeCompare(sent[:], digest[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="cpa-ext", charset="UTF-8"`)
			http.Error(w, "Authentication required", 401)
			return
		}
		if (r.Method == "PUT" || r.Method == "POST") && r.Header.Get("X-CPA-Ext-Admin") != "1" {
			reply(w, 403, map[string]string{"error": "admin request header required"})
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

func (s Service) overview(w http.ResponseWriter, r *http.Request) {
	since := time.Now().AddDate(0, 0, -30).UnixMilli()
	s.query(w, `SELECT model,key_id,count(*) requests,sum(input) input,sum(output) output,sum(cached) cached,
 sum(cache_write) cache_write, sum(total) total, sum(cost) cost, sum(failed) failures,
 round(avg(latency),1) latency, round(avg(CASE WHEN ttft>0 THEN ttft END),1) ttft,
 round(CASE WHEN sum(input+cached+cache_write)>0 THEN 100.0*sum(cached)/sum(input+cached+cache_write) END,2) cache_pct,
 sum(CASE WHEN cost IS NULL THEN 1 ELSE 0 END) pending
 FROM events WHERE happened>=? GROUP BY model,key_id ORDER BY requests DESC LIMIT 500`, since)
}

func (s Service) keys(w http.ResponseWriter, r *http.Request) {
	rows, err := store.Query(s.DB, `SELECT k.*,
 (SELECT coalesce(sum(amount),0) FROM spend WHERE key_id=k.id) spent,
 (SELECT coalesce(sum(amount),0) FROM spend WHERE key_id=k.id AND day=?) spent_today,
 (SELECT coalesce(sum(held),0) FROM leases WHERE key_id=k.id) held,
 (SELECT coalesce(sum(held),0) FROM leases WHERE key_id=k.id AND day=?) held_today
 FROM keys k ORDER BY name,id`, billing.Day(time.Now()), billing.Day(time.Now()))
	if err != nil {
		reply(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	for _, row := range rows {
		available, inFlight := s.Ledger.Snapshot(row["id"].(string))
		row["admission_capacity"] = available
		row["in_flight"] = inFlight
		row["reserved_unresolved"] = max(0, row["held"].(int64)-inFlight-available)
	}
	reply(w, 200, rows)
}

func (s Service) updateKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		billing.Key
		Secret string `json:"secret"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Secret != "" {
		input.ID = cpa.KeyID(input.Secret)
	}
	if _, err := hex.DecodeString(input.ID); err != nil {
		reply(w, 400, map[string]string{"error": "invalid key id"})
		return
	}
	if err := s.Ledger.UpdateKey(input.Key); err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	reply(w, 200, input.Key)
}

func (s Service) addPrice(w http.ResponseWriter, r *http.Request) {
	var p billing.Price
	if !decode(w, r, &p) {
		return
	}
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

func page(r *http.Request) int64 {
	n, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if err != nil || n <= 0 {
		return time.Now().Add(time.Hour).UnixMilli()
	}
	return n
}
func (s Service) sessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("session") != "" && q.Get("key") != "" {
		s.query(w, `SELECT t.*,e.cost,e.input,e.output,e.cached,e.cache_write,e.total,e.blocked FROM turns t LEFT JOIN events e ON e.turn_id=t.id WHERE t.key_id=? AND t.session=? AND t.started<? ORDER BY t.started DESC LIMIT 100`, q.Get("key"), q.Get("session"), page(r))
		return
	}
	s.query(w, `SELECT t.key_id,t.session,max(t.started) started,count(*) turns,min(t.settled) all_settled,
 sum(e.cost) cost, sum(e.total) tokens FROM turns t LEFT JOIN events e ON e.turn_id=t.id
 WHERE (?='' OR t.key_id=?) GROUP BY t.key_id,t.session HAVING max(t.started)<? ORDER BY started DESC LIMIT 100`, q.Get("key"), q.Get("key"), page(r))
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

func (s Service) events(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	s.query(w, `SELECT e.id,e.happened,e.key_id,e.trace,e.request_id,e.model,e.billed_model,e.provider,e.input,e.output,e.cached,e.cache_write,e.total,
 e.cost,e.failed,e.turn_id,e.blocked,e.latency,e.ttft FROM events e WHERE e.happened<? AND (?='' OR e.key_id=?)
 ORDER BY e.happened DESC LIMIT 100`, page(r), key, key)
}
