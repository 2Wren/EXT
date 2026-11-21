package audit

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type Object = map[string]any
type Output struct {
	ID    string  `json:"id,omitempty"`
	Text  string  `json:"text"`
	Tools []*Tool `json:"tools"`
}
type Tool struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func obj(v any) Object { m, _ := v.(map[string]any); return m }
func arr(v any) []any  { a, _ := v.([]any); return a }
func str(v any) string { s, _ := v.(string); return s }
func num(v any) string { n, _ := v.(float64); return strconv.Itoa(int(n)) }
func asJSON(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func Parse(protocol string, raw []byte) Output {
	var out Output
	var text strings.Builder
	indexed := map[string]*Tool{}
	tool := func(index, id, name, args string, appendDelta bool) {
		t := indexed[index]
		if t == nil {
			t = &Tool{}
			indexed[index] = t
			out.Tools = append(out.Tools, t)
		}
		if id != "" {
			t.ID = id
		}
		if name != "" {
			t.Name = name
		}
		if args != "" {
			if appendDelta {
				t.Arguments += args
			} else {
				t.Arguments = args
			}
		}
	}
	var finalText string
	consume := func(m Object) {
		if out.ID == "" {
			out.ID = str(m["id"])
		}
		switch protocol {
		case "chat":
			for _, choice := range arr(m["choices"]) {
				c := obj(choice)
				d := obj(c["delta"])
				delta := d != nil
				if !delta {
					d = obj(c["message"])
				}
				text.WriteString(str(d["content"]))
				for i, entry := range arr(d["tool_calls"]) {
					t := obj(entry)
					fn := obj(t["function"])
					idx := strconv.Itoa(i)
					if t["index"] != nil {
						idx = num(t["index"])
					}
					tool(num(c["index"])+":"+idx, str(t["id"]), str(fn["name"]), str(fn["arguments"]), delta)
				}
			}
		case "responses", "responses-ws":
			switch str(m["type"]) {
			case "response.output_text.delta":
				text.WriteString(str(m["delta"]))
			case "response.output_item.added", "response.output_item.done":
				it := obj(m["item"])
				if str(it["type"]) == "function_call" {
					tool(str(it["id"]), str(it["call_id"]), str(it["name"]), str(it["arguments"]), false)
				}
			case "response.function_call_arguments.delta":
				tool(str(m["item_id"]), "", "", str(m["delta"]), true)
			}
			r := obj(m["response"])
			if r == nil && m["output"] != nil {
				r = m
			}
			if r != nil {
				if id := str(r["id"]); id != "" {
					out.ID = id
				}
				var terminal strings.Builder
				for _, item := range arr(r["output"]) {
					it := obj(item)
					switch str(it["type"]) {
					case "function_call":
						tool(str(it["id"]), str(it["call_id"]), str(it["name"]), str(it["arguments"]), false)
					case "message":
						for _, part := range arr(it["content"]) {
							terminal.WriteString(str(obj(part)["text"]))
						}
					}
				}
				if terminal.Len() > 0 {
					finalText = terminal.String()
				}
			}
		case "messages":
			if id := str(obj(m["message"])["id"]); id != "" {
				out.ID = id
			}
			block := obj(m["content_block"])
			idx := num(m["index"])
			if block != nil {
				text.WriteString(str(block["text"]))
				if str(block["type"]) == "tool_use" {
					tool(idx, str(block["id"]), str(block["name"]), "", false)
				}
			}
			d := obj(m["delta"])
			text.WriteString(str(d["text"]))
			if frag := str(d["partial_json"]); frag != "" {
				tool(idx, "", "", frag, true)
			}
			for i, part := range arr(m["content"]) {
				b := obj(part)
				text.WriteString(str(b["text"]))
				if str(b["type"]) == "tool_use" {
					tool(strconv.Itoa(i), str(b["id"]), str(b["name"]), asJSON(b["input"]), false)
				}
			}
		case "gemini":
			if id := str(m["responseId"]); id != "" {
				out.ID = id
			}
			for _, entry := range arr(m["candidates"]) {
				c := obj(entry)
				for i, part := range arr(obj(c["content"])["parts"]) {
					p := obj(part)
					text.WriteString(str(p["text"]))
					if fn := obj(p["functionCall"]); fn != nil {
						id := str(fn["id"])
						idx := id
						if idx == "" {
							idx = num(c["index"]) + ":" + strconv.Itoa(i) + ":" + str(fn["name"])
						}
						tool(idx, id, str(fn["name"]), asJSON(fn["args"]), false)
					}
				}
			}
		}
	}
	var one Object
	if json.Unmarshal(raw, &one) == nil {
		consume(one)
	} else {
		var event bytes.Buffer
		flush := func() {
			if event.Len() == 0 {
				return
			}
			var m Object
			if json.Unmarshal(event.Bytes(), &m) == nil {
				consume(m)
			}
			event.Reset()
		}
		for line := range bytes.SplitSeq(raw, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				flush()
				continue
			}
			if v, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				if event.Len() > 0 {
					event.WriteByte('\n')
				}
				event.Write(bytes.TrimSpace(v))
			} else if line[0] == '{' && event.Len() == 0 {
				var m Object
				if json.Unmarshal(line, &m) == nil {
					consume(m)
				}
			}
		}
		flush()
	}
	out.Text = text.String()
	if finalText != "" {
		out.Text = finalText
	}
	return out
}
