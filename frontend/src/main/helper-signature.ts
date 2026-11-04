import { execFile } from "node:child_process";
import { statSync } from "node:fs";

// Verifies that a bundled executable is still signed by our Developer ID team
// before the app spawns it (audit R4). A same-user process can write into a
// user-owned ~/Applications install and swap a helper; the Developer ID anchor
// plus team requirement makes that swap fail closed.

export type HelperSignatureResult =
	| { status: "verified"; path: string; cached: boolean; ms: number }
	| { status: "skipped"; path: string; reason: string }
	| { status: "failed"; path: string; reason: string };

export type CodesignRunner = (args: string[]) => Promise<{ code: number; stderr: string }>;

export type HelperSignatureOptions = {
	isPackaged: boolean;
	platform: NodeJS.Platform;
	/** Apple Team ID baked in at build time; empty for unsigned/ad-hoc builds. */
	teamId: string;
	run?: CodesignRunner;
	/** Override for tests; defaults to a per-process in-memory cache. */
	cache?: Map<string, string>;
	now?: () => number;
};

const TEAM_ID_PATTERN = /^[A-Z0-9]{10}$/;
const defaultCache = new Map<string, string>();

export function teamRequirement(teamId: string): string {
	return `anchor apple generic and certificate leaf[subject.OU] = "${teamId}"`;
}

const defaultRun: CodesignRunner = (args) =>
	new Promise((resolve) => {
		execFile("/usr/bin/codesign", args, { timeout: 30_000 }, (err, _stdout, stderr) => {
			const code = err ? (typeof (err as { code?: unknown }).code === "number" ? (err as { code: number }).code : 1) : 0;
			resolve({ code, stderr: String(stderr ?? "") });
		});
	});

// Identity of the file contents we verified: replacing the binary changes inode
// or mtime (or size), which invalidates the cache entry.
function fileStamp(path: string): string | null {
	try {
		const s = statSync(path);
		return `${s.ino}:${s.mtimeMs}:${s.size}`;
	} catch {
		return null;
	}
}

export async function verifyHelperSignature(path: string, opts: HelperSignatureOptions): Promise<HelperSignatureResult> {
	if (!opts.isPackaged) return { status: "skipped", path, reason: "development build (not packaged)" };
	if (opts.platform !== "darwin") return { status: "skipped", path, reason: `code signing is not checked on ${opts.platform}` };
	if (!TEAM_ID_PATTERN.test(opts.teamId)) {
		return { status: "skipped", path, reason: "unsigned build (no Developer ID team id baked in)" };
	}
	const now = opts.now ?? Date.now;
	const cache = opts.cache ?? defaultCache;
	const stamp = fileStamp(path);
	if (stamp === null) return { status: "failed", path, reason: "file is missing or unreadable" };
	const key = `${opts.teamId}|${path}`;
	if (cache.get(key) === stamp) return { status: "verified", path, cached: true, ms: 0 };

	const started = now();
	const { code, stderr } = await (opts.run ?? defaultRun)(["--verify", "--strict", `-R=${teamRequirement(opts.teamId)}`, path]);
	if (code !== 0) {
		return { status: "failed", path, reason: stderr.trim().split("\n")[0] || `codesign exited with ${code}` };
	}
	// Re-stat after the check: a swap during verification must not be cached.
	if (fileStamp(path) === stamp) cache.set(key, stamp);
	return { status: "verified", path, cached: false, ms: now() - started };
}

function signatureFailureMessage(p: string, reason: string): string {
	return `${p} failed its code-signature check (${reason}). It may have been modified or replaced. Reinstall the app (drag a fresh copy into /Applications or ~/Applications) and relaunch.`;
}

/** Verify several helpers; returns the first failure message, or null. Logs skips and timings. */
export async function verifyBundledHelpers(
	paths: string[],
	opts: HelperSignatureOptions,
	log: (msg: string) => void = console.info,
): Promise<string | null> {
	for (const p of paths) {
		const r = await verifyHelperSignature(p, opts);
		if (r.status === "skipped") {
			log(`Helper signature check skipped for ${p}: ${r.reason}`);
		} else if (r.status === "verified") {
			log(`Helper signature verified for ${p} (${r.cached ? "cached" : `${r.ms} ms`})`);
		} else {
			return signatureFailureMessage(p, r.reason);
		}
	}
	return null;
}

export type StageOps = {
	exists: (p: string) => boolean;
	remove: (p: string) => Promise<void>;
	copy: (source: string, destination: string) => Promise<void>;
};

/**
 * Keeps a staged copy of a bundled binary (kept outside the bundle, e.g. tmux
 * under the data dir) trustworthy. An existing copy that fails verification is
 * discarded and re-staged from the bundled source; the source must verify first
 * and the fresh copy must verify too. Returns an error message, or null when
 * `destination` may be used.
 */
export async function ensureVerifiedStagedCopy(
	source: string,
	destination: string,
	opts: HelperSignatureOptions,
	ops: StageOps,
): Promise<string | null> {
	if (ops.exists(destination)) {
		const existing = await verifyHelperSignature(destination, opts);
		if (existing.status !== "failed") return null;
	}
	const src = await verifyHelperSignature(source, opts);
	if (src.status === "failed") return signatureFailureMessage(source, src.reason);
	await ops.remove(destination);
	await ops.copy(source, destination);
	const copied = await verifyHelperSignature(destination, opts);
	if (copied.status === "failed") return signatureFailureMessage(destination, copied.reason);
	return null;
}
