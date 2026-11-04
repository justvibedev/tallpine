package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/config"
)

// releaseRepo is the GitHub "owner/repo" that `tallpine start` fetches the desktop app
// from. It defaults to the production target and is overridable at build time so
// a test binary fetches from the fork without a source edit:
//
//	go build -ldflags "-X tallpine.eu/tallpine/backend/internal/cli.releaseRepo=<owner>/<repo>" ./cmd/tallpine
//
// Mirrors how version.go's Version var is stamped by release tooling.
//
// It is empty in source on purpose: a plain build must never download and
// install an app from a repository the release configuration did not name.
// scripts/daemon-build.sh injects it from TALLPINE_RELEASE_GITHUB_REPO. When empty,
// `tallpine start` does not download anything.
var releaseRepo = ""

// appBundleName is the macOS bundle directory name produced by electron-forge
// (spaced, per frontend/forge.config.ts).
const appBundleName = brand.ProductName + ".app"

// appStateFileName is the marker the desktop app writes under ~/.tallpine on every
// launch (spec §5). `tallpine start` is a read-only consumer of it.
const appStateFileName = "app-state.json"

// appState mirrors the app-written ~/.tallpine/app-state.json marker (spec §5). Only
// the desktop app writes it; `tallpine start` reads it as a fast-path hint and never
// trusts appPath without stat-ing it (invariant 2).
type appState struct {
	SchemaVersion    int    `json:"schemaVersion"`
	AppPath          string `json:"appPath"`
	Version          string `json:"version"`
	InstalledAt      string `json:"installedAt"`
	LastReconciledAt string `json:"lastReconciledAt"`
	InstallSource    string `json:"installSource"`
}

type startOptions struct {
	json                  bool
	releaseVersion        string
	allowUnverifiedSigner bool
}

// startResult is the JSON shape emitted with --json: what `tallpine start` resolved,
// whether it fetched, whether it opened, and the resulting bundle path.
type startResult struct {
	Resolved bool   `json:"resolved"`
	Fetched  bool   `json:"fetched"`
	Opened   bool   `json:"opened"`
	AppPath  string `json:"appPath"`
}

func newStartCommand(ctx *commandContext) *cobra.Command {
	opts := startOptions{}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Fetch (if needed) and open the " + brand.ProductName + " desktop app",
		Long: "Fetch (if needed) and open the " + brand.ProductName + " desktop app.\n\n" +
			"The desktop app now owns the daemon, state, and updates. `tallpine start` no\n" +
			"longer runs a daemon: it resolves the installed app (or downloads the\n" +
			"latest release), opens it, and exits.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runStart(cmd.Context(), cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output start result as JSON")
	cmd.Flags().StringVar(&opts.releaseVersion, "release-version", "", "Install this release tag instead of the latest (env "+releaseVersionEnv+")")
	cmd.Flags().BoolVar(&opts.allowUnverifiedSigner, "allow-unverified-signer", false, "DEV ONLY: install even though this build has no expected Team ID (env "+allowUnverifiedSignerEnv+"=1)")
	return cmd
}

// runStart implements the spec §6.1 algorithm: resolve the installed app, fetch
// it if absent, open it, then print the deprecation notice. It never blocks or
// supervises the launched app.
func (c *commandContext) runStart(ctx context.Context, cmd *cobra.Command, opts startOptions) error {
	// The installed app owns the human's real state. An agent session already
	// runs inside it; a worker following old "go run ./cmd/tallpine start" advice
	// would open (or relaunch) the production app instead of a dev build.
	if sessionID, ok := config.InAgentSession(os.Getenv); ok {
		return fmt.Errorf("refusing to open the installed %s from inside agent session %s: it owns the real state; "+
			"run a dev build instead (`cd frontend && npm run dev` isolates its state per checkout), or set %s=1",
			brand.ProductName, sessionID, config.AllowRealDataEnv)
	}
	out := cmd.OutOrStdout()
	res := startResult{}

	appPath := c.resolveApp()
	res.Resolved = appPath != ""

	var err error
	if appPath == "" {
		tag, tagErr := resolveReleaseTag(opts.releaseVersion)
		if tagErr != nil {
			return tagErr
		}
		// Progress for the fetch path goes to stderr so stdout stays pure JSON
		// under --json. The resolve-and-launch fast path above stays quiet.
		appPath, err = c.fetchApp(ctx, cmd.ErrOrStderr(), tag, opts.allowUnverifiedSigner || os.Getenv(allowUnverifiedSignerEnv) == "1")
		if err != nil {
			return err
		}
		res.Fetched = true
	}
	res.AppPath = appPath

	if runtime.GOOS == "linux" {
		if err := c.registerLinuxProtocolHandler(ctx, appPath); err != nil {
			return err
		}
	}

	opened, err := c.openApp(ctx, appPath)
	if err != nil {
		return err
	}
	res.Opened = opened

	if opts.json {
		return writeJSON(out, res)
	}

	c.printDeprecationNotice(out)
	if !opened {
		c.printManualOpen(out, appPath)
	}
	return nil
}

