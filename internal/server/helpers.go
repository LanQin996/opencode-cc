// Package server wires the HTTP layer: routes, auth, upstream proxying with
// streaming, and request logging. The panel API lives in package api.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"
)

// writeJSON writes v as JSON with the given status. Shared by handlers in this
// package (proxy has its own copy to avoid a cycle).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAnthropicError writes an Anthropic-shaped error response.
func writeAnthropicError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": msg},
	})
}

// writeOpenAIError writes an OpenAI-compatible error response.
func writeOpenAIError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    errType,
			"param":   nil,
			"code":    nil,
		},
	})
}

func (s *Server) upstreamClient(stream bool, timeoutSeconds int) *http.Client {
	if stream || timeoutSeconds <= 0 {
		return s.httpClient
	}
	return &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
}

func shouldCoolUpstreamError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return true
}

func shouldCoolUpstreamStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (s *Server) noUpstreamError() (int, string, string) {
	if s.cfg.HasConfiguredUpstream() {
		return http.StatusServiceUnavailable, "api_error",
			"all upstreams are temporarily cooling down after recent failures; retry shortly."
	}
	return http.StatusUnauthorized, "authentication_error",
		"no upstream API key configured. Set one in the web panel (Settings → upstreams)."
}
