package acp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenericACPHasNoVendorRateLimitParser keeps vendor usage formats in the
// binding that owns them (Config.UsageRateLimits).
func TestGenericACPHasNoVendorRateLimitParser(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "_claude/rateLimit") {
			t.Errorf("%s parses the Claude rate-limit extension; it belongs in claudeacp", f)
		}
	}
}
