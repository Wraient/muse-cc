package proxy

// Anthropic request/response types. Field names follow the Anthropic Messages
// API. Only the subset Claude Code actually sends is modelled; unknown fields
// are ignored by json decoding.

// AnthropicRequest is the body of POST /v1/messages.
type AnthropicRequest struct {
	Model       string              `json:"model"`
	Messages    []AnthropicMessage  `json:"messages"`
	System      AnthropicSystem     `json:"system,omitempty"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature *float64            `json:"temperature,omitempty"`
	TopP        *float64            `json:"top_p,omitempty"`
	TopK        *int                `json:"top_k,omitempty"`
	Stop        []string            `json:"stop_sequences,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
	Tools       []AnthropicTool     `json:"tools,omitempty"`
	ToolChoice  AnthropicToolChoice `json:"tool_choice,omitempty"`
	Thinking    *AnthropicThinking  `json:"thinking,omitempty"`
	Metadata    jsonRawMessage      `json:"metadata,omitempty"`
	// Other rarely-used fields are ignored.
}

// MarshalJSON omits Anthropic-only optional fields when they are empty. The
// standard encoding/json omitempty rule does not omit zero-value structs, which
// would otherwise send tool_choice: {"type":""} to native upstreams.
func (r AnthropicRequest) MarshalJSON() ([]byte, error) {
	type request struct {
		Model       string               `json:"model"`
		Messages    []AnthropicMessage   `json:"messages"`
		System      *AnthropicSystem     `json:"system,omitempty"`
		MaxTokens   int                  `json:"max_tokens"`
		Temperature *float64             `json:"temperature,omitempty"`
		TopP        *float64             `json:"top_p,omitempty"`
		TopK        *int                 `json:"top_k,omitempty"`
		Stop        []string             `json:"stop_sequences,omitempty"`
		Stream      bool                 `json:"stream,omitempty"`
		Tools       []AnthropicTool      `json:"tools,omitempty"`
		ToolChoice  *AnthropicToolChoice `json:"tool_choice,omitempty"`
		Thinking    *AnthropicThinking   `json:"thinking,omitempty"`
		Metadata    jsonRawMessage       `json:"metadata,omitempty"`
	}
	out := request{
		Model:       r.Model,
		Messages:    r.Messages,
		MaxTokens:   r.MaxTokens,
		Temperature: r.Temperature,
		TopP:        r.TopP,
		TopK:        r.TopK,
		Stop:        r.Stop,
		Stream:      r.Stream,
		Tools:       r.Tools,
		Thinking:    r.Thinking,
		Metadata:    r.Metadata,
	}
	if len(r.System.Blocks) > 0 {
		out.System = &r.System
	}
	if r.ToolChoice.Type != "" {
		out.ToolChoice = &r.ToolChoice
	}
	return jsonMarshal(out)
}

// AnthropicSystem is either a plain string or a list of content blocks. We
// accept both shapes via a custom unmarshaler.
type AnthropicSystem struct {
	Blocks []AnthropicContent `json:"-"`
}

// UnmarshalJSON accepts string or array forms.
func (s *AnthropicSystem) UnmarshalJSON(b []byte) error {
	// Try string first.
	if len(b) > 0 && b[0] == '"' {
		var str string
		if err := jsonUnmarshal(b, &str); err != nil {
			return err
		}
		s.Blocks = []AnthropicContent{{Type: "text", Text: str}}
		return nil
	}
	// Null or empty.
	if string(b) == "null" || len(b) == 0 {
		return nil
	}
	var blocks []AnthropicContent
	if err := jsonUnmarshal(b, &blocks); err != nil {
		return err
	}
	s.Blocks = blocks
	return nil
}

// MarshalJSON encodes as either null or an array.
func (s AnthropicSystem) MarshalJSON() ([]byte, error) {
	if len(s.Blocks) == 0 {
		return []byte("null"), nil
	}
	return jsonMarshal(s.Blocks)
}

// AnthropicMessage is one message in the conversation.
type AnthropicMessage struct {
	Role    string                  `json:"role"`
	Content AnthropicMessageContent `json:"content"`
}

// AnthropicMessageContent is either a string or an array of content blocks.
type AnthropicMessageContent struct {
	Text   string             `json:"-"`
	Blocks []AnthropicContent `json:"-"`
	IsStr  bool               `json:"-"`
}

// UnmarshalJSON accepts string or array forms.
func (c *AnthropicMessageContent) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var str string
		if err := jsonUnmarshal(b, &str); err != nil {
			return err
		}
		c.Text = str
		c.IsStr = true
		return nil
	}
	if string(b) == "null" || len(b) == 0 {
		return nil
	}
	var blocks []AnthropicContent
	if err := jsonUnmarshal(b, &blocks); err != nil {
		return err
	}
	c.Blocks = blocks
	return nil
}

