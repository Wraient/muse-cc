package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wraient/muse-cc/internal/config"
	"github.com/Wraient/muse-cc/internal/proxy"
)

// proxyResponsesPassthrough forwards a Responses API request for a
// Responses-native model (see proxy.IsResponsesNativeModel) directly to the
// upstream /v1/responses endpoint instead of translating it to Chat
// Completions. Meta muse-spark* models are served ONLY there — translation
// 500s upstream.
//
// The body is sanitized first (model rewrite + input dedupe + tool_choice
// coercion), then relayed: streams pass SSE through minus ping keepalives,
// non-stream responses pass bytes through as-is.
// museSparkUpstreamSlots caps concurrent upstream Meta requests for
// Responses-native models. Free-tier Meta 503s when several big-history
// streams run at once (burst of parallel subagents + retries); queuing in
// the proxy converts those 503s into slower 200s. Held for the whole relay.
var museSparkUpstreamSlots = make(chan struct{}, 2)

func (s *Server) proxyResponsesPassthrough(
	w http.ResponseWriter,
	r *http.Request,
	in *proxy.ResponsesRequest,
	cfg *config.Config,
	upstream, museKey string,
	incomingModel, targetModel string,
	reqBody []byte,
	start time.Time,
) {
	upBody, err := sanitizeResponsesPassthroughBody(reqBody, targetModel)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"could not prepare upstream Responses request: "+err.Error())
		return
	}
	// Client-supplied effort names are normalized into the target model's
	// known scale (unknown -> model maximum); requests without an effort
	// pass through untouched.
	upBody = s.clampPassthroughEffort(upBody, targetModel, incomingModel)

	upURL := strings.TrimRight(upstream, "/") + "/v1/responses"

	select {
	case museSparkUpstreamSlots <- struct{}{}:
		defer func() { <-museSparkUpstreamSlots }()
	case <-r.Context().Done():
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "client went away while queued for upstream slot")
		s.logFailed(r.Context(), r, incomingModel, targetModel, in.Stream,
			http.StatusBadGateway, "queue wait canceled", reqBody, time.Since(start))
		return
	}

	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upURL, bytes.NewReader(upBody))
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error",
			"could not build upstream request: "+err.Error())
		return
	}
	upReq.Header.Set("Authorization", "Bearer "+museKey)
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("User-Agent", museUA())
	setMuseHeaders(upReq, r.Header, in.PromptCacheKey)
	if in.Stream {
		upReq.Header.Set("Accept", "text/event-stream")
	} else {
		upReq.Header.Set("Accept", "application/json")
	}

	upStart := time.Now()
	httpClient := s.upstreamClient(in.Stream, cfg.RequestTimeoutSeconds)
	resp, err := doUpstreamWithRetry(httpClient, upReq, upBody)
	if err != nil {
		logUpstreamError(r, incomingModel, targetModel, in.Stream, time.Since(upStart), err)
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream request failed: "+err.Error())
		s.logFailed(r.Context(), r, incomingModel, targetModel, in.Stream,
			http.StatusBadGateway, err.Error(), reqBody, time.Since(start))
		return
	}
	if newResp, newBody, ok := s.maybeRetryStaleReasoning(httpClient, upReq, upBody, resp,
		incomingModel, targetModel, in.Stream, start); ok {
		resp, upBody = newResp, newBody
	}
	if newResp, _, ok := s.maybeRetryInvalidEffort(httpClient, upReq, upBody, resp,
		incomingModel, targetModel, in.Stream, start); ok {
		resp = newResp
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if in.Stream && resp.StatusCode < http.StatusBadRequest &&
		(contentType == "" || strings.Contains(contentType, "text/event-stream")) {
		s.relayResponsesPassthroughStream(w, resp, r, incomingModel, targetModel, reqBody, start)
		return
	}
	s.relayResponsesPassthroughJSON(w, resp, r, incomingModel, targetModel, in.Stream, reqBody, start)
}

// sanitizeResponsesPassthroughBody rewrites a client Responses request for
// direct upstream delivery:
//   - model is set to the resolved upstream target,
//   - duplicate function_call_output items for one call_id are collapsed to
//     the LAST one (long sessions can accumulate two tool_results — e.g.
//     "cancelled by user" plus the real result — which Meta rejects), and
//     duplicate function_call items keep the FIRST,
//   - tool_choice is coerced to "auto": Meta currently accepts only that.
func sanitizeResponsesPassthroughBody(body []byte, targetModel string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]json.RawMessage{}
	}
	payload["model"], _ = json.Marshal(targetModel)

	if raw, ok := payload["input"]; ok && len(raw) > 0 && strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		payload["input"], _ = json.Marshal(dedupeResponsesInputItems(items))
	}

	if raw, ok := payload["tool_choice"]; ok && len(raw) > 0 {
		var choice string
		if err := json.Unmarshal(raw, &choice); err != nil || choice != "auto" {
			payload["tool_choice"], _ = json.Marshal("auto")
		}
	}
	return json.Marshal(payload)
}

