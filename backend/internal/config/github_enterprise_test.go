package config

import (
	"reflect"
	"testing"
)

func TestLoadGitHubEnterprise(t *testing.T) {
	t.Setenv("TALLPINE_GITHUB_ENTERPRISE_HOSTS", " Ghes.Corp.Example, ,ghes2:8443 ")
	t.Setenv("TALLPINE_GITHUB_HOST_TOKENS", "GHES.corp.example=tok1, acme.ghe.com = tok2")
	got, err := LoadGitHubEnterprise()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ghes.corp.example", "ghes2:8443"}; !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("hosts = %v", got.Hosts)
	}
	if got.HostTokens["ghes.corp.example"] != "tok1" || got.HostTokens["acme.ghe.com"] != "tok2" {
		t.Fatalf("tokens = %v", got.HostTokens)
	}
}

func TestLoadGitHubEnterpriseRejectsAmbiguousToken(t *testing.T) {
	t.Setenv("TALLPINE_GITHUB_HOST_TOKENS", "h=a=b")
	if _, err := LoadGitHubEnterprise(); err == nil {
		t.Fatal("want error")
	}
}
