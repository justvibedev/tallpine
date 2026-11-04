package httpd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
)

// capHost is the capability-bearing workspace preview host (no port) for id.
func capHost(t *testing.T, id string) string {
	t.Helper()
	return hostOf(t, func() (string, error) { return previewutil.FileURL("", domain.SessionID(id), "index.html") })
}

func hostOf(t *testing.T, build func() (string, error)) string {
	t.Helper()
	raw, err := build()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

func TestPreviewHostRequiresCapability(t *testing.T) {
	good := capHost(t, "tp-1") + ":3101"
	labels := strings.Split(good, ".")
	idOnly := strings.Join(append(labels[:len(labels)-2:len(labels)-2], labels[len(labels)-1]), ".")
	other := strings.Split(capHost(t, "tp-2"), ".")
	forged := strings.Join(append(append([]string{}, labels[:len(labels)-2]...), other[len(other)-2], labels[len(labels)-1]), ".")
	for host, want := range map[string]bool{good: true, idOnly: false, forged: false} {
		if got := isPreviewHost(host); got != want {
			t.Errorf("isPreviewHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestIDOnlyPreviewHostIsRefusedByTheRouter(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(config.Config{}, discardLogger(), nil))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/index.html", http.NoBody)
	req.Host = "ao-preview.mfxs2mi.localhost:3101"
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("id-only preview host = %d, want 403", resp.StatusCode)
	}
}

func capArtifactHost(t *testing.T, id string) string {
	t.Helper()
	return hostOf(t, func() (string, error) { return previewutil.ArtifactFileURL("", domain.SessionID(id), "index.html") })
}

func capInlineHost(t *testing.T, id string) string {
	t.Helper()
	return hostOf(t, func() (string, error) {
		return previewutil.InlineArtifactFileURL("", domain.SessionID(id), "index.html")
	})
}

// The capability key must come from the daemon token, so capabilities are
// stable across restarts of one install and change when the token does.
func TestServerDerivesPreviewCapabilitiesFromTheDaemonToken(t *testing.T) {
	previewutil.SetCapabilityKey("not-the-daemon-token")
	t.Cleanup(func() { previewutil.SetCapabilityKey("") })
	before := capHost(t, "tp-1")

	srv, err := NewWithDeps(config.Config{
		Host:        "127.0.0.1",
		RunFilePath: filepath.Join(t.TempDir(), "running.json"),
	}, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.closeListeners()
	if _, ok := previewutil.AuthorizedSessionIDFromHost(before); ok {
		t.Fatal("capability minted under another key still opens a preview after the server set its key")
	}
	if _, ok := previewutil.AuthorizedSessionIDFromHost(capHost(t, "tp-1")); !ok {
		t.Fatal("a freshly minted capability is not authorized")
	}
}
