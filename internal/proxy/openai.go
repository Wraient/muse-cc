package proxy

import (
	"encoding/json"
	"strings"
)

// OpenAI Chat Completions types — the format we send to Meta and receive from
// it. Only the fields needed for conversion are modelled.

// OpenAIRequest is POST /v1/chat/completions.
type OpenAIRequest struct {
	Model                string               `json:"model"`
	Messages             []OpenAIMessage      `json:"messages"`
	MaxTokens            *int                 `json:"max_tokens,omitempty"`
	Temperature          *float64             `json:"temperature,omitempty"`
	TopP                 *float64             `json:"top_p,omitempty"`
	Stream               bool                 `json:"stream,omitempty"`
	StreamOptions        *OpenAIStreamOptions `json:"stream_options,omitempty"`
	Stop                 []string             `json:"stop,omitempty"`
	Tools                []OpenAITool         `json:"tools,omitempty"`
	ToolChoice           any                  `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool                `json:"parallel_tool_calls,omitempty"`
	PromptCacheKey       string               `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string               `json:"prompt_cache_retention,omitempty"`
	PromptCacheHint      string               `json:"-"`
	ReasoningEffort      string               `json:"reasoning_effort,omitempty"`
	ThinkingBudget       *int                 `json:"thinking_budget,omitempty"`
	Thinking             *OpenAIThinking      `json:"thinking,omitempty"`
}

// OpenAIThinking carries provider-specific thinking controls used by GLM.
type OpenAIThinking struct {
	Type          string `json:"type"`
	BudgetTokens  *int   `json:"budget_tokens,omitempty"`
	ClearThinking *bool  `json:"clear_thinking,omitempty"`
}

// OpenAIStreamOptions asks the upstream to include usage in the final chunk.
type OpenAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// OpenAIMessage is one chat message.
type OpenAIMessage struct {
	Role             string              `json:"role"`
	Content          any                 `json:"content,omitempty"` // string or []OpenAIContentPart
	ReasoningContent string              `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall    `json:"tool_calls,omitempty"`
	FunctionCall     *OpenAIFunctionCall `json:"function_call,omitempty"`
	ToolCallID       string              `json:"tool_call_id,omitempty"`
	Name             string              `json:"name,omitempty"`
}

// OpenAIContentPart is one part of a multi-part message (text / image / video).
type OpenAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *OpenAIImageURL `json:"image_url,omitempty"`
	VideoURL *OpenAIVideoURL `json:"video_url,omitempty"`
}

// OpenAIImageURL wraps the url (data: or http(s):).
type OpenAIImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// OpenAIVideoURL wraps a video url (data:video/mp4 or http(s):). Meta
// muse-spark models accept video_url parts on Chat Completions.
type OpenAIVideoURL struct {
	URL string `json:"url"`
}

// OpenAIToolCall is a tool invocation produced by the model.
type OpenAIToolCall struct {
	Index    int                `json:"index,omitempty"`
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function OpenAIFunctionCall `json:"function"`
}

// OpenAIFunctionCall is the function payload of a tool call.
type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

func (f *OpenAIFunctionCall) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Name = raw.Name
	f.Arguments = ""
	if len(raw.Arguments) == 0 || strings.TrimSpace(string(raw.Arguments)) == "null" {
		return nil
	}
	var args string
	if err := json.Unmarshal(raw.Arguments, &args); err == nil {
		f.Arguments = args
		return nil
	}
	if canonical, ok := canonicalJSON(raw.Arguments); ok {
		f.Arguments = string(canonical)
		return nil
	}
	f.Arguments = strings.TrimSpace(string(raw.Arguments))
	return nil
}

// OpenAITool is a tool definition in the request.
type OpenAITool struct {
	Type     string             `json:"type"` // "function"
	Function OpenAIToolFunction `json:"function"`
}

// OpenAIToolFunction is the function schema.
type OpenAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  jsonRawMessage `json:"parameters"`
}

// ---- Streaming chunks ----

// OpenAIStreamChunk is one SSE "data:" line from the upstream.
type OpenAIStreamChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

// OpenAIChoice is one choice in a chunk.
type OpenAIChoice struct {
	Index        int            `json:"index"`
	Delta        OpenAIDelta    `json:"delta,omitempty"`
	Message      *OpenAIMessage `json:"message,omitempty"`
	FinishReason *string        `json:"finish_reason"`
}

// OpenAIDelta is the streaming delta.
type OpenAIDelta struct {
	Role             string              `json:"role,omitempty"`
	Content          string              `json:"content,omitempty"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall    `json:"tool_calls,omitempty"`
	FunctionCall     *OpenAIFunctionCall `json:"function_call,omitempty"`
}

// OpenAIUsage is the usage block.
type OpenAIUsage struct {
	PromptTokens        int                        `json:"prompt_tokens"`
	CompletionTokens    int                        `json:"completion_tokens"`
	TotalTokens         int                        `json:"total_tokens"`
	PromptTokensDetails *OpenAIPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// OpenAIPromptTokensDetails carries provider prompt-cache accounting when an
// OpenAI-compatible upstream exposes it.
type OpenAIPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// CachedPromptTokens returns the cached-token count reported by the upstream.
func (u OpenAIUsage) CachedPromptTokens() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CachedTokens
}

// ---- Non-streaming response ----

// OpenAIResponse is the body of a non-streaming completion.
type OpenAIResponse struct {
	ID      string         `json:"id"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

// OpenAIModelList is /v1/models.
type OpenAIModelList struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

// OpenAIModel is one entry in the model list.
type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by,omitempty"`
}
