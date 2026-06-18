package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// withLogging is a minimal access logger. It avoids the overhead of reading
// request bodies or computing stats on the hot path; detailed per-request
// recording happens in the proxy handlers via the store.
func (s *Server) withLogging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic serving %s %s: %v", r.Method, r.URL.Path, recovered)
				writeRecoveredPanic(rw, r, fmt.Sprint(recovered))
			}
			_ = start
			// Intentionally silent; the panel surfaces real data from the store.
			_ = rw.status
		}()
		h.ServeHTTP(rw, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

// Flush forwards to the underlying writer so streaming (http.Flusher) works
// through this wrapper. Without it, w.(http.Flusher) assertions fail and kill
// every streamed response.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController and the standard library detect the
// underlying writer for interfaces like Flusher / Hijacker.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func writeRecoveredPanic(w *statusRecorder, r *http.Request, detail string) {
	const clientMessage = "internal server error"
	if w.wroteHeader {
		writeRecoveredStreamingError(w, r, clientMessage)
		return
	}
	switch {
	case r.URL.Path == "/v1/messages" || strings.HasPrefix(r.URL.Path, "/v1/messages/"):
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", clientMessage)
	case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/models":
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", clientMessage)
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": clientMessage})
	}
	_ = detail
}

func writeRecoveredStreamingError(w *statusRecorder, r *http.Request, message string) {
	if !strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream") {
		return
	}
	var payload []byte
	var event string
	switch r.URL.Path {
	case "/v1/responses":
		event = "response.failed"
		payload, _ = json.Marshal(map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"status": "failed",
				"error": map[string]string{
					"type":    "api_error",
					"message": message,
				},
			},
		})
	case "/v1/chat/completions":
		event = ""
		payload, _ = json.Marshal(map[string]any{
			"error": map[string]string{
				"type":    "api_error",
				"message": message,
			},
		})
	default:
		event = "error"
		payload, _ = json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    "api_error",
				"message": message,
			},
		})
	}
	if event != "" {
		_, _ = fmt.Fprintf(w.ResponseWriter, "event: %s\ndata: %s\n\n", event, payload)
	} else {
		_, _ = fmt.Fprintf(w.ResponseWriter, "data: %s\n\n", payload)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