// resolveApp returns the path to a usable desktop bundle, or "" when none is
// found (spec §6.2). Resolution order is marker path -> stat -> known location
// scan, with one exception: on macOS a real bundle in /Applications outranks
// the marker. It still never compares versions (invariant 5).
//
// The exception exists because the marker is not authoritative about which copy
// the user wants. The app rewrites it on every launch, so a stale copy (the
// original download, one on a dmg) that got launched leaves the marker pointing
// at itself, and `tallpine start` would then keep reopening that copy instead of the
// install. /Applications is the one location the updater maintains, so prefer
// it and let the marker cover the rest (~/Applications, unusual layouts).
func (c *commandContext) resolveApp() string {
	if p := preferredAppPath(); p != "" && isUsableBundle(p) {
		return p
	}
	if p := c.markerAppPath(); p != "" && isUsableBundle(p) {
		return p
	}
	for _, p := range appScanLocations() {
		if isUsableBundle(p) {
			return p
		}
	}
	return ""
}

// preferredAppPath is the location that outranks the marker, or "" when the
// platform has none. A package var for the same reason as appScanLocations:
// tests point it at a temp bundle instead of the real system path.
var preferredAppPath = darwinApplicationsBundle

// darwinApplicationsBundle is /Applications/<bundle> on macOS, "" elsewhere.
// That is where Squirrel installs updates, so it is the copy actually kept
// current. Windows and Linux have no equivalent single maintained location, so
// they keep the original marker-first order.
func darwinApplicationsBundle() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	// Concatenated, not filepath.Join'd, to match knownAppLocations (and to keep
	// gocritic's filepathJoin check happy about the leading separator).
	return "/Applications/" + appBundleName
}

// appScanLocations is the known-location scan source. It is a package var so
// tests can point the scan at a temp bundle instead of real system paths.
var appScanLocations = knownAppLocations

// markerAppPath reads ~/.tallpine/app-state.json and returns its recorded appPath, or
// "" if the marker is missing/unreadable. It does not stat the path; callers do.
func (c *commandContext) markerAppPath() string {
	dir, err := tallpineStateDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, appStateFileName))
	if err != nil {
		return ""
	}
	var st appState
	if err := json.Unmarshal(data, &st); err != nil {
		return ""
	}
	return st.AppPath
}

// tallpineStateDir resolves the canonical ~/.tallpine home, honoring TALLPINE_DATA_DIR exactly as
// the daemon's config does (the marker lives beside running.json under ~/.tallpine).
func tallpineStateDir() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	// running.json lives directly under ~/.tallpine; the marker sits beside it.
	return filepath.Dir(cfg.RunFilePath), nil
}

// knownAppLocations lists the platform's standard install paths to scan when the
// marker misses (covers website installs and stale markers, spec §6.2).
func knownAppLocations() []string {
	switch runtime.GOOS {
	case "darwin":
		paths := []string{"/Applications/" + appBundleName}
		if home, err := os.UserHomeDir(); err == nil {
			paths = append(paths, filepath.Join(home, "Applications", appBundleName))
		}
		return paths
	case "windows":
		var paths []string
		// Default electron-builder NSIS per-user install (perMachine:false).
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			paths = append(paths, windowsInstalledExe(local))
		}
		// Per-machine fallback (if a user chose an all-users install).
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			paths = append(paths, filepath.Join(pf, "Tallpine", "tallpine.exe"))
		}
		return paths
	case "linux":
		paths := []string{linuxAppImagePath()}
		if home, err := os.UserHomeDir(); err == nil {
			paths = append(paths, filepath.Join(home, "Applications", "tallpine.AppImage"))
		}
		return paths
	default:
		return nil
	}
}

