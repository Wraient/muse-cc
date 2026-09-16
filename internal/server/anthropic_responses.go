package server

// Anthropic-over-Responses bridge for Responses-native models.
//
// Meta muse-spark* models are served ONLY on the upstream Responses API, so
// POST /v1/messages for them is translated to upstream /v1/responses here
// (Anthropic request -> Responses request, convert back on the way out)
// instead of failing fast. Shares the museSparkUpstreamSlots semaphore with
// the Responses passthrough path: same upstream pool, same free-tier
// concurrency cap.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Kiowx/muse-cc/internal/proxy"
)

// bridgeSessionKey returns the sticky upstream-selection key for the bridge,
// falling back to one process-stable id. The key pins a bridge session to
// one upstream pool entry so provider-side prompt caches stay warm; without
// a client sticky key all bridge traffic shares one entry (single-user
// proxy: acceptable).
var (
	bridgeSessionOnce sync.Once
	bridgeSessionID   string
)

func bridgeSessionKey(stickyKey string) string {
	if strings.TrimSpace(stickyKey) != "" {
		return stickyKey
	}
	bridgeSessionOnce.Do(func() {
		const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
		var b [26]byte
		if _, err := rand.Read(b[:]); err != nil {
			bridgeSessionID = "ses_00000000000000000000000000"
			return
		}
		for i, v := range b {
			b[i] = alphabet[int(v)%len(alphabet)]
		}
		bridgeSessionID = "ses_" + string(b[:])
	})
	return bridgeSessionID
}

// proxyAnthropicViaResponses serves POST /v1/messages for a Responses-native
// model by bridging to upstream /v1/responses.
func (s *Server) proxyAnthropicViaResponses(
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	areq *proxy.AnthropicRequest,
	upstream, museKey string,
	incomingModel, targetModel string,
	stickyKey string,
	timeoutSeconds int,
	start time.Time,
) {
	upBody, err := proxy.ConvertAnthropicToResponsesBody(areq, targetModel, proxy.BridgeOptions{
		PromptCacheKey: stickyKey,
		EffortLevels:   s.levelsForModel(targetModel),
		DefaultEffort:  s.cfg.BridgeDefaultEffort,
	})
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		s.logFailed(r.Context(), r, incomingModel, targetModel, areq.Stream,
			http.StatusBadRequest, err.Error(), body, time.Since(start))
		return
	}
	// Same sanitize pass as the Responses passthrough path (model rewrite is
	// a no-op here since the builder already set it; dedupe + tool_choice
	// coercion still apply).
	upBody, err = sanitizeResponsesPassthroughBody(upBody, targetModel)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error",
			"could not prepare upstream Responses request: "+err.Error())
		s.logFailed(r.Context(), r, incomingModel, targetModel, areq.Stream,
			http.StatusInternalServerError, err.Error(), body, time.Since(start))
		return
	}

	select {
	case museSparkUpstreamSlots <- struct{}{}:
		defer func() { <-museSparkUpstreamSlots }()
	case <-r.Context().Done():
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "client went away while queued for upstream slot")
		s.logFailed(r.Context(), r, incomingModel, targetModel, areq.Stream,
			http.StatusBadGateway, "queue wait canceled", body, time.Since(start))
		return
	}

	upURL := strings.TrimRight(upstream, "/") + "/v1/responses"
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upURL, bytes.NewReader(upBody))
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error",
			"could not build upstream request: "+err.Error())
		s.logFailed(r.Context(), r, incomingModel, targetModel, areq.Stream,
			http.StatusInternalServerError, err.Error(), body, time.Since(start))
		return
	}
	upReq.Header.Set("Authorization", "Bearer "+museKey)
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("User-Agent", museUA())
	setMuseHeaders(upReq, r.Header, stickyKey)
	if areq.Stream {
		upReq.Header.Set("Accept", "text/event-stream")
	} else {
		upReq.Header.Set("Accept", "application/json")
	}

	httpClient := s.upstreamClient(areq.Stream, timeoutSeconds)
	resp, err := doUpstreamWithRetry(httpClient, upReq, upBody)
	if err != nil {
		logUpstreamError(r, incomingModel, targetModel, areq.Stream, time.Since(start), err)
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "upstream request failed: "+err.Error())
		s.logFailed(r.Context(), r, incomingModel, targetModel, areq.Stream,
			http.StatusBadGateway, err.Error(), body, time.Since(start))
		return
	}
	if newResp, newBody, ok := s.maybeRetryStaleReasoning(httpClient, upReq, upBody, resp,
		incomingModel, targetModel, areq.Stream, start); ok {
		resp, upBody = newResp, newBody
	}
	if newResp, _, ok := s.maybeRetryInvalidEffort(httpClient, upReq, upBody, resp,
		incomingModel, targetModel, areq.Stream, start); ok {
		resp = newResp
	}

	if resp.StatusCode >= http.StatusBadRequest {
		s.passUpstreamError(w, resp, r, incomingModel, targetModel, body, start)
		return
	}

	if areq.Stream {
		s.relayAnthropicResponsesStream(w, resp, r, incomingModel, targetModel, areq, body, start)
		return
	}
	s.relayAnthropicResponsesJSON(w, resp, r, incomingModel, targetModel, areq, body, start)
}

