// Package config holds all runtime configuration for opencode-cc.
// Configuration is loaded with the following precedence (highest first):
//  1. environment variables
//  2. config.json (persisted from the web panel)
//  3. built-in defaults
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Default values used when nothing else is configured.
const (
	DefaultListenAddr            = ":8787"
	DefaultUpstreamBase          = "https://opencode.ai/zen"
	DefaultDefaultModel          = "glm-4.6"
	DefaultDataDir               = "data"
	DefaultConfigFile            = "config.json"
	DefaultOpenCodeGoWorkspaceID = "Default"

	upstreamCooldownBase = 30 * time.Second
	upstreamCooldownMax  = 5 * time.Minute
)

// ModelMapping maps an incoming Anthropic model name (often "claude-*") to the
// target model that Zen's /v1/chat/completions endpoint understands.
type ModelMapping struct {
	// Match is the pattern to match against the incoming model name.
	// Use "*" to match everything, or a prefix like "claude-".
	Match string `json:"match"`
	// Target is the model name sent to the upstream.
	Target string `json:"target"`
}

// Upstream is one backend (base URL + API key) the proxy can forward to.
// Multiple upstreams form a pool that is round-robined per request.
type Upstream struct {
	ID      string `json:"id,omitempty"` // stable row id used to preserve secrets across reorder/delete
	BaseURL string `json:"base_url"`     // e.g. https://opencode.ai/zen/go or https://opencode.ai/zen/
	APIKey  string `json:"api_key"`
	Name    string `json:"name"`    // optional human label
	Enabled bool   `json:"enabled"` // skip when false
	// Optional OpenCode Go dashboard credentials used only for quota display.
	OpenCodeGoWorkspaceID string `json:"opencode_go_workspace_id,omitempty"`
	OpenCodeGoAuthCookie  string `json:"opencode_go_auth_cookie,omitempty"`
	OpenCodeGoShowRolling *bool  `json:"opencode_go_show_rolling,omitempty"`
	OpenCodeGoShowWeekly  *bool  `json:"opencode_go_show_weekly,omitempty"`
	OpenCodeGoShowMonthly *bool  `json:"opencode_go_show_monthly,omitempty"`
}

// UpstreamSelection is the concrete upstream picked for one proxy request.
// It is reported back to the config after the attempt so temporary failures can
// cool down without disabling the account permanently.
type UpstreamSelection struct {
	ID      string
	BaseURL string
	APIKey  string
}

type upstreamHealth struct {
	Failures      int
	CooldownUntil time.Time
	Reason        string
}

// ThinkingBudgetMapping maps Anthropic extended-thinking budgets to model-
// specific OpenAI-compatible request fields. Field currently supports
// "thinking", "thinking_budget" and "reasoning_effort"; empty/"none" disables
// forwarding.
type ThinkingBudgetMapping struct {
	Match  string `json:"match"`
	Field  string `json:"field"`
	Low    int    `json:"low,omitempty"`
	Medium int    `json:"medium,omitempty"`
	High   int    `json:"high,omitempty"`
	Max    int    `json:"max,omitempty"`
}

