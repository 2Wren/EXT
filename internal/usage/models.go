package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Models only reads the model catalog. Client secrets never leave the backend.
func (c *Collector) Models(ctx context.Context) ([]string, error) {
	keys, err := c.cpa.Keys(ctx)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, errors.New("请先在 CPA 创建客户端 Key，再读取可用模型")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cpa.Base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+keys[0])
	resp, err := c.cpa.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("CPA model catalog: status %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if result.Data == nil {
		return nil, errors.New("CPA returned an unexpected model catalog")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, m := range result.Data {
		if m.ID != "" && !seen[m.ID] {
			ids = append(ids, m.ID)
			seen[m.ID] = true
		}
	}
	return ids, nil
}
