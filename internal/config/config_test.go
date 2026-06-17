package config

import "testing"

func TestResolveModelStripsProviderPrefix(t *testing.T) {
	c := Default()
	c.DefaultModel = "glm-5.1"
	c.ModelMappings = []ModelMapping{{Match: "*", Target: ""}} // pass-through

	cases := []struct{ in, want string }{
		{"anthropic/kimi-k2.7-code", "kimi-k2.7-code"}, // Claude Code style
		{"openai/gpt-5.2", "gpt-5.2"},
		{"kimi-k2.7-code", "kimi-k2.7-code"}, // already bare
		{"glm-5.1", "glm-5.1"},               // already bare
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
		{Match: "claude-3-5-sonnet", Target: "glm-5.1"},
		{Match: "*", Target: ""},
	}
	if got := c.ResolveModel("claude-3-5-sonnet-20241022"); got != "glm-5.1" {
		t.Errorf("explicit mapping: got %q, want glm-5.1", got)
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
		c.PromptCacheKeyPrefix != "opencode-cc" ||
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
