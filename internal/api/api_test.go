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
	cfg.LogRequests = true
	cfg.MaxBodyLogBytes = 1234
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
	if !got.RequireAPIKey || !got.LogRequests || got.MaxBodyLogBytes != 1234 {
		t.Fatalf("omitted fields changed: %+v", got)
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
