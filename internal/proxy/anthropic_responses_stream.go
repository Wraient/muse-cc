package proxy

// ResponsesToAnthropicStreamer converts an upstream Responses API SSE stream
// into Anthropic Messages SSE events. Function calls are buffered and emitted
// whole at output_item.done (start + full input_json_delta + stop) so a
// missing output_item.added can never produce a nameless tool_use block;
// text deltas stream live.

import (
	"bufio"
	"fmt"
	"strings"
)

type pendingResponsesTool struct {
	itemID  string
	callID  string
	name    string
	args    strings.Builder
	emitted bool
}

// ResponsesToAnthropicStreamer maintains streaming state. Create with
// NewResponsesToAnthropicStreamer, feed upstream SSE frames with HandleEvent,
// then Finalize. Flushes are the caller's job after each event.
type ResponsesToAnthropicStreamer struct {
	w     *bufio.Writer
	model string

	allowed map[string]struct{} // nil = allow all tool names

	msgStarted bool
	msgID      string
	textOpen   bool
	nextIndex  int

	pending map[string]*pendingResponsesTool
	order   []string // item_ids in first-seen order (stable indexing)

	hasToolUse bool
	incomplete bool
	usage      AnthropicUsage
	failed     string
	finalized  bool
}

// NewResponsesToAnthropicStreamer wraps w and notes the model to echo in
// message_start. Call RestrictTools to drop undeclared function names.
func NewResponsesToAnthropicStreamer(w *bufio.Writer, model string) *ResponsesToAnthropicStreamer {
	return &ResponsesToAnthropicStreamer{
		w:       w,
		model:   model,
		msgID:   "msg_" + randHex(24),
		pending: map[string]*pendingResponsesTool{},
	}
}

// RestrictTools drops function calls whose name is not listed.
func (c *ResponsesToAnthropicStreamer) RestrictTools(names []string) {
	if names == nil {
		return
	}
	c.allowed = map[string]struct{}{}
	for _, n := range names {
		c.allowed[n] = struct{}{}
	}
}

func (c *ResponsesToAnthropicStreamer) pendingFor(itemID string) *pendingResponsesTool {
	p, ok := c.pending[itemID]
	if !ok {
		p = &pendingResponsesTool{itemID: itemID}
		c.pending[itemID] = p
		c.order = append(c.order, itemID)
	}
	return p
}

// HandleEvent processes one upstream SSE frame (event name without the
// "event:" prefix, raw data payload). "ping" frames are skipped.
func (c *ResponsesToAnthropicStreamer) HandleEvent(event string, data []byte) error {
	if event == "ping" {
		return nil
	}
	var payload responsesStreamFrame
	if err := jsonUnmarshal(data, &payload); err != nil {
		return nil // tolerate unknown/heartbeat frames
	}
	// Prefer the embedded type; fall back to the SSE event name so a frame
	// carrying only one of the two still routes correctly.
	typ := payload.Type
	if typ == "" {
		typ = event
	}
	switch typ {
	case "response.output_text.delta":
		if err := c.ensureTextBlock(); err != nil {
			return err
		}
		return c.writeEvent("content_block_delta", streamContentBlockDelta{
			Type: "content_block_delta", Index: c.nextIndex - 1,
			Delta: streamDelta{Type: "text_delta", Text: payload.Delta},
		})
	case "response.output_text.done":
		return c.closeTextBlock()
	case "response.function_call_arguments.delta":
		p := c.pendingFor(payload.ItemID)
		p.args.WriteString(payload.Delta)
		return nil
	case "response.output_item.added":
		if payload.Item == nil || payload.Item.Type != "function_call" {
			return nil
		}
		p := c.pendingFor(payload.ItemID)
		if payload.Item.CallID != "" {
			p.callID = payload.Item.CallID
		}
		if payload.Item.Name != "" {
			p.name = payload.Item.Name
		}
		if payload.Item.Arguments != "" {
			p.args.WriteString(payload.Item.Arguments)
		}
		return nil
	case "response.function_call_arguments.done", "response.output_item.done":
		if payload.Item != nil && payload.Item.Type == "function_call" {
			p := c.pendingFor(payload.ItemID)
			if payload.Item.CallID != "" {
				p.callID = payload.Item.CallID
			}
			if payload.Item.Name != "" {
				p.name = payload.Item.Name
			}
			if payload.Item.Arguments != "" {
				p.args.WriteString(payload.Item.Arguments)
			}
		}
		return c.flushTool(payload.ItemID)
	case "response.completed":
		if payload.Response != nil {
			c.applyUsage(payload.Response.Usage)
			if payload.Response.Status == "incomplete" {
				c.incomplete = true
			}
		}
		return c.finalizeWith("completed")
	case "response.incomplete":
		c.incomplete = true
		if payload.Response != nil {
			c.applyUsage(payload.Response.Usage)
		}
		return nil
	case "response.failed":
		msg := "upstream response failed"
		if payload.Response != nil && payload.Response.Error != "" {
			msg = payload.Response.Error
		}
		if payload.Error != "" {
			msg = payload.Error
		}
		c.failed = msg
		return nil
	default:
		// response.created, response.in_progress, content_part.*,
		// reasoning summaries, output_text.done handled above: ignore.
		return nil
	}
}

