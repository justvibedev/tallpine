package preview

import (
	"net/url"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func hostFor(t *testing.T, build func(string, domain.SessionID, string) (string, error), id domain.SessionID) string {
	t.Helper()
	raw, err := build("http://127.0.0.1:3101", id, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestCapabilityHostRoundTripsAndIsLongEnough(t *testing.T) {
	host := hostFor(t, FileURL, "tp-1")
	labels := strings.Split(strings.Split(host, ":")[0], ".")
	capLabel := labels[len(labels)-2]
	if !strings.HasPrefix(capLabel, "k-") || len(capLabel) != 2+32 {
		t.Fatalf("capability label %q, want k- plus 128 bits of hex", capLabel)
	}
	if id, ok := AuthorizedSessionIDFromHost(host); !ok || id != "tp-1" {
		t.Fatalf("authorized = %q %v", id, ok)
	}
	if id, ok := SessionIDFromHost(host); !ok || id != "tp-1" {
		t.Fatalf("tolerant parse = %q %v", id, ok)
	}
}

func TestIDOnlyHostIsNotAuthorizedButStillParses(t *testing.T) {
	host := "ao-preview.mfxs2mi.localhost:3101"
	if _, ok := AuthorizedSessionIDFromHost(host); ok {
		t.Fatal("id-only host was authorized")
	}
	// mfxs2mi is the base32 of the pre-rename session id "ao-1": persisted URLs
	// keep decoding to the id they were minted with.
	if id, ok := SessionIDFromHost(host); !ok || id != "ao-1" {
		t.Fatalf("legacy parse = %q %v: persisted pre-capability URLs must still decode", id, ok)
	}
}

func TestCapabilityIsPerSessionAndPerOrigin(t *testing.T) {
	one := hostFor(t, FileURL, "tp-1")
	two := hostFor(t, FileURL, "tp-2")
	swap := func(host, from string) string {
		a, b := strings.Split(host, "."), strings.Split(from, ".")
		a[len(a)-2] = strings.Split(b[len(b)-2], ":")[0]
		return strings.Join(a, ".")
	}
	if _, ok := AuthorizedSessionIDFromHost(swap(one, two)); ok {
		t.Fatal("session tp-2's capability opened tp-1")
	}
	artifact := hostFor(t, ArtifactFileURL, "tp-1")
	if _, ok := AuthorizedSessionIDFromArtifactHost(swap(artifact, one)); ok {
		t.Fatal("workspace-origin capability opened the artifact origin")
	}
	if _, ok := AuthorizedSessionIDFromInlineArtifactHost(swap(hostFor(t, InlineArtifactFileURL, "tp-1"), one)); ok {
		t.Fatal("workspace-origin capability opened the inline-artifact origin")
	}
}

func TestCapabilityDependsOnTheKey(t *testing.T) {
	t.Cleanup(func() { SetCapabilityKey("") })
	SetCapabilityKey("key-a")
	a := hostFor(t, FileURL, "tp-1")
	if got := hostFor(t, FileURL, "tp-1"); got != a {
		t.Fatal("capability is not stable for one key")
	}
	SetCapabilityKey("key-b")
	if _, ok := AuthorizedSessionIDFromHost(a); ok {
		t.Fatal("a capability survived a key change")
	}
}
