import path from "node:path";
import type { Stats } from "node:fs";
import { hasInAppLinkScheme, parseArtifactLink } from "../shared/session-link-parse";
import type { LocalImageSource, LocalPathInspection, LocalPathResult } from "../shared/local-path";
import { isLocalImagePath, isLocalMediaPath } from "../shared/local-image-paths";

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
	/** A session's worktree (absolute), for chat thumbnails of images inside it. */
	resolveWorkspace: (sessionId: string) => Promise<string>;
};

const SESSION_ID = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

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

/** The path of `candidate` strictly inside `base`, or undefined when it is `base` itself or outside it. */
function pathInside(api: path.PlatformPath, base: string, candidate: string): string | undefined {
	const rel = api.relative(base, candidate);
	return rel !== "" && !rel.startsWith("..") && !api.isAbsolute(rel) ? rel : undefined;
}

export function createLocalPathActions(deps: LocalPathActionsDeps) {
	const api = deps.platform === "win32" ? path.win32 : path.posix;
	const rootOf = (resolve: (sessionId: string) => Promise<string>, sessionId: string) =>
		resolve(sessionId).then((root) => (api.isAbsolute(root) ? api.normalize(root) : undefined), () => undefined);
	const routePath = (rel: string) => rel.split(api.sep).join("/");
	// The `artifacts` directory above a session's artifact dir; the parent when none is named so.
	const artifactsRoot = (dir: string) => {
		for (let up = dir; api.dirname(up) !== up; up = api.dirname(up)) if (api.basename(up) === "artifacts") return up;
		return api.dirname(dir);
	};

	// Which confined route serves an image file for `sessionId`'s chat: only a file strictly inside a real root
	// maps — that session's worktree or artifact directory, or another session's artifact directory under the same
	// artifacts root, checked against that session's own directory. A same-named segment elsewhere maps nothing.
	async function imageSource(target: string, sessionId: string): Promise<LocalImageSource | undefined> {
		const workspace = await rootOf(deps.resolveWorkspace, sessionId);
		const inWorkspace = workspace && pathInside(api, workspace, target);
		if (inWorkspace) return { kind: "workspace", path: routePath(inWorkspace) };
		const artifactDir = await rootOf(deps.resolveArtifactDir, sessionId);
		if (!artifactDir) return undefined;
		// Artifact dirs are `<artifacts>/<project>/<session>` or, for older sessions, `<artifacts>/<session>`, so the
		// owner is the second or the first segment below the artifacts root (the deeper one first, since the first may be a project folder). Each candidate is checked against that
		// session's own directory, so a same-named segment elsewhere maps nothing.
		const segments = pathInside(api, artifactsRoot(artifactDir), target)?.split(api.sep) ?? [];
		for (const owner of segments.slice(0, 2).reverse()) {
			if (!SESSION_ID.test(owner)) continue;
			const dir = owner === sessionId ? artifactDir : await rootOf(deps.resolveArtifactDir, owner);
			const inArtifacts = dir && pathInside(api, dir, target);
			if (inArtifacts) return { kind: "artifact", sessionId: owner, path: routePath(inArtifacts) };
		}
		return undefined;
	}

	// A tallpine:// artifact link becomes the file inside that session's artifact dir; anything that would
	// leave the dir (traversal, a symlink pointing out) is invalid. Other tallpine:// forms are not paths.
	async function resolveArtifactLink(input: string): Promise<string | LocalPathInspection> {
		const link = parseArtifactLink(input);
		if (!link) return { ok: false, reason: "invalid" };
		const dir = await deps.resolveArtifactDir(link.sessionId).catch(() => undefined);
		if (!dir || !api.isAbsolute(dir)) return { ok: false, reason: "not_found" };
		const target = api.join(dir, ...link.path.split("/"));
		const inside = (base: string, candidate: string) => pathInside(api, base, candidate) !== undefined;
		if (!inside(dir, target)) return { ok: false, reason: "invalid" };
		const realDir = await deps.realPath(dir).catch(() => undefined);
		const realTarget = await deps.realPath(target).catch(() => undefined);
		if (!realDir || !realTarget) return { ok: false, reason: "not_found" };
		return inside(realDir, realTarget) ? target : { ok: false, reason: "invalid" };
	}

	/** `sessionId`, when given, is the chat the path is shown in: an image, video or audio file there also gets its {@link imageSource}. */
	async function inspect(input: unknown, sessionId?: unknown): Promise<LocalPathInspection> {
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
		const image = kind === "file" && typeof sessionId === "string" && SESSION_ID.test(sessionId) && (isLocalImagePath(resolved) || isLocalMediaPath(resolved))
			? await imageSource(resolved, sessionId)
			: undefined;
		return { ok: true, path: resolved, kind: kind ?? "file", canOpen, ...(image ? { image } : {}) };
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
