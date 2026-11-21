package audit

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"syscall"
	"time"

	"cpa-ext/internal/billing"
)

const RequestCap = 1 << 20
const ResponseCap = 4 << 20
const slots = 16

var validID = regexp.MustCompile("^[a-f0-9]{32}$")

type Turn struct {
	ID, KeyID, Protocol, Model, Session, Parent, Trace string
	Started                                            time.Time
	Duration, TTFB                                     int64
	Status                                             int
	Grant                                              billing.Grant
	Request, Response                                  []byte
	Truncated, Interrupted                             bool
}

type Archive struct {
	db             *sql.DB
	dir            string
	jobs           chan *Turn
	free           chan struct{}
	done           chan struct{}
	storage        atomic.Bool
	retain         time.Duration
	limit, reserve int64
	used           int64
}

func New(db *sql.DB, root string, retain time.Duration, limit, reserve int64) (*Archive, error) {
	dir := filepath.Join(root, "bodies")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	a := &Archive{db: db, dir: dir, jobs: make(chan *Turn, slots), free: make(chan struct{}, slots), done: make(chan struct{}), retain: retain, limit: limit, reserve: reserve}
	if err := a.Prune(time.Now()); err != nil {
		return nil, err
	}
	go a.run()
	return a, nil
}

func (a *Archive) Acquire() bool {
	if !a.storage.Load() {
		return false
	}
	select {
	case a.free <- struct{}{}:
		return true
	default:
		return false
	}
}

func (a *Archive) Release()       { <-a.free }
func (a *Archive) Submit(t *Turn) { a.jobs <- t }
func (a *Archive) Close()         { close(a.jobs); <-a.done }

func (a *Archive) run() {
	defer close(a.done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case t, ok := <-a.jobs:
			if !ok {
				return
			}
			if err := a.save(t); err != nil {
				a.storage.Store(false)
				log.Printf("save audit %s: %v", t.ID, err)
			}
			a.Release()
		case now := <-ticker.C:
			if err := a.Prune(now); err != nil {
				a.storage.Store(false)
				log.Printf("audit retention: %v", err)
			}
		}
	}
}

func (a *Archive) save(t *Turn) error {
	state := "complete"
	if t.Interrupted {
		state = "interrupted"
	}
	if t.Truncated {
		state = "truncated"
	}
	response := Parse(t.Protocol, t.Response)
	if t.Session == "" && t.Parent != "" {
		err := a.db.QueryRow("SELECT session FROM turns WHERE key_id=? AND response_id=? ORDER BY started DESC LIMIT 1", t.KeyID, t.Parent).Scan(&t.Session)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	items := history(t.Protocol, t.Request)
	if t.Session == "" {
		var err error
		t.Session, err = a.resolveHistory(t, items)
		if err != nil {
			return err
		}
	}
	if t.Session == "" {
		t.Session = t.ID
	}
	capture := struct {
		Request  []byte `json:"request_raw"`
		Response []byte `json:"response_raw"`
		Output   Output `json:"output"`
	}{t.Request, t.Response, response}
	if err := a.write(t.ID, capture); err != nil {
		state = "missing"
		log.Printf("archive write %s: %v", t.ID, err)
		a.storage.Store(false)
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO turns(id,key_id,day,started,protocol,model,session,parent_response,response_id,trace,price_id,lease_id,reserved,status,audit_state,latency,ttfb)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, t.ID, t.KeyID, billing.Day(t.Started), t.Started.UnixMilli(), t.Protocol, t.Model, t.Session, t.Parent, response.ID, t.Trace, t.Grant.PriceID, t.Grant.LeaseID, t.Grant.Amount, t.Status, state, t.Duration, t.TTFB)
	if err != nil {
		return err
	}
	if t.Status >= 200 && t.Status < 300 && !t.Interrupted && !t.Truncated && len(items) > 0 && response.Text != "" && len(response.Tools) == 0 {
		digest := historyDigest(append(items, message{"assistant", response.Text}))
		if _, err = tx.Exec("INSERT OR IGNORE INTO conversation_history VALUES(?,?,?,?)", t.KeyID, t.Protocol, digest, t.Session); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *Archive) write(id string, data any) error {
	path := filepath.Join(a.dir, id+".json.gz")
	tmp, err := os.CreateTemp(a.dir, ".turn-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	z := gzip.NewWriter(tmp)
	err = json.NewEncoder(z).Encode(data)
	err = errors.Join(err, z.Close())
	if err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err != nil {
		return err
	}
	info, err := os.Stat(tmp.Name())
	if err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	a.used += info.Size()
	if err = a.checkStorage(); err != nil {
		a.storage.Store(false)
	}
	return nil
}

func (a *Archive) Read(id string) ([]byte, error) {
	if !validID.MatchString(id) {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(a.dir, id+".json.gz"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	return io.ReadAll(io.LimitReader(z, 32<<20))
}

func (a *Archive) Prune(now time.Time) error {
	all, err := os.ReadDir(a.dir)
	if err != nil {
		return err
	}
	var size int64
	for _, e := range all {
		if e.IsDir() {
			continue
		}
		stat, err := e.Info()
		if err != nil {
			return err
		}
		name := e.Name()
		if filepath.Ext(name) == ".gz" && validID.MatchString(bytesToID(name)) && now.Sub(stat.ModTime()) > a.retain {
			if err = os.Remove(filepath.Join(a.dir, name)); err != nil {
				return err
			}
			if _, err = a.db.Exec("UPDATE turns SET audit_state='expired' WHERE id=?", bytesToID(name)); err != nil {
				return err
			}
			continue
		}
		size += stat.Size()
	}
	a.used = size
	return a.checkStorage()
}

func (a *Archive) checkStorage() error {
	var disk syscall.Statfs_t
	if err := syscall.Statfs(a.dir, &disk); err != nil {
		return err
	}
	a.storage.Store(a.used+(slots*(RequestCap+ResponseCap)*3) < a.limit && int64(disk.Bavail)*int64(disk.Bsize) > a.reserve+slots*(RequestCap+ResponseCap)*3)
	return nil
}

func bytesToID(name string) string {
	return string(bytes.TrimSuffix([]byte(name), []byte(".json.gz")))
}
