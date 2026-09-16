package proxy

// Native video ingress.
//
// Stock Anthropic clients (Claude Code, Codex) cannot emit video blocks —
// the Messages API has no video type and the clients drop video attachments —
// so the proxy accepts video two ways:
//
//  1. Explicit "video" content blocks (proxy-specific, same source shape as
//     "image"): {"type":"video","source":{"type":"base64","media_type":
//     "video/mp4","data":"..."}} or {"type":"video","source":{"type":"url",
//     "url":"https://..."}}. A "document" block with a video/* base64 source
//     is accepted the same way.
//  2. Plain local .mp4 paths mentioned in user text (absolute or ~/ —
//     exactly how you'd reference the file talking to Claude Code, e.g.
//     "what happens in /home/u/clip.mp4?"). The proxy reads the file off
//     disk, so the clip never transits the client's context window. Paths
//     that don't resolve to a readable file are left as plain text.
//  3. Explicit markers [[video <src>]] for remote URLs, inline data: URIs,
//     or forcing a path: [[video https://example.com/a.mp4]].
//
// Both reach upstream as native input_video / video_url parts — never
// frame-split. Upstream accepts mp4 only; inline payloads are capped at
// maxVideoBytes (see anthropic_responses.go).

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// videoMarker matches explicit [[video <src>]] markers. The src runs to the
// closing brackets; surrounding quotes are stripped during resolution.
var videoMarker = regexp.MustCompile(`\[\[video\s+([^\]]+?)\]\]`)

// localVideoPath matches a plain local .mp4 path in user text: an absolute
// path or ~/-anchored one, exactly as a user would type it to Claude Code
// ("describe /home/u/clip.mp4"). Trailing punctuation is trimmed by the
// resolver. Relative paths are deliberately NOT matched — the client, not the
// proxy, owns the working directory, so a relative path would be a guess.
var localVideoPath = regexp.MustCompile(`(?:^|[\s"'\(\[])(~?/(?:[^\s"'\)\]]+/)*[^\s"'\)\]]+\.[mM][pP]4)\b`)

// maybeHasVideoRef is a cheap pre-check before splitVideoMarkers: true when
// the text holds an explicit marker or a plausible local .mp4 path. Lets
// string-content fast paths skip file I/O for ordinary messages.
func maybeHasVideoRef(text string) bool {
	return videoMarker.MatchString(text) || localVideoPath.MatchString(text)
}

// textVideoSpan is one span of marker-split user text: either literal text or
// a resolved upstream video URL (data:, https:, or file://-derived data URI).
type textVideoSpan struct {
	text     string
	videoURL string
	isVideo  bool
}

// isPlaceholderVideoRef reports whether an explicit marker payload is
// documentation placeholder text rather than a real reference — e.g.
// [[video ...]] or [[video <src>]] appearing in design docs, code comments,
// or example text that the user pasted into the conversation. Resolving
// those would always fail; they must stay plain text.
func isPlaceholderVideoRef(ref string) bool {
	t := strings.Trim(ref, `"' `)
	switch t {
	case "", "...", "…", "<src>", "src", "<path>", "path", "<url>", "url",
		"<video-src>", "/home/u/clip.mp4":
		return true
	}
	// Any angle brackets = template placeholder, not a path/URL.
	if strings.ContainsAny(t, "<>") {
		return true
	}
	return false
}

