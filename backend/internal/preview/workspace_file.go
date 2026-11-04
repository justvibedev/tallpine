package preview

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// CleanWorkspacePath normalizes a browser/workspace path without discarding
// meaningful whitespace from valid Unix filenames. The leading slash used by
// browser requests is treated as the preview root, not as a host filesystem
// root.
func CleanWorkspacePath(raw string) (string, bool) {
	raw = strings.ReplaceAll(raw, `\`, "/")
	for _, segment := range strings.Split(raw, "/") {
		if segment == ".." {
			return "", false
		}
	}
	clean := strings.TrimPrefix(path.Clean("/"+raw), "/")
	if clean == "" || clean == "." {
		return "", false
	}
	return clean, true
}

// OpenWorkspaceFile opens a regular file beneath workspacePath using os.Root.
// os.Root follows symlinks that remain inside the workspace and rejects links
// that escape it, so callers can safely serve the returned handle without a
// second path lookup. Anything but a regular file is fs.ErrNotExist.
func OpenWorkspaceFile(workspacePath, assetPath string) (*os.File, fs.FileInfo, string, error) {
	clean, ok := CleanWorkspacePath(assetPath)
	if !ok {
		return nil, nil, "", fs.ErrNotExist
	}
	root, err := os.OpenRoot(workspacePath)
	if err != nil {
		return nil, nil, "", err
	}
	defer func() { _ = root.Close() }()

	// O_NONBLOCK: opening a FIFO would otherwise block until a writer appears,
	// and the path can be swapped for one at any time. A regular file's reads
	// ignore it. What was opened is checked on the open file.
	file, err := root.OpenFile(filepath.FromSlash(clean), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, "", err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, "", fs.ErrNotExist
	}
	return file, info, clean, nil
}

// EntryAtPath resolves an existing regular workspace file with the same rooted
// confinement used by the HTTP serving path.
func EntryAtPath(workspacePath, assetPath string) (Entry, bool) {
	file, info, clean, err := OpenWorkspaceFile(workspacePath, assetPath)
	if err != nil {
		return Entry{}, false
	}
	_ = file.Close()
	absPath, _ := ConfinedPath(workspacePath, clean)
	return Entry{Path: clean, AbsPath: absPath, ModTime: info.ModTime(), Size: info.Size()}, true
}

// secretDirs are directories whose contents are credentials or repository
// internals (".git/config" can carry tokens in remote URLs).
var secretDirs = map[string]bool{
	".git": true, ".ssh": true, ".aws": true, ".gnupg": true, ".kube": true,
	".azure": true, ".terraform": true, ".gcloud": true,
}

// secretFiles are file names that hold credentials.
var secretFiles = map[string]bool{
	".npmrc": true, ".netrc": true, ".pypirc": true, ".yarnrc.yml": true,
	".pgpass": true, ".git-credentials": true, ".htpasswd": true, ".dockercfg": true,
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	".envrc": true, "credentials.json": true, "kubeconfig": true,
}

// secretSuffixes are extensions of key, certificate-bundle and state files.
var secretSuffixes = []string{
	".pem", ".key", ".p12", ".pfx", ".tfstate", ".tfstate.backup",
	".jks", ".keystore", ".ppk", ".p8", ".kdbx", ".tfvars",
}

// backupSuffixes are what editors and humans append to a copy of a secret
// file (server.key.bak, server.pem~) without making it any less secret.
var backupSuffixes = []string{"~", ".bak", ".old", ".orig", ".backup", ".save", ".swp", ".tmp"}

// isPrivateKeyName matches OpenSSH key file names and their variants
// (id_ed25519_sk, id_rsa.bak); the public half (.pub) is not a secret.
func isPrivateKeyName(segment string) bool {
	if strings.HasSuffix(segment, ".pub") {
		return false
	}
	for _, kind := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
		if strings.HasPrefix(segment, kind) {
			return true
		}
	}
	return false
}

// secretByName reports whether one path segment names a credential file: a
// private-key name (id_rsa, id_ed25519_sk, id_rsa.bak, but not id_rsa.pub), or
// a key/certificate/state extension, optionally followed by a backup suffix.
func secretByName(segment string) bool {
	if isPrivateKeyName(segment) {
		return true
	}
	for _, backup := range append([]string{""}, backupSuffixes...) {
		base, ok := strings.CutSuffix(segment, backup)
		if !ok {
			continue
		}
		for _, suffix := range secretSuffixes {
			if strings.HasSuffix(base, suffix) {
				return true
			}
		}
	}
	return false
}

// envTemplates are .env variants meant to be committed and shared.
var envTemplates = map[string]bool{
	".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true,
}

// IsSecretPath reports whether a slash- or backslash-separated
// workspace-relative path names a file likely to hold credentials: anything
// under .git, .ssh, .aws, .gnupg, .kube, .azure, .terraform, .gcloud, or under .tallpine (or the legacy .ao) other
// than .tallpine/attachments; .env and .env.*
// (except shared templates such as .env.example); .npmrc, .netrc, .pypirc and
// similar; SSH private keys; .docker/config.json; and *.pem, *.key, *.p12,
// *.pfx and Terraform state. Other dotfiles (.github, .claude, .storybook,
// .tallpine/attachments) are ordinary project files. Matching ignores case, since
// macOS file systems do.
func IsSecretPath(rel string) bool {
	segments := strings.Split(strings.ToLower(strings.ReplaceAll(rel, `\`, "/")), "/")
	for i, segment := range segments {
		// A secret directory, or a file of that name (a worktree's ".git"
		// file names its git dir).
		if secretDirs[segment] || secretFiles[segment] {
			return true
		}
		if segment == ".env" || (strings.HasPrefix(segment, ".env.") && !envTemplates[segment]) {
			return true
		}
		if segment == ".docker" && i+1 < len(segments) && segments[i+1] == "config.json" {
			return true
		}
		// Tallpine's own per-workspace dir: only chat attachments are content the
		// Browser tab opens; launch.json and the rest are configuration.
		if (segment == ".tallpine" || segment == ".ao") && i < len(segments)-1 && segments[i+1] != "attachments" {
			return true
		}
		if i == len(segments)-1 && secretByName(segment) {
			return true
		}
		// A private key in any directory, whatever the file around it is called.
		if i < len(segments)-1 && isPrivateKeyName(segment) {
			return true
		}
	}
	return false
}