// windowsInstalledExe is the default per-user electron-builder NSIS install
// target for the app exe under %LOCALAPPDATA%.
func windowsInstalledExe(localAppData string) string {
	return filepath.Join(localAppData, "Programs", "Tallpine", "tallpine.exe")
}

// linuxAppImagePath is the stable location `tallpine start` downloads the AppImage to
// and scans for. Keeping it out of the cleared staging dir lets re-runs resolve
// the existing download instead of re-fetching (spec §6.2/§6.3).
func linuxAppImagePath() string {
	dir, err := tallpineStateDir()
	if err != nil {
		// Fall back to a bare filename so a misconfigured state dir surfaces as a
		// clear "not found" rather than a panic; fetch will re-error on download.
		return "tallpine.AppImage"
	}
	return filepath.Join(dir, "tallpine.AppImage")
}

const linuxDesktopEntryName = "tallpine-app.desktop"

func linuxApplicationsDir() (string, error) {
	// A URL-scheme handler is OS integration metadata, not Tallpine runtime state.
	// freedesktop.org requires desktop entries under XDG_DATA_HOME; keeping the
	// executable and all mutable Tallpine data under ~/.tallpine is unchanged.
	if dataHome := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dataHome) {
		return filepath.Join(dataHome, "applications"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for Linux protocol registration: %w", err)
	}
	return filepath.Join(home, ".local", "share", "applications"), nil
}

func desktopExecPath(appPath string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		"$", `\$`,
		"%", "%%",
	).Replace(appPath)
	return `"` + escaped + `"`
}

