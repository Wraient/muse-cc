package server

import (
	"net/http"
)

// Meta's Model API (https://api.meta.ai) needs no vendor session headers:
// "Authorization: Bearer <key>" is the whole auth story on every surface
// (verified live: bare Bearer + Content-Type returns 200 on /v1/models,
// /v1/messages, /v1/chat/completions and /v1/responses). setMuseHeaders
// therefore only stamps the honest proxy User-Agent. It deliberately sends
// nothing else — in particular none of the x-opencode-* identity headers a
// different upstream family requires.
//
// The down/stickyKey parameters are kept so every proxy call site shares one
// helper signature; Meta ignores them.
func setMuseHeaders(upReq *http.Request, _ http.Header, _ string) {
	if upReq == nil {
		return
	}
	upReq.Header.Set("User-Agent", museUA())
}