// Config is the full application configuration.
type Config struct {
	ListenAddr string `json:"listen_addr"`
	// UpstreamBase is the Zen base URL without a trailing slash, e.g.
	// "https://opencode.ai/zen".
	UpstreamBase string `json:"upstream_base"`
	// NativeAnthropic enables smart native routing. Anthropic-native target
	// models (claude-*, qwen*) use <upstream>/v1/messages; other target models
	// are translated through OpenAI-compatible endpoints.
	NativeAnthropic bool `json:"native_anthropic"`
	// ZenAPIKey is the bearer token used to authenticate against Zen.
	ZenAPIKey string `json:"zen_api_key"`
	// Upstreams is the round-robin pool of backends. When non-empty it takes
	// precedence over the legacy single UpstreamBase/ZenAPIKey fields; those
	// are kept for backward compatibility and auto-migration.
	Upstreams []Upstream `json:"upstreams"`
	// PanelToken gates access to the web panel and its API. If empty the panel
	// is open (convenient for local use). Set one before exposing the port.
	PanelToken string `json:"panel_token"`
	// RequireAPIKey gates the /v1/* proxy endpoints behind a valid client API
	// key. When true, requests must carry a Bearer key matching one in the
	// api_keys table. When false, access is open for local single-user use.
	RequireAPIKey bool `json:"require_api_key"`
	// DefaultModel is used when the incoming request has no model and no
	// mapping matches.
	DefaultModel string `json:"default_model"`
	// ModelMappings are evaluated in order; first match wins. The final
	// entry should have Match:"*" as a catch-all.
	ModelMappings []ModelMapping `json:"model_mappings"`
	// LogRequests records each request/response to SQLite for the panel.
	LogRequests bool `json:"log_requests"`
	// MaxBodyLogBytes caps how much of a request/response body is stored.
	MaxBodyLogBytes int `json:"max_body_log_bytes"`
	// RequestTimeoutSeconds is the upstream timeout. 0 = no timeout (streams
	// can run long); a sane upper bound is still recommended.
	RequestTimeoutSeconds int `json:"request_timeout_seconds"`
	// PromptCacheEnabled enables request normalization and upstream prompt-cache
	// hints that improve cache hits without changing user-visible prompt text.
	PromptCacheEnabled bool `json:"prompt_cache_enabled"`
	// PromptCacheKeyPrefix prefixes automatically generated prompt_cache_key
	// values for OpenAI-compatible upstream requests.
	PromptCacheKeyPrefix string `json:"prompt_cache_key_prefix"`
	// PromptCacheAnthropicControl adds Anthropic cache_control markers when the
	// request has a stable system/tool prefix and no marker was provided.
	PromptCacheAnthropicControl bool `json:"prompt_cache_anthropic_control"`
	// PromptCacheNormalize keeps cacheable request structure stable: system
	// messages first, sorted tools/context blocks, and volatile metadata removed.
	PromptCacheNormalize bool `json:"prompt_cache_normalize"`
	// ThinkingBudgetMappings are evaluated by target model. They translate
	// Anthropic thinking budget_tokens into provider-specific request fields.
	ThinkingBudgetMappings []ThinkingBudgetMapping `json:"thinking_budget_mappings"`

	dataDir    string
	configPath string
	mu         sync.RWMutex
	rr         uint64 // round-robin cursor for NextUpstream (atomic)

	healthMu sync.Mutex
	health   map[string]upstreamHealth
}

// Patch represents a partial update from the control panel. Pointer fields
// distinguish omitted JSON properties from explicit zero values.
type Patch struct {
	ListenAddr                  *string                  `json:"listen_addr"`
	UpstreamBase                *string                  `json:"upstream_base"`
	NativeAnthropic             *bool                    `json:"native_anthropic"`
	ZenAPIKey                   *string                  `json:"zen_api_key"`
	Upstreams                   *[]Upstream              `json:"upstreams"`
	PanelToken                  *string                  `json:"panel_token"`
	RequireAPIKey               *bool                    `json:"require_api_key"`
	DefaultModel                *string                  `json:"default_model"`
	ModelMappings               *[]ModelMapping          `json:"model_mappings"`
	LogRequests                 *bool                    `json:"log_requests"`
	MaxBodyLogBytes             *int                     `json:"max_body_log_bytes"`
	RequestTimeoutSeconds       *int                     `json:"request_timeout_seconds"`
	PromptCacheEnabled          *bool                    `json:"prompt_cache_enabled"`
	PromptCacheKeyPrefix        *string                  `json:"prompt_cache_key_prefix"`
	PromptCacheAnthropicControl *bool                    `json:"prompt_cache_anthropic_control"`
	PromptCacheNormalize        *bool                    `json:"prompt_cache_normalize"`
	ThinkingBudgetMappings      *[]ThinkingBudgetMapping `json:"thinking_budget_mappings"`
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		ListenAddr:                  DefaultListenAddr,
		UpstreamBase:                DefaultUpstreamBase,
		NativeAnthropic:             true,
		RequireAPIKey:               false, // open by default for single-user local use
		DefaultModel:                DefaultDefaultModel,
		ModelMappings:               DefaultModelMappings(),
		LogRequests:                 true,
		MaxBodyLogBytes:             1 << 14, // 16 KiB per body side
		RequestTimeoutSeconds:       0,
		PromptCacheEnabled:          true,
		PromptCacheKeyPrefix:        "opencode-cc",
		PromptCacheAnthropicControl: true,
		PromptCacheNormalize:        true,
		ThinkingBudgetMappings:      DefaultThinkingBudgetMappings(),
	}
}