// MarshalJSON re-encodes into the original shape.
func (c AnthropicMessageContent) MarshalJSON() ([]byte, error) {
	if c.IsStr {
		return jsonMarshal(c.Text)
	}
	if len(c.Blocks) == 0 {
		return []byte("null"), nil
	}
	return jsonMarshal(c.Blocks)
}

// AnthropicContent is a single content block. We keep it permissive: only the
// fields relevant to conversion are strongly typed, the rest live in Raw.
type AnthropicContent struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image / video
	// Source carries the media payload for "image" blocks and for the
	// proxy-specific "video" block (base64 mp4 or remote URL). The struct
	// name is historical; it is media-generic.
	Source *AnthropicImageSource `json:"source,omitempty"`

	// tool_use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input jsonRawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string                   `json:"tool_use_id,omitempty"`
	Content   *AnthropicMessageContent `json:"content,omitempty"`
	IsError   bool                     `json:"is_error,omitempty"`

	// web_search_tool_result (server tool)
	WebSearchResults []AnthropicWebSearchResult `json:"-"`
	WebSearchError   *AnthropicWebSearchError   `json:"-"`

	// thinking (extended) — passed through as-is via Raw when present
	Thinking string `json:"thinking,omitempty"`

	// cache_control marks Anthropic prompt-cache boundaries. OpenAI-compatible
	// upstreams do not understand it directly, but the converter uses it to
	// choose a more precise prompt_cache_key.
	CacheControl jsonRawMessage `json:"cache_control,omitempty"`

	// Catch-all for anything we don't specifically model.
	Raw jsonRawMessage `json:"-"`
}

// MarshalJSON handles content blocks whose "content" field is not an
// AnthropicMessageContent, notably server-side web search results.
func (c AnthropicContent) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case "server_tool_use":
		type serverToolUse struct {
			Type  string         `json:"type"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input jsonRawMessage `json:"input"`
		}
		input := c.Input
		if len(input) == 0 {
			input = jsonRawMessage(`{}`)
		}
		return jsonMarshal(serverToolUse{
			Type:  c.Type,
			ID:    c.ID,
			Name:  c.Name,
			Input: input,
		})
	case "web_search_tool_result":
		type webSearchToolResult struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   any    `json:"content"`
		}
		var content any = c.WebSearchResults
		if c.WebSearchError != nil {
			content = c.WebSearchError
		}
		return jsonMarshal(webSearchToolResult{
			Type:      c.Type,
			ToolUseID: c.ToolUseID,
			Content:   content,
		})
	default:
		type alias AnthropicContent
		return jsonMarshal(alias(c))
	}
}

// AnthropicWebSearchResult mirrors Anthropic's server-side web_search result
// block enough for Claude clients to recognize that a search happened.
type AnthropicWebSearchResult struct {
	Type             string `json:"type"`
	Title            string `json:"title"`
	URL              string `json:"url"`
	URI              string `json:"uri,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
	PageAge          string `json:"page_age,omitempty"`
}

type AnthropicWebSearchError struct {
	Type      string `json:"type"`
	ErrorCode string `json:"error_code"`
}

// AnthropicImageSource describes an image source (base64 only; url support is
// upstream-dependent).
type AnthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url,omitempty"`
}

// AnthropicTool is a tool definition.
type AnthropicTool struct {
	Type         string         `json:"type,omitempty"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  jsonRawMessage `json:"input_schema"`
	MaxUses      int            `json:"max_uses,omitempty"`
	CacheControl jsonRawMessage `json:"cache_control,omitempty"`
}

// AnthropicToolChoice mirrors the tool_choice object.
type AnthropicToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	// disable_parallel_tools is ignored.
}

// AnthropicThinking is the top-level extended-thinking request option sent by
// Claude Code when the user enables thinking.
type AnthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	// Effort carries newer clients' named thinking level (e.g. "max").
	// Unknown names clamp to the model's maximum, never fail.
	Effort string `json:"effort,omitempty"`
}

// ---- Non-streaming response ----

// AnthropicResponse is the body of a non-streaming /v1/messages response.
type AnthropicResponse struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Role         string             `json:"role"`
	Model        string             `json:"model"`
	Content      []AnthropicContent `json:"content"`
	StopReason   *string            `json:"stop_reason"`
	StopSequence *string            `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage     `json:"usage"`
}

// AnthropicUsage is the usage block.
type AnthropicUsage struct {
	InputTokens              int                     `json:"input_tokens"`
	OutputTokens             int                     `json:"output_tokens"`
	CacheReadInputTokens     int                     `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int                     `json:"cache_creation_input_tokens,omitempty"`
	ServerToolUse            *AnthropicServerToolUse `json:"server_tool_use,omitempty"`
}

type AnthropicServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests,omitempty"`
}

// CountTokensResponse is returned by /v1/messages/count_tokens.
type CountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

// AnthropicError is the body of an error response.
type AnthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// SSE event payloads and the streaming state machine live in stream.go — they
// are built there with exact wire-format structs to match the Anthropic spec.
