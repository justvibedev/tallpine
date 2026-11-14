package pricing

import (
	"os/exec"
	"strings"
	"testing"
)

// A Windows checkout with core.autocrlf=true rewrites the embedded catalog to
// CRLF, which breaks its hash ("provider ... hash mismatch" at daemon start).
func TestBundledCatalogIsPinnedToLF(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	for _, path := range []string{"bundled/v1/manifest.json", "../../../pricing/catalog/v1/manifest.json"} {
		out, err := exec.Command("git", "check-attr", "eol", "--", path).CombinedOutput()
		if err != nil {
			t.Skipf("git check-attr: %v: %s", err, out)
		}
		if !strings.HasSuffix(strings.TrimSpace(string(out)), ": eol: lf") {
			t.Errorf("%s is not pinned to eol=lf: %s", path, out)
		}
	}
}
