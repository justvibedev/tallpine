package systeminstall

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestParseOfficialVersionRejectsMetadataWithoutVersion(t *testing.T) {
	source, _ := officialSourceFor(TargetKiro)
	if _, err := parseOfficialVersion(source, []byte(`{"packages":[]}`)); err == nil {
		t.Fatal("expected an error for a manifest without a version")
	}
	source, _ = officialSourceFor(Target("grok"))
	if _, err := parseOfficialVersion(source, []byte("<html>maintenance</html>")); err == nil {
		t.Fatal("expected an error for a non-version body")
	}
}

func TestUpdateAdvisoryFallsBackToOfficialReleaseForUnownedBinary(t *testing.T) {
	s := newTestService("darwin", "npm", "brew")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.local/bin/claude", Output: "2.1.200 (Claude Code)\n"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	var packageLookup atomic.Bool
	s.managedVersion = func(context.Context, Plan) (string, error) {
		packageLookup.Store(true)
		return "9.9.9", nil
	}
	s.officialVersion = func(_ context.Context, target Target) (string, error) {
		if target != TargetClaudeCode {
			t.Fatalf("official lookup for %s", target)
		}
		return "2.1.291", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusBehindLatest || advisory.Source != officialReleaseSource || advisory.CurrentVersion != "2.1.200" || advisory.LatestVersion != "2.1.291" {
		t.Fatalf("advisory = %+v", advisory)
	}
	if packageLookup.Load() {
		t.Fatal("package registry was queried for a binary no package manager owns")
	}
}

func TestUpdateAdvisoryPrefersOwningPackageOverOfficialRelease(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{Output: "codex-cli 1.3.0"}, nil
	})
	s.ownsInstallation = func(_ context.Context, _ string, method, _ string, _ bool) (bool, error) { return method == "npm", nil }
	s.managedVersion = fixedManagedVersion("1.3.0", nil)
	s.officialVersion = func(context.Context, Target) (string, error) {
		t.Fatal("official release queried for a package-owned binary")
		return "", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusCurrent || advisory.Source != "npm" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryUnknownWithoutOfficialSource(t *testing.T) {
	s := newTestService("darwin")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) { return VerifyResult{Output: "qwen 0.1.0"}, nil })
	s.officialVersion = func(context.Context, Target) (string, error) { return "", errNoOfficialSource }
	advisory, err := s.UpdateAdvisory(context.Background(), TargetCodex, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusUnknown || advisory.LatestVersion != "" || advisory.Reason != "unsupported_source" {
		t.Fatalf("advisory = %+v", advisory)
	}
}

func TestUpdateAdvisoryComparesOfficialPrereleaseWithLatest(t *testing.T) {
	for _, tt := range []struct {
		installed string
		want      UpdateStatus
	}{
		{installed: "2.0.0-beta.1", want: UpdateStatusBehindLatest},
		{installed: "2.1.0-beta.1", want: UpdateStatusAheadOfLatest},
	} {
		t.Run(tt.installed, func(t *testing.T) {
			s := newTestService("darwin")
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: "/Users/test/.local/bin/claude", Output: "claude " + tt.installed}, nil
			})
			s.officialVersion = func(context.Context, Target) (string, error) { return "2.0.0", nil }
			advisory, err := s.UpdateAdvisory(context.Background(), TargetClaudeCode, false)
			if err != nil {
				t.Fatal(err)
			}
			if advisory.Status != tt.want || advisory.LatestVersion != "2.0.0" || advisory.Reason != "" {
				t.Fatalf("advisory = %+v", advisory)
			}
		})
	}
}