// splitVideoMarkers splits user text on [[video ...]] markers AND on plain
// local .mp4 paths, resolving each to an upstream video URL. Literal text
// (possibly empty) is preserved in order. An unresolvable EXPLICIT marker is
// an error — failing loudly beats sending the model a prompt that silently
// lost its video. A plain path that doesn't resolve to a readable file is
// left as ordinary text (it may be hypothetical, a URL fragment, whatever).
func splitVideoMarkers(text string) ([]textVideoSpan, error) {
	// One ordered candidate list. full = span consumed from the text;
	// ref = span resolved to a video URL.
	type cand struct {
		fullStart, fullEnd int
		refStart, refEnd   int
		explicit           bool
	}
	var all []cand
	for _, m := range videoMarker.FindAllStringSubmatchIndex(text, -1) {
		all = append(all, cand{m[0], m[1], m[2], m[3], true})
	}
	for _, m := range localVideoPath.FindAllStringSubmatchIndex(text, -1) {
		// Skip plain paths nested inside an explicit marker.
		inside := false
		for _, e := range all {
			if e.explicit && m[0] >= e.fullStart && m[1] <= e.fullEnd {
				inside = true
				break
			}
		}
		if !inside {
			all = append(all, cand{m[2], m[3], m[2], m[3], false})
		}
	}
	// Sort by start offset (insertion sort: a message holds few matches).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].fullStart < all[j-1].fullStart; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) == 0 {
		return []textVideoSpan{{text: text}}, nil
	}
	var spans []textVideoSpan
	// appendText merges with the previous span when both are text, so an
	// unresolvable plain path leaves the text byte-identical to the input.
	appendText := func(t string) {
		if t == "" {
			return
		}
		if n := len(spans); n > 0 && !spans[n-1].isVideo {
			spans[n-1].text += t
			return
		}
		spans = append(spans, textVideoSpan{text: t})
	}
	pos := 0
	for _, h := range all {
		if h.fullStart < pos {
			continue // overlapped by previous match
		}
		if h.fullStart > pos {
			appendText(text[pos:h.fullStart])
		}
		ref := strings.TrimSpace(text[h.refStart:h.refEnd])
		if h.explicit {
			if isPlaceholderVideoRef(ref) {
				// Docs/example text, not a real reference: plain text.
				appendText(text[h.fullStart:h.fullEnd])
				pos = h.fullEnd
				continue
			}
			url, err := resolveVideoRef(ref)
			if err != nil {
				return nil, err
			}
			spans = append(spans, textVideoSpan{videoURL: url, isVideo: true})
		} else if url, err := readVideoFile(expandHome(strings.Trim(ref, `"'`))); err == nil {
			spans = append(spans, textVideoSpan{videoURL: url, isVideo: true})
		} else {
			// Plain path that doesn't resolve: ordinary text, untouched.
			appendText(text[h.fullStart:h.fullEnd])
		}
		pos = h.fullEnd
	}
	if pos < len(text) {
		appendText(text[pos:])
	}
	return spans, nil
}

// expandHome resolves a leading ~/ to the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

// resolveVideoRef maps a marker payload to an upstream video_url: inline
// data: URIs and remote URLs pass through; anything else is a local file path
// read off disk and encoded as a data: URI.
func resolveVideoRef(src string) (string, error) {
	src = strings.Trim(src, `"' `)
	if src == "" {
		return "", fmt.Errorf("empty [[video ...]] marker")
	}
	lower := strings.ToLower(src)
	if strings.HasPrefix(lower, "data:video/mp4;base64,") {
		if len(src) > maxVideoBytes*4/3+len("data:video/mp4;base64,") {
			return "", fmt.Errorf("inline video exceeds the %dMB cap", maxVideoBytes>>20)
		}
		return src, nil
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return src, nil
	}
	return readVideoFile(expandHome(src))
}

// videoTranscodeThreshold is the raw size above which a local mp4 is
// re-encoded with ffmpeg before inline delivery. Staging probes (Sep 2026)
// showed upstream degrading hard with size on /v1/responses: 2MB→11s,
// 5MB→18s, 7MB→65s (with a header-timeout retry), 10MB→never responds,
// hanging the client ~110s and wedging a muse-spark upstream slot so other
// threads stall too. Transcoding to ~480p keeps the payload near
// videoTranscodeTarget, where upstream answers in seconds.
const (
	videoTranscodeThreshold = 4 << 20
	videoTranscodeTarget    = 4 << 20
	videoCacheEntries       = 4
)

// videoCache memoizes resolved data: URIs by file identity so repeated turns
// in one session (history re-sends the path every turn) don't re-read and
// re-transcode. Bounded to videoCacheEntries; each entry holds up to
// ~maxVideoBytes base64.
var videoCache = struct {
	sync.Mutex
	m     map[string]string
	order []string
}{m: make(map[string]string)}

