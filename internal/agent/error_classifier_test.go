package agent

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyError_ByStatus(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		wantReason FailoverReason
		retryable  bool
	}{
		{"401 auth", errors.New("unauthorized"), 401, ReasonAuth, true},
		{"403 forbidden", errors.New("forbidden"), 403, ReasonAuth, true},
		{"401 invalid key", errors.New("invalid api key provided"), 401, ReasonAuthPermanent, false},
		{"402 billing", errors.New("payment required"), 402, ReasonBilling, false},
		{"429 rate limit", errors.New("too many requests"), 429, ReasonRateLimit, true},
		{"429 billing disguised", errors.New("exceeded your current quota"), 429, ReasonBilling, false},
		{"400 format", errors.New("bad request"), 400, ReasonFormatError, false},
		{"400 context overflow", errors.New("maximum context length exceeded"), 400, ReasonContextOverflow, true},
		{"400 rate limit in body", errors.New("rate limit reached"), 400, ReasonRateLimit, true},
		{"404 model", errors.New("model not found"), 404, ReasonModelNotFound, false},
		{"413 payload", errors.New("payload too large"), 413, ReasonContextOverflow, true},
		{"500 server", errors.New("internal server error"), 500, ReasonServerError, true},
		{"502 bad gateway", errors.New("bad gateway"), 502, ReasonServerError, true},
		{"503 overloaded", errors.New("service unavailable"), 503, ReasonOverloaded, true},
		{"529 overloaded", errors.New("overloaded"), 529, ReasonOverloaded, true},
		{"504 unknown 5xx", errors.New("gateway timeout"), 504, ReasonServerError, true},
		{"418 unknown 4xx", errors.New("i'm a teapot"), 418, ReasonUnknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyError(tt.err, tt.status, "openai", "gpt-4")
			if result.Reason != tt.wantReason {
				t.Errorf("reason = %v, want %v", result.Reason, tt.wantReason)
			}
			if result.Retryable != tt.retryable {
				t.Errorf("retryable = %v, want %v", result.Retryable, tt.retryable)
			}
		})
	}
}

func TestClassifyError_ByMessage(t *testing.T) {
	tests := []struct {
		name       string
		msg        string
		wantReason FailoverReason
	}{
		{"rate limit", "rate limit exceeded", ReasonRateLimit},
		{"throttled", "request throttled", ReasonRateLimit},
		{"billing", "insufficient credits", ReasonBilling},
		{"auth", "invalid api key", ReasonAuth},
		{"context overflow", "context_length_exceeded", ReasonContextOverflow},
		{"model not found", "model not found", ReasonModelNotFound},
		{"timeout", "context deadline exceeded", ReasonTimeout},
		{"unknown", "something went wrong", ReasonUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyError(errors.New(tt.msg), 0, "anthropic", "claude")
			if result.Reason != tt.wantReason {
				t.Errorf("reason = %v, want %v", result.Reason, tt.wantReason)
			}
		})
	}
}

func TestClassifyError_NilError(t *testing.T) {
	result := ClassifyError(nil, 200, "", "")
	if result != nil {
		t.Errorf("expected nil for nil error, got %+v", result)
	}
}

func TestClassifyError_RecoveryHints(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		status         int
		shouldCompress bool
		shouldRotate   bool
		shouldFallback bool
	}{
		{"context overflow compresses", errors.New("context too long"), 413, true, false, false},
		{"auth rotates", errors.New("unauthorized"), 401, false, true, true},
		{"billing falls back", errors.New("no credits"), 402, false, true, true},
		{"model falls back", errors.New("not found"), 404, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ClassifyError(tt.err, tt.status, "", "")
			if r.ShouldCompress != tt.shouldCompress {
				t.Errorf("ShouldCompress = %v, want %v", r.ShouldCompress, tt.shouldCompress)
			}
			if r.ShouldRotateCredential != tt.shouldRotate {
				t.Errorf("ShouldRotateCredential = %v, want %v", r.ShouldRotateCredential, tt.shouldRotate)
			}
			if r.ShouldFallback != tt.shouldFallback {
				t.Errorf("ShouldFallback = %v, want %v", r.ShouldFallback, tt.shouldFallback)
			}
		})
	}
}

func TestClassifiedError_IsTransient(t *testing.T) {
	tests := []struct {
		reason    FailoverReason
		transient bool
	}{
		{ReasonRateLimit, true},
		{ReasonOverloaded, true},
		{ReasonServerError, true},
		{ReasonTimeout, true},
		{ReasonUnknown, true},
		{ReasonAuth, false},
		{ReasonBilling, false},
		{ReasonFormatError, false},
		{ReasonModelNotFound, false},
	}
	for _, tt := range tests {
		t.Run(tt.reason.String(), func(t *testing.T) {
			e := &ClassifiedError{Reason: tt.reason}
			if got := e.IsTransient(); got != tt.transient {
				t.Errorf("IsTransient() = %v, want %v", got, tt.transient)
			}
		})
	}
}

func TestFailoverReason_String(t *testing.T) {
	tests := []struct {
		reason FailoverReason
		want   string
	}{
		{ReasonAuth, "auth"},
		{ReasonRateLimit, "rate_limit"},
		{ReasonServerError, "server_error"},
		{ReasonUnknown, "unknown"},
		{FailoverReason(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.reason.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- JitteredBackoff ---

func TestJitteredBackoff_Increasing(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Second,
		MaxDelay:    60 * time.Second,
		JitterRatio: 0, // no jitter for deterministic test
	}

	prev := time.Duration(0)
	for attempt := 1; attempt <= 4; attempt++ {
		delay := JitteredBackoff(attempt, cfg)
		if delay <= prev {
			t.Errorf("attempt %d: delay %v should be > %v", attempt, delay, prev)
		}
		prev = delay
	}
}

func TestJitteredBackoff_Capped(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 10,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
		JitterRatio: 0.5,
	}

	delay := JitteredBackoff(20, cfg)
	maxWithJitter := 10*time.Second + 5*time.Second // max + 50% jitter
	if delay > maxWithJitter {
		t.Errorf("delay %v exceeded max with jitter %v", delay, maxWithJitter)
	}
}

func TestJitteredBackoff_Jitter(t *testing.T) {
	cfg := DefaultRetryConfig()

	// Same attempt should produce different delays due to jitter.
	delays := make(map[time.Duration]bool)
	for i := 0; i < 10; i++ {
		d := JitteredBackoff(1, cfg)
		delays[d] = true
	}
	if len(delays) < 2 {
		t.Error("expected jitter to produce varying delays")
	}
}

// --- RetryBudget ---

func TestRetryBudget(t *testing.T) {
	budget := NewRetryBudget(3)

	if !budget.CanRetry("key1") {
		t.Error("should be able to retry initially")
	}

	budget.Record("key1")
	budget.Record("key1")
	budget.Record("key1")

	if budget.CanRetry("key1") {
		t.Error("should not retry after max attempts")
	}
	if budget.Count("key1") != 3 {
		t.Errorf("count = %d, want 3", budget.Count("key1"))
	}

	// Different key unaffected.
	if !budget.CanRetry("key2") {
		t.Error("different key should have budget")
	}

	// Reset.
	budget.Reset("key1")
	if !budget.CanRetry("key1") {
		t.Error("should retry after reset")
	}
}
