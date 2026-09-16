package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wraient/muse-cc/internal/config"
)

// flushCountingWriter records every Write and signals every Flush. It models
// the real net/http behaviour the proxy must drive: bytes handed to Write are
// NOT visible downstream until Flush is called (the HTTP layer buffers).
type flushCountingWriter struct {
	mu         sync.Mutex
	header     http.Header
	buf        strings.Builder
	flushes    int
	flushedCh  chan struct{}
	statusCode int
}

func newFlushCountingWriter() *flushCountingWriter {
	return &flushCountingWriter{header: make(http.Header), flushedCh: make(chan struct{}, 64)}
}

func (w *flushCountingWriter) Header() http.Header { return w.header }

func (w *flushCountingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *flushCountingWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.statusCode = status
}

func (w *flushCountingWriter) Flush() {
	w.mu.Lock()
	w.flushes++
	w.mu.Unlock()
	select {
	case w.flushedCh <- struct{}{}:
	default:
	}
}

func (w *flushCountingWriter) flushCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushes
}

func (w *flushCountingWriter) body() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// TestHandleStreamResponseFlushesIncrementally feeds one upstream chunk, then
// asserts the client already received flushed bytes BEFORE the upstream
// stream ends. Without per-chunk flusher.Flush() the first flush only happens
// at stream end (net/http holds sub-2KB responses), making TTFB == total.
func TestHandleStreamResponseFlushesIncrementally(t *testing.T) {
	cfg := config.Default()
	srv, _ := newTestServerWithCfg(t, cfg)

	upReader, upWriter := io.Pipe()
	upResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       upReader,
	}
	upResp.Header.Set("Content-Type", "text/event-stream")

	down := newFlushCountingWriter()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	reqBody := []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"stream":true}`)

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleStreamResponse(down, upResp, req, "m", "target-m", reqBody, time.Now())
	}()

	// Headers + message_start must flush before any upstream chunk arrives.
	select {
	case <-down.flushedCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("no flush before first upstream chunk (headers/message_start held back)")
	}

	// One upstream text chunk, upstream stays open.
	if _, err := io.WriteString(upWriter, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"); err != nil {
		t.Fatalf("write upstream chunk: %v", err)
	}
	// Drain flush signals until the text delta is visible, or time out.
	sawText := false
	timeout := time.After(5 * time.Second)
	for !sawText {
		select {
		case <-down.flushedCh:
			if strings.Contains(down.body(), "text_delta") {
				sawText = true
			}
		case <-timeout:
			t.Fatalf("text chunk not flushed while upstream still open (body=%q)", down.body())
		case <-done:
			t.Fatalf("handler returned before upstream EOF")
		}
	}

	// End the upstream stream cleanly and wait for the handler.
	if _, err := io.WriteString(upWriter, "data: [DONE]\n\n"); err != nil {
		t.Fatalf("write [DONE]: %v", err)
	}
	_ = upWriter.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("handler did not finish after upstream EOF")
	}

	body := down.body()
	for _, want := range []string{"message_start", "text_delta", "message_stop"} {
		if !strings.Contains(body, want) {
			t.Errorf("downstream body missing %q:\n%s", want, body)
		}
	}
	// headers + message_start + 1 chunk + final = 4 flushes minimum.
	if n := down.flushCount(); n < 4 {
		t.Errorf("flush count = %d, want >= 4 (headers, message_start, chunk, final)", n)
	}
}