// relayAnthropicResponsesJSON converts a non-streaming upstream Responses
// body to an Anthropic Messages response.
func (s *Server) relayAnthropicResponsesJSON(
	w http.ResponseWriter,
	resp *http.Response,
	r *http.Request,
	incomingModel, targetModel string,
	areq *proxy.AnthropicRequest,
	reqBody []byte,
	start time.Time,
) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "could not read upstream body: "+err.Error())
		s.logFailed(r.Context(), r, incomingModel, targetModel, false,
			http.StatusBadGateway, err.Error(), reqBody, time.Since(start))
		return
	}
	if len(raw) > maxResponseBytes {
		const msg = "upstream response exceeded the maximum allowed size"
		writeAnthropicError(w, http.StatusBadGateway, "api_error", msg)
		s.logFailed(r.Context(), r, incomingModel, targetModel, false,
			http.StatusBadGateway, msg, reqBody, time.Since(start))
		return
	}
	aresp, err := proxy.ConvertResponsesToAnthropicResponse(raw, incomingModel)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", err.Error())
		s.logFailed(r.Context(), r, incomingModel, targetModel, false,
			http.StatusBadGateway, err.Error(), reqBody, time.Since(start))
		return
	}
	filterUndeclaredToolUses(aresp, areq.Tools)
	writeJSON(w, http.StatusOK, aresp)

	stop := ""
	if aresp.StopReason != nil {
		stop = *aresp.StopReason
	}
	s.logSuccessWithCache(r.Context(), r, incomingModel, targetModel, false, http.StatusOK,
		aresp.Usage.InputTokens, aresp.Usage.OutputTokens,
		aresp.Usage.CacheReadInputTokens, aresp.Usage.CacheCreationInputTokens,
		stop, string(reqBody), mustJSON(aresp), time.Since(start))
}

// relayAnthropicResponsesStream converts an upstream Responses SSE stream to
// Anthropic SSE events, flushing continuously.
func (s *Server) relayAnthropicResponsesStream(
	w http.ResponseWriter,
	resp *http.Response,
	r *http.Request,
	incomingModel, targetModel string,
	areq *proxy.AnthropicRequest,
	reqBody []byte,
	start time.Time,
) {
	defer resp.Body.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "streaming not supported by this server")
		return
	}

	reader := io.Reader(resp.Body)
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			writeAnthropicError(w, http.StatusBadGateway, "api_error", "could not decompress upstream stream: "+err.Error())
			return
		}
		defer gz.Close()
		reader = gz
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	bw := bufio.NewWriter(w)
	conv := proxy.NewResponsesToAnthropicStreamer(bw, incomingModel)
	conv.RestrictTools(anthropicToolNames(areq.Tools))

	// First byte now, not at first text: upstream sends response.created
	// immediately but the first convertible content can lag seconds behind
	// (reasoning phase). Idempotent — content paths ensure it anyway.
	if err := conv.EmitMessageStart(); err != nil {
		return
	}
	if err := bw.Flush(); err != nil {
		return
	}
	flusher.Flush()

	scanErr := scanAnthropicSSE(reader, func(event string, data []byte) error {
		if err := conv.HandleEvent(event, data); err != nil {
			return err
		}
		if err := bw.Flush(); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if scanErr != nil && !errors.Is(scanErr, io.EOF) {
		_ = conv.EmitError("api_error", "upstream stream error: "+scanErr.Error())
		_ = bw.Flush()
		flusher.Flush()
		s.logFailed(r.Context(), r, incomingModel, targetModel, true,
			http.StatusBadGateway, scanErr.Error(), reqBody, time.Since(start))
		return
	}
	if err := conv.Finalize(); err != nil {
		s.logFailed(r.Context(), r, incomingModel, targetModel, true,
			http.StatusBadGateway, err.Error(), reqBody, time.Since(start))
		return
	}
	_ = bw.Flush()
	flusher.Flush()

	s.logSuccessWithCache(r.Context(), r, incomingModel, targetModel, true, http.StatusOK,
		conv.InputTokens(), conv.OutputTokens(), conv.CachedTokens(), 0,
		conv.StopReason(), string(reqBody), "[streamed]", time.Since(start))
}

func anthropicToolNames(tools []proxy.AnthropicTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
