package server

import "testing"

func TestQuotaLikeUpstreamMessageMatchesInsufficientBalanceBilling(t *testing.T) {
	msg := "Insufficient balance. Manage your billing here: https://opencode.ai/workspace/wrk_01KVCKYMX3GHKVEXDA8YQBTMNQ/billing"
	if !quotaLikeUpstreamMessage(msg) {
		t.Fatalf("expected insufficient balance billing message to be retryable")
	}
}
