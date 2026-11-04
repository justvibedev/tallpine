package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/releasesigner"
)

// checksumsAsset is the per-release manifest of "<sha256>  <asset>" lines that
// the release tooling publishes next to every asset (scripts/release-checksums).
const checksumsAsset = "SHA256SUMS"

// releaseVersionEnv pins `tallpine start` to one release tag instead of "latest",
// for managed installs. The --release-version flag overrides it.
const releaseVersionEnv = "TALLPINE_RELEASE_VERSION"

// allowUnverifiedSignerEnv ("1") is the dev opt-out for a build without an
// injected Team ID; the --allow-unverified-signer flag does the same.
const allowUnverifiedSignerEnv = "TALLPINE_ALLOW_UNVERIFIED_SIGNER"

// errNoReleaseSource is returned when this build has no release repository.
var errNoReleaseSource = errors.New("tallpine start: this build has no release source configured, so it will not download an app; " +
	"install " + brand.ProductName + " manually, or use a build with a configured release source (TALLPINE_RELEASE_GITHUB_REPO at build time)")

var releaseTagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// releaseDownloadBase is the host serving release assets; tests point it at a
// fake server.
var releaseDownloadBase = "https://github.com"

// validateReleaseTag rejects tags that could alter the download URL path.
func validateReleaseTag(tag string) error {
	if tag == "" || releaseTagPattern.MatchString(tag) {
		return nil
	}
	return fmt.Errorf("tallpine start: invalid release version %q", tag)
}

// resolveReleaseTag returns the pinned tag from the flag, else the env var, else
// "" meaning latest.
func resolveReleaseTag(flagValue string) (string, error) {
	tag := strings.TrimSpace(flagValue)
	if tag == "" {
		tag = strings.TrimSpace(os.Getenv(releaseVersionEnv))
	}
	if err := validateReleaseTag(tag); err != nil {
		return "", err
	}
	return tag, nil
}

// parseChecksums parses sha256sum-format lines ("<hex>  <name>" or "<hex> *<name>").
func parseChecksums(r io.Reader) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return sums, sc.Err()
}

// fetchChecksum downloads SHA256SUMS for the release and returns the hash listed
// for asset. A missing manifest or a missing entry is an error: refusing is the
// only safe outcome when the hash cannot be established.
func (c *commandContext) fetchChecksum(ctx context.Context, tag, asset string) (string, error) {
	url := downloadURLFor(tag, checksumsAsset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", err
	}
	resp, err := c.deps.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("tallpine start: download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tallpine start: cannot verify release: %s returned %s (the release publishes no %s; refusing to install an unverified download)", url, resp.Status, checksumsAsset)
	}
	sums, err := parseChecksums(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("tallpine start: read %s: %w", checksumsAsset, err)
	}
	want, ok := sums[asset]
	if !ok {
		return "", fmt.Errorf("tallpine start: cannot verify release: %s has no entry for %s", checksumsAsset, asset)
	}
	return want, nil
}

// verifyFileSHA256 hashes path and compares it with want.
func verifyFileSHA256(path, want string) error {
	f, err := os.Open(path) //nolint:gosec // G304: path is under the staging dir we created
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("tallpine start: checksum mismatch for downloaded release (expected %s, got %s); refusing to install", want, got)
	}
	return nil
}

// downloadVerified downloads asset to dst and verifies it against the release's
// SHA256SUMS before returning; on mismatch dst is removed.
func (c *commandContext) downloadVerified(ctx context.Context, w io.Writer, tag, asset, dst string) error {
	url := downloadURLFor(tag, asset)
	want, err := c.fetchChecksum(ctx, tag, asset)
	if err != nil {
		return err
	}
	if err := c.download(ctx, w, url, asset, dst); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if err := verifyFileSHA256(dst, want); err != nil {
		_ = os.Remove(dst)
		return err
	}
	_, _ = fmt.Fprintf(w, "Verified SHA-256 of %s.\n", asset)
	return nil
}

var teamIDLine = regexp.MustCompile(`(?m)^TeamIdentifier=(.*)$`)

// verifyBundleSignature runs codesign --verify --deep --strict on the unpacked
// bundle and, when a Team ID was injected at build time, checks the signer.
func (c *commandContext) verifyBundleSignature(ctx context.Context, w io.Writer, appPath string, allowUnverified bool) error {
	args := []string{"--verify", "--deep", "--strict"}
	if releasesigner.ExpectedTeamID != "" {
		if !releasesigner.TeamIDPattern.MatchString(releasesigner.ExpectedTeamID) {
			return fmt.Errorf("tallpine start: this build's expected Team ID %q is malformed; refusing to install", releasesigner.ExpectedTeamID)
		}
		// An explicit requirement anchored to Apple's roots: without -R codesign only
		// checks the bundle against its own signer-chosen designated requirement,
		// and TeamIdentifier is plain certificate metadata a self-made cert can set.
		args = append(args, "-R="+releasesigner.TeamRequirement(releasesigner.ExpectedTeamID))
	}
	args = append(args, appPath)
	if out, err := c.deps.CommandOutput(ctx, "codesign", args...); err != nil {
		msg := strings.TrimSpace(string(out))
		if releasesigner.ExpectedTeamID != "" {
			msg += teamMismatchHint(c.deps.CommandOutput(ctx, "codesign", "-dv", "--verbose=4", appPath))
		}
		return fmt.Errorf("tallpine start: code signature verification failed for the unpacked app; refusing to install: %w: %s", err, msg)
	}
	if releasesigner.ExpectedTeamID == "" {
		if !allowUnverified {
			return fmt.Errorf("tallpine start: this build has no expected Team ID configured, so the app's signer cannot be verified; refusing to install "+
				"(dev builds only: pass --allow-unverified-signer or set %s=1)", allowUnverifiedSignerEnv)
		}
		_, _ = fmt.Fprintln(w, "WARNING: installing WITHOUT verifying the signer (no expected Team ID in this build; --allow-unverified-signer given). Do not use this for managed installs.")
	}
	return nil
}

// teamMismatchHint turns `codesign -dv` output into a friendly suffix naming
// the Team ID the bundle claims; informational only, never a trust decision.
func teamMismatchHint(out []byte, err error) string {
	if err != nil {
		return ""
	}
	if m := teamIDLine.FindStringSubmatch(string(out)); m != nil {
		return fmt.Sprintf(" (bundle claims Team ID %q, expected %q)", strings.TrimSpace(m[1]), releasesigner.ExpectedTeamID)
	}
	return ""
}
