package admin

import (
	"cpa-ext/internal/store"
	"net/http"
)

func (s Service) models(w http.ResponseWriter, r *http.Request) {
	ids, err := s.Collector.Models(r.Context())
	if err != nil {
		reply(w, 502, map[string]string{"error": err.Error()})
		return
	}
	rows, err := store.Query(s.DB, `SELECT p.model,p.protocol,p.is_reference FROM prices p WHERE p.id=(SELECT q.id FROM prices q WHERE q.model=p.model AND q.protocol=p.protocol ORDER BY q.is_reference ASC,q.id DESC LIMIT 1)`)
	if err != nil {
		reply(w, 500, map[string]string{"error": "model price lookup failed"})
		return
	}
	out := []map[string]any{}
	for _, id := range ids {
		protocols := []string{}
		reference := true
		for _, p := range rows {
			if p["model"] == id {
				protocols = append(protocols, p["protocol"].(string))
				if p["is_reference"].(int64) == 0 {
					reference = false
				}
			}
		}
		out = append(out, map[string]any{"id": id, "priced": len(protocols) > 0, "reference": len(protocols) > 0 && reference, "protocols": protocols})
	}
	reply(w, 200, out)
}
