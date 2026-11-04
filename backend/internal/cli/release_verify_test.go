package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/releasesigner"
)

const testAsset = "tallpine-darwin-arm64.zip"

func sumOf(b string) string { h := sha256.Sum256([]byte(b)); return hex.EncodeToString(h[:]) }

// releaseServer serves asset bytes and a SHA256SUMS listing sums (nil => 404).
func releaseServer(t *testing.T, body string, sums map[string]string, paths *[]string) *commandContext {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if paths != nil {
			*paths = append(*paths, r.URL.Path)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+checksumsAsset):
			if sums == nil {
				http.NotFound(w, r)
				return
			}
			for name, sum := range sums {
				_, _ = w.Write([]byte(sum + "  " + name + "\n"))
			}
		case strings.HasSuffix(r.URL.Path, "/"+testAsset):
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	orig, origRepo := releaseDownloadBase, releaseRepo
	releaseDownloadBase, releaseRepo = srv.URL, "o/r"
	t.Cleanup(func() { releaseDownloadBase, releaseRepo = orig, origRepo })
	return &commandContext{deps: Deps{HTTPClient: srv.Client()}.withDefaults()}
}

func TestDownloadVerified_AcceptsMatchingHash(t *testing.T) {
	c := releaseServer(t, "zip", map[string]string{testAsset: sumOf("zip")}, nil)
	dst := filepath.Join(t.TempDir(), testAsset)
	if err := c.downloadVerified(context.Background(), &bytes.Buffer{}, "", testAsset, dst); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadVerified_RefusesMismatchAndRemovesFile(t *testing.T) {
	c := releaseServer(t, "tampered", map[string]string{testAsset: sumOf("zip")}, nil)
	dst := filepath.Join(t.TempDir(), testAsset)
	err := c.downloadVerified(context.Background(), &bytes.Buffer{}, "", testAsset, dst)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("tampered download left on disk")
	}
}

func TestDownloadVerified_RefusesMissingManifestOrEntry(t *testing.T) {
	c := releaseServer(t, "zip", nil, nil)
	if err := c.downloadVerified(context.Background(), &bytes.Buffer{}, "", testAsset, filepath.Join(t.TempDir(), "a")); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("missing manifest must refuse, got %v", err)
	}
	c = releaseServer(t, "zip", map[string]string{"other.zip": sumOf("zip")}, nil)
	if err := c.downloadVerified(context.Background(), &bytes.Buffer{}, "", testAsset, filepath.Join(t.TempDir(), "a")); err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("missing entry must refuse, got %v", err)
	}
}

func TestDownloadVerified_PinnedTagUsesTaggedURLs(t *testing.T) {
	var paths []string
	c := releaseServer(t, "zip", map[string]string{testAsset: sumOf("zip")}, &paths)
	if err := c.downloadVerified(context.Background(), &bytes.Buffer{}, "v1.2.3", testAsset, filepath.Join(t.TempDir(), "a")); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if !strings.Contains(p, "/releases/download/v1.2.3/") {
			t.Fatalf("unpinned path %q", p)
		}
	}
}

func TestResolveReleaseTag(t *testing.T) {
	t.Setenv(releaseVersionEnv, "v9")
	if got, _ := resolveReleaseTag(""); got != "v9" {
		t.Fatalf("env: %q", got)
	}
	if got, _ := resolveReleaseTag("v1"); got != "v1" {
		t.Fatalf("flag must win: %q", got)
	}
	if _, err := resolveReleaseTag("../evil"); err == nil {
		t.Fatal("path-traversal tag accepted")
	}
}

type fakeCodesign struct {
	verifyErr error
	details   string
	calls     [][]string
}

func (f *fakeCodesign) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if args[0] == "--verify" {
		return []byte("bad"), f.verifyErr
	}
	return []byte(f.details), nil
}

func codesignCtx(f *fakeCodesign) *commandContext {
	d := Deps{}.withDefaults()
	d.CommandOutput = f.run
	return &commandContext{deps: d}
}

func TestVerifyBundleSignature(t *testing.T) {
	orig := releasesigner.ExpectedTeamID
	t.Cleanup(func() { releasesigner.ExpectedTeamID = orig })
	ctx := context.Background()

	releasesigner.ExpectedTeamID = "TEAM123456"
	ok := &fakeCodesign{details: "Identifier=x\nTeamIdentifier=TEAM123456\n"}
	if err := codesignCtx(ok).verifyBundleSignature(ctx, &bytes.Buffer{}, "/a.app", false); err != nil {
		t.Fatal(err)
	}
	wantReq := `-R=anchor apple generic and certificate leaf[subject.OU] = "TEAM123456" and certificate 1[field.1.2.840.113635.100.6.2.6] and certificate leaf[field.1.2.840.113635.100.6.1.13]`
	if got := strings.Join(ok.calls[0], " "); got != "codesign --verify --deep --strict "+wantReq+" /a.app" {
		t.Fatalf("verify must pin an Apple-anchored requirement, got: %s", got)
	}
	// A bundle failing the requirement (e.g. self-made cert claiming the Team ID) is refused.
	wrong := &fakeCodesign{verifyErr: errors.New("exit 3"), details: "TeamIdentifier=OTHER00000\n"}
	if err := codesignCtx(wrong).verifyBundleSignature(ctx, &bytes.Buffer{}, "/a.app", false); err == nil || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("wrong team must refuse: %v", err)
	}
	bad := &fakeCodesign{verifyErr: errors.New("exit 1")}
	if err := codesignCtx(bad).verifyBundleSignature(ctx, &bytes.Buffer{}, "/a.app", false); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("invalid signature must refuse: %v", err)
	}
}

func TestVerifyBundleSignature_NoInjectedTeamIDRefusesByDefault(t *testing.T) {
	orig := releasesigner.ExpectedTeamID
	releasesigner.ExpectedTeamID = ""
	t.Cleanup(func() { releasesigner.ExpectedTeamID = orig })
	err := codesignCtx(&fakeCodesign{}).verifyBundleSignature(context.Background(), &bytes.Buffer{}, "/a.app", false)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("missing Team ID must refuse by default, got %v", err)
	}
}

func TestRunStart_NoReleaseSourceDoesNotDownload(t *testing.T) {
	orig := releaseRepo
	releaseRepo = ""
	t.Cleanup(func() { releaseRepo = orig })
	hit := false
	c := &commandContext{deps: Deps{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		hit = true
		return nil, errors.New("network used")
	})}}.withDefaults()}
	_, err := c.fetchApp(context.Background(), &bytes.Buffer{}, "", false)
	if !errors.Is(err, errNoReleaseSource) || hit {
		t.Fatalf("want errNoReleaseSource without network, got %v hit=%v", err, hit)
	}
}

func TestVerifyBundleSignature_NoInjectedTeamIDWarns(t *testing.T) {
	orig := releasesigner.ExpectedTeamID
	releasesigner.ExpectedTeamID = ""
	t.Cleanup(func() { releasesigner.ExpectedTeamID = orig })
	var out bytes.Buffer
	f := &fakeCodesign{}
	if err := codesignCtx(f).verifyBundleSignature(context.Background(), &out, "/a.app", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "WARNING") || len(f.calls) != 1 {
		t.Fatalf("want warning and only the verify call, out=%q calls=%d", out.String(), len(f.calls))
	}
}
