package server

import (
	"strings"
	"testing"
)

func TestSummarizeUpstreamBody(t *testing.T) {
	got := summarizeUpstreamBody([]byte(`{"model":"m","stream":true,
		"input":[
			{"type":"message","role":"user","content":[]},
			{"type":"function_call","call_id":"c1"},
			{"type":"function_call_output","call_id":"c1"},
			{"type":"reasoning","id":"rs_1"}
		],
		"tools":[{"type":"function","name":"a"},{"type":"function","name":"b"}],
		"reasoning":{"effort":"minimal"}}`))
	for _, want := range []string{
		"model=m", "stream=true", "input=4{message:1,function_call:1,function_call_output:1,reasoning:1}",
		"tools=2", "effort=minimal", "bytes=",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q must contain %q", got, want)
		}
	}
	if strings.Contains(got, "call_1") || strings.Contains(got, "rs_1") {
		t.Errorf("summary must not leak ids/content: %q", got)
	}
	if got := summarizeUpstreamBody([]byte(`{"model":"m","input":"hi"}`)); !strings.Contains(got, "input=str") {
		t.Errorf("string input: %q", got)
	}
	if got := summarizeUpstreamBody([]byte(`{nope`)); got != "unparsed" {
		t.Errorf("invalid JSON: %q", got)
	}
	chained := summarizeUpstreamBody([]byte(`{"model":"m","previous_response_id":"r","input":[]}`))
	if !strings.Contains(chained, "chained") {
		t.Errorf("must flag chained requests: %q", chained)
	}
}