// EmitMessageStart writes message_start immediately, before any content.
// Call right after the upstream connection is established so the client sees
// activity during the upstream reasoning phase instead of a silent stall
// (Claude Code cancels streams that stay quiet too long). Idempotent: later
// content paths call ensureMsgStarted anyway.
func (c *ResponsesToAnthropicStreamer) EmitMessageStart() error {
	return c.ensureMsgStarted()
}

func (c *ResponsesToAnthropicStreamer) ensureMsgStarted() error {
	if c.msgStarted {
		return nil
	}
	c.msgStarted = true
	return c.writeEvent("message_start", streamMessageStart{
		Type: "message_start",
		Message: streamMessage{
			ID: c.msgID, Type: "message", Role: "assistant",
			Model: c.model, Usage: AnthropicUsage{}, Content: []any{},
		},
	})
}

func (c *ResponsesToAnthropicStreamer) ensureTextBlock() error {
	if err := c.ensureMsgStarted(); err != nil {
		return err
	}
	if c.textOpen {
		return nil
	}
	if err := c.closeTextBlock(); err != nil {
		return err
	}
	c.textOpen = true
	// Claim the index at open (like flushTool): deltas and the later stop
	// both address nextIndex-1 while this block is open. Incrementing only
	// at close made every text delta emit index -1, which strict clients
	// (Claude Code SDK) reject by aborting the whole stream.
	idx := c.nextIndex
	c.nextIndex++
	empty := ""
	return c.writeEvent("content_block_start", streamContentBlockStart{
		Type:  "content_block_start",
		Index: idx,
		ContentBlock: streamContentRef{
			Type: "text", Text: &empty,
		},
	})
}

func (c *ResponsesToAnthropicStreamer) closeTextBlock() error {
	if !c.textOpen {
		return nil
	}
	c.textOpen = false
	return c.writeEvent("content_block_stop", streamContentBlockStop{
		Type: "content_block_stop", Index: c.nextIndex - 1,
	})
}

// flushTool emits a buffered function call whole, unless already emitted or
// restricted. Unopened text is closed first so block indexes stay ordered.
func (c *ResponsesToAnthropicStreamer) flushTool(itemID string) error {
	p, ok := c.pending[itemID]
	if !ok || p.emitted {
		return nil
	}
	p.emitted = true
	if p.name == "" || (c.allowed != nil && !c.toolAllowed(p.name)) {
		return nil
	}
	if err := c.ensureMsgStarted(); err != nil {
		return err
	}
	if err := c.closeTextBlock(); err != nil {
		return err
	}
	callID := p.callID
	if callID == "" {
		callID = "call_" + randHex(24)
	}
	args := strings.TrimSpace(p.args.String())
	if args == "" {
		args = "{}"
	}
	idx := c.nextIndex
	c.nextIndex++
	if err := c.writeEvent("content_block_start", streamContentBlockStart{
		Type:  "content_block_start",
		Index: idx,
		ContentBlock: streamContentRef{
			Type: "tool_use", ID: callID, Name: p.name,
			Input: jsonRawMessage("{}"),
		},
	}); err != nil {
		return err
	}
	if err := c.writeEvent("content_block_delta", streamContentBlockDelta{
		Type: "content_block_delta", Index: idx,
		Delta: streamDelta{Type: "input_json_delta", PartialJSON: args},
	}); err != nil {
		return err
	}
	c.hasToolUse = true
	return c.writeEvent("content_block_stop", streamContentBlockStop{
		Type: "content_block_stop", Index: idx,
	})
}

