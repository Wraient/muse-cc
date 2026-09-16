package config

import "testing"

func TestResolveModelStripsProviderPrefix(t *testing.T) {
	c := Default()
	c.DefaultModel = "muse-spark-1.3-contributor"
	c.ModelMappings = []ModelMapping{{Match: "*", Target: ""}} // pass-through

	cases := []struct{ in, want string }{
		{"anthropic/kimi-k2.7-code", "kimi-k2.7-code"}, // Claude Code style
		{"openai/gpt-5.2", "gpt-5.2"},
		{"kimi-k2.7-code", "kimi-k2.7-code"},                         // already bare
		{"muse-spark-1.3-contributor", "muse-spark-1.3-contributor"}, // already bare
		{"anthropic/claude-sonnet-4-5", "claude-sonnet-4-5"},
	}
	for _, tc := range cases {
		got := c.ResolveModel(tc.in)
		if got != tc.want {
			t.Errorf("ResolveModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveModelExplicitMappingWins(t *testing.T) {
	c := Default()
	c.ModelMappings = []ModelMapping{
		{Match: "claude-3-5-sonnet", Target: "muse-spark-1.3-contributor"},
		{Match: "*", Target: ""},
	}
	if got := c.ResolveModel("claude-3-5-sonnet-20241022"); got != "muse-spark-1.3-contributor" {
		t.Errorf("explicit mapping: got %q, want muse-spark-1.3-contributor", got)
	}
	if got := c.ResolveModel("anthropic/kimi-k2.7-code"); got != "kimi-k2.7-code" {
		t.Errorf("pass-through w/ prefix: got %q, want kimi-k2.7-code", got)
	}
}

func TestNativeAnthropicConfigPatch(t *testing.T) {
	c := Default()
	if !c.NativeAnthropic {
		t.Fatal("NativeAnthropic default = false, want true")
	}
	disabled := false
	c.ApplyPatch(&Patch{NativeAnthropic: &disabled})
	if c.NativeAnthropic {
		t.Fatal("NativeAnthropic was not disabled by patch")
	}
	if c.Snapshot().NativeAnthropic {
		t.Fatal("Snapshot did not preserve NativeAnthropic")
	}
}

func TestPromptCacheConfigPatch(t *testing.T) {
	c := Default()
	if !c.PromptCacheEnabled ||
		c.PromptCacheKeyPrefix != "muse-cc" ||
		!c.PromptCacheAnthropicControl ||
		!c.PromptCacheNormalize {
		t.Fatalf("unexpected prompt cache defaults: %+v", c)
	}

	disabled := false
	prefix := "local-dev"
	c.ApplyPatch(&Patch{
		PromptCacheEnabled:          &disabled,
		PromptCacheKeyPrefix:        &prefix,
		PromptCacheAnthropicControl: &disabled,
		PromptCacheNormalize:        &disabled,
	})
	snap := c.Snapshot()
	if snap.PromptCacheEnabled ||
		snap.PromptCacheKeyPrefix != "local-dev" ||
		snap.PromptCacheAnthropicControl ||
		snap.PromptCacheNormalize {
		t.Fatalf("prompt cache patch/snapshot mismatch: %+v", snap)
	}
}

func TestWebSearchModelConfigPatch(t *testing.T) {
	c := Default()
	if got := c.ResolveWebSearchModel("glm-main"); got != "glm-main" {
		t.Fatalf("default web search model = %q, want fallback", got)
	}
	if got := c.ResolveWebSearchMode(); got != WebSearchModeAuto {
		t.Fatalf("default web search mode = %q, want auto", got)
	}

	model := "anthropic/glm-cheap"
	mode := "shim"
	baseURL := "https://api.deepseek.com/anthropic/"
	apiKey := "search-key"
	c.ApplyPatch(&Patch{
		WebSearchModel:   &model,
		WebSearchMode:    &mode,
		WebSearchBaseURL: &baseURL,
		WebSearchAPIKey:  &apiKey,
	})
	snap := c.Snapshot()
	if snap.WebSearchModel != "glm-cheap" ||
		snap.WebSearchMode != WebSearchModeTranslate ||
		snap.WebSearchBaseURL != "https://api.deepseek.com/anthropic" ||
		snap.WebSearchAPIKey != "search-key" {
		t.Fatalf("web search model patch/snapshot mismatch: %+v", snap)
	}
	if got := c.ResolveWebSearchModel("glm-main"); got != "glm-cheap" {
		t.Fatalf("ResolveWebSearchModel = %q, want glm-cheap", got)
	}
	if got := c.ResolveWebSearchMode(); got != WebSearchModeTranslate {
		t.Fatalf("ResolveWebSearchMode = %q, want translate", got)
	}
	gotBase, gotKey := c.ResolveWebSearchUpstream("https://main.example", "main-key")
	if gotBase != "https://api.deepseek.com/anthropic" || gotKey != "search-key" {
		t.Fatalf("ResolveWebSearchUpstream = %q/%q", gotBase, gotKey)
	}

	clear := ""
	c.ApplyPatch(&Patch{WebSearchModel: &clear})
	if got := c.ResolveWebSearchModel("glm-main"); got != "glm-main" {
		t.Fatalf("cleared web search model = %q, want fallback", got)
	}

	c = Default()
	gotBase, gotKey = c.ResolveWebSearchUpstream("https://main.example", "main-key")
	if gotBase != "https://main.example" || gotKey != "main-key" {
		t.Fatalf("default ResolveWebSearchUpstream = %q/%q", gotBase, gotKey)
	}
}

func TestResolveThinkingBudgetMapping(t *testing.T) {
	c := Default()

	glmMapping, ok := c.ResolveThinkingBudgetMapping("openai/glm-5.2")
	if !ok {
		t.Fatal("expected default GLM thinking mapping")
	}
	if glmMapping.Field != "thinking" {
		t.Fatalf("unexpected GLM mapping: %+v", glmMapping)
	}

	mapping, ok := c.ResolveThinkingBudgetMapping("anthropic/kimi-k2.7-code")
	if !ok {
		t.Fatal("expected default Kimi thinking budget mapping")
	}
	if mapping.Field != "thinking_budget" || mapping.Max != 16384 {
		t.Fatalf("unexpected mapping: %+v", mapping)
	}

	if _, ok := c.ResolveThinkingBudgetMapping("deepseek-v4-flash"); ok {
		t.Fatal("DeepSeek should not receive an explicit thinking budget field by default")
	}

	custom := []ThinkingBudgetMapping{{Match: "deepseek-", Field: "reasoning_effort"}}
	c.ApplyPatch(&Patch{ThinkingBudgetMappings: &custom})
	snap := c.Snapshot()
	if len(snap.ThinkingBudgetMappings) != 1 || snap.ThinkingBudgetMappings[0].Field != "reasoning_effort" {
		t.Fatalf("thinking budget patch/snapshot mismatch: %+v", snap.ThinkingBudgetMappings)
	}
}
