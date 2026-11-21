package cpa

import (
	"strings"
	"testing"
)

func TestV8CanonicalAccounting(t *testing.T) {
	raw := []byte(`{"api_key":"private-test-key","trace_id":"trace","execution_id":"execution","timestamp":"2026-11-21T08:00:00Z","model":"model-a","tokens":{"input_tokens":100,"output_tokens":45,"cached_tokens":13},"token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":172,"input":{"uncached_tokens":100,"cache_read_tokens":13,"cache_write_tokens":6},"output":{"total_tokens":53}}}`)
	e, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if e.Unsupported || e.Input != 100 || e.Output != 53 || e.Cached != 13 || e.CacheWrite != 6 || e.Total != 172 || e.KeyID != KeyID("private-test-key") {
		t.Fatalf("unexpected canonical usage: %+v", e)
	}
	if strings.Contains(e.Body, "private-test-key") {
		t.Fatal("client key retained")
	}
}
