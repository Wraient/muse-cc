package proxy

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// inputVideoURLs extracts every input_video URL from a built Responses body.
func inputVideoURLs(t *testing.T, body []byte) []string {
	t.Helper()
	var req struct {
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				VideoURL string `json:"video_url"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var urls []string
	for _, item := range req.Input {
		for _, c := range item.Content {
			if c.Type == "input_video" {
				urls = append(urls, c.VideoURL)
			}
		}
	}
	return urls
}

func bridgeReq(msgs ...AnthropicMessage) *AnthropicRequest {
	return &AnthropicRequest{
		Model:     "muse-spark-1.3-contributor-free",
		MaxTokens: 64,
		Messages:  msgs,
	}
}

func userBlocks(blocks ...AnthropicContent) AnthropicMessage {
	return AnthropicMessage{Role: "user", Content: AnthropicMessageContent{Blocks: blocks}}
}

func TestBridgeExplicitVideoBlock(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: "describe this"},
		AnthropicContent{Type: "video", Source: &AnthropicImageSource{Type: "base64", MediaType: "video/mp4", Data: "QUJD"}},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	urls := inputVideoURLs(t, body)
	if len(urls) != 1 || urls[0] != "data:video/mp4;base64,QUJD" {
		t.Errorf("expected one inline input_video, got %v: %s", urls, body)
	}
}

func TestBridgeVideoURLBlock(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "video", Source: &AnthropicImageSource{Type: "url", URL: "https://example.com/clip.mp4"}},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	urls := inputVideoURLs(t, body)
	if len(urls) != 1 || urls[0] != "https://example.com/clip.mp4" {
		t.Errorf("expected passthrough video_url, got %v", urls)
	}
}

func TestBridgeDocumentVideoBlock(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "document", Source: &AnthropicImageSource{Type: "base64", MediaType: "video/mp4", Data: "QUJD"}},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if urls := inputVideoURLs(t, body); len(urls) != 1 {
		t.Errorf("document video/mp4 should map to input_video, got %v: %s", urls, body)
	}
}

func TestBridgeNonVideoDocumentFallsBackToText(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "document", Text: "hello pdf"},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if urls := inputVideoURLs(t, body); len(urls) != 0 {
		t.Errorf("non-video document must not produce input_video: %v", urls)
	}
	if !strings.Contains(string(body), "hello pdf") {
		t.Errorf("document text lost: %s", body)
	}
}

func TestBridgeVideoMarkerFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte{0x00, 0x00, 0x00, 0x18}, 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: "what happens in [[video " + path + "]] ?"},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	urls := inputVideoURLs(t, body)
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "data:video/mp4;base64,") {
		t.Errorf("marker should resolve to inline input_video, got %v", urls)
	}
	if !strings.Contains(string(body), "what happens in") || !strings.Contains(string(body), "?") {
		t.Errorf("surrounding text must be preserved: %s", body)
	}
}

func TestBridgeVideoMarkerURL(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: "[[video https://example.com/a.mp4]] describe"},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	urls := inputVideoURLs(t, body)
	if len(urls) != 1 || urls[0] != "https://example.com/a.mp4" {
		t.Errorf("marker URL should pass through, got %v", urls)
	}
}

func TestBridgeVideoMarkerBadPathErrors(t *testing.T) {
	_, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: "look [[video /nonexistent/x.mp4]]"},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err == nil {
		t.Error("expected error for unreadable marker path")
	}
}

func TestBridgeVideoNonMP4Errors(t *testing.T) {
	_, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "video", Source: &AnthropicImageSource{Type: "base64", MediaType: "video/webm", Data: "QUJD"}},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err == nil {
		t.Error("expected error for non-mp4 video block")
	}
}

func TestBridgePlaceholderMarkersStayText(t *testing.T) {
	for _, in := range []string{
		"use [[video ...]] markers",
		"use [[video <src>]] markers",
		"e.g. [[video /home/u/clip.mp4]] in docs",
	} {
		body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
			AnthropicContent{Type: "text", Text: in},
		)), "muse-spark-1.3-contributor-free", BridgeOptions{})
		if err != nil {
			t.Fatalf("placeholder %q must not error, got: %v", in, err)
		}
		if urls := inputVideoURLs(t, body); len(urls) != 0 {
			t.Errorf("placeholder %q must stay text, got %v", in, urls)
		}
		if !strings.Contains(string(body), "[[video") {
			t.Errorf("placeholder text altered: %s", body)
		}
	}
}

func TestBridgeNonVideoTextUnaffected(t *testing.T) {
	in := "plain text with [[brackets]] and [video x]"
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: in},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if urls := inputVideoURLs(t, body); len(urls) != 0 {
		t.Errorf("no video expected: %v", urls)
	}
	if !strings.Contains(string(body), in) {
		t.Errorf("text altered: %s", body)
	}
}

func TestBridgePlainPathAutoDetect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte{0x00, 0x00, 0x00, 0x18}, 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: "what happens in " + path + "?"},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	urls := inputVideoURLs(t, body)
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "data:video/mp4;base64,") {
		t.Errorf("plain .mp4 path should auto-resolve to inline input_video, got %v", urls)
	}
	if !strings.Contains(string(body), "what happens in") {
		t.Errorf("surrounding text must be preserved: %s", body)
	}
}

func TestBridgeStringContentPlainPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte{0x00, 0x00, 0x00, 0x18}, 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(
		AnthropicMessage{Role: "user", Content: AnthropicMessageContent{Text: "what is in " + path + "?", IsStr: true}},
	), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	urls := inputVideoURLs(t, body)
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "data:video/mp4;base64,") {
		t.Errorf("string content with .mp4 path should attach input_video, got %v", urls)
	}
}

func TestBridgePlainPathMissingStaysText(t *testing.T) {
	in := "look at /nonexistent/clip.mp4 tomorrow"
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: in},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if urls := inputVideoURLs(t, body); len(urls) != 0 {
		t.Errorf("unresolvable path must stay text, got %v", urls)
	}
	if !strings.Contains(string(body), in) {
		t.Errorf("text altered: %s", body)
	}
}

func TestBridgePlainPathInsideMarkerNotDoubled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte{0x00, 0x00, 0x00, 0x18}, 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: "watch [[video " + path + "]] now"},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if urls := inputVideoURLs(t, body); len(urls) != 1 {
		t.Errorf("marker path must resolve exactly once, got %v", urls)
	}
}

func TestBridgeRelativePathIgnored(t *testing.T) {
	in := "watch clips/highlight.mp4 later"
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "text", Text: in},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if urls := inputVideoURLs(t, body); len(urls) != 0 {
		t.Errorf("relative path must not resolve, got %v", urls)
	}
	if !strings.Contains(string(body), in) {
		t.Errorf("text altered: %s", body)
	}
}

func TestChatPathVideoBlock(t *testing.T) {
	oreq := ConvertRequest(&AnthropicRequest{
		Model:     "some-model",
		MaxTokens: 64,
		Messages: []AnthropicMessage{userBlocks(
			AnthropicContent{Type: "text", Text: "see this"},
			AnthropicContent{Type: "video", Source: &AnthropicImageSource{Type: "url", URL: "https://example.com/b.mp4"}},
		)},
	}, func(string) string { return "some-model" })
	if len(oreq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(oreq.Messages))
	}
	parts, ok := oreq.Messages[0].Content.([]OpenAIContentPart)
	if !ok {
		t.Fatalf("expected parts content, got %T", oreq.Messages[0].Content)
	}
	if len(parts) != 2 || parts[1].Type != "video_url" ||
		parts[1].VideoURL == nil || parts[1].VideoURL.URL != "https://example.com/b.mp4" {
		t.Errorf("video_url part missing: %+v", parts)
	}
}

func TestChatPathVideoMarker(t *testing.T) {
	oreq := ConvertRequest(&AnthropicRequest{
		Model:     "some-model",
		MaxTokens: 64,
		Messages: []AnthropicMessage{userBlocks(
			AnthropicContent{Type: "text", Text: "watch [[video https://example.com/c.mp4]] now"},
		)},
	}, func(string) string { return "some-model" })
	b, _ := json.Marshal(oreq)
	if !strings.Contains(string(b), `"type":"video_url"`) || !strings.Contains(string(b), "https://example.com/c.mp4") {
		t.Errorf("chat marker should become video_url part: %s", b)
	}
}

func TestIsVideoSourceURLMedia(t *testing.T) {
	cases := []struct {
		name string
		src  *AnthropicImageSource
		want bool
	}{
		{"nil", nil, false},
		{"base64 video", &AnthropicImageSource{Type: "base64", MediaType: "video/mp4", Data: "QUJD"}, true},
		{"base64 pdf", &AnthropicImageSource{Type: "base64", MediaType: "application/pdf", Data: "QUJD"}, false},
		{"url no media stays video", &AnthropicImageSource{Type: "url", URL: "https://example.com/c.mp4"}, true},
		{"url video media", &AnthropicImageSource{Type: "url", URL: "https://example.com/c.mp4", MediaType: "video/mp4"}, true},
		// A document block pointing at a PDF URL must NOT become input_video:
		// upstream would try to download it as media (media_url_origin_error).
		{"url pdf media", &AnthropicImageSource{Type: "url", URL: "https://example.com/d.pdf", MediaType: "application/pdf"}, false},
	}
	for _, c := range cases {
		if got := isVideoSource(c.src); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBridgeDocumentURLPDFStaysText(t *testing.T) {
	body, err := ConvertAnthropicToResponsesBody(bridgeReq(userBlocks(
		AnthropicContent{Type: "document", Text: "see pdf",
			Source: &AnthropicImageSource{Type: "url", URL: "https://example.com/d.pdf", MediaType: "application/pdf"}},
	)), "muse-spark-1.3-contributor-free", BridgeOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if urls := inputVideoURLs(t, body); len(urls) != 0 {
		t.Errorf("PDF-URL document must not produce input_video: %v", urls)
	}
}

func TestResolveVideoRefExpandsHome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte{0x00, 0x00, 0x00, 0x18}, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	url, err := resolveVideoRef("~/clip.mp4")
	if err != nil {
		t.Fatalf("marker ~/ path must expand, got: %v", err)
	}
	if !strings.HasPrefix(url, "data:video/mp4;base64,") {
		t.Errorf("expected inline data URI, got %q", url[:32])
	}
}

func TestReadVideoFileOversizeFailsFast(t *testing.T) {
	// maxVideoBytes+1 of zeros: not valid mp4, so no transcoder can save it
	// (and none is needed) — must fail here, fast, not hang upstream.
	path := filepath.Join(t.TempDir(), "big.mp4")
	if err := os.WriteFile(path, make([]byte, maxVideoBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := readVideoFile(path)
	if err == nil {
		t.Fatal("expected over-cap error")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error should name the cap, got: %v", err)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("oversize must fail fast, took %v", d)
	}
}

func TestTranscodeShrinks(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "big.mp4")
	// High-bitrate 720p testsrc: large enough that 480p/crf28 must shrink it.
	if out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=1280x720:rate=30",
		"-c:v", "libx264", "-b:v", "8M", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Skipf("cannot generate fixture: %v %s", err, out)
	}
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture: %.2fMB", float64(fi.Size())/(1<<20))
	raw, err := transcodeVideo(src)
	if err != nil {
		t.Fatalf("transcode: %v", err)
	}
	if len(raw) >= int(fi.Size()) {
		t.Errorf("transcode must shrink: %d -> %d", fi.Size(), len(raw))
	}
	if len(raw) == 0 || !bytes.HasPrefix(raw, []byte{0x00, 0x00, 0x00}) {
		t.Errorf("transcode output does not look like mp4 (%d bytes)", len(raw))
	}
}
