package agent

import "regexp"

// secretPatterns holds compiled regexes for known secret formats.
// All patterns are compiled once at package init time.
var secretPatterns []*regexp.Regexp

func init() {
	rawPatterns := []string{
		// API key prefixes
		`sk-[a-zA-Z0-9]{20,}`,
		`sk-ant-[a-zA-Z0-9-]{20,}`,
		`ghp_[a-zA-Z0-9]{36,}`,
		`gho_[a-zA-Z0-9]{36,}`,
		`ghu_[a-zA-Z0-9]{36,}`,
		`ghs_[a-zA-Z0-9]{36,}`,
		`AKIA[A-Z0-9]{16}`,
		`xox[bpors]-[a-zA-Z0-9-]+`,
		`AIza[a-zA-Z0-9_\-]{35}`,
		`ya29\.[a-zA-Z0-9_\-]+`,
		`fal_[a-zA-Z0-9]{32,}`,
		`hf_[a-zA-Z0-9]{34,}`,
		`gsk_[a-zA-Z0-9]{20,}`,
		`pplx-[a-zA-Z0-9]{40,}`,
		`r8_[a-zA-Z0-9]{37,}`,
		`sq0[a-z]{3}-[a-zA-Z0-9\-]{22,}`,
		`sk_live_[a-zA-Z0-9]{24,}`,
		`rk_live_[a-zA-Z0-9]{24,}`,
		`pk_live_[a-zA-Z0-9]{24,}`,
		// Structural patterns
		`(?i)Authorization:\s*Bearer\s+[a-zA-Z0-9._~+/=\-]{20,}`,
		`(?i)(?:API_KEY|TOKEN|SECRET|PASSWORD)\s*[=:]\s*["']?[^\s"']{8,}`,
		`-----BEGIN\s(?:RSA\s)?PRIVATE\sKEY-----[\s\S]*?-----END`,
		`\d{5,}:[a-zA-Z0-9_\-]{20,}`,
	}

	secretPatterns = make([]*regexp.Regexp, len(rawPatterns))
	for i, p := range rawPatterns {
		secretPatterns[i] = regexp.MustCompile(p)
	}
}

// RedactSecrets replaces known secret patterns in text with [REDACTED].
func RedactSecrets(text string) string {
	for _, re := range secretPatterns {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// ContainsSecret returns true if text contains any known secret pattern.
func ContainsSecret(text string) bool {
	for _, re := range secretPatterns {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}
