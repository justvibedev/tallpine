package preview

import "testing"

func TestIsSecretPath(t *testing.T) {
	for path, want := range map[string]bool{
		// Ordinary project files, dotfiles included.
		"index.html":                  false,
		".github/workflows/ci.yml":    false,
		".claude/settings.json":       false,
		".storybook/main.ts":          false,
		".tallpine/attachments/a.png": false,
		".ao/attachments/a.png":       false,
		".ao/launch.json":             true,
		".well-known/assetlinks.json": false,
		".env.example":                false,
		"config/.env.sample":          false,
		"docs/keys.md":                false,
		"src/monkey.ts":               false,
		"gitignore/.gitkeep":          false,
		"public/.htaccess":            false,
		".dockerignore":               false,
		".docker/daemon.example.json": false,
		// Credentials and repository internals.
		".env":                           true,
		".env.local":                     true,
		"web/.ENV.Production":            true,
		".git/config":                    true,
		".git":                           true,
		"vendor/lib/.git/HEAD":           true,
		".ssh/id_ed25519":                true,
		"deploy/id_rsa":                  true,
		".aws/credentials":               true,
		".kube/config":                   true,
		".npmrc":                         true,
		"pkg\\.npmrc":                    true,
		".netrc":                         true,
		".pypirc":                        true,
		".git-credentials":               true,
		".docker/config.json":            true,
		"certs/server.key":               true,
		"certs/ca.PEM":                   true,
		"infra/terraform.tfstate":        true,
		"infra/terraform.tfstate.backup": true,
		".tallpine/daemon.token":         true,
		".tallpine/launch.json":          true,
		".tallpine/attachments/b.pdf":    false,
		// Defense in depth: variants of the same secrets.
		"a/b/c/id_ed25519_sk":          true,
		"deploy/id_rsa.bak":            true,
		"keys/id_ecdsa_old":            true,
		"id_rsa/anything":              true,
		"certs/server.key.bak":         true,
		"certs/ca.pem~":                true,
		"certs/ca.PEM.old":             true,
		"ci/release.keystore":          true,
		"ci/upload.jks":                true,
		"infra/prod.tfvars":            true,
		".terraform/terraform.tfstate": true,
		"x/.aws/config":                true,
		"web/.envrc":                   true,
		"gcp/credentials.json":         true,
		"home/.kube/config":            true,
		"deploy/id_rsa.pub":            false,
		"src/id_card.png":              false,
		"src/keyboard.key.ts":          false,
	} {
		if got := IsSecretPath(path); got != want {
			t.Errorf("IsSecretPath(%q) = %v, want %v", path, got, want)
		}
	}
}