// DefaultModelMappings returns the built-in mapping table.
//
// The default is a single pass-through rule: the incoming model name is
// forwarded to the upstream verbatim. This means you send the real Zen model
// id (e.g. glm-5.1, kimi-k2.7-code) as the "model" field and it's used as-is.
// Add specific rules if you want to rename models.
func DefaultModelMappings() []ModelMapping {
	return []ModelMapping{
		{Match: "*", Target: ""}, // pass-through
	}
}

// DefaultThinkingBudgetMappings keeps provider-specific thinking controls off
// by default except where Zen-compatible model families document a matching
// extension field.
func DefaultThinkingBudgetMappings() []ThinkingBudgetMapping {
	return []ThinkingBudgetMapping{
		{Match: "glm-", Field: "thinking"},
		{Match: "kimi-", Field: "thinking_budget", Low: 1024, Medium: 4096, High: 8192, Max: 16384},
		{Match: "moonshot-", Field: "thinking_budget", Low: 1024, Medium: 4096, High: 8192, Max: 16384},
	}
}

// DataDir returns the directory used for SQLite + config persistence.
func (c *Config) DataDir() string { return c.dataDir }

// Load reads config.json from dataDir, applies env overrides, and returns the
// merged Config. If the file does not exist a default config is returned (and
// the file is not created here — call Save to persist).
func Load(dataDir string) (*Config, error) {
	c := Default()
	c.dataDir = dataDir
	c.configPath = filepath.Join(dataDir, DefaultConfigFile)

	if b, err := os.ReadFile(c.configPath); err == nil {
		// Merge onto defaults so newly-added fields keep their defaults.
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	c.applyEnv()
	c.migrateLegacyUpstream()
	c.ensureUpstreamIDs()
	return c, nil
}

// migrateLegacyUpstream promotes the legacy single UpstreamBase + ZenAPIKey
// pair into the Upstreams pool when the pool is empty. This makes pre-existing
// config.json files upgrade transparently. Idempotent.
func (c *Config) migrateLegacyUpstream() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.Upstreams) > 0 {
		return
	}
	if c.UpstreamBase != "" && c.ZenAPIKey != "" {
		c.Upstreams = []Upstream{{
			ID:                    newUpstreamID(),
			BaseURL:               strings.TrimRight(c.UpstreamBase, "/"),
			APIKey:                c.ZenAPIKey,
			Enabled:               true,
			OpenCodeGoWorkspaceID: DefaultOpenCodeGoWorkspaceID,
			OpenCodeGoShowRolling: boolPtr(true),
			OpenCodeGoShowWeekly:  boolPtr(true),
			OpenCodeGoShowMonthly: boolPtr(true),
		}}
	}
}

// NextUpstream returns the next backend by round-robin for a request.
// Enabled upstreams with a non-empty key are cycled through atomically. If the
// Upstreams pool is empty it falls back to the legacy single fields. ok is false
// when no usable upstream is configured (the caller should respond with an
// "upstream not configured" error).
//
// Temporarily cooled-down upstreams are skipped. If all candidates are cooling
// down, ok is false so callers can fail locally instead of repeatedly hammering
// an unavailable upstream. Once the cooldown expires, the upstream naturally
// becomes eligible again.
func (c *Config) NextUpstream() (UpstreamSelection, bool) {
	return c.NextUpstreamExcluding(nil)
}

