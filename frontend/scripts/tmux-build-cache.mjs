// Pure helpers for the bundled-tmux build cache (key derivation and manifest
// verification). Kept free of side effects so build-tmux.mjs stays thin and the
// rules are unit-testable. The cache holds unsigned build products only:
// codesigning happens later on the copy placed in the packaged app.
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, renameSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export function sha256File(file) {
	return createHash("sha256").update(readFileSync(file)).digest("hex");
}

// Key covers everything that changes the produced bytes: source pins, target
// platform, build-script content, compiler and deployment target.
export function deriveCacheKey({ platform, arch, sources, scriptHash, toolchain }) {
	const material = JSON.stringify({
		platform,
		arch,
		sources: sources.map((s) => [s.name, s.version, s.sha256]),
		scriptHash,
		toolchain,
	});
	return `${platform}-${arch}-${createHash("sha256").update(material).digest("hex").slice(0, 24)}`;
}

// Cache lives under the app state root (~/.tallpine), never an OS-default location.
export function buildCacheRoot(env = process.env, home = homedir()) {
	return join(env.TALLPINE_BUILD_CACHE_DIR || join(home, ".tallpine", "build-cache"), "bundled-tmux");
}

// files: relative path -> sha256 of the cached bytes.
export function createManifest(key, files) {
	return JSON.stringify({ key, files: Object.fromEntries(Object.entries(files).sort()) }, null, 2) + "\n";
}

// Returns true only when the manifest matches the key and every listed file
// exists with the recorded digest. Any doubt means rebuild.
export function verifyManifest(dir, key, requiredFiles) {
	let manifest;
	try {
		manifest = JSON.parse(readFileSync(join(dir, "manifest.json"), "utf8"));
	} catch {
		return false;
	}
	if (!manifest || manifest.key !== key || typeof manifest.files !== "object" || manifest.files === null) return false;
	for (const required of requiredFiles) {
		if (typeof manifest.files[required] !== "string") return false;
	}
	for (const [rel, digest] of Object.entries(manifest.files)) {
		const file = join(dir, rel);
		if (rel.includes("..") || !existsSync(file) || sha256File(file) !== digest) return false;
	}
	return true;
}

function pidAlive(pid) {
	try {
		process.kill(pid, 0);
		return true;
	} catch (error) {
		return error?.code === "EPERM";
	}
}

// Removes work-<pid>/staging-<pid> dirs left by builds that died (process.exit
// skips finally blocks, SIGKILL skips everything). Returns the removed names.
export function sweepStaleDirs(root, isAlive = pidAlive) {
	let names;
	try {
		names = readdirSync(root);
	} catch {
		return [];
	}
	const removed = [];
	for (const name of names) {
		const match = /^(?:work|staging)-(\d+)$/.exec(name);
		if (!match || isAlive(Number(match[1]))) continue;
		rmSync(join(root, name), { recursive: true, force: true });
		removed.push(name);
	}
	return removed;
}

// Moves a finished staging dir into place. Another worktree may have published
// the same key meanwhile: a valid existing entry wins and staging is dropped;
// only an entry that fails verification is replaced.
export function publishBundle(staging, bundleDir, key, requiredFiles) {
	if (verifyManifest(bundleDir, key, requiredFiles)) {
		rmSync(staging, { recursive: true, force: true });
		return "existing";
	}
	rmSync(bundleDir, { recursive: true, force: true });
	try {
		renameSync(staging, bundleDir);
	} catch (error) {
		if ((error?.code === "ENOTEMPTY" || error?.code === "EEXIST") && verifyManifest(bundleDir, key, requiredFiles)) {
			rmSync(staging, { recursive: true, force: true });
			return "existing";
		}
		throw error;
	}
	return "published";
}
