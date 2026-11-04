/* Preview URL surface of the remote proxy (B303). */
import { randomBytes } from "node:crypto";
import { isIP } from "node:net";
import { agentBrowserRunning, agentViewerUrl } from "../agent-viewer";
import { ActiveProxy, PreviewTarget } from "./common";
import { previewHostForSession, withoutPreviewCapability } from "./preview-paths";

export type PreviewApiDeps = {
	previewHosts: Map<string, PreviewTarget>;
	artifactHosts: Map<string, { host: string; sourceHref: string }>;
	previewSessions: Map<string, { host: string; sourceUrl: string; sourceHref: string; url: string }>;
	originMac: (subject: string) => Buffer;
	token: string;
	port: number;
	securePort: number;
};

// The preview/agent-browser URL surface of an active remote proxy.
export function createPreviewApi({ previewHosts, previewSessions, artifactHosts, originMac, token, port, securePort }: PreviewApiDeps): Pick<ActiveProxy, "previewUrl" | "resolvePreviewUrl" | "agentBrowserUrl" | "isAgentBrowserUrl" | "externalUrl"> {
	return {
		previewUrl: (sessionId, sourceUrl) => {
			if (!sessionId || sessionId.length > 256) throw new Error("invalid preview session");
			const raw = sourceUrl.trim();
			if (!raw) {
				const previous = previewSessions.get(sessionId);
				if (previous) { previewHosts.delete(previous.host); previewSessions.delete(sessionId); }
				return "";
			}
			const parsed = new URL(/^(?:localhost|127\.\d+\.\d+\.\d+|0\.0\.0\.0|\[::1\])(?::\d+)?(?:[/?#]|$)/i.test(raw) ? `http://${raw}` : raw);
			if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("unsupported preview URL");
			if (withoutPreviewCapability(parsed.hostname) === previewHostForSession(sessionId, "ao-preview-artifact")) {
				// Artifact reads and images must not replace the active Browser app
				// preview. Keep one independent, read-only origin per session.
				let artifact = artifactHosts.get(sessionId);
				if (!artifact) {
					const host = `ao-preview-${randomBytes(16).toString("hex")}.localhost`;
					artifact = { host: `${host}:${port}`, sourceHref: parsed.href };
					artifactHosts.set(sessionId, artifact);
					previewHosts.set(host, { sessionId, kind: "artifact", entry: "" });
				}
				return `http://${artifact.host}${parsed.pathname}${parsed.search}${parsed.hash}`;
			}
			const previous = previewSessions.get(sessionId);
			if (previous?.sourceUrl === sourceUrl) return previous.url;
			if (previous) {
				previewHosts.delete(previous.host);
				previewSessions.delete(sessionId);
			}
			let kind: PreviewTarget["kind"];
			const previewHost = parsed.hostname.replace(/^\[|\]$/g, "").replace(/\.+$/, "");
			const loopback = previewHost === "localhost" || previewHost === "0.0.0.0" || previewHost === "::1" ||
				(isIP(previewHost) === 4 && previewHost.startsWith("127.")) ||
				(isIP(previewHost) === 6 && /^::ffff:7f[0-9a-f]{2}:/i.test(previewHost));
			if (withoutPreviewCapability(previewHost) === previewHostForSession(sessionId)) kind = "static";
			else if (parsed.protocol === "http:" && loopback) kind = "app";
			else if (loopback) throw new Error("unsupported local preview protocol");
			else if (previewHost.endsWith(".localhost")) throw new Error("preview does not belong to this session");
			else return sourceUrl;
			// Per kind too: a static preview (HTML an agent wrote) must not share the app's storage.
			const host = `ao-preview-${originMac(`${kind}\n${sessionId}`).toString("hex", 0, 16)}.localhost`;
			previewHosts.set(host, { sessionId, kind, entry: parsed.pathname });
			const url = `https://${host}:${securePort}${parsed.pathname}${parsed.search}${parsed.hash}`;
			previewSessions.set(sessionId, { host, sourceUrl, sourceHref: parsed.href, url });
			return url;
		},
		resolvePreviewUrl: (sessionId, viewedUrl) => {
			let viewed: URL;
			try { viewed = new URL(viewedUrl); } catch { return viewedUrl; }
			if (!/^ao-preview-[0-9a-f]{32}\.localhost$/.test(viewed.hostname)) return viewedUrl;
			const artifact = artifactHosts.get(sessionId);
			if (artifact && viewed.host === artifact.host) {
				const source = new URL(artifact.sourceHref);
				source.pathname = viewed.pathname;
				source.search = viewed.search;
				source.hash = viewed.hash;
				return source.href;
			}
			const active = previewSessions.get(sessionId);
			if (!active || viewed.origin !== new URL(active.url).origin) return "";
			const source = new URL(active.sourceHref);
			source.pathname = viewed.pathname;
			source.search = viewed.search;
			source.hash = viewed.hash;
			return source.href;
		},
		agentBrowserUrl: async (sessionId) => {
			if (!sessionId || sessionId.length > 256) throw new Error("invalid preview session");
			// Only a host whose agent browser answers has one. A daemon without one
			// (or too old for the route) answers 404, and a host this proxy cannot
			// reach gets the proxy's own 502: neither may show an agent tab.
			const probe = await fetch(`http://127.0.0.1:${port}/${token}/api/v1/sessions/${encodeURIComponent(sessionId)}/agent-browser/stats`, { signal: AbortSignal.timeout(8_000) })
				.catch(() => null);
			if (!(await agentBrowserRunning(probe))) return "";
			// Its own origin per session: never the app's, so the two keep apart.
			const host = `ao-preview-${originMac(`agent\n${sessionId}`).toString("hex", 0, 16)}.localhost`;
			previewHosts.set(host, { sessionId, kind: "agent", entry: "" });
			return agentViewerUrl(host, securePort);
		},
		isAgentBrowserUrl: (url) => {
			try {
				const parsed = new URL(url);
				return parsed.protocol === "https:" && parsed.port === String(securePort) && parsed.pathname === "/v/cdp" && previewHosts.get(parsed.hostname)?.kind === "agent";
			} catch {
				return false;
			}
		},
		externalUrl: (url) => {
			let parsed: URL;
			try { parsed = new URL(url); } catch { return url; }
			const target = previewHosts.get(parsed.hostname);
			if (!target || target.kind === "agent") return url;
			// The plain server: the system browser does not trust the preview certificate.
			parsed.protocol = "http:";
			parsed.port = String(port);
			return parsed.href;
		},
	};
}