// NextUpstreamExcluding is like NextUpstream but skips upstream IDs already
// tried by the current client request.
func (c *Config) NextUpstreamExcluding(exclude map[string]bool) (UpstreamSelection, bool) {
	c.mu.RLock()
	// Snapshot the enabled pool under the read lock.
	pool := make([]Upstream, 0, len(c.Upstreams))
	hasUpstreamPool := len(c.Upstreams) > 0
	for _, u := range c.Upstreams {
		if u.Enabled && u.APIKey != "" && !exclude[upstreamRuntimeID(u)] {
			pool = append(pool, u)
		}
	}
	legacyBase, legacyKey := c.UpstreamBase, c.ZenAPIKey
	c.mu.RUnlock()

	if len(pool) == 0 {
		if !hasUpstreamPool && legacyBase != "" && legacyKey != "" {
			sel := UpstreamSelection{
				ID:      legacyUpstreamID(legacyBase, legacyKey),
				BaseURL: strings.TrimRight(legacyBase, "/"),
				APIKey:  legacyKey,
			}
			if exclude[sel.ID] {
				return UpstreamSelection{}, false
			}
			if c.selectionCooling(sel, time.Now()) {
				return UpstreamSelection{}, false
			}
			return sel, true
		}
		return UpstreamSelection{}, false
	}
	pool = c.filterCoolingUpstreams(pool, time.Now())
	if len(pool) == 0 {
		return UpstreamSelection{}, false
	}
	// Atomic round-robin: advance the cursor and pick modulo the pool size.
	// AddUint64 returns the new value; subtract 1 so a fresh config starts at
	// the first enabled upstream in the configured order.
	n := uint64(len(pool))
	idx := (atomic.AddUint64(&c.rr, 1) - 1) % n
	u := pool[idx]
	return UpstreamSelection{ID: upstreamRuntimeID(u), BaseURL: strings.TrimRight(u.BaseURL, "/"), APIKey: u.APIKey}, true
}

// HasConfiguredUpstream reports whether at least one enabled upstream with a
// key exists, ignoring temporary cooldown state.
func (c *Config) HasConfiguredUpstream() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.Upstreams) > 0 {
		for _, u := range c.Upstreams {
			if u.Enabled && u.APIKey != "" {
				return true
			}
		}
		return false
	}
	for _, u := range c.Upstreams {
		if u.Enabled && u.APIKey != "" {
			return true
		}
	}
	return c.UpstreamBase != "" && c.ZenAPIKey != ""
}

func (c *Config) filterCoolingUpstreams(pool []Upstream, now time.Time) []Upstream {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()

	active := make([]Upstream, 0, len(pool))
	for _, u := range pool {
		id := upstreamRuntimeID(u)
		st, cooling := c.health[id]
		if !cooling || st.CooldownUntil.IsZero() || !now.Before(st.CooldownUntil) {
			active = append(active, u)
			continue
		}
	}
	return active
}

func (c *Config) selectionCooling(sel UpstreamSelection, now time.Time) bool {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	st, ok := c.health[sel.ID]
	return ok && !st.CooldownUntil.IsZero() && now.Before(st.CooldownUntil)
}

// ReportUpstreamResult updates the temporary health state for one selected
// upstream. Success clears cooldown. Failures use exponential cooldown:
// 30s, 1m, 2m, 4m, capped at 5m.
func (c *Config) ReportUpstreamResult(sel UpstreamSelection, success bool, reason string) {
	id := strings.TrimSpace(sel.ID)
	if id == "" {
		id = legacyUpstreamID(sel.BaseURL, sel.APIKey)
	}
	if id == "" {
		return
	}

	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	if success {
		delete(c.health, id)
		return
	}
	if c.health == nil {
		c.health = make(map[string]upstreamHealth)
	}
	st := c.health[id]
	st.Failures++
	delay := upstreamCooldownBase
	for i := 1; i < st.Failures; i++ {
		delay *= 2
		if delay >= upstreamCooldownMax {
			delay = upstreamCooldownMax
			break
		}
	}
	st.CooldownUntil = time.Now().Add(delay)
	st.Reason = strings.TrimSpace(reason)
	c.health[id] = st
}

func (c *Config) resetUpstreamHealthLocked() {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	c.health = nil
}

