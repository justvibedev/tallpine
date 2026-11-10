// Build metadata shared by every step that stamps a build: the Go daemon's
// -ldflags (build-daemon.mjs), the macOS Info.plist keys (forge.config.ts) and
// the About dialog (vite.main.config.ts). Each value can be pinned with a
// TALLPINE_BUILD_* variable; scripts/release-local sets all four once so the daemon,
// the bundle and the About dialog agree on the same commit and build time.
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const frontendRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");

export const BUILDINFO_PACKAGE = "tallpine.eu/tallpine/backend/internal/buildinfo";

function git(args, cwd) {
	const result = spawnSync("git", args, { cwd, encoding: "utf8", windowsHide: true });
	return result.status === 0 ? result.stdout.trim() : null;
}

export function resolveBuildInfo({ env = process.env, cwd = frontendRoot, now = new Date() } = {}) {
	const version =
		env.TALLPINE_BUILD_VERSION || JSON.parse(readFileSync(join(frontendRoot, "package.json"), "utf8")).version;
	const commit = env.TALLPINE_BUILD_COMMIT ?? git(["rev-parse", "HEAD"], cwd) ?? "";
	let dirty = env.TALLPINE_BUILD_DIRTY;
	if (dirty !== "true" && dirty !== "false") {
		const status = commit ? git(["status", "--porcelain"], cwd) : null;
		dirty = status ? "true" : "false";
	}
	const date = env.TALLPINE_BUILD_DATE || now.toISOString().replace(/\.\d{3}Z$/, "Z");
	// "local" marks a non-public build (release-local without --notarize): it never self-updates.
	const kind = env.TALLPINE_BUILD_KIND === "local" ? "local" : "release";
	return { version, commit, date, dirty, kind };
}

const GITHUB_REPO_PATTERN = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
const CLI_PACKAGE = "tallpine.eu/tallpine/backend/internal/cli";
const SIGNER_PACKAGE = "tallpine.eu/tallpine/backend/internal/releasesigner";

// Team ID inside the release signing identity "Name (TEAMID)".
export function teamIdFromIdentity(identity) {
	const m = /\(([A-Z0-9]{10})\)\s*$/.exec(identity ?? "");
	return m ? m[1] : "";
}

// Release-source flags read by `tallpine start` to verify downloaded releases:
// the release repository (TALLPINE_RELEASE_GITHUB_REPO) and the expected Apple Team ID,
// parsed from the signing identity release-local already exports. Nothing is
// hardcoded; a flag is omitted when its source is unset, and the resulting
// build then refuses to download or to install unverified (see
// backend/internal/cli/release_verify.go). Keep in sync with
// scripts/daemon-build.sh.
export function releaseSourceLdflags(env = process.env) {
	const flags = [];
	const repo = (env.TALLPINE_RELEASE_GITHUB_REPO ?? "").trim();
	if (repo && !GITHUB_REPO_PATTERN.test(repo)) {
		throw new Error(`TALLPINE_RELEASE_GITHUB_REPO must be a GitHub "owner/repo" slug, got "${repo}"`);
	}
	if (repo) flags.push(`-X ${CLI_PACKAGE}.releaseRepo=${repo}`);
	const teamId = teamIdFromIdentity((env.TALLPINE_SIGN_IDENTITY || env.APPLE_SIGNING_IDENTITY || "").trim());
	if (teamId) flags.push(`-X ${SIGNER_PACKAGE}.ExpectedTeamID=${teamId}`);
	// The daemon's peer check is active only on builds carrying this (B631).
	if (teamId) flags.push(`-X ${BUILDINFO_PACKAGE}.TeamID=${teamId}`);
	return flags;
}

export function goLdflags(info, env = process.env) {
	return [
		["Version", info.version],
		["Commit", info.commit],
		["Date", info.date],
		["Dirty", info.dirty],
	]
		.map(([name, value]) => `-X ${BUILDINFO_PACKAGE}.${name}=${value}`)
		.concat(releaseSourceLdflags(env))
		.join(" ");
}
