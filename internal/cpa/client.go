package cpa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cpa-ext/internal/store"
)

type Client struct {
	Base, Secret string
	HTTP         *http.Client
}

type Event struct {
	ID, KeyID, Trace, Request, Session, Provider, Model, BilledModel, AuthIndex, Body string
	Input, Output, Cached, CacheWrite, Total, Latency, TTFT, Happened                 int64
	Failed                                                                            bool
	Unsupported                                                                       bool
}

func KeyID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func Decode(raw json.RawMessage) (Event, error) {
	var d struct {
		APIKey        string    `json:"api_key"`
		KeyHash       string    `json:"access_token_sha256"`
		Trace         string    `json:"trace_id"`
		Request       string    `json:"request_id"`
		Execution     string    `json:"execution_id"`
		Session       string    `json:"session_id"`
		Provider      string    `json:"provider"`
		Model         string    `json:"model"`
		Alias         string    `json:"alias"`
		ResponseModel string    `json:"response_model"`
		AuthIndex     string    `json:"auth_index"`
		Tier          string    `json:"effective_service_tier"`
		Timestamp     time.Time `json:"timestamp"`
		Latency       int64     `json:"latency_ms"`
		TTFT          int64     `json:"ttft_ms"`
		Failed        bool      `json:"failed"`
		Generate      *bool     `json:"generate"`
		WebSearch     int64     `json:"web_search_requests"`
		Images        int64     `json:"image_count"`
		Tokens        struct {
			Input            int64 `json:"input_tokens"`
			Output           int64 `json:"output_tokens"`
			Cached           int64 `json:"cached_tokens"`
			CacheRead        int64 `json:"cache_read_tokens"`
			CacheReadPresent bool  `json:"cache_read_tokens_present"`
			CacheWrite       int64 `json:"cache_creation_tokens"`
			Total            int64 `json:"total_tokens"`
			ImageIn          int64 `json:"image_input_tokens"`
			ImageOut         int64 `json:"image_output_tokens"`
			AudioIn          int64 `json:"audio_input_tokens"`
			AudioOut         int64 `json:"audio_output_tokens"`
			VideoIn          int64 `json:"video_input_tokens"`
			FileIn           int64 `json:"file_input_tokens"`
		} `json:"tokens"`
		Breakdown struct {
			Version int    `json:"schema_version"`
			Quality string `json:"quality"`
			Total   int64  `json:"total_tokens"`
			Input   struct {
				Uncached int64 `json:"uncached_tokens"`
				Cached   int64 `json:"cache_read_tokens"`
				Write    int64 `json:"cache_write_tokens"`
			} `json:"input"`
			Output struct {
				Total int64 `json:"total_tokens"`
			} `json:"output"`
		} `json:"token_breakdown"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return Event{}, err
	}
	if d.Timestamp.IsZero() {
		return Event{}, errors.New("missing event timestamp")
	}
	body, err := store.RedactJSON(raw)
	if err != nil {
		return Event{}, err
	}
	key := strings.ToLower(d.KeyHash)
	if d.APIKey != "" {
		key = KeyID(d.APIKey)
	}
	if _, err := hex.DecodeString(key); err != nil || len(key) != 64 {
		key = ""
	}
	id := d.Execution
	if id == "" {
		id = KeyID(string(raw))
	}
	cached := d.Tokens.Cached
	if d.Tokens.CacheReadPresent {
		cached = d.Tokens.CacheRead
	}
	input, output := max(0, d.Tokens.Input-cached-d.Tokens.CacheWrite), d.Tokens.Output
	cacheWrite, total := d.Tokens.CacheWrite, d.Tokens.Total
	canonical := d.Breakdown.Version == 2 && d.Breakdown.Quality == "complete"
	if canonical {
		input, cached, cacheWrite = d.Breakdown.Input.Uncached, d.Breakdown.Input.Cached, d.Breakdown.Input.Write
		output, total = d.Breakdown.Output.Total, d.Breakdown.Total
	}
	if d.KeyHash != "" && d.APIKey != "" && !strings.EqualFold(d.KeyHash, key) {
		key = ""
	}
	model := d.Alias
	if model == "" {
		model = d.Model
	}
	billed := d.ResponseModel
	if billed == "" {
		billed = d.Model
	}
	return Event{ID: id, KeyID: key, Trace: d.Trace, Request: d.Request, Session: d.Session, Provider: d.Provider, Model: model, BilledModel: billed, AuthIndex: d.AuthIndex, Body: body,
		Input: input, Output: output, Cached: cached, CacheWrite: cacheWrite, Total: total, Latency: d.Latency, TTFT: d.TTFT, Happened: d.Timestamp.UnixMilli(), Failed: d.Failed,
		Unsupported: !canonical || d.WebSearch > 0 || d.Images > 0 || d.Tokens.ImageIn+d.Tokens.ImageOut+d.Tokens.AudioIn+d.Tokens.AudioOut+d.Tokens.VideoIn+d.Tokens.FileIn > 0 || (d.Tier != "" && d.Tier != "default" && d.Tier != "standard" && d.Tier != "auto")}, nil
}

func (c Client) Get(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/v8/management/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CPA management %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(result)
}