func (c *ResponsesToAnthropicStreamer) toolAllowed(name string) bool {
	if c.allowed == nil {
		return true
	}
	if _, ok := c.allowed[name]; ok {
		return true
	}
	for allowed := range c.allowed {
		if strings.EqualFold(allowed, name) {
			return true
		}
	}
	return false
}

func (c *ResponsesToAnthropicStreamer) applyUsage(u *responsesStreamUsage) {
	if u == nil {
		return
	}
	c.usage.InputTokens = u.InputTokens
	c.usage.OutputTokens = u.OutputTokens
	c.usage.CacheReadInputTokens = u.EffectiveCachedTokens()
}

// Finalize emits message_delta/message_stop. Call once after the upstream
// stream ends; safe to call twice.
func (c *ResponsesToAnthropicStreamer) Finalize() error {
	return c.finalizeWith("stream_end")
}

func (c *ResponsesToAnthropicStreamer) finalizeWith(reason string) error {
	if c.finalized {
		return nil
	}
	c.finalized = true
	if c.failed != "" {
		return c.writeEvent("error", streamErrorEvent{
			Type:  "error",
			Error: AnthropicError{Type: "api_error", Message: c.failed},
		})
	}
	if err := c.ensureMsgStarted(); err != nil {
		return err
	}
	for _, itemID := range c.order {
		if err := c.flushTool(itemID); err != nil {
			return err
		}
	}
	if err := c.closeTextBlock(); err != nil {
		return err
	}
	stop := "end_turn"
	switch {
	case c.hasToolUse:
		stop = "tool_use"
	case c.incomplete:
		stop = "max_tokens"
	case reason == "upstream_error":
		stop = "stream_error"
	}
	if err := c.writeEvent("message_delta", streamMessageDelta{
		Type:  "message_delta",
		Delta: streamMessageBody{StopReason: &stop},
		Usage: streamDeltaUsage{
			InputTokens:              c.usage.InputTokens,
			CacheCreationInputTokens: c.usage.CacheCreationInputTokens,
			CacheReadInputTokens:     c.usage.CacheReadInputTokens,
			OutputTokens:             c.usage.OutputTokens,
		},
	}); err != nil {
		return err
	}
	return c.writeEvent("message_stop", streamMessageStop{Type: "message_stop"})
}

// EmitError writes an Anthropic error event (mid-stream failure surface).
func (c *ResponsesToAnthropicStreamer) EmitError(typ, msg string) error {
	c.finalized = true
	return c.writeEvent("error", streamErrorEvent{
		Type:  "error",
		Error: AnthropicError{Type: typ, Message: msg},
	})
}

// InputTokens, OutputTokens and CachedTokens report the usage observed on
// the upstream stream (zeros when the run never reached response.completed).
func (c *ResponsesToAnthropicStreamer) InputTokens() int  { return c.usage.InputTokens }
func (c *ResponsesToAnthropicStreamer) OutputTokens() int { return c.usage.OutputTokens }
func (c *ResponsesToAnthropicStreamer) CachedTokens() int { return c.usage.CacheReadInputTokens }

// StopReason reports the stop reason emitted in message_delta.
func (c *ResponsesToAnthropicStreamer) StopReason() string {
	switch {
	case c.hasToolUse:
		return "tool_use"
	case c.incomplete:
		return "max_tokens"
	default:
		return "end_turn"
	}
}

func (c *ResponsesToAnthropicStreamer) writeEvent(event string, payload any) error {
	b, err := jsonMarshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	return nil
}

// ---- upstream frame shapes (tolerant: unknown fields ignored) ----

type responsesStreamFrame struct {
	Type     string                  `json:"type"`
	ItemID   string                  `json:"item_id"`
	Delta    string                  `json:"delta"`
	Item     *responsesStreamItem    `json:"item"`
	Response *responsesStreamOutcome `json:"response"`
	Error    string                  `json:"error"`
}

type responsesStreamItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesStreamOutcome struct {
	Status string                `json:"status"`
	Error  string                `json:"error"`
	Usage  *responsesStreamUsage `json:"usage"`
}

type responsesStreamUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CachedTokens int `json:"cached_tokens"`
	Details      *struct {
		Cached int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (u *responsesStreamUsage) EffectiveCachedTokens() int {
	if u == nil {
		return 0
	}
	if u.Details != nil && u.Details.Cached > 0 {
		return u.Details.Cached
	}
	return u.CachedTokens
}
