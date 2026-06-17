package proxy

import "strings"

// IsNativeAnthropicModel reports whether a Zen model id should use the native
// Anthropic Messages upstream path instead of OpenAI-compatible translation.
func IsNativeAnthropicModel(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if slash := strings.IndexByte(model, '/'); slash >= 0 {
		model = model[slash+1:]
	}
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "qwen")
}