// applyEnv overlays environment variables on top of the loaded config.
func (c *Config) applyEnv() {
	if v := os.Getenv("OPENCODE_CC_LISTEN"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("OPENCODE_CC_UPSTREAM"); v != "" {
		c.UpstreamBase = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("OPENCODE_CC_NATIVE_ANTHROPIC"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.NativeAnthropic = b
		}
	}
	if v := os.Getenv("ZEN_API_KEY"); v != "" {
		c.ZenAPIKey = v
	}
	if v := os.Getenv("OPENCODE_CC_PANEL_TOKEN"); v != "" {
		c.PanelToken = v
	}
	if v := os.Getenv("OPENCODE_CC_REQUIRE_API_KEY"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.RequireAPIKey = b
		}
	}
	if v := os.Getenv("OPENCODE_CC_DEFAULT_MODEL"); v != "" {
		c.DefaultModel = v
	}
	if v := os.Getenv("OPENCODE_CC_LOG_REQUESTS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.LogRequests = b
		}
	}
	if v := os.Getenv("OPENCODE_CC_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.RequestTimeoutSeconds = n
		}
	}
	if v := os.Getenv("OPENCODE_CC_PROMPT_CACHE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.PromptCacheEnabled = b
		}
	}
	if v := os.Getenv("OPENCODE_CC_PROMPT_CACHE_KEY_PREFIX"); v != "" {
		c.PromptCacheKeyPrefix = v
	}
	if v := os.Getenv("OPENCODE_CC_PROMPT_CACHE_ANTHROPIC_CONTROL"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.PromptCacheAnthropicControl = b
		}
	}
	if v := os.Getenv("OPENCODE_CC_PROMPT_CACHE_NORMALIZE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.PromptCacheNormalize = b
		}
	}
}

// Save persists the config to disk. Caller is responsible for holding any
// higher-level lock; this method takes the write lock around file I/O.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.dataDir == "" {
		return nil
	}
	c.ensureUpstreamIDsLocked()
	if err := os.MkdirAll(c.dataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.configPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.configPath)
}

// Snapshot returns a deep copy safe for handing to callers / JSON marshalling.
// It builds a fresh Config field-by-field to avoid copying the mutex.
func (c *Config) Snapshot() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cp := &Config{
		ListenAddr:                  c.ListenAddr,
		UpstreamBase:                c.UpstreamBase,
		NativeAnthropic:             c.NativeAnthropic,
		ZenAPIKey:                   c.ZenAPIKey,
		Upstreams:                   cloneUpstreams(c.Upstreams),
		PanelToken:                  c.PanelToken,
		RequireAPIKey:               c.RequireAPIKey,
		DefaultModel:                c.DefaultModel,
		LogRequests:                 c.LogRequests,
		MaxBodyLogBytes:             c.MaxBodyLogBytes,
		RequestTimeoutSeconds:       c.RequestTimeoutSeconds,
		PromptCacheEnabled:          c.PromptCacheEnabled,
		PromptCacheKeyPrefix:        c.PromptCacheKeyPrefix,
		PromptCacheAnthropicControl: c.PromptCacheAnthropicControl,
		PromptCacheNormalize:        c.PromptCacheNormalize,
	}
	if c.ModelMappings != nil {
		cp.ModelMappings = append([]ModelMapping(nil), c.ModelMappings...)
	}
	if c.ThinkingBudgetMappings != nil {
		cp.ThinkingBudgetMappings = append([]ThinkingBudgetMapping(nil), c.ThinkingBudgetMappings...)
	}
	// dataDir / configPath are deliberately left zero (unpersisted bookkeeping).
	return cp
}

// ResolveModel maps an incoming model name to the upstream target.
//
// Pass-through: if a rule matches with an empty Target (e.g. the catch-all
// {"match":"*","target":""}), the incoming model name is returned unchanged
// (after stripping any provider prefix). This lets you send Zen model ids
// (glm-5.1, kimi-k2.7-code, ...) directly as the "model" field.
//
// Provider prefixes that clients add — "anthropic/", "openai/", "provider/" —
// are always stripped: Claude Code sends e.g. "anthropic/kimi-k2.7-code" but
// Zen expects the bare "kimi-k2.7-code". This happens for both pass-through
// and explicit mapping matches.
func (c *Config) ResolveModel(in string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	bare := stripProviderPrefix(in)
	for _, m := range c.ModelMappings {
		if m.Match == "*" || strings.HasPrefix(in, m.Match) || strings.HasPrefix(bare, m.Match) {
			if m.Target == "" {
				// Pass-through: use the (de-prefixed) incoming model name.
				if bare != "" {
					return bare
				}
				break // fall through to DefaultModel
			}
			return m.Target
		}
	}
	if c.DefaultModel != "" {
		return c.DefaultModel
	}
	return DefaultDefaultModel
}

