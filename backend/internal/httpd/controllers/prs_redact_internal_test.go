package controllers

import (
	"errors"
	"strings"
	"testing"
)

func TestSafeErrorCauseRedactsCredentials(t *testing.T) {
	const hexPAT = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name    string
		in      string
		secrets []string
		want    string // exact output when set
	}{
		{"authorization token scheme with legacy hex PAT", "github: Authorization: token " + hexPAT + " rejected", []string{hexPAT, "0123456789"}, "github: Authorization: [redacted]"},
		{"authorization bearer", `request header authorization="Bearer abc.DEF-ghi_123"`, []string{"abc.DEF-ghi_123"}, ""},
		{"proxy authorization basic", "Proxy-Authorization: Basic dXNlcjpwYXNzd29yZA==, retrying", []string{"dXNlcjpwYXNzd29yZA"}, "Proxy-Authorization: [redacted], retrying"},
		{"bare token scheme", "sent token " + hexPAT, []string{hexPAT}, "sent token [redacted]"},
		{"short token scheme credential", "auth: token abc123def", []string{"abc123def"}, "auth: token [redacted]"},
		{"github ghp", "bad ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", []string{"ghp_"}, "bad [redacted]"},
		{"github gho/ghu/ghs/ghr", "gho_abcdefgh12345678 ghu_abcdefgh12345678 ghs_abcdefgh12345678 ghr_abcdefgh12345678", []string{"gho_", "ghu_", "ghs_", "ghr_"}, ""},
		{"github fine-grained", "github_pat_11ABCDEFG0123456789_abcdefghijklmnop", []string{"github_pat_"}, ""},
		{"gitlab glpat", "glpat-abcdefghij1234567890", []string{"glpat-"}, ""},
		{"gitlab other prefixes", "gloas-abcdefghij123456 gldt-abcdefghij123456 glrt-abcdefghij123456 glcbt-abcdefghij123456 glptt-abcdefghij123456 glft-abcdefghij123456 glimt-abcdefghij123456 glsoat-abcdefghij123456", []string{"gloas-", "gldt-", "glrt-", "glcbt-", "glptt-", "glft-", "glimt-", "glsoat-"}, ""},
		{"unprefixed legacy hex token", "using credential " + hexPAT + " for push", []string{hexPAT}, "using credential [redacted] for push"},
		{"unprefixed oauth token", "oauth token ya29.a0AfH6SMBx3abcDEFghiJKLmnoPQRstuVWXyz0123456789 expired", []string{"a0AfH6SMBx3abcDEFghiJKLmnoPQRstuVWXyz0123456789"}, ""},
		{"key value pairs", "GET /api?access_token=s3cr3t&private_token=abc password=hunter2 client_secret: xyz9", []string{"s3cr3t", "=abc", "hunter2", "xyz9"}, ""},
		{"url userinfo", "push https://x-access-token:supersecret@github.com/acme/widgets.git failed", []string{"supersecret", "x-access-token"}, "push https://[redacted]@github.com/acme/widgets.git failed"},
		{"quoted authorization header value", `Authorization: "Basic dXNlcjpwYXNz"`, []string{"dXNlcjpwYXNz", "Basic"}, `Authorization: "[redacted]"`},
		{"json authorization value", `{"Authorization":"token abcdefghijklmnopqrstuv"}`, []string{"abcdefghijklmnopqrstuv"}, `{"Authorization":"[redacted]"}`},
		{"single-quoted authorization value", `authorization='Bearer abcdefghijkl'`, []string{"abcdefghijkl"}, `authorization='[redacted]'`},
		{"authorization basic", "auth failed: Authorization: Basic dXNlcjpwYXNzd29yZDEyMw== for user", []string{"dXNlcjpwYXNzd29yZDEyMw"}, "auth failed: Authorization: [redacted]"},
		{"authorization header to end of field", "headers: Authorization: token " + hexPAT + " extra; Accept: json", []string{hexPAT}, "headers: Authorization: [redacted]; Accept: json"},
		{"gitlab gldt glrt gloas glcbt glptt glft", "gldt-ABCDEFGH12345678 glrt-ABCDEFGH12345678 gloas-ABCDEFGH12345678 glcbt-ABCDEFGH12345678 glptt-ABCDEFGH12345678 glft-ABCDEFGH12345678", []string{"gldt-", "glrt-", "gloas-", "glcbt-", "glptt-", "glft-", "ABCDEFGH12345678"}, ""},
		{"unprefixed long hex alone", hexPAT, []string{hexPAT}, "[redacted]"},
		{"unprefixed url-safe base64", "cred Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZg_- used", []string{"Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZg"}, "cred [redacted] used"},
		{"unprefixed base64 with slash and plus", "cred AbCd1234/EfGh5678+IjKl9012/MnOp3456+QrSt== used", []string{"AbCd1234", "EfGh5678", "IjKl9012", "MnOp3456", "QrSt"}, "cred [redacted] used"},
		{"gitlab glagent gltst", "glagent-abcdefghijklmnop gltst-abcdefghijklmnop", []string{"glagent-", "gltst-", "abcdefghijklmnop"}, "[redacted] [redacted]"},
		{"unknown gitlab prefix with digits", "glnew-abcd1234efgh5678", []string{"glnew-", "abcd1234"}, "[redacted]"},
		{"control characters", "boom\x1b[31m ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", []string{"\x1b", "ghp_"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeErrorCause(errors.New(tt.in))
			for _, secret := range tt.secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("safeErrorCause(%q) = %q leaks %q", tt.in, got, secret)
				}
			}
			if tt.want != "" && got != tt.want {
				t.Fatalf("safeErrorCause(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSafeErrorCauseKeepsDiagnostics(t *testing.T) {
	for _, in := range []string{
		"merge pull request: github scm: 502 Bad Gateway",
		"pull request https://github.com/acme/widgets/pull/42 is not mergeable",
		"fetch ssh://git@github.com/acme/widgets.git: connection reset",
		"invalid token rejected by provider",
		"refresh pull request before merge: context deadline exceeded",
		"POST https://api.github.com/repos/acme/widgets/pulls/42/merge: 405 Method Not Allowed",
		"global-configuration-settings failed",
		"glance-dashboard-widget and glob-pattern-matching disabled",
	} {
		if got := safeErrorCause(errors.New(in)); got != in {
			t.Fatalf("safeErrorCause(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestSafeErrorCauseRedactsBeforeTruncating(t *testing.T) {
	secret := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	in := strings.Repeat("x ", (maxErrorCauseRunes-10)/2) + secret
	got := safeErrorCause(errors.New(in))
	if strings.Contains(got, "ghp_") || strings.Contains(got, "ABCDEF") {
		t.Fatalf("truncated cause leaks part of a secret: %q", got[len(got)-40:])
	}
	if len([]rune(got)) > maxErrorCauseRunes+1 {
		t.Fatalf("cause length = %d, want capped", len([]rune(got)))
	}
}
