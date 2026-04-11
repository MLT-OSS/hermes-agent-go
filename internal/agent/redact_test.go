package agent

import "testing"

func TestRedactSecrets_Empty(t *testing.T) {
	if got := RedactSecrets(""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestRedactSecrets_NoSecrets(t *testing.T) {
	input := "Hello, world! No secrets here."
	if got := RedactSecrets(input); got != input {
		t.Errorf("expected unchanged string, got %q", got)
	}
}

func TestRedactSecrets_OpenAI(t *testing.T) {
	input := "key=sk-abcdefghijklmnopqrstuvwxyz123456"
	got := RedactSecrets(input)
	if ContainsSecret(got) {
		t.Errorf("OpenAI key not redacted: %q", got)
	}
}

func TestRedactSecrets_Anthropic(t *testing.T) {
	input := "using sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890"
	got := RedactSecrets(input)
	if ContainsSecret(got) {
		t.Errorf("Anthropic key not redacted: %q", got)
	}
}

func TestRedactSecrets_GitHub(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"PAT", "ghp_abcdefghijklmnopqrstuvwxyz1234567890"},
		{"OAuth", "gho_abcdefghijklmnopqrstuvwxyz1234567890"},
		{"User", "ghu_abcdefghijklmnopqrstuvwxyz1234567890"},
		{"Server", "ghs_abcdefghijklmnopqrstuvwxyz1234567890"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactSecrets(tt.input)
			if ContainsSecret(got) {
				t.Errorf("GitHub token not redacted: %q", got)
			}
		})
	}
}

func TestRedactSecrets_AWS(t *testing.T) {
	input := "AWS key: AKIAIOSFODNN7EXAMPLE"
	got := RedactSecrets(input)
	if ContainsSecret(got) {
		t.Errorf("AWS key not redacted: %q", got)
	}
}

func TestRedactSecrets_Slack(t *testing.T) {
	input := "token=" + "xox" + "b-1234567890-abcdefghijklmnop"
	got := RedactSecrets(input)
	if ContainsSecret(got) {
		t.Errorf("Slack token not redacted: %q", got)
	}
}

func TestRedactSecrets_Bearer(t *testing.T) {
	input := "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig"
	got := RedactSecrets(input)
	if ContainsSecret(got) {
		t.Errorf("Bearer token not redacted: %q", got)
	}
}

func TestRedactSecrets_PEM(t *testing.T) {
	input := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END"
	got := RedactSecrets(input)
	if ContainsSecret(got) {
		t.Errorf("PEM key not redacted: %q", got)
	}
}

func TestRedactSecrets_PreservesNonSecret(t *testing.T) {
	input := "the result is 42 and status is ok"
	got := RedactSecrets(input)
	if got != input {
		t.Errorf("non-secret text was modified: got %q", got)
	}
}

func TestContainsSecret_True(t *testing.T) {
	if !ContainsSecret("sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Error("expected ContainsSecret to return true for OpenAI key")
	}
}

func TestContainsSecret_False(t *testing.T) {
	if ContainsSecret("no secrets here at all") {
		t.Error("expected ContainsSecret to return false")
	}
}