// ResolveThinkingBudgetMapping returns the first budget mapping that matches
// the target model id. Provider prefixes are stripped before matching.
func (c *Config) ResolveThinkingBudgetMapping(model string) (ThinkingBudgetMapping, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	bare := stripProviderPrefix(model)
	for _, m := range c.ThinkingBudgetMappings {
		if m.Match == "*" || strings.HasPrefix(model, m.Match) || strings.HasPrefix(bare, m.Match) {
			return m, true
		}
	}
	return ThinkingBudgetMapping{}, false
}

// stripProviderPrefix removes a leading "provider/" segment that clients add.
// Claude Code sends model names like "anthropic/kimi-k2.7-code"; Zen only
// accepts the bare id "kimi-k2.7-code". We strip a single segment before the
// first "/" if the part after it looks like the real model (i.e. the slash
// isn't part of the model id itself). Zen model ids never contain "/", so this
// is safe.
func stripProviderPrefix(in string) string {
	if i := strings.IndexByte(in, '/'); i >= 0 {
		return in[i+1:]
	}
	return in
}

// RLock / RUnlock expose the read lock for hot-path callers that want to read
// several fields consistently.
func (c *Config) RLock()   { c.mu.RLock() }
func (c *Config) RUnlock() { c.mu.RUnlock() }

// SetZenAPIKey is a convenience setter used by the panel API.
func (c *Config) SetZenAPIKey(v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ZenAPIKey = v
}

// ApplyPatch merges an explicitly partial config update from the panel.
func (c *Config) ApplyPatch(src *Patch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if src.ListenAddr != nil && *src.ListenAddr != "" {
		c.ListenAddr = *src.ListenAddr
	}
	if src.UpstreamBase != nil && *src.UpstreamBase != "" {
		c.UpstreamBase = strings.TrimRight(*src.UpstreamBase, "/")
	}
	if src.NativeAnthropic != nil {
		c.NativeAnthropic = *src.NativeAnthropic
	}
	// ZenAPIKey: empty means "don't change" so the panel never clobbers it.
	if src.ZenAPIKey != nil && *src.ZenAPIKey != "" {
		c.ZenAPIKey = *src.ZenAPIKey
	}
	// Upstreams pool: when provided, replace wholesale. Per-item APIKey uses
	// the "empty = keep existing key" sentinel so masked edits don't wipe keys.
	if src.Upstreams != nil {
		next := *src.Upstreams
		// Preserve existing keys/cookies where the patch left them blank.
		// Prefer the stable upstream ID so reordering/deleting rows never moves
		// a secret to the wrong account. Position matching is kept only for
		// legacy clients that send the unchanged full list without IDs.
		prev := c.Upstreams
		prevByID := make(map[string]Upstream, len(prev))
		for _, u := range prev {
			if id := strings.TrimSpace(u.ID); id != "" {
				prevByID[id] = u
			}
		}
		incomingHasIDs := false
		for _, u := range next {
			if strings.TrimSpace(u.ID) != "" {
				incomingHasIDs = true
				break
			}
		}
		seenNextID := map[string]bool{}
		for i := range next {
			next[i].ID = strings.TrimSpace(next[i].ID)
			if next[i].ID != "" {
				if seenNextID[next[i].ID] {
					// Duplicated IDs would duplicate secrets; treat the later
					// row as new instead.
					next[i].ID = ""
				} else {
					seenNextID[next[i].ID] = true
				}
			}
			var old Upstream
			matched := false
			if next[i].ID != "" {
				old, matched = prevByID[next[i].ID]
			} else if !incomingHasIDs && len(next) == len(prev) && i < len(prev) {
				old, matched = prev[i], true
				next[i].ID = old.ID
			}
			if next[i].ID == "" {
				next[i].ID = newUpstreamID()
			}
			if matched && next[i].APIKey == "" && old.APIKey != "" {
				next[i].APIKey = old.APIKey
			}
			if matched && next[i].OpenCodeGoAuthCookie == "" && old.OpenCodeGoAuthCookie != "" {
				next[i].OpenCodeGoAuthCookie = old.OpenCodeGoAuthCookie
			}
			if matched && next[i].OpenCodeGoShowRolling == nil {
				next[i].OpenCodeGoShowRolling = old.OpenCodeGoShowRolling
			}
			if matched && next[i].OpenCodeGoShowWeekly == nil {
				next[i].OpenCodeGoShowWeekly = old.OpenCodeGoShowWeekly
			}
			if matched && next[i].OpenCodeGoShowMonthly == nil {
				next[i].OpenCodeGoShowMonthly = old.OpenCodeGoShowMonthly
			}
			normalizeUpstream(&next[i])
		}
		c.Upstreams = cloneUpstreams(next)
		atomic.StoreUint64(&c.rr, 0)
		c.resetUpstreamHealthLocked()
	}
	if src.PanelToken != nil {
		c.PanelToken = *src.PanelToken
	}
	if src.RequireAPIKey != nil {
		c.RequireAPIKey = *src.RequireAPIKey
	}
	if src.DefaultModel != nil && *src.DefaultModel != "" {
		c.DefaultModel = *src.DefaultModel
	}
	if src.ModelMappings != nil {
		c.ModelMappings = append([]ModelMapping(nil), (*src.ModelMappings)...)
	}
	if src.LogRequests != nil {
		c.LogRequests = *src.LogRequests
	}
	if src.MaxBodyLogBytes != nil && *src.MaxBodyLogBytes >= 0 {
		c.MaxBodyLogBytes = *src.MaxBodyLogBytes
	}
	if src.RequestTimeoutSeconds != nil && *src.RequestTimeoutSeconds >= 0 {
		c.RequestTimeoutSeconds = *src.RequestTimeoutSeconds
	}
	if src.PromptCacheEnabled != nil {
		c.PromptCacheEnabled = *src.PromptCacheEnabled
	}
	if src.PromptCacheKeyPrefix != nil {
		c.PromptCacheKeyPrefix = *src.PromptCacheKeyPrefix
	}
	if src.PromptCacheAnthropicControl != nil {
		c.PromptCacheAnthropicControl = *src.PromptCacheAnthropicControl
	}
	if src.PromptCacheNormalize != nil {
		c.PromptCacheNormalize = *src.PromptCacheNormalize
	}
	if src.ThinkingBudgetMappings != nil {
		c.ThinkingBudgetMappings = append([]ThinkingBudgetMapping(nil), (*src.ThinkingBudgetMappings)...)
	}
}