// dedupeResponsesInputItems collapses duplicate tool items by call_id,
// preserving overall order of first appearance: the FIRST function_call per
// call_id wins, the LAST function_call_output per call_id wins. Items without
// a call_id (or of other types) pass through untouched.
func dedupeResponsesInputItems(items []map[string]json.RawMessage) []map[string]json.RawMessage {
	var itemType struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
	}
	out := make([]map[string]json.RawMessage, 0, len(items))
	// outputIdx records where each call_id's surviving output item lives so a
	// later duplicate can replace it in place (keeps position stable).
	outputIdx := map[string]int{}
	seenCall := map[string]bool{}
	for _, item := range items {
		itemType.Type, itemType.CallID = "", ""
		_ = json.Unmarshal(mustJSONMap(item), &itemType)
		switch itemType.Type {
		case "function_call_output":
			if itemType.CallID == "" {
				out = append(out, item)
				continue
			}
			if idx, dup := outputIdx[itemType.CallID]; dup {
				out[idx] = item // keep LAST
				continue
			}
			outputIdx[itemType.CallID] = len(out)
			out = append(out, item)
		case "function_call":
			if itemType.CallID == "" {
				out = append(out, item)
				continue
			}
			if seenCall[itemType.CallID] {
				continue // keep FIRST
			}
			seenCall[itemType.CallID] = true
			out = append(out, item)
		default:
			out = append(out, item)
		}
	}
	return out
}

func mustJSONMap(m map[string]json.RawMessage) []byte {
	b, _ := json.Marshal(m)
	return b
}

// relayResponsesPassthroughJSON relays a non-stream upstream /v1/responses
// body to the client byte-for-byte (upstream behavior, including
// response.incomplete on small caps, is faithfully preserved).
func (s *Server) relayResponsesPassthroughJSON(
	w http.ResponseWriter,
	resp *http.Response,
	r *http.Request,
	incomingModel, targetModel string,
	stream bool,
	reqBody []byte,
	start time.Time,
) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "could not read upstream body: "+err.Error())
		s.logFailed(r.Context(), r, incomingModel, targetModel, stream,
			http.StatusBadGateway, err.Error(), reqBody, time.Since(start))
		return
	}
	if len(raw) > maxResponseBytes {
		const msg = "upstream response exceeded the maximum allowed size"
		writeOpenAIError(w, http.StatusBadGateway, "api_error", msg)
		s.logFailed(r.Context(), r, incomingModel, targetModel, stream,
			http.StatusBadGateway, msg, reqBody, time.Since(start))
		return
	}
	if resp.StatusCode >= http.StatusBadRequest {
		copyOpenAIHeaders(w.Header(), resp.Header, false)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(raw)
		message := extractOpenAIError(raw)
		if message == "" {
			message = strings.TrimSpace(string(raw))
		}
		s.logFailed(r.Context(), r, incomingModel, targetModel, stream,
			resp.StatusCode, message, reqBody, time.Since(start))
		return
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)

	var usage struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &usage)
	s.logSuccess(r.Context(), r, incomingModel, targetModel, stream, http.StatusOK,
		usage.Usage.InputTokens, usage.Usage.OutputTokens, "complete",
		string(reqBody), string(raw), time.Since(start))
}

// relayResponsesPassthroughStream relays an upstream SSE stream verbatim
// except for `event: ping` keepalive frames, which are stripped: some clients
// (grok ≤1.0.13) fail the whole turn on the unknown `ping` variant even
// though the answer already arrived.
func (s *Server) relayResponsesPassthroughStream(
	w http.ResponseWriter,
	resp *http.Response,
	r *http.Request,
	incomingModel, targetModel string,
	reqBody []byte,
	start time.Time,
) {
	defer resp.Body.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error",
			"streaming not supported by this server")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	responseLog := &limitedLogWriter{limit: s.cfg.Snapshot().MaxBodyLogBytes}
	multi := io.MultiWriter(w, responseLog)
	stripped := 0
	scanErr := relaySSEStrippingPing(resp.Body, multi, func() { stripped++; flusher.Flush() }, flusher)
	flusher.Flush()
	if scanErr != nil {
		s.logFailed(r.Context(), r, incomingModel, targetModel, true,
			http.StatusBadGateway, "upstream stream error: "+scanErr.Error(), reqBody, time.Since(start))
		return
	}
	s.logSuccess(r.Context(), r, incomingModel, targetModel, true, http.StatusOK,
		0, 0, "stop", string(reqBody), responseLog.String(), time.Since(start))
}

// relaySSEStrippingPing copies SSE frames from src to dst, dropping frames
// whose event name is "ping". onForwarded is called after each forwarded frame
// (may be nil); flusher, if non-nil, is flushed after each forwarded frame.
// A nil flusher disables per-frame flushing.
func relaySSEStrippingPing(src io.Reader, dst io.Writer, onForwarded func(), flusher http.Flusher) error {
	reader := bufio.NewReader(src)
	var frame []string
	flush := func() error {
		if len(frame) == 0 {
			return nil
		}
		event := ""
		for _, line := range frame {
			if name, ok := sseField(line, "event"); ok {
				event = strings.TrimSpace(name)
			}
		}
		lines := frame
		frame = nil
		if event == "ping" {
			return nil
		}
		for _, line := range lines {
			if _, err := io.WriteString(dst, line+"\n"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(dst, "\n"); err != nil {
			return err
		}
		if onForwarded != nil {
			onForwarded()
		} else if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
		} else if strings.HasPrefix(line, ":") {
			// SSE comment keepalive: forward (harmless, keeps conn alive).
			frame = append(frame, line)
		} else if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "data:") ||
			strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
			frame = append(frame, line)
		} else {
			// Unknown line inside a frame: preserve rather than corrupt.
			frame = append(frame, line)
		}
		if err != nil {
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// sseField splits an "name: value" SSE line.
func sseField(line, name string) (string, bool) {
	if !strings.HasPrefix(line, name+":") {
		return "", false
	}
	return strings.TrimPrefix(line, name+":"), true
}
