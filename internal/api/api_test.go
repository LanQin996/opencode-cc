package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kiowx/opencode-cc/internal/config"
	"github.com/Kiowx/opencode-cc/internal/store"
)

func newTestAPI(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mux := http.NewServeMux()
	New(cfg, st).Mount(mux)
	return mux
}

func TestEmptyListEndpointsReturnArrays(t *testing.T) {
	mux := newTestAPI(t, config.Default())

	for _, path := range []string{
		"/api/stats/hourly?hours=24",
		"/api/stats/models?hours=24",
		"/api/logs?limit=100",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
				t.Fatalf("body = %q, want []", body)
			}
		})
	}
}

func TestConfigPatchPreservesOmittedFields(t *testing.T) {
	cfg := config.Default()
	cfg.PanelToken = "old-password"
	cfg.RequireAPIKey = true
	cfg.NativeAnthropic = true
	cfg.LogRequests = true
	cfg.MaxBodyLogBytes = 1234
	cfg.PromptCacheEnabled = true
	cfg.PromptCacheKeyPrefix = "stable"
	mux := newTestAPI(t, cfg)

	body := bytes.NewBufferString(`{"panel_token":"new-password"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", body)
	req.Header.Set("Authorization", "Bearer old-password")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := cfg.Snapshot()
	if got.PanelToken != "new-password" {
		t.Fatalf("panel token = %q, want new-password", got.PanelToken)
	}
	if !got.RequireAPIKey ||
		!got.NativeAnthropic ||
		!got.LogRequests ||
		got.MaxBodyLogBytes != 1234 ||
		!got.PromptCacheEnabled ||
		got.PromptCacheKeyPrefix != "stable" {
		t.Fatalf("omitted fields changed: %+v", got)
	}
}

func TestConfigPatchUpdatesNativeAnthropic(t *testing.T) {
	cfg := config.Default()
	mux := newTestAPI(t, cfg)

	body := bytes.NewBufferString(`{"native_anthropic":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if cfg.Snapshot().NativeAnthropic {
		t.Fatal("NativeAnthropic was not updated")
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["native_anthropic"] != false {
		t.Fatalf("native_anthropic missing from public config: %s", rec.Body.String())
	}
}

func TestConfigPatchUpdatesPromptCache(t *testing.T) {
	cfg := config.Default()
	mux := newTestAPI(t, cfg)

	body := bytes.NewBufferString(`{
		"prompt_cache_enabled":false,
		"prompt_cache_key_prefix":"local",
		"prompt_cache_anthropic_control":false,
		"prompt_cache_normalize":false
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	snap := cfg.Snapshot()
	if snap.PromptCacheEnabled ||
		snap.PromptCacheKeyPrefix != "local" ||
		snap.PromptCacheAnthropicControl ||
		snap.PromptCacheNormalize {
		t.Fatalf("prompt cache config was not updated: %+v", snap)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["prompt_cache_enabled"] != false ||
		out["prompt_cache_key_prefix"] != "local" ||
		out["prompt_cache_anthropic_control"] != false ||
		out["prompt_cache_normalize"] != false {
		t.Fatalf("prompt cache missing from public config: %s", rec.Body.String())
	}
}

func TestPanelTokenChangeInvalidatesSessions(t *testing.T) {
	invalidateSessions()
	t.Cleanup(invalidateSessions)

	cfg := config.Default()
	cfg.PanelToken = "old-password"
	mux := newTestAPI(t, cfg)

	loginBody, _ := json.Marshal(map[string]string{"password": "old-password"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewBufferString(`{"panel_token":"new-password"}`))
	updateReq.AddCookie(cookies[0])
	updateRec := httptest.NewRecorder()
	mux.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateRec.Code, updateRec.Body.String())
	}

	checkReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	checkReq.AddCookie(cookies[0])
	checkRec := httptest.NewRecorder()
	mux.ServeHTTP(checkRec, checkReq)
	if checkRec.Code != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, want 401", checkRec.Code)
	}
}
