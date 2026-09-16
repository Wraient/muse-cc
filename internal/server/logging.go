package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// accessLogEnabled reports whether per-request access lines are emitted to
// stderr (journald). Default on; set MUSE_CC_ACCESS_LOG=0 to silence.
// debugLogEnabled adds request-start lines for tracing in-flight requests.
func accessLogEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MUSE_CC_ACCESS_LOG"))
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return true
	}
	return b
}

func debugLogEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MUSE_CC_DEBUG"))
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

// withLogging emits one access line per completed request (method, path,
// status, latency) plus the client remote addr. Before Sep 2026 this
// middleware was intentionally silent, which made a wedged process
// undiagnosable: haning requests simply never appeared anywhere. Detailed
// per-request recording still happens in the proxy handlers via the store;
// this is the cheap always-on signal for journalctl.
func (s *Server) withLogging(h http.Handler) http.Handler {
	debug := debugLogEnabled()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if debug {
			log.Printf("muse-cc: started %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		}
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("muse-cc: panic serving %s %s: %v", r.Method, r.URL.Path, recovered)
				writeRecoveredPanic(rw, r, fmt.Sprint(recovered))
			}
			if accessLogEnabled() {
				log.Printf("muse-cc: %s %s -> %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
			}
		}()
		h.ServeHTTP(rw, r)
	})
}

// logUpstreamError emits an stderr line when an upstream round-trip fails
// (connection stall, DNS, refused, timeout). These lines are the early
// warning for wedge-class incidents: requests that never complete never
// reach the access log, but their upstream failure does land here.
func logUpstreamError(r *http.Request, incomingModel, targetModel string, stream bool, after time.Duration, err error) {
	sess := ""
	if r != nil {
		sess = r.Header.Get("X-Request-ID")
	}
	log.Printf("muse-cc: upstream error %s %s model=%s target=%s stream=%v req=%s after=%s err=%v",
		r.Method, r.URL.Path, incomingModel, targetModel, stream, sess, after.Round(time.Millisecond), err)
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

// Flush forwards to the underlying writer so streaming (http.Flusher) works
// through this wrapper. Without it, w.(http.Flusher) assertions fail and kill
// every streamed response.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController and the standard library detect the
// underlying writer for interfaces like Flusher / Hijacker.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func writeRecoveredPanic(w *statusRecorder, r *http.Request, detail string) {
	const clientMessage = "internal server error"
	if w.wroteHeader {
		writeRecoveredStreamingError(w, r, clientMessage)
		return
	}
	switch {
	case r.URL.Path == "/v1/messages" || strings.HasPrefix(r.URL.Path, "/v1/messages/"):
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", clientMessage)
	case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/models":
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", clientMessage)
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": clientMessage})
	}
	_ = detail
}

func writeRecoveredStreamingError(w *statusRecorder, r *http.Request, message string) {
	if !strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream") {
		return
	}
	var payload []byte
	var event string
	switch r.URL.Path {
	case "/v1/responses":
		event = "response.failed"
		payload, _ = json.Marshal(map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"status": "failed",
				"error": map[string]string{
					"type":    "api_error",
					"message": message,
				},
			},
		})
	case "/v1/chat/completions":
		event = ""
		payload, _ = json.Marshal(map[string]any{
			"error": map[string]string{
				"type":    "api_error",
				"message": message,
			},
		})
	default:
		event = "error"
		payload, _ = json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    "api_error",
				"message": message,
			},
		})
	}
	if event != "" {
		_, _ = fmt.Fprintf(w.ResponseWriter, "event: %s\ndata: %s\n\n", event, payload)
	} else {
		_, _ = fmt.Fprintf(w.ResponseWriter, "data: %s\n\n", payload)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
