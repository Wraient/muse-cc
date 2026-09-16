package server

// Transparent recovery for stale encrypted reasoning.
//
// When a client replays Responses history carrying encrypted-reasoning blobs
// issued to a different caller identity, upstream fails fast with HTTP 400
// "reasoning `encrypted_content` was not issued to this caller". The blobs
// carry no visible text, so instead of surfacing the error, the proxy strips
// them from the upstream request and retries once — the client receives the
// correct response. Used by every path that posts to upstream /v1/responses
// (Responses passthrough + Anthropic bridge), so all harnesses (claude, grok,
// codex, claude) are covered without client changes.

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Kiowx/muse-cc/internal/proxy"
)

// recoveryMaxErrBody caps how much of an upstream error body is buffered for
// signature matching. Real 400 payloads are <1KB; beyond the cap the response
// is relayed untouched (never retried blind).
const recoveryMaxErrBody = 64 * 1024

// maybeRetryStaleReasoning inspects an upstream 400 for the stale-reasoning
// signature and, on a match, replays the request once with encrypted blobs
// stripped. It returns the replacement response and true when a retry was
// performed (the original resp is closed); otherwise the original resp with
// its body restored for the normal relays, and false. The returned body is
// the request actually sent upstream (cleaned on retry), so chained
// recoveries compose instead of resending the poison.
func (s *Server) maybeRetryStaleReasoning(
	client *http.Client,
	template *http.Request,
	upBody []byte,
	resp *http.Response,
	incomingModel, targetModel string,
	stream bool,
	start time.Time,
) (*http.Response, []byte, bool) {
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		return resp, upBody, false
	}
	saved, ok := peekErrorBody(resp)
	if !ok || !proxy.IsStaleReasoningError(resp.StatusCode, saved) {
		restoreErrorBody(resp, saved)
		return resp, upBody, false
	}
	cleaned, stripped, err := proxy.StripStaleReasoningBlobs(upBody)
	if err != nil || stripped == 0 {
		// Nothing to fix (e.g. the poison lives server-side behind a
		// previous_response_id chain): relay the original error.
		restoreErrorBody(resp, saved)
		return resp, upBody, false
	}
	retryReq := cloneWithBody(template, cleaned)
	log.Printf("muse-cc: stale-reasoning retry model=%s target=%s stream=%v stripped=%d",
		incomingModel, targetModel, stream, stripped)
	newResp, err := doUpstreamWithRetry(client, retryReq, cleaned)
	if err != nil {
		log.Printf("muse-cc: stale-reasoning retry failed model=%s: %v", incomingModel, err)
		restoreErrorBody(resp, saved)
		return resp, upBody, false
	}
	log.Printf("muse-cc: stale-reasoning retry model=%s -> upstream %s after=%s",
		incomingModel, newResp.Status, time.Since(start).Round(time.Millisecond))
	return newResp, cleaned, true
}

// maybeRetryInvalidEffort handles upstream 400s that reject reasoning.effort:
// it learns the taught level set (persisted per model), clamps the request
// into it, and retries once. If the clamped value fails again with an
// effort error, the top level is demoted for next time and the new error is
// relayed (one retry per request, never a loop). Non-matching responses pass
// through with bodies restored, exactly like maybeRetryStaleReasoning.
func (s *Server) maybeRetryInvalidEffort(
	client *http.Client,
	template *http.Request,
	upBody []byte,
	resp *http.Response,
	incomingModel, targetModel string,
	stream bool,
	start time.Time,
) (*http.Response, []byte, bool) {
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		return resp, upBody, false
	}
	saved, ok := peekErrorBody(resp)
	if !ok || !proxy.IsInvalidEffortError(resp.StatusCode, saved) {
		restoreErrorBody(resp, saved)
		return resp, upBody, false
	}
	current, ok := proxy.ExtractRequestEffort(upBody)
	if !ok {
		restoreErrorBody(resp, saved)
		return resp, upBody, false
	}
	levels := s.levelsForModel(targetModel)
	if taught := proxy.ParseExpectedEfforts(string(saved)); len(taught) > 0 {
		s.learnEffortLevels(targetModel, taught)
		levels = s.levelsForModel(targetModel)
	}
	clamped := proxy.ClampEffort(current, levels)
	if strings.EqualFold(clamped, current) {
		// The taught list lied about our value (e.g. max listed but
		// rejected): demote the top and try the next one down.
		levels = s.demoteEffortTop(targetModel)
		clamped = proxy.ClampEffort(current, levels)
		if strings.EqualFold(clamped, current) {
			restoreErrorBody(resp, saved)
			return resp, upBody, false
		}
	}
	fixed, err := proxy.SetRequestEffort(upBody, clamped)
	if err != nil {
		restoreErrorBody(resp, saved)
		return resp, upBody, false
	}
	log.Printf("muse-cc: effort-clamp retry model=%s target=%s stream=%v %q -> %q",
		incomingModel, targetModel, stream, current, clamped)
	newResp, err := doUpstreamWithRetry(client, cloneWithBody(template, fixed), fixed)
	if err != nil {
		log.Printf("muse-cc: effort-clamp retry failed model=%s: %v", incomingModel, err)
		restoreErrorBody(resp, saved)
		return resp, upBody, false
	}
	if saved2, ok2 := peekErrorBody(newResp); ok2 && proxy.IsInvalidEffortError(newResp.StatusCode, saved2) {
		// Clamped value rejected too: remember the top as bad for next
		// time, then relay this error (no second retry).
		s.demoteEffortTop(targetModel)
		log.Printf("muse-cc: effort-clamp retry still 400 model=%s (demoted top)", incomingModel)
		restoreErrorBody(newResp, saved2)
	} else {
		restoreErrorBody(newResp, saved2)
		log.Printf("muse-cc: effort-clamp retry model=%s -> upstream %s after=%s",
			incomingModel, newResp.Status, time.Since(start).Round(time.Millisecond))
	}
	return newResp, fixed, true
}

func (s *Server) learnEffortLevels(model string, levels []string) {
	if s == nil || s.effortLevels == nil {
		return
	}
	s.effortLevels.Learn(model, levels)
}

func (s *Server) demoteEffortTop(model string) []string {
	if s == nil || s.effortLevels == nil {
		return append([]string(nil), proxy.DefaultReasoningLevels...)
	}
	return s.effortLevels.DemoteTop(model)
}

// peekErrorBody buffers an upstream error body for signature matching. ok is
// false when the body is unreadable or implausibly large (relays must pass
// such responses through untouched).
func peekErrorBody(resp *http.Response) (saved []byte, ok bool) {
	saved, err := io.ReadAll(io.LimitReader(resp.Body, recoveryMaxErrBody+1))
	_ = resp.Body.Close()
	if err != nil || len(saved) > recoveryMaxErrBody {
		return saved, false
	}
	return saved, true
}

func restoreErrorBody(resp *http.Response, saved []byte) {
	resp.Body = io.NopCloser(bytes.NewReader(saved))
}

func cloneWithBody(template *http.Request, body []byte) *http.Request {
	retryReq := template.Clone(template.Context())
	retryReq.Body = io.NopCloser(bytes.NewReader(body))
	retryReq.ContentLength = int64(len(body))
	return retryReq
}