// videoCacheKey identifies a file on disk; recordings are effectively
// immutable, so path+size+mtime is stable enough.
func videoCacheKey(path string, fi os.FileInfo) string {
	return fmt.Sprintf("%s|%d|%d", path, fi.Size(), fi.ModTime().UnixNano())
}

func videoCacheGet(key string) (string, bool) {
	videoCache.Lock()
	defer videoCache.Unlock()
	u, ok := videoCache.m[key]
	return u, ok
}

func videoCachePut(key, url string) {
	videoCache.Lock()
	defer videoCache.Unlock()
	if _, ok := videoCache.m[key]; !ok {
		videoCache.order = append(videoCache.order, key)
	}
	videoCache.m[key] = url
	for len(videoCache.order) > videoCacheEntries {
		old := videoCache.order[0]
		videoCache.order = videoCache.order[1:]
		delete(videoCache.m, old)
	}
}

// haveFFmpeg is the LookPath result, resolved once: transcode is best-effort,
// never a hard dependency.
var haveFFmpeg = sync.OnceValue(func() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
})

// transcodeVideo re-encodes src (mp4) down toward videoTranscodeTarget,
// trying 480p/crf28 then 360p/crf32, and returns the raw bytes of the first
// attempt that fits. Full duration is kept — only resolution/quality drop,
// so the model still sees the whole clip. An error means ffmpeg is missing
// or both attempts missed; the caller falls back to the cap check.
func transcodeVideo(src string) ([]byte, error) {
	if !haveFFmpeg() {
		return nil, fmt.Errorf("ffmpeg not available")
	}
	var best []byte
	for _, tc := range []struct {
		width int
		crf   string
	}{
		{854, "28"},
		{640, "32"},
	} {
		out, err := func() (string, error) {
			f, err := os.CreateTemp("", "occ-video-*.mp4")
			if err != nil {
				return "", err
			}
			out := f.Name()
			_ = f.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-loglevel", "error",
				"-i", src,
				"-vf", fmt.Sprintf("scale=%d:-2", tc.width),
				"-c:v", "libx264", "-preset", "veryfast", "-crf", tc.crf,
				"-c:a", "aac", "-b:a", "64k",
				"-movflags", "+faststart",
				out)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				os.Remove(out)
				return "", fmt.Errorf("ffmpeg %dp: %w: %s", tc.width, err, strings.TrimSpace(stderr.String()))
			}
			return out, nil
		}()
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(out)
		os.Remove(out)
		if err != nil || len(raw) == 0 {
			continue
		}
		if len(raw) <= videoTranscodeTarget {
			return raw, nil
		}
		if best == nil || len(raw) < len(best) {
			best = raw
		}
	}
	if best != nil {
		return best, nil
	}
	return nil, fmt.Errorf("could not transcode %q under target", src)
}

// readVideoFile loads a local mp4 for inline delivery: mp4 only. Files over
// videoTranscodeThreshold are ffmpeg-transcoded toward videoTranscodeTarget
// first; anything still over maxVideoBytes fails fast here instead of
// hanging upstream for ~110s and wedging a shared upstream slot.
func readVideoFile(path string) (string, error) {
	if strings.ToLower(filepath.Ext(path)) != ".mp4" {
		return "", fmt.Errorf("unsupported video file %q: .mp4 only", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("could not read video file %q: %w", path, err)
	}
	if key := videoCacheKey(path, fi); key != "" {
		if u, ok := videoCacheGet(key); ok {
			return u, nil
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("could not read video file %q: %w", path, err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("video file %q is empty", path)
	}
	if len(raw) > videoTranscodeThreshold {
		if tc, terr := transcodeVideo(path); terr == nil && len(tc) > 0 && len(tc) < len(raw) {
			raw = tc
		}
	}
	if len(raw) > maxVideoBytes {
		return "", fmt.Errorf("video file %q is %.1fMB, over the %dMB cap",
			path, float64(len(raw))/(1<<20), maxVideoBytes>>20)
	}
	url := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(raw)
	videoCachePut(videoCacheKey(path, fi), url)
	return url, nil
}
