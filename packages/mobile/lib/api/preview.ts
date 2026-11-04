import { httpBase, normalizeServerHost, type ServerConfig } from "../config";
import { API, req } from "./http";

// ---- Preview (in-app browser) ----------------------------------------------

// Load the session's selected app preview, never a discovered repository file.
// Build static URLs from our configured base so TLS tunnels stay on HTTPS.
/**
 * The daemon's preview-files route for a workspace path, each segment escaped.
 * Shared by the preview button and chat attachment images so the route shape
 * lives in one place.
 */
export function previewFilePath(sessionId: string, path: string): string {
	return `${API}/sessions/${encodeURIComponent(sessionId)}/preview/files/${path.split("/").map(encodeURIComponent).join("/")}`;
}

/** The daemon's route for a file in a session's artifact folder, each segment escaped. */
export function previewArtifactPath(sessionId: string, entry: string): string {
	return `${API}/sessions/${encodeURIComponent(sessionId)}/preview/artifacts/${entry.split("/").map(encodeURIComponent).join("/")}`;
}

export async function getPreview(cfg: ServerConfig, id: string): Promise<{ entry: string; url: string; authenticated: boolean } | null> {
	const res = await req(cfg, `${API}/sessions/${encodeURIComponent(id)}/preview`);
	const data = await res.json();
	const entry = typeof data?.entry === "string" ? data.entry.trim() : "";
	if (entry) {
		const path = data?.source === "artifact" ? previewArtifactPath(id, entry) : previewFilePath(id, entry);
		const url = `${httpBase(cfg)}${path}`;
		return { entry, url, authenticated: true };
	}
	const external = mobileReachablePreviewURL(typeof data?.previewUrl === "string" ? data.previewUrl : undefined, cfg.host);
	return external ? { entry: external.hostname, url: external.href, authenticated: false } : null;
}

/** Rewrite host-loopback previews for the phone without ever forwarding Tallpine auth. */
export function mobileReachablePreviewURL(raw: string | undefined, tallpineHost: string): URL | undefined {
	if (!raw) return undefined;
	try {
		const url = new URL(raw);
		if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
		if (["localhost", "127.0.0.1", "::1", "[::1]"].includes(url.hostname)) {
			const host = normalizeServerHost(tallpineHost);
			if (!host) return undefined;
			url.hostname = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host;
		}
		return url;
	} catch {
		return undefined;
	}
}

