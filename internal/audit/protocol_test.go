package audit

import "testing"

func TestChatAndTools(t *testing.T) {
	raw := []byte("data: {\"id\":\"resp\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one \",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"two\",\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n")
	out := Parse("chat", raw)
	if out.ID != "resp" || out.Text != "one two" || len(out.Tools) != 1 || out.Tools[0].Arguments != "{\"q\":1}" {
		t.Fatalf("incorrect transcript: %+v", out)
	}
}
