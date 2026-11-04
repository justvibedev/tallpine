import path from "node:path";
import type { Stats } from "node:fs";
import { hasInAppLinkScheme, parseArtifactLink } from "../shared/session-link-parse";
import type { LocalPathInspection, LocalPathResult } from "../shared/local-path";

const MAX_PATH_LENGTH = 4096;
// Open is an allowlist: only inert document, media and text files (zip and everything else is reveal-only).
// Everything else (scripts, installers, shortcuts, handlers that run on open) reveals instead.
const OPENABLE_FILE_EXTENSIONS = new Set([
	".html", ".htm", ".json", ".md", ".txt", ".csv", ".log", ".pdf",
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".mp4", ".mov",
]);
// Directories that are really application bundles: opening one launches it.
const BUNDLE_EXTENSIONS = new Set([
	".app", ".workflow", ".action", ".prefpane", ".saver", ".scptd", ".appex", ".xpc", ".component", ".framework", ".plugin", ".kext",
]);

export type LocalPathActionsDeps = {
	platform: NodeJS.Platform;
	homeDir: string;
	stat: (target: string) => Promise<Pick<Stats, "isFile" | "isDirectory" | "mode">>;
	realPath: (target: string) => Promise<string>;
	revealPath: (target: string) => void;
	openPath: (target: string) => Promise<string>;
	copyText: (text: string) => void;
	/** A session's artifact directory (absolute), for `tallpine://sessions/<project>/<session>/artifacts/<path>` links. */
	resolveArtifactDir: (sessionId: string) => Promise<string>;
};

/** Absolute, normalized path for renderer-supplied text, or undefined when it is not a plain local path. */
export function resolveLocalPath(input: unknown, homeDir: string, platform: NodeJS.Platform): string | undefined {
	if (typeof input !== "string" || input.length === 0 || input.length > MAX_PATH_LENGTH) return undefined;
	if (/[\0\r\n]/.test(input)) return undefined;
	// Two or more letters before ":" is a URL scheme; a single letter is a Windows drive.
	if (/^[A-Za-z][A-Za-z0-9+.-]+:/.test(input)) return undefined;
	const api = platform === "win32" ? path.win32 : path.posix;
	let expanded = input;
	if (input === "~" || input.startsWith("~/") || (platform === "win32" && input.startsWith("~\\"))) {
		expanded = api.join(homeDir, input.slice(1));
	}
	if (!api.isAbsolute(expanded)) return undefined;
	if (platform !== "win32" && expanded.startsWith("//")) return undefined;
	return api.normalize(expanded);
}

export function isOpenableLocalPath(target: string, kind: "file" | "directory", mode: number, platform: NodeJS.Platform): boolean {
	const api = platform === "win32" ? path.win32 : path.posix;
	const extension = api.extname(target).toLowerCase();
	if (kind === "directory") return !BUNDLE_EXTENSIONS.has(extension);
	if (!OPENABLE_FILE_EXTENSIONS.has(extension)) return false;
	return platform === "win32" || (mode & 0o111) === 0;
}

export function createLocalPathActions(deps: LocalPathActionsDeps) {
	// A tallpine:// artifact link becomes the file inside that session's artifact dir; anything that would
	// leave the dir (traversal, a symlink pointing out) is invalid. Other tallpine:// forms are not paths.
	async function resolveArtifactLink(input: string): Promise<string | LocalPathInspection> {
		const link = parseArtifactLink(input);
		if (!link) return { ok: false, reason: "invalid" };
		const api = deps.platform === "win32" ? path.win32 : path.posix;
		const dir = await deps.resolveArtifactDir(link.sessionId).catch(() => undefined);
		if (!dir || !api.isAbsolute(dir)) return { ok: false, reason: "not_found" };
		const target = api.join(dir, ...link.path.split("/"));
		const inside = (base: string, candidate: string) => {
			const rel = api.relative(base, candidate);
			return rel !== "" && !rel.startsWith("..") && !api.isAbsolute(rel);
		};
		if (!inside(dir, target)) return { ok: false, reason: "invalid" };
		const realDir = await deps.realPath(dir).catch(() => undefined);
		const realTarget = await deps.realPath(target).catch(() => undefined);
		if (!realDir || !realTarget) return { ok: false, reason: "not_found" };
		return inside(realDir, realTarget) ? target : { ok: false, reason: "invalid" };
	}

	async function inspect(input: unknown): Promise<LocalPathInspection> {
		let resolved: string | undefined;
		if (typeof input === "string" && hasInAppLinkScheme(input)) {
			const artifact = await resolveArtifactLink(input);
			if (typeof artifact !== "string") return artifact;
			resolved = artifact;
		} else {
			resolved = resolveLocalPath(input, deps.homeDir, deps.platform);
		}
		if (!resolved) return { ok: false, reason: "invalid" };
		const info = await deps.stat(resolved).catch(() => undefined);
		if (!info) return { ok: false, reason: "not_found" };
		const kind = info.isDirectory() ? "directory" : info.isFile() ? "file" : undefined;
		// Sockets, devices and FIFOs can be revealed and copied but never opened.
		const real = kind ? await deps.realPath(resolved).catch(() => undefined) : undefined;
		const canOpen = Boolean(kind && real && isOpenableLocalPath(resolved, kind, info.mode, deps.platform) && isOpenableLocalPath(real, kind, info.mode, deps.platform));
		return { ok: true, path: resolved, kind: kind ?? "file", canOpen };
	}

	async function perform(input: unknown, action: unknown): Promise<LocalPathResult> {
		if (action !== "reveal" && action !== "open" && action !== "copy") return { ok: false, reason: "invalid" };
		const target = await inspect(typeof input === "object" && input !== null ? (input as { path?: unknown }).path : input);
		if (!target.ok) return target;
		try {
			if (action === "copy") {
				deps.copyText(target.path);
				return { ok: true, performed: "copy" };
			}
			if (action === "open" && target.canOpen) {
				const error = await deps.openPath(target.path);
				if (error) return { ok: false, reason: "failed" };
				return { ok: true, performed: "open" };
			}
			deps.revealPath(target.path);
			return { ok: true, performed: "reveal" };
		} catch {
			return { ok: false, reason: "failed" };
		}
	}

	return { inspect, perform };
}
