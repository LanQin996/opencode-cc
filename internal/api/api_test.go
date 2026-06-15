package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kiowx/opencode-cc/internal/config"
	"github.com/Kiowx/opencode-cc/internal/store"
)

func TestEmptyListEndpointsReturnArrays(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mux := http.NewServeMux()
	New(config.Default(), st).Mount(mux)

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
