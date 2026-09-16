package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kiowx/muse-cc/internal/config"
	"github.com/Kiowx/muse-cc/internal/store"
)

const staleReasoningUpstreamErr = `{"model":"muse-spark-1.3-contributor-free","error":{"param":null,"type":"invalid_request_error","message":"Error from provider (Console): Upstream request failed: [invalid_request_error] reasoning ` +
	"`encrypted_content`" + ` was not issued to this caller"}}`

const recoveryOKResponse = `{"id":"resp_rec","object":"response","status":"completed",
	"model":"muse-spark-1.3-contributor-free","output":[
		{"id":"msg_r","type":"message","status":"completed","role":"assistant",
		 "content":[{"type":"output_text","text":"recovered","annotations":[]}]}],
	"usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24,
		"input_tokens_details":{},"output_tokens_details":{}}}`

// recoveryTestStack serves the full proxy mux with model * mapped to the
// Responses-native muse model and the given mock upstream.
func recoveryTestStack(t *testing.T, upstream http.Handler) *httptest.Server {
	t.Helper()
	return recoveryTestStackWithTarget(t, upstream, "muse-spark-1.3-contributor-free")
}

// recoveryTestStackWithTarget is recoveryTestStack with an explicit target
// model (levels are resolved per target, so learning tests need control).
func recoveryTestStackWithTarget(t *testing.T, upstream http.Handler, target string) *httptest.Server {
	t.Helper()
	mock := httptest.NewServer(upstream)
	t.Cleanup(mock.Close)
	cfg := config.Default()
	cfg.UpstreamBase = mock.URL
	cfg.MuseAPIKey = "upstream-test-key"
	cfg.ModelMappings = []config.ModelMapping{{Match: "*", Target: target}}
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := New(cfg, st)
	httpSrv := httptest.NewServer(srv.Handler(nil, nil))
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

func TestPassthroughRetriesStaleReasoning(t *testing.T) {
	var hits int
	var secondBody string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "encrypted_content") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, staleReasoningUpstreamErr)
			return
		}
		secondBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, recoveryOKResponse)
	})
	httpSrv := recoveryTestStack(t, upstream)

	clientBody := `{"model":"client-model","stream":false,"store":false,
		"input":[
			{"type":"reasoning","id":"rs_stale","encrypted_content":"BLOBSTALE","summary":[]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]}
		]}`
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(clientBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client must get 200 after transparent retry, got %d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "recovered") {
		t.Errorf("client must get the recovered response: %s", raw)
	}
	if hits != 2 {
		t.Fatalf("expected exactly 1 retry (2 upstream hits), got %d", hits)
	}
	if strings.Contains(secondBody, "encrypted_content") || strings.Contains(secondBody, "rs_stale") {
		t.Errorf("retry must strip blob and dangling id: %s", secondBody)
	}
	if !strings.Contains(secondBody, `"type":"message"`) {
		t.Errorf("retry must keep real input items: %s", secondBody)
	}
}

func TestPassthroughRelaysUnrelated400(t *testing.T) {
	var hits int
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"bad tool_choice"}}`)
	})
	httpSrv := recoveryTestStack(t, upstream)

	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(
		`{"model":"client-model","stream":false,"input":"hi"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unrelated 400 must relay as 400, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(raw), "bad tool_choice") {
		t.Errorf("error body must pass through intact: %s", raw)
	}
	if hits != 1 {
		t.Errorf("no retry expected for unrelated errors, hits=%d", hits)
	}
}

func TestPassthroughSkipsRetryWhenNothingToStrip(t *testing.T) {
	var hits int
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, staleReasoningUpstreamErr)
	})
	httpSrv := recoveryTestStack(t, upstream)

	// Stateful chain: poison lives server-side behind previous_response_id,
	// the visible input has no blobs, so a strip+retry cannot help.
	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(
		`{"model":"client-model","stream":false,"previous_response_id":"resp_old",
		  "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]}]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("must relay original 400, got %d", resp.StatusCode)
	}
	if hits != 1 {
		t.Errorf("must not retry when nothing strippable, hits=%d", hits)
	}
}

func TestBridgeRelaysStaleReasoning400WithoutRetry(t *testing.T) {
	// The bridge builds fresh input without reasoning blobs, so a signature
	// 400 there has nothing to strip: it must relay (this also pins that the
	// bridge hook never loops or breaks the error path).
	var hits int
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, staleReasoningUpstreamErr)
	})
	httpSrv := recoveryTestStack(t, upstream)

	resp, err := http.Post(httpSrv.URL+"/v1/messages", "application/json", bytes.NewReader(bridgeAnthropicBody(t, false)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("must relay 400, got %d: %s", resp.StatusCode, raw)
	}
	if hits != 1 {
		t.Errorf("bridge must not retry blob-free requests, hits=%d", hits)
	}
}

const invalidEffortUpstreamErr = `{"model":"muse-spark-9-test","error":{"param":"reasoning.effort","type":"invalid_request_error","message":"Upstream request failed: [invalid_request_error] ` + "`reasoning.effort`" + `: unknown variant ` + "`ultra`" + `, expected one of ` + "`low`" + `, ` + "`medium`" + `, ` + "`high`" + `}}`

// TestPassthroughRetriesInvalidEffort pins the self-healing path: an unknown
// effort is pre-clamped to the known max; if upstream still rejects it, the
// proxy learns the taught set, clamps, and retries once — the client gets
// the recovered 200 and the model is fixed permanently.
func TestPassthroughRetriesInvalidEffort(t *testing.T) {
	var hits int
	var seenEfforts []string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		_ = json.Unmarshal(raw, &body)
		seenEfforts = append(seenEfforts, body.Reasoning.Effort)
		// Unknown model "muse-spark-9-test": only low/medium/high exist. First hit
		// carries the default-clamped xhigh and must fail; the retry with
		// the taught max (high) succeeds.
		if body.Reasoning.Effort == "high" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, recoveryOKResponse)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, invalidEffortUpstreamErr)
	})
	httpSrv := recoveryTestStackWithTarget(t, upstream, "muse-spark-9-test")

	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(
		`{"model":"muse-spark-9-test","stream":false,"reasoning":{"effort":"ultra"},
		  "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]}]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client must get 200 after clamp+retry, got %d: %s", resp.StatusCode, raw)
	}
	if hits != 2 {
		t.Fatalf("expected exactly 1 retry (2 hits), got %d (%v)", hits, seenEfforts)
	}
	if seenEfforts[0] != "xhigh" || seenEfforts[1] != "high" {
		t.Errorf("upstream must see default-clamped xhigh then taught max high: %v", seenEfforts)
	}
}

// TestPassthroughDemotesListedButRejectedTop covers the max-style case: the
// taught list names a top level upstream still rejects. The proxy demotes
// it (persisted for next time) and relays the second error without looping.
func TestPassthroughDemotesListedButRejectedTop(t *testing.T) {
	var hits int
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, invalidEffortUpstreamErr)
	})
	httpSrv := recoveryTestStackWithTarget(t, upstream, "muse-spark-9-test")

	resp, err := http.Post(httpSrv.URL+"/v1/responses", "application/json", strings.NewReader(
		`{"model":"muse-spark-9-test","stream":false,"reasoning":{"effort":"ultra"},
		  "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]}]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("double failure must relay 400, got %d", resp.StatusCode)
	}
	if hits != 2 {
		t.Errorf("exactly one retry, no loop: hits=%d", hits)
	}
}
