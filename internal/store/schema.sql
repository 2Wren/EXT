PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS keys (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, enabled INTEGER NOT NULL, daily INTEGER NOT NULL, total INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS prices (
 id INTEGER PRIMARY KEY, protocol TEXT NOT NULL, model TEXT NOT NULL, billed_model TEXT NOT NULL,
 input INTEGER NOT NULL, output INTEGER NOT NULL, cached INTEGER NOT NULL, cache_write INTEGER NOT NULL,
 max_input INTEGER NOT NULL, max_output INTEGER NOT NULL, verified INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS price_lookup ON prices(protocol, model, id DESC);
CREATE TABLE IF NOT EXISTS leases (
 id INTEGER PRIMARY KEY, key_id TEXT NOT NULL REFERENCES keys(id), day TEXT NOT NULL, held INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS lease_key ON leases(key_id, day);
CREATE TABLE IF NOT EXISTS turns (
 id TEXT PRIMARY KEY, key_id TEXT NOT NULL REFERENCES keys(id), day TEXT NOT NULL, started INTEGER NOT NULL,
 protocol TEXT NOT NULL, model TEXT NOT NULL, session TEXT NOT NULL, parent_response TEXT NOT NULL,
 response_id TEXT NOT NULL, trace TEXT NOT NULL, price_id INTEGER NOT NULL, lease_id INTEGER NOT NULL,
 reserved INTEGER NOT NULL, status INTEGER NOT NULL, audit_state TEXT NOT NULL,
 latency INTEGER NOT NULL, ttfb INTEGER NOT NULL, settled INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS turn_trace ON turns(key_id,trace);
CREATE INDEX IF NOT EXISTS turn_session ON turns(key_id,session,started DESC);
CREATE INDEX IF NOT EXISTS turn_response ON turns(key_id,response_id);
CREATE TABLE IF NOT EXISTS events (
 id TEXT PRIMARY KEY, key_id TEXT NOT NULL, trace TEXT NOT NULL, request_id TEXT NOT NULL,
 session TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, billed_model TEXT NOT NULL, auth_index TEXT NOT NULL,
 input INTEGER NOT NULL, output INTEGER NOT NULL, cached INTEGER NOT NULL, cache_write INTEGER NOT NULL,
 total INTEGER NOT NULL, latency INTEGER NOT NULL, ttft INTEGER NOT NULL, failed INTEGER NOT NULL,
 happened INTEGER NOT NULL, body TEXT NOT NULL, turn_id TEXT REFERENCES turns(id), cost INTEGER, blocked TEXT
);
CREATE INDEX IF NOT EXISTS event_match ON events(key_id,trace,request_id) WHERE turn_id IS NULL AND blocked IS NULL;
CREATE INDEX IF NOT EXISTS event_time ON events(happened DESC);
CREATE TABLE IF NOT EXISTS spend (
 event_id TEXT PRIMARY KEY REFERENCES events(id), key_id TEXT NOT NULL, day TEXT NOT NULL, amount INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS spend_key ON spend(key_id, day);

CREATE TABLE IF NOT EXISTS conversation_history (
 key_id TEXT NOT NULL, protocol TEXT NOT NULL, digest TEXT NOT NULL, session TEXT NOT NULL,
 PRIMARY KEY(key_id, protocol, digest, session)
);

CREATE TABLE IF NOT EXISTS cpa_keys (id TEXT PRIMARY KEY REFERENCES keys(id), masked_key TEXT NOT NULL, cpa_present INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS cpa_keys_sync (id INTEGER PRIMARY KEY CHECK(id=1), last_success TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS price_catalog (
 model TEXT PRIMARY KEY, provider TEXT NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
 cached INTEGER NOT NULL, cache_write INTEGER NOT NULL, tiers TEXT NOT NULL, context INTEGER NOT NULL, max_output INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS price_sync (id INTEGER PRIMARY KEY CHECK(id=1), etag TEXT NOT NULL, checked TEXT NOT NULL, changed TEXT NOT NULL, error TEXT NOT NULL);

CREATE INDEX IF NOT EXISTS event_unpriced ON events(happened,id) WHERE cost IS NULL AND blocked IS NULL;
