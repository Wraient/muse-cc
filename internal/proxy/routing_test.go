package proxy

import "testing"

func TestIsNativeAnthropicModel(t *testing.T) {
	cases := map[string]bool{
		"claude-fable-5-1":                true,
		"anthropic/claude-fable-5-1":      true,
		"qwen3-max":                       true,
		"gpt-5-mini":                      false,
		"muse-spark-1.3-contributor-free": true, // Meta serves /v1/messages natively
		"":                                false,
	}
	for in, want := range cases {
		if got := IsNativeAnthropicModel(in); got != want {
			t.Errorf("IsNativeAnthropicModel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsResponsesNativeModel(t *testing.T) {
	cases := map[string]bool{
		"muse-spark-1.3-contributor-free":          true,
		"muse-spark-1.2-contributor-free":          true,
		"muse-spark-1.2":                           true,
		"MUSE-Spark-1.3-Contributor-Free":          true, // case-insensitive
		"provider/muse-spark-1.3-contributor-free": true, // prefix stripped
		"muse-ember-1.0":                           true, // Responses is the only open surface
		"muse-wonder-1.0":                          true,
		"muse-wonder-1.0-contributor":              true,
		"gpt-5-mini":                               false,
		"big-pickle":                               false,
		"claude-fable-5-1":                         false,
		"":                                         false,
	}
	for in, want := range cases {
		if got := IsResponsesNativeModel(in); got != want {
			t.Errorf("IsResponsesNativeModel(%q) = %v, want %v", in, got, want)
		}
	}
}
