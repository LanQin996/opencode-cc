package server

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/Kiowx/opencode-cc/internal/config"
)

type upstreamAttemptFailure struct {
	Status  int
	ErrType string
	Message string
}

func (s *Server) doUpstreamWithRetry(
	r *http.Request,
	stream bool,
	timeoutSeconds int,
	build func(config.UpstreamSelection) (*http.Request, error),
) (*http.Response, config.UpstreamSelection, *upstreamAttemptFailure) {
	tried := map[string]bool{}
	var last *upstreamAttemptFailure
	client := s.upstreamClient(stream, timeoutSeconds)

	for {
		upstream, ok := s.cfg.NextUpstreamExcluding(tried)
		if !ok {
			if last != nil {
				return nil, config.UpstreamSelection{}, last
			}
			status, errType, msg := s.noUpstreamError()
			return nil, config.UpstreamSelection{}, &upstreamAttemptFailure{Status: status, ErrType: errType, Message: msg}
		}
		tried[upstream.ID] = true

		req, err := build(upstream)
		if err != nil {
			msg := "could not build upstream request: " + err.Error()
			s.cfg.ReportUpstreamResult(upstream, false, msg)
			last = &upstreamAttemptFailure{Status: http.StatusBadGateway, ErrType: "api_error", Message: msg}
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			msg := "upstream request failed: " + err.Error()
			if shouldCoolUpstreamError(err) {
				s.cfg.ReportUpstreamResult(upstream, false, err.Error())
				last = &upstreamAttemptFailure{Status: http.StatusBadGateway, ErrType: "api_error", Message: msg}
				continue
			}
			return nil, config.UpstreamSelection{}, &upstreamAttemptFailure{Status: http.StatusBadGateway, ErrType: "api_error", Message: msg}
		}

		if resp.StatusCode < http.StatusBadRequest {
			s.cfg.ReportUpstreamResult(upstream, true, "")
			return resp, upstream, nil
		}

		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		msg := upstreamErrorMessage(resp.StatusCode, raw)

		if shouldRetryUpstreamResponse(resp.StatusCode, msg) {
			s.cfg.ReportUpstreamResult(upstream, false, msg)
			last = &upstreamAttemptFailure{
				Status:  resp.StatusCode,
				ErrType: openAIErrorTypeForStatus(resp.StatusCode),
				Message: msg,
			}
			continue
		}

		// Non-retryable 4xx means the upstream is reachable and the request
		// itself is probably invalid; don't keep this account in cooldown.
		s.cfg.ReportUpstreamResult(upstream, true, "")
		return resp, upstream, nil
	}
}

func upstreamErrorMessage(status int, raw []byte) string {
	if msg := extractOpenAIError(raw); msg != "" {
		return msg
	}
	if msg := extractAnthropicError(raw); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(string(raw)); msg != "" {
		return msg
	}
	if text := http.StatusText(status); text != "" {
		return text
	}
	return "upstream returned an error"
}

func shouldRetryUpstreamResponse(status int, message string) bool {
	if quotaLikeUpstreamMessage(message) {
		return true
	}
	switch status {
	case http.StatusPaymentRequired:
		return true
	default:
		return shouldCoolUpstreamStatus(status)
	}
}

func quotaLikeUpstreamMessage(message string) bool {
	m := strings.ToLower(message)
	needles := []string{
		"insufficient",
		"balance",
		"quota",
		"credit",
		"billing",
		"payment",
		"rate limit",
		"rate_limit",
		"too many requests",
		"no available",
		"quota exceeded",
		"usage limit",
		"余额",
		"额度",
		"用量",
		"欠费",
		"余额不足",
		"额度不足",
		"账户余额",
		"限额",
		"无可用",
	}
	for _, needle := range needles {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
}