func boolPtr(v bool) *bool { return &v }

func defaultOpenCodeGoWorkspaceID(workspaceID string) string {
	ws := strings.TrimSpace(workspaceID)
	if ws == "" {
		return DefaultOpenCodeGoWorkspaceID
	}
	return ws
}

func (c *Config) ensureUpstreamIDs() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureUpstreamIDsLocked()
}

func (c *Config) ensureUpstreamIDsLocked() {
	for i := range c.Upstreams {
		normalizeUpstream(&c.Upstreams[i])
	}
}

func normalizeUpstream(u *Upstream) {
	u.ID = strings.TrimSpace(u.ID)
	if u.ID == "" {
		u.ID = newUpstreamID()
	}
	u.BaseURL = strings.TrimRight(u.BaseURL, "/")
	u.OpenCodeGoWorkspaceID = defaultOpenCodeGoWorkspaceID(u.OpenCodeGoWorkspaceID)
}

func upstreamRuntimeID(u Upstream) string {
	if id := strings.TrimSpace(u.ID); id != "" {
		return id
	}
	return legacyUpstreamID(u.BaseURL, u.APIKey)
}

func legacyUpstreamID(base, key string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || strings.TrimSpace(key) == "" {
		return ""
	}
	return "legacy:" + base + ":" + maskKeyForID(key)
}

func maskKeyForID(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return key
	}
	return key[:4] + ":" + key[len(key)-4:]
}

var upstreamIDFallback uint64

func newUpstreamID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "up_" + hex.EncodeToString(b[:])
	}
	// Extremely unlikely fallback; unique enough inside one process.
	return "up_fallback_" + strconv.FormatUint(atomic.AddUint64(&upstreamIDFallback, 1), 36)
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func cloneUpstreams(in []Upstream) []Upstream {
	if in == nil {
		return nil
	}
	out := append([]Upstream(nil), in...)
	for i := range out {
		out[i].OpenCodeGoShowRolling = cloneBoolPtr(in[i].OpenCodeGoShowRolling)
		out[i].OpenCodeGoShowWeekly = cloneBoolPtr(in[i].OpenCodeGoShowWeekly)
		out[i].OpenCodeGoShowMonthly = cloneBoolPtr(in[i].OpenCodeGoShowMonthly)
	}
	return out
}
