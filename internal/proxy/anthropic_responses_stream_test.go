package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type anthropicStreamEvent struct {
	Type         string         `json:"type"`
	Index        int            `json:"index"`
	Message      map[string]any `json:"message"`
	ContentBlock map[string]any `json:"content_block"`
	Delta        map[string]any `json:"delta"`
	Usage        map[string]any `json:"usage"`
}

func feedResponsesStream(t *testing.T, conv *ResponsesToAnthropicStreamer, frames [][2]string) []anthropicStreamEvent {
	t.Helper()
	for _, f := range frames {
		if err := conv.HandleEvent(f[0], []byte(f[1])); err != nil {
			t.Fatalf("HandleEvent %s: %v", f[0], err)
		}
	}
	if err := conv.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return nil
}

func collectAnthropicEvents(t *testing.T, buf *bytes.Buffer) []anthropicStreamEvent {
	t.Helper()
	var out []anthropicStreamEvent
	for _, frame := range strings.Split(strings.TrimSpace(buf.String()), "\n\n") {
		var event, data string
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "event:") {
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if event == "" || data == "" {
			continue
		}
		var ev anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatalf("decode %s: %v", event, err)
		}
		ev.Type = event
		out = append(out, ev)
	}
	return out
}

func runResponsesStream(t *testing.T, restrict []string, frames [][2]string) []anthropicStreamEvent {
	t.Helper()
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	conv := NewResponsesToAnthropicStreamer(bw, "muse-spark-1.3-contributor-free")
	conv.RestrictTools(restrict)
	feedResponsesStream(t, conv, frames)
	if err := bw.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return collectAnthropicEvents(t, &buf)
}

func TestResponsesStreamTextAndToolCall(t *testing.T) {
	events := runResponsesStream(t, []string{"get_time"}, [][2]string{
		{"response.output_text.delta", `{"item_id":"a","delta":"Hi"}`},
		{"response.output_item.added", `{"item_id":"b","item":{"id":"fc1","type":"function_call","call_id":"call_1","name":"get_time"}}`},
		{"response.function_call_arguments.delta", `{"item_id":"b","delta":"{\"tz\":"}`},
		{"response.function_call_arguments.delta", `{"item_id":"b","delta":"\"UTC\"}"}`},
		{"response.output_item.done", `{"item_id":"b","item":{"id":"fc1","type":"function_call","call_id":"call_1","name":"get_time","arguments":"{\"tz\":\"UTC\"}"}}`},
		{"response.completed", `{"response":{"status":"completed","usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":1}}}}`},
	})
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	want := []string{"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("event sequence:\n got %v\nwant %v", types, want)
	}
	toolStart := events[4].ContentBlock
	if toolStart["type"] != "tool_use" || toolStart["id"] != "call_1" || toolStart["name"] != "get_time" {
		t.Errorf("tool_use start: %v", toolStart)
	}
	toolDelta := events[5].Delta
	if toolDelta["type"] != "input_json_delta" || !strings.Contains(toolDelta["partial_json"].(string), "UTC") {
		t.Errorf("tool delta: %v", toolDelta)
	}
	msgDelta := events[7]
	if msgDelta.Delta["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason: %v", msgDelta.Delta)
	}
	if msgDelta.Usage["input_tokens"].(float64) != 7 || msgDelta.Usage["output_tokens"].(float64) != 3 {
		t.Errorf("usage: %v", msgDelta.Usage)
	}
}

func TestResponsesStreamBlockIndexes(t *testing.T) {
	events := runResponsesStream(t, []string{"get_time"}, [][2]string{
		{"response.output_text.delta", `{"item_id":"a","delta":"Hi"}`},
		{"response.output_item.added", `{"item_id":"b","item":{"id":"fc1","type":"function_call","call_id":"call_1","name":"get_time"}}`},
		{"response.function_call_arguments.delta", `{"item_id":"b","delta":"{}"}`},
		{"response.output_item.done", `{"item_id":"b","item":{"id":"fc1","type":"function_call","call_id":"call_1","name":"get_time","arguments":"{}"}}`},
		{"response.output_text.delta", `{"item_id":"c","delta":"Bye"}`},
		{"response.output_text.done", `{"item_id":"c"}`},
		{"response.completed", `{"response":{"status":"completed","usage":{"input_tokens":7,"output_tokens":3}}}}`},
	})
	type ti struct {
		typ string
		idx int
	}
	var got []ti
	for _, e := range events {
		switch e.Type {
		case "content_block_start", "content_block_delta", "content_block_stop":
			got = append(got, ti{e.Type, e.Index})
		}
	}
	want := []ti{
		{"content_block_start", 0}, {"content_block_delta", 0}, {"content_block_stop", 0},
		{"content_block_start", 1}, {"content_block_delta", 1}, {"content_block_stop", 1},
		{"content_block_start", 2}, {"content_block_delta", 2}, {"content_block_stop", 2},
	}
	if len(got) != len(want) {
		t.Fatalf("block events:\n got %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("block event %d:\n got %+v\nwant %+v\nfull: %v", i, got[i], want[i], got)
		}
	}
}

func TestResponsesStreamDropsUndeclaredToolAndSkipsPing(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	conv := NewResponsesToAnthropicStreamer(bw, "m")
	conv.RestrictTools([]string{"allowed_tool"})
	frames := [][2]string{
		{"ping", `{}`},
		{"response.output_item.added", `{"item_id":"b","item":{"id":"fc1","type":"function_call","call_id":"call_1","name":"evil_tool"}}`},
		{"response.function_call_arguments.delta", `{"item_id":"b","delta":"{}"}`},
		{"response.output_item.done", `{"item_id":"b","item":{"id":"fc1","type":"function_call","call_id":"call_1","name":"evil_tool","arguments":"{}"}}`},
		{"response.completed", `{"response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}}`},
	}
	feedResponsesStream(t, conv, frames)
	if err := bw.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "tool_use") {
		t.Errorf("undeclared tool must be dropped:\n%s", got)
	}
	for _, ev := range collectAnthropicEvents(t, &buf) {
		if ev.Type == "message_delta" && ev.Delta["stop_reason"] != "end_turn" {
			t.Errorf("stop_reason without tool_use: %v", ev.Delta)
		}
	}
}
