/* Request rules of the remote proxy (B303). */
import { type IncomingHttpHeaders, type OutgoingHttpHeaders } from "node:http";
import type { RemoteEntry } from "../remotes-store";
import { PreviewTarget, STRIP_REQUEST_HEADERS, equalsToken } from "./common";
import { previewAssetPath } from "./preview-paths";

export type RequestRulesDeps = {
	rendererOrigin: string;
	token: string;
	prefix: string;
	upstream: URL;
	previewHosts: Map<string, PreviewTarget>;
	entry: RemoteEntry;
};

// Pure request rules: token stripping, preview routing, header forwarding and origin checks.
export function createRequestRules({ entry, prefix, previewHosts, rendererOrigin, token, upstream }: RequestRulesDeps) {
	const stripToken = (rawUrl: string | undefined): string | null => {
		if (!rawUrl || !rawUrl.startsWith("/")) return null;
		const slash = rawUrl.indexOf("/", 1);
		const first = slash === -1 ? rawUrl.slice(1) : rawUrl.slice(1, slash);
		if (!equalsToken(first, token)) return null;
		return prefix + (slash === -1 ? "/" : rawUrl.slice(slash));
	};

	const previewPath = (rawHost: string | undefined, rawUrl: string | undefined, method: string | undefined, scheme: "http" | "https"): { path: string; kind: PreviewTarget["kind"] } | null => {
		if (!rawHost || !rawUrl?.startsWith("/")) return null;
		// By hostname, whatever the port: the plain server answers a preview too,
		// at the address a browser outside Tallpine opens (externalUrl).
		const target = previewHosts.get(rawHost.toLowerCase().replace(/:\d+$/, ""));
		// Except the agent viewer, which drives the host's browser: it is the
		// Browser panel's alone, over HTTPS.
		if (!target || (scheme === "http" && target.kind === "agent")) return null;
		if (target.kind !== "app" && target.kind !== "agent" && method !== "GET" && method !== "HEAD") return null;
		// The target is a path: "//x" names no host, and must not throw as one.
		const parsed = new URL(`http://localhost${rawUrl}`);
		const asset = target.kind === "static" ? previewAssetPath(target.entry, parsed.pathname) : parsed.pathname.replace(/^\/+/, "");
		if (target.kind === "agent") {
			return { path: `${prefix}/api/v1/sessions/${encodeURIComponent(target.sessionId)}/agent-browser/${asset}${parsed.search}`, kind: target.kind };
		}
		if (target.kind === "artifact") {
			const query = new URLSearchParams(parsed.search);
			query.set("source", "artifact");
			return { path: `${prefix}/api/v1/sessions/${encodeURIComponent(target.sessionId)}/preview/files/${asset}?${query}`, kind: target.kind };
		}
		const route = target.kind === "static" ? "files" : "app";
		return { path: `${prefix}/api/v1/sessions/${encodeURIComponent(target.sessionId)}/preview/${route}/${asset}${parsed.search}`, kind: target.kind };
	};

	const forwardHeaders = (incoming: IncomingHttpHeaders, previewKind?: PreviewTarget["kind"]): OutgoingHttpHeaders => {
		const out: OutgoingHttpHeaders = {};
		for (const [name, value] of Object.entries(incoming)) {
			// HTTP/2 pseudo-headers (":authority", ":path") are not forwardable headers.
			if (!name.startsWith(":") && !STRIP_REQUEST_HEADERS.includes(name.toLowerCase())) out[name] = value;
		}
		// The outer Authorization authenticates to Tallpine. Keep app credentials and
		// same-origin intent in separate headers for the managed-app proxy only.
		if (previewKind === "app") {
			if (incoming.authorization) out["x-ao-preview-app-authorization"] = incoming.authorization;
			if (incoming.origin) out["x-ao-preview-app-origin"] = incoming.origin;
		}
		if (previewKind === undefined) {
			delete out["sec-fetch-site"];
			delete out["sec-fetch-mode"];
			delete out["sec-fetch-dest"];
		}
		out.host = upstream.host;
		out.authorization = `Bearer ${entry.password}`;
		if (entry.hostId) out["x-ao-expected-host-id"] = entry.hostId;
		// The host rewrites session links to ao:// for the phone app; desktop parses only tallpine://.
		out["x-vo-client"] = "desktop";
		return out;
	};

	// A token authenticates the local native client, not arbitrary pages that
	// happen to learn its URL. Check before stripping Origin/injecting auth.
	// Only sandboxed render-document GET/HEAD navigations may omit Origin.
	const validControlOrigin = (req: { headers: IncomingHttpHeaders; method?: string; url?: string }): boolean =>
		req.headers.origin === undefined
			? req.headers["sec-fetch-site"] === undefined || (
				(req.method === "GET" || req.method === "HEAD") &&
				/^\/api\/v1\/sessions\/[^/]+\/renders\/(?!check(?:$|[?]))[^/?]+(?:\?.*)?$/.test((stripToken(req.url) ?? "").slice(prefix.length))
			)
			: req.headers.origin === rendererOrigin;

	const validPreviewOrigin = (scheme: "http" | "https", host: string | undefined, origin: string | undefined, kind?: PreviewTarget["kind"]): boolean =>
		(kind !== "app" && kind !== "agent") || !origin || origin === `${scheme}://${host}`;

	return { stripToken, previewPath, forwardHeaders, validControlOrigin, validPreviewOrigin };
}
