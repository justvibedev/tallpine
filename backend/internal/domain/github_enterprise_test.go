package domain

import "testing"

func TestGitHubEnterpriseListedHostRegistry(t *testing.T) {
	t.Cleanup(func() { SetGitHubEnterpriseHosts(nil) })
	if IsGitHubHost("ghes.corp.example") {
		t.Fatal("unlisted host must not be GitHub by default")
	}
	SetGitHubEnterpriseHosts([]string{" GHES.corp.example ", "ghes2.example:8443"})
	for host, want := range map[string]bool{
		"ghes.corp.example": true, "GHES.CORP.EXAMPLE": true, "ghes2.example:8443": true,
		"ghes2.example": false, "evil.ghes.corp.example": false,
	} {
		if got := IsGitHubHost(host); got != want {
			t.Errorf("IsGitHubHost(%q) = %v, want %v", host, got, want)
		}
	}
	if got := RepositoryProvider("ghes.corp.example"); got != "github" {
		t.Errorf("RepositoryProvider = %q", got)
	}
	ref, err := ParsePastedChangeRequestURL("https://ghes2.example:8443/o/r/pull/3")
	if err != nil || ref.Provider != "github" || ref.Host != "ghes2.example:8443" {
		t.Fatalf("listed port PR URL = %+v, %v", ref, err)
	}
	if _, err := ParsePastedChangeRequestURL("https://ghes2.example:9999/o/r/pull/3"); err == nil {
		t.Fatal("other port accepted")
	}
	if InferTrackerProvider("https://ghes.corp.example/o/r.git") != TrackerProviderGitHub {
		t.Fatal("tracker must use the shared GitHub host rule")
	}
}

func TestParseRepositoryIdentityListedGHESPort(t *testing.T) {
	SetGitHubEnterpriseHosts([]string{"ghes2.example:8443"})
	t.Cleanup(func() { SetGitHubEnterpriseHosts(nil) })
	id, err := ParseRepositoryIdentity("https://ghes2.example:8443/o/r.git")
	if err != nil || id.Provider != "github" || id.Host != "ghes2.example:8443" {
		t.Fatalf("identity = %+v, %v", id, err)
	}
	if _, err := ParseRepositoryIdentity("https://ghes2.example:8443/a/b/c"); err == nil {
		t.Fatal("github hosts take exactly owner/repo")
	}
}

func TestInferTrackerProviderGHEHosts(t *testing.T) {
	SetGitHubEnterpriseHosts([]string{"ghes.corp.example"})
	t.Cleanup(func() { SetGitHubEnterpriseHosts(nil) })
	for _, u := range []string{"https://acme.ghe.com/o/r", "https://ghes.corp.example/o/r", "git@acme.ghe.com:o/r.git"} {
		if got := InferTrackerProvider(u); got != TrackerProviderGitHub {
			t.Errorf("%s = %q, want github", u, got)
		}
	}
}

func TestPRLifecycleState(t *testing.T) {
	if got := PRLifecycleState(true, true, true); got != PRStateMerged {
		t.Fatal(got)
	}
	if got := PRLifecycleState(false, true, true); got != PRStateClosed {
		t.Fatal(got)
	}
	if got := PRLifecycleState(false, false, true); got != PRStateDraft {
		t.Fatal(got)
	}
}
