package server

import (
	"github.com/Wraient/muse-cc/internal/config"
	"github.com/Wraient/muse-cc/internal/proxy"
)

func promptCacheOptionsFromConfig(cfg *config.Config) proxy.PromptCacheOptions {
	if cfg == nil {
		return proxy.PromptCacheOptions{}
	}
	return proxy.PromptCacheOptions{
		Enabled:               cfg.PromptCacheEnabled,
		KeyPrefix:             cfg.PromptCacheKeyPrefix,
		AnthropicCacheControl: cfg.PromptCacheAnthropicControl,
		NormalizePrompts:      cfg.PromptCacheNormalize,
	}
}
