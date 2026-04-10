package agent

import (
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// FailoverReason describes why an API call failed.
type FailoverReason int

const (
	ReasonUnknown         FailoverReason = iota + 1 // 规范八：枚举从 1 开始
	ReasonAuth                                      // 401/403 transient auth
	ReasonAuthPermanent                             // auth failed after refresh
	ReasonBilling                                   // 402 / credit exhaustion
	ReasonRateLimit                                 // 429 / throttling
	ReasonOverloaded                                // 503/529 server overloaded
	ReasonServerError                               // 500/502 internal error
	ReasonTimeout                                   // connection/read timeout
	ReasonContextOverflow                           // context too large
	ReasonModelNotFound                             // 404 / invalid model
	ReasonFormatError                               // 400 bad request
)

// String returns the human-readable reason name.
func (r FailoverReason) String() string {
	switch r {
	case ReasonAuth:
		return "auth"
	case ReasonAuthPermanent:
		return "auth_permanent"
	case ReasonBilling:
		return "billing"
	case ReasonRateLimit:
		return "rate_limit"
	case ReasonOverloaded:
		return "overloaded"
	case ReasonServerError:
		return "server_error"
	case ReasonTimeout:
		return "timeout"
	case ReasonContextOverflow:
		return "context_overflow"
	case ReasonModelNotFound:
		return "model_not_found"
	case ReasonFormatError:
		return "format_error"
	default:
		return "unknown"
	}
}

// ClassifiedError is the structured classification of an API error.
type ClassifiedError struct {
	Reason     FailoverReason
	StatusCode int
	Provider   string
	Model      string
	Message    string

	// Recovery hints — the retry loop checks these.
	Retryable              bool
	ShouldCompress         bool
	ShouldRotateCredential bool
	ShouldFallback         bool
	RetryAfter             time.Duration // from Retry-After header, if any
}

// IsTransient returns true if the error is expected to resolve on retry.
func (e *ClassifiedError) IsTransient() bool {
	switch e.Reason {
	case ReasonRateLimit, ReasonOverloaded, ReasonServerError, ReasonTimeout, ReasonUnknown:
		return true
	default:
		return false
	}
}

// --- Pattern matching ---

var billingPatterns = []string{
	"insufficient credits",
	"insufficient_quota",
	"credit balance",
	"credits have been exhausted",
	"payment required",
	"billing hard limit",
	"exceeded your current quota",
	"account is deactivated",
}

var rateLimitPatterns = []string{
	"rate limit",
	"rate_limit",
	"too many requests",
	"throttled",
	"requests per minute",
	"tokens per minute",
}

var authPatterns = []string{
	"invalid api key",
	"invalid_api_key",
	"incorrect api key",
	"authentication",
	"unauthorized",
	"permission denied",
	"access denied",
	"forbidden",
	"invalid x-api-key",
	"invalid bearer",
}

var contextOverflowPatterns = []string{
	"context_length_exceeded",
	"maximum context length",
	"too many tokens",
	"context window",
	"max_tokens",
	"token limit",
	"input is too long",
}

var modelNotFoundPatterns = []string{
	"model not found",
	"model_not_found",
	"does not exist",
	"invalid model",
	"no such model",
	"not available",
}

// ClassifyError classifies an API error based on status code and message.
func ClassifyError(err error, statusCode int, provider, model string) *ClassifiedError {
	if err == nil {
		return nil
	}

	msg := strings.ToLower(err.Error())

	result := &ClassifiedError{
		Reason:     ReasonUnknown,
		StatusCode: statusCode,
		Provider:   provider,
		Model:      model,
		Message:    err.Error(),
		Retryable:  true,
	}

	// Status-code based classification first.
	if statusCode > 0 {
		classifyByStatus(result, statusCode, msg)
		return result
	}

	// No status code — classify by message patterns.
	classifyByMessage(result, msg)
	return result
}

func classifyByStatus(r *ClassifiedError, status int, msg string) {
	switch {
	case status == 401 || status == 403:
		r.Reason = ReasonAuth
		r.ShouldRotateCredential = true
		r.ShouldFallback = true
		if matchesAny(msg, []string{"invalid api key", "incorrect api key", "deactivated"}) {
			r.Reason = ReasonAuthPermanent
			r.Retryable = false
		}

	case status == 402:
		r.Reason = ReasonBilling
		r.ShouldFallback = true
		r.ShouldRotateCredential = true
		// Billing might be transient (credit top-up) but usually not.
		r.Retryable = false

	case status == 429:
		r.Reason = ReasonRateLimit
		r.Retryable = true
		// Check if it's actually billing disguised as 429.
		if matchesAny(msg, billingPatterns) {
			r.Reason = ReasonBilling
			r.ShouldFallback = true
			r.Retryable = false
		}

	case status == 400:
		r.Reason = ReasonFormatError
		r.Retryable = false
		if matchesAny(msg, contextOverflowPatterns) {
			r.Reason = ReasonContextOverflow
			r.ShouldCompress = true
			r.Retryable = true
		}
		if matchesAny(msg, rateLimitPatterns) {
			r.Reason = ReasonRateLimit
			r.Retryable = true
		}

	case status == 404:
		r.Reason = ReasonModelNotFound
		r.ShouldFallback = true
		r.Retryable = false

	case status == 413:
		r.Reason = ReasonContextOverflow
		r.ShouldCompress = true
		r.Retryable = true

	case status == 500 || status == 502:
		r.Reason = ReasonServerError
		r.Retryable = true

	case status == 503 || status == 529:
		r.Reason = ReasonOverloaded
		r.Retryable = true

	default:
		if status >= 500 {
			r.Reason = ReasonServerError
			r.Retryable = true
		} else {
			r.Reason = ReasonUnknown
			r.Retryable = false
		}
	}
}

func classifyByMessage(r *ClassifiedError, msg string) {
	switch {
	case matchesAny(msg, rateLimitPatterns):
		r.Reason = ReasonRateLimit
		r.Retryable = true
	case matchesAny(msg, billingPatterns):
		r.Reason = ReasonBilling
		r.ShouldFallback = true
		r.Retryable = false
	case matchesAny(msg, authPatterns):
		r.Reason = ReasonAuth
		r.ShouldRotateCredential = true
		r.ShouldFallback = true
	case matchesAny(msg, contextOverflowPatterns):
		r.Reason = ReasonContextOverflow
		r.ShouldCompress = true
		r.Retryable = true
	case matchesAny(msg, modelNotFoundPatterns):
		r.Reason = ReasonModelNotFound
		r.ShouldFallback = true
		r.Retryable = false
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		r.Reason = ReasonTimeout
		r.Retryable = true
	default:
		r.Reason = ReasonUnknown
		r.Retryable = true
	}
}

func matchesAny(msg string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// --- Retry with jittered backoff ---

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	JitterRatio float64
}

// DefaultRetryConfig returns sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   5 * time.Second,
		MaxDelay:    120 * time.Second,
		JitterRatio: 0.5,
	}
}

