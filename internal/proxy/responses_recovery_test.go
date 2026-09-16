package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsStaleReasoningError(t *testing.T) {
	sig := "Error from provider (Console): Upstream request failed: [invalid_request_error] reasoning `encrypted_content` was not issued to this caller"
	if !IsStaleReasoningError(400, []byte(sig)) {
		t.Error("exact upstream message must match")
	}
	if !IsStaleReasoningError(400, []byte(`{"error":{"type":"invalid_request_error","message":"REASONING ENCRYPTED_CONTENT WAS NOT ISSUED TO THIS CALLER"}}`)) {
		t.Error("match must be case-insensitive")
	}
	if IsStaleReasoningError(500, []byte(sig)) {
		t.Error("non-400 must not match")
	}
	if IsStaleReasoningError(400, []byte("rate limit exceeded")) {
		t.Error("unrelated 400 must not match")
	}
	if IsStaleReasoningError(400, []byte("has encrypted_content only")) {
		t.Error("single marker must not match")
	}
	if IsStaleReasoningError(400, nil) {
		t.Error("empty body must not match")
	}
}

func TestStripStaleReasoningBlobs(t *testing.T) {
	in := []byte(`{"model":"m","store":false,"input":[
		{"type":"reasoning","id":"rs_1","encrypted_content":"BLOB","summary":[]},
		{"type":"reasoning","id":"rs_2","encrypted_content":"BLOB2",
		 "summary":[{"type":"summary_text","text":"kept summary"}]},
		{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_1","output":"mentions encrypted_content in prose"},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
	]}`)
	cleaned, n, err := StripStaleReasoningBlobs(in)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if n != 3 { // 2 blobs + 1 dropped empty item
		t.Errorf("expected 3 changes, got %d: %s", n, cleaned)
	}
	var req struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(cleaned, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Input) != 4 {
		t.Fatalf("empty reasoning item must be dropped, got %d items: %s", len(req.Input), cleaned)
	}
	for _, item := range req.Input {
		for _, k := range []string{"encrypted_content", "reasoningEncryptedContent", "encryptedContent"} {
			if _, present := item[k]; present {
				t.Errorf("blob key %s survives: %v", k, item)
			}
		}
	}
	// Summary item kept, blob + dangling id gone, summary intact.
	kept := req.Input[0]
	if kept["type"] != "reasoning" {
		t.Fatalf("first item must be kept reasoning: %v", kept)
	}
	if _, present := kept["id"]; present {
		t.Errorf("dangling id must go with its blob: %v", kept)
	}
	sum, _ := kept["summary"].([]any)
	if len(sum) != 1 {
		t.Errorf("summary must survive: %v", kept)
	}
	// Untouched items pass through byte-comparable.
	if req.Input[1]["type"] != "function_call" || req.Input[1]["call_id"] != "call_1" {
		t.Errorf("function_call altered: %v", req.Input[1])
	}
	out, _ := req.Input[2]["output"].(string)
	if !strings.Contains(out, "encrypted_content") {
		t.Errorf("prose mentioning blobs must not be touched: %v", req.Input[2])
	}
}

func TestStripStaleReasoningBlobsNoOp(t *testing.T) {
	clean := []byte(`{"model":"m","input":[{"type":"message","role":"user","content":[]}]}`)
	out, n, err := StripStaleReasoningBlobs(clean)
	if err != nil || n != 0 {
		t.Fatalf("clean body must be a no-op: n=%d err=%v", n, err)
	}
	if string(out) != string(clean) {
		t.Error("no-op must return the input untouched")
	}
	strForm := []byte(`{"model":"m","input":"just text"}`)
	if _, n, err := StripStaleReasoningBlobs(strForm); err != nil || n != 0 {
		t.Fatalf("string input must be a no-op: n=%d err=%v", n, err)
	}
	if _, _, err := StripStaleReasoningBlobs([]byte("{nope")); err == nil {
		t.Error("invalid JSON must error")
	}
}
