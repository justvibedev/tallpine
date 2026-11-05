import { authHeaders, httpBase, type ServerConfig } from "../config";
import { artifactFilePath, artifactFilesPath } from "../artifacts";
import { resolveArtifactReference } from "../artifactPageInliner";
import { parseVersion, parseVersionSummaries, type ArtifactVersion, type ArtifactVersionSummary } from "../artifactVersions";
import { API, req } from "./http";
import { mapSession } from "./normalize";
import type { DashboardSession } from "./sessions";

// ---- Artifacts --------------------------------------------------------------

/** Full URL of one artifact's bytes, for a WebView or Image with auth headers. */
/** The URL a WebView loads one artifact from by itself, with the preview cookie (see artifactFilePath). */
export function artifactFileUrl(cfg: ServerConfig, sessionId: string, path: string): string | undefined {
	const route = artifactFilePath(sessionId, path);
	return route ? `${httpBase(cfg)}${route}` : undefined;
}

/** The URL the app reads one artifact's bytes from, with the Bearer header (see artifactFilesPath). */
export function artifactBytesUrl(cfg: ServerConfig, sessionId: string, path: string): string | undefined {
	const route = artifactFilesPath(sessionId, path);
	return route ? `${httpBase(cfg)}${route}` : undefined;
}

/**
 * An authenticated image source for one artifact file. `version` (its modified
 * time) busts the image cache when the agent rewrites the file. Pass it through
 * `imageSourceForPlatform` (lib/imageSource.ts): React Native's Android Image
 * forwards headers only for an array source.
 */
export function artifactImageSource(cfg: ServerConfig, sessionId: string, path: string, version?: string): { uri: string; headers: Record<string, string> } | undefined {
	const url = artifactBytesUrl(cfg, sessionId, path);
	if (!url) return undefined;
	return { uri: version ? `${url}?v=${encodeURIComponent(version)}` : url, headers: authHeaders(cfg) };
}

/**
 * What an image a markdown artifact names should load: a file of the artifact
 * folder (beside the document, or by its absolute path into the folder, as an
 * HTML page may name it), with the connection's credential, or a web image as
 * it is, with none. Undefined when it names neither. `versionOf` gives a
 * listed file's modified time, so a regenerated image is not served from the
 * image cache.
 */
export function artifactMarkdownImageSource(cfg: ServerConfig, sessionId: string, documentPath: string, url: string, versionOf?: (path: string) => string | undefined): { uri: string; headers?: Record<string, string> } | undefined {
	const trimmed = url.trim();
	if (/^(https?:|data:)/i.test(trimmed)) return { uri: trimmed };
	const path = resolveArtifactReference(documentPath, trimmed, sessionId);
	return path ? artifactImageSource(cfg, sessionId, path, versionOf?.(path)) : undefined;
}

/**
 * One artifact's contents as text. The caller decides whether it is really
 * text. A page is read raw from the preview route: the artifact-files route
 * would write AO's bootstrap into it.
 */
export async function getArtifactText(cfg: ServerConfig, sessionId: string, path: string, init?: Pick<RequestInit, "signal">): Promise<string> {
	const route = /\.html?$/i.test(path) ? artifactFilePath(sessionId, path, { raw: true }) : artifactFilesPath(sessionId, path);
	if (!route) throw new Error("This artifact's path can't be opened.");
	const res = await req(cfg, route, init);
	return res.text();
}

/** An HTML artifact as a page, with AO's theme bootstrap written in (see artifactFilesPath). */
export async function getArtifactPage(cfg: ServerConfig, sessionId: string, path: string, init?: Pick<RequestInit, "signal">): Promise<string> {
	const route = artifactFilesPath(sessionId, path);
	if (!route) throw new Error("This artifact's path can't be opened.");
	const res = await req(cfg, route, init);
	return res.text();
}

/** Deletes one listed artifact file; returns the session with its refreshed list. */
export async function deleteArtifact(cfg: ServerConfig, sessionId: string, path: string): Promise<DashboardSession> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(sessionId)}/artifact-files?path=${encodeURIComponent(path)}`, { method: "DELETE" });
	const data = await res.json();
	return mapSession(data?.session ?? data);
}

function artifactVersionsPath(sessionId: string): string {
	return `${API}/sessions/${encodeURIComponent(sessionId)}/artifact-versions`;
}

/** One file's kept versions, newest first; the first is current. Only reads. */
export async function listArtifactVersions(cfg: ServerConfig, sessionId: string, path: string, init?: Pick<RequestInit, "signal">): Promise<ArtifactVersionSummary[]> {
	const res = await req(cfg, `${artifactVersionsPath(sessionId)}?path=${encodeURIComponent(path)}`, init);
	const data = await res.json();
	return parseVersionSummaries(data?.versions);
}

/**
 * Records an edit of the file the daemon has not picked up yet, then lists the
 * file's versions like listArtifactVersions. The daemon waits for the file to
 * settle first, so this can take a couple of seconds.
 */
export async function syncArtifactVersions(cfg: ServerConfig, sessionId: string, path: string, init?: Pick<RequestInit, "signal">): Promise<ArtifactVersionSummary[]> {
	const res = await req(cfg, `${artifactVersionsPath(sessionId)}/sync`, { ...init, method: "POST", body: JSON.stringify({ path }) });
	const data = await res.json();
	return parseVersionSummaries(data?.versions);
}

/** One version's text. A saved version never changes. */
export async function getArtifactVersion(cfg: ServerConfig, sessionId: string, path: string, version: number, init?: Pick<RequestInit, "signal">): Promise<ArtifactVersion> {
	const res = await req(cfg, `${artifactVersionsPath(sessionId)}/${version}?path=${encodeURIComponent(path)}`, init);
	const parsed = parseVersion(await res.json());
	if (!parsed) throw new Error("This version couldn't be read.");
	return parsed;
}

/**
 * Rewrites the file with an earlier version. The content it replaces is kept
 * as a version first, so the restore itself can be undone.
 */
export async function restoreArtifactVersion(cfg: ServerConfig, sessionId: string, path: string, version: number): Promise<void> {
	await req(cfg, `${artifactVersionsPath(sessionId)}/restore`, { method: "POST", body: JSON.stringify({ path, version }) });
}