// jitter state — decorrelates concurrent retries (规范四：mutex, not anonymous).
var (
	jitterMu      sync.Mutex
	jitterCounter uint64
)

// JitteredBackoff computes a jittered exponential backoff delay.
// attempt is 1-based.
func JitteredBackoff(attempt int, cfg RetryConfig) time.Duration {
	jitterMu.Lock()
	jitterCounter++
	tick := jitterCounter
	jitterMu.Unlock()

	exponent := attempt - 1
	if exponent < 0 {
		exponent = 0
	}

	baseMs := float64(cfg.BaseDelay.Milliseconds())
	delay := baseMs * math.Pow(2, float64(exponent))
	maxMs := float64(cfg.MaxDelay.Milliseconds())
	if delay > maxMs {
		delay = maxMs
	}

	// Decorrelated jitter using tick + time.
	seed := time.Now().UnixNano() ^ int64(tick*0x9E3779B9)
	rng := rand.New(rand.NewSource(seed))
	jitter := rng.Float64() * cfg.JitterRatio * delay

	return time.Duration(delay+jitter) * time.Millisecond
}

// --- Retry budget ---

// RetryBudget tracks retry attempts per session to prevent infinite loops.
type RetryBudget struct {
	mu       sync.Mutex
	attempts map[string]int
	max      int
}

// NewRetryBudget creates a retry budget with the given max attempts per key.
func NewRetryBudget(maxAttempts int) *RetryBudget {
	return &RetryBudget{
		attempts: make(map[string]int),
		max:      maxAttempts,
	}
}

// CanRetry returns true if the key has budget remaining.
func (b *RetryBudget) CanRetry(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.attempts[key] < b.max
}

// Record increments the attempt count for a key.
func (b *RetryBudget) Record(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.attempts[key]++
}

// Reset clears the attempt count for a key.
func (b *RetryBudget) Reset(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.attempts, key)
}

// Count returns the current attempt count for a key.
func (b *RetryBudget) Count(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.attempts[key]
}