func (c *commandContext) registerLinuxProtocolHandler(
	ctx context.Context,
	appPath string,
) error {
	applicationsDir, err := linuxApplicationsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(applicationsDir, 0o750); err != nil {
		return fmt.Errorf("create Linux applications directory: %w", err)
	}
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Tallpine
Exec=%s %%u
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/tallpine;
`, desktopExecPath(appPath))
	target := filepath.Join(applicationsDir, linuxDesktopEntryName)
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, []byte(entry), 0o600); err != nil { // #nosec G703 -- target is the per-user applications dir plus a fixed file name
		return fmt.Errorf("write Linux desktop entry: %w", err)
	}
	if err := os.Chmod(temporary, 0o644); err != nil { //nolint:gosec // G302: desktop entries must be world-readable
		_ = os.Remove(temporary)
		return fmt.Errorf("chmod Linux desktop entry: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("install Linux desktop entry: %w", err)
	}
	if out, err := c.deps.CommandOutput(
		ctx,
		"xdg-mime",
		"default",
		linuxDesktopEntryName,
		"x-scheme-handler/tallpine",
	); err != nil {
		return fmt.Errorf(
			"register tallpine URL handler with xdg-mime (install xdg-utils or use the .deb/.rpm package): %w: %s",
			err,
			out,
		)
	}
	out, err := c.deps.CommandOutput(
		ctx,
		"xdg-mime",
		"query",
		"default",
		"x-scheme-handler/tallpine",
	)
	if err != nil {
		return fmt.Errorf("verify tallpine URL handler: %w: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != linuxDesktopEntryName {
		return fmt.Errorf(
			"verify tallpine URL handler: xdg-mime selected %q instead of %q",
			strings.TrimSpace(string(out)),
			linuxDesktopEntryName,
		)
	}
	return nil
}

// isUsableBundle reports whether p stats as a usable app bundle. On macOS a
// bundle is a directory; on Windows/Linux it is a regular file (the installed
// exe / the AppImage). The filesystem is the source of truth (invariant 2).
func isUsableBundle(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	if runtime.GOOS == "darwin" {
		return info.IsDir()
	}
	// Windows exe / Linux AppImage: must be a regular file, not a directory.
	return info.Mode().IsRegular()
}

// fetchApp downloads the latest desktop release for this platform and returns
// the resolved bundle path (spec §6.3). macOS unpacks a signed zip into staging,
// Windows runs the NSIS installer silently, and Linux drops a chmod'd AppImage at
// a stable path.
func (c *commandContext) fetchApp(ctx context.Context, w io.Writer, tag string, allowUnverified bool) (string, error) {
	if releaseRepo == "" {
		return "", errNoReleaseSource
	}
	switch runtime.GOOS {
	case "darwin":
		return c.fetchAppDarwin(ctx, w, tag, allowUnverified)
	case "windows":
		return c.fetchAppWindows(ctx, w, tag)
	case "linux":
		return c.fetchAppLinux(ctx, w, tag)
	default:
		return "", fmt.Errorf("tallpine start: fetch not supported on %s", runtime.GOOS)
	}
}

// fetchAppDarwin downloads the latest macOS release zip and unpacks it into a
// staging dir under ~/.tallpine/staging, returning the .app bundle path (spec §6.3).
func (c *commandContext) fetchAppDarwin(ctx context.Context, w io.Writer, tag string, allowUnverified bool) (string, error) {
	asset, err := assetName()
	if err != nil {
		return "", err
	}

	stateDir, err := tallpineStateDir()
	if err != nil {
		return "", err
	}
	staging := filepath.Join(stateDir, "staging")
	// Clear any stale or partial prior unpack so ditto extracts into a clean dir
	// (a leftover bundle could otherwise merge with the new one).
	if err := os.RemoveAll(staging); err != nil {
		return "", fmt.Errorf("clear staging dir: %w", err)
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	zipPath := filepath.Join(staging, asset)
	if err := c.downloadVerified(ctx, w, tag, asset, zipPath); err != nil {
		return "", err
	}

	// The unpack step is silent and can take seconds on a large bundle; announce
	// it so a quiet `tallpine start` doesn't look hung after the download finishes.
	_, _ = fmt.Fprintln(w, "Unpacking...")
	// ditto preserves the .app code signature; plain unzip corrupts it (spec §6.3).
	if out, err := c.deps.CommandOutput(ctx, "ditto", "-x", "-k", zipPath, staging); err != nil {
		return "", fmt.Errorf("ditto unpack: %w: %s", err, out)
	}

	appPath := filepath.Join(staging, appBundleName)
	if !isUsableBundle(appPath) {
		return "", fmt.Errorf("tallpine start: %s not found in unpacked release at %s", appBundleName, staging)
	}
	if err := c.verifyBundleSignature(ctx, w, appPath, allowUnverified); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	return appPath, nil
}

// fetchAppWindows downloads the NSIS installer into staging, runs it silently,
// and returns the default per-user install path (spec §6.3).
//
// ponytail: the silent-install flow (NSIS `/S`, default per-user dir) is
// untested on real Windows hardware (this build host is macOS). If the installed
// exe isn't where electron-builder's defaults put it, this surfaces as a clear
// "not found" error rather than silently launching the wrong thing.
func (c *commandContext) fetchAppWindows(ctx context.Context, w io.Writer, tag string) (string, error) {
	asset, err := assetName()
	if err != nil {
		return "", err
	}

	stateDir, err := tallpineStateDir()
	if err != nil {
		return "", err
	}
	staging := filepath.Join(stateDir, "staging")
	if err := os.RemoveAll(staging); err != nil {
		return "", fmt.Errorf("clear staging dir: %w", err)
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	installerPath := filepath.Join(staging, asset)
	if err := c.downloadVerified(ctx, w, tag, asset, installerPath); err != nil {
		return "", err
	}

	// The `/S` installer runs with no UI and no output; announce it so the wait
	// after the download reads as progress, not a hang.
	_, _ = fmt.Fprintln(w, "Installing...")
	// NSIS silent install (`/S`) to the default per-user location. The installer
	// is configured oneClick:false/perMachine:false, so `/S` installs without UI
	// under %LOCALAPPDATA% for the current user.
	if out, err := c.deps.CommandOutput(ctx, installerPath, "/S"); err != nil {
		return "", fmt.Errorf("silent install: %w: %s", err, out)
	}

	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", fmt.Errorf("tallpine start: LOCALAPPDATA not set; cannot locate installed app")
	}
	appPath := windowsInstalledExe(local)
	if !isUsableBundle(appPath) {
		return "", fmt.Errorf("tallpine start: installed app not found at %s", appPath)
	}
	return appPath, nil
}

// fetchAppLinux downloads the self-contained AppImage to a stable path under
// ~/.tallpine, makes it executable, and returns it. runStart separately installs the
// freedesktop URL handler on every launch, including for an existing AppImage.
func (c *commandContext) fetchAppLinux(ctx context.Context, w io.Writer, tag string) (string, error) {
	asset, err := assetName()
	if err != nil {
		return "", err
	}

	appPath := linuxAppImagePath()
	if err := os.MkdirAll(filepath.Dir(appPath), 0o750); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	// Download to a temp name in the same dir, then rename for atomicity so a
	// killed download never leaves a half-written executable at the stable path.
	tmpPath := appPath + ".part"
	if err := c.downloadVerified(ctx, w, tag, asset, tmpPath); err != nil {
		return "", err
	}
	// chmod+rename is near-instant, but a one-line marker keeps the post-download
	// step from looking silent and matches the other platforms.
	_, _ = fmt.Fprintln(w, "Installing...")
	// An AppImage is a self-contained executable; it must be 0755 to launch.
	if err := os.Chmod(tmpPath, 0o755); err != nil { //nolint:gosec // G302: AppImage must be executable
		return "", fmt.Errorf("chmod AppImage: %w", err)
	}
	if err := os.Rename(tmpPath, appPath); err != nil {
		return "", fmt.Errorf("install AppImage: %w", err)
	}
	if !isUsableBundle(appPath) {
		return "", fmt.Errorf("tallpine start: AppImage not found at %s", appPath)
	}
	return appPath, nil
}

// download streams url to dst using the injected HTTP client, reporting progress
// to w. asset is the human-facing filename used in the announce line. A silent
// hundreds-of-MB fetch reads as a hang, so we print a start line (with the size
// from Content-Length when present) and then progress: a live carriage-return
// percentage when w is an interactive terminal, or a plain start+done pair when
// it is not (CI, pipes, redirected output).
func (c *commandContext) download(ctx context.Context, w io.Writer, url, asset, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	// deps.HTTPClient carries a short (2s) timeout sized for loopback daemon
	// probes; a release asset is hundreds of MB. Copy the client and drop the
	// timeout, relying on ctx for cancellation. The Transport is preserved so
	// tests still reach their httptest server.
	client := *c.deps.HTTPClient
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	// Content-Length is -1 when the server omits the header; total<=0 means the
	// percentage is unknown and we report transferred bytes instead.
	total := resp.ContentLength
	if total > 0 {
		_, _ = fmt.Fprintf(w, "Downloading %s (%s, ~%s) from %s...\n", brand.ProductName, asset, humanBytes(total), releaseRepo)
	} else {
		_, _ = fmt.Fprintf(w, "Downloading %s (%s) from %s...\n", brand.ProductName, asset, releaseRepo)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	pw := &progressWriter{w: w, total: total, tty: writerIsInteractive(w)}
	if _, err := io.Copy(f, io.TeeReader(resp.Body, pw)); err != nil {
		return err
	}
	pw.done()
	return f.Close()
}

// progressWriter counts bytes copied through it and renders download progress to
// w. On an interactive terminal it overwrites a single line with a carriage
// return; otherwise it stays quiet during the copy and prints one "Done" line at
// the end, so logs and pipes never fill with \r spam or per-chunk noise.
type progressWriter struct {
	w       io.Writer
	total   int64 // bytes; <=0 when Content-Length was absent
	tty     bool
	written int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	if p.tty {
		if p.total > 0 {
			pct := p.written * 100 / p.total
			_, _ = fmt.Fprintf(p.w, "\r  %d%% (%s / %s)", pct, humanBytes(p.written), humanBytes(p.total))
		} else {
			_, _ = fmt.Fprintf(p.w, "\r  %s", humanBytes(p.written))
		}
	}
	return n, nil
}

// done emits the terminal progress line. On a TTY it closes the live line with a
// newline; off a TTY it prints the single completion line.
func (p *progressWriter) done() {
	if p.tty {
		_, _ = fmt.Fprintln(p.w)
		return
	}
	_, _ = fmt.Fprintf(p.w, "Downloaded %s.\n", humanBytes(p.written))
}

// writerIsInteractive reports whether w is an interactive terminal, mirroring
// the same rule as elsewhere: only a real *os.File backed by a character device counts,
// so a bytes.Buffer or pipe is treated as non-interactive.
func writerIsInteractive(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// humanBytes formats a byte count with a binary-unit suffix (KiB, MiB, ...),
// e.g. 314572800 -> "300.0 MiB". A tiny hand-rolled formatter avoids promoting
// the already-indirect go-humanize dependency to a direct one for one string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// assetName maps the current GOOS/GOARCH to the stable release asset name the
// release pipeline publishes (spec §6.3, §8). The pipeline uses "x64" for amd64.
//   - darwin: tallpine-darwin-{arm64,x64}.zip (signed bundle zip)
//   - windows: tallpine-win32-x64.exe (NSIS installer, amd64 only)
//   - linux: tallpine-linux-x64.AppImage (self-contained, amd64 only)
func assetName() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		arch, err := assetArch(runtime.GOARCH)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("tallpine-darwin-%s.zip", arch), nil
	case "windows":
		if _, err := requireAMD64(); err != nil {
			return "", err
		}
		return "tallpine-win32-x64.exe", nil
	case "linux":
		if _, err := requireAMD64(); err != nil {
			return "", err
		}
		return "tallpine-linux-x64.AppImage", nil
	default:
		return "", fmt.Errorf("tallpine start: no release asset for %s", runtime.GOOS)
	}
}

// requireAMD64 enforces the amd64/x64-only support window for Windows and Linux.
// arm64 Windows/Linux are not published yet; surface a clear unsupported error
// mirroring assetArch rather than fetching a 404.
func requireAMD64() (string, error) {
	if runtime.GOARCH != "amd64" {
		return "", fmt.Errorf("tallpine start: unsupported architecture %q on %s (only amd64 is published)", runtime.GOARCH, runtime.GOOS)
	}
	return "x64", nil
}

// assetArch maps a Go GOARCH to the release-asset arch token.
func assetArch(goarch string) (string, error) {
	switch goarch {
	case "arm64":
		return "arm64", nil
	case "amd64":
		return "x64", nil
	default:
		return "", fmt.Errorf("tallpine start: unsupported architecture %q", goarch)
	}
}

// downloadURL builds the constant releases/latest/download URL for asset.
func downloadURL(asset string) string { return downloadURLFor("", asset) }

// downloadURLFor builds the asset URL for tag, or for the latest release when
// tag is empty.
func downloadURLFor(tag, asset string) string {
	if tag == "" {
		return fmt.Sprintf("%s/%s/releases/latest/download/%s", releaseDownloadBase, releaseRepo, asset)
	}
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", releaseDownloadBase, releaseRepo, tag, asset)
}

// openApp launches the resolved bundle detached and reports whether it launched
// (spec §6.5). It passes --installed-via=npm-bootstrap so the app can record the
// install source in its marker. It never waits on the app. A launch failure
// returns (false, nil) so the caller falls back to manual-open instructions.
func (c *commandContext) openApp(ctx context.Context, appPath string) (bool, error) {
	switch runtime.GOOS {
	case "darwin":
		// `open` returns immediately; --args forwards the rest to the app.
		if out, err := c.deps.CommandOutput(ctx, "open", appPath, "--args", "--installed-via=npm-bootstrap"); err != nil {
			return false, fmt.Errorf("open %s: %w: %s", appPath, err, out)
		}
		return true, nil
	case "windows", "linux":
		// No `open`-style launcher on these platforms; exec the bundle directly,
		// detached, so `tallpine start` does not block on the app. StartProcess uses
		// cmd.Start() + a detached SysProcAttr (see process.go).
		//
		// ponytail: on some Linux hosts the AppImage may need --no-sandbox; not
		// added here without evidence the bundled Electron requires it. If sandbox
		// launch failures appear, append "--no-sandbox" as the follow-up.
		err := c.deps.StartProcess(processStartConfig{
			Path: appPath,
			Args: []string{"--installed-via=npm-bootstrap"},
		})
		if err != nil {
			// Treat a launch failure as "not opened" so the caller prints the
			// manual-open path rather than aborting the whole command.
			return false, nil //nolint:nilerr // launch failure is reported via the bool, not as an error
		}
		return true, nil
	default:
		return false, nil
	}
}

// printDeprecationNotice explains the new role of the npm `tallpine` binary. Keep it
// honest: Track B (live auto-update) is not done, so it does not promise it.
func (c *commandContext) printDeprecationNotice(w io.Writer) {
	_, _ = fmt.Fprint(w, brand.ProductName+" is now a desktop app, and the npm `tallpine` is just its launcher.\n"+
		"The app is distributed from the website and GitHub Releases; it owns the daemon and updates itself.\n"+
		"You can keep running `tallpine start` to fetch (if needed) and open it.\n")
}

// printManualOpen tells the user how to open the bundle when `tallpine start` could
// not launch it for them (non-darwin, or a failed launch handled upstream).
func (c *commandContext) printManualOpen(w io.Writer, appPath string) {
	_, _ = fmt.Fprintf(w, "Could not open the app automatically. Open it manually: %s\n", appPath)
}
