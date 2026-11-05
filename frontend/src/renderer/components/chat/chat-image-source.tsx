/**
 * Resolves relative chat image references against the current session workspace.
 *
 * Absolute sources pass through unchanged. Relative sources are resolved through
 * the workspace blob route at render time, so stored transcripts remain portable
 * across daemon ports and sessions.
 */

import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from "react";
import { getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { buildWorkspaceBlobUrl, isAbsoluteMarkdownAssetSrc, resolveMarkdownImageSrc } from "../../lib/markdown-image-resolver";
import { tallpineBridge } from "../../lib/bridge";
import type { LocalImageSource } from "../../../shared/local-path";
import type { SessionArtifact } from "../../types/workspace";
import { isInlineArtifactUrl } from "../../../shared/agent-page-url";

type ChatImageSource = { sessionId: string; version: number; baseUrl?: string; remoteHost: boolean; artifacts?: SessionArtifact[] };

const ChatImageSourceContext = createContext<ChatImageSource | undefined>(undefined);

export function ChatImageSourceProvider({ sessionId, assetBaseUrl, remoteHost = false, artifacts, children }: { sessionId: string; assetBaseUrl?: string; remoteHost?: boolean; artifacts?: SessionArtifact[]; children: ReactNode }) {
	// The blob route is no-store, so a mount-specific version makes rewritten
	// workspace images reload when the chat is reopened.
	const [version] = useState(() => Date.now());
	const localBaseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const baseUrl = remoteHost ? assetBaseUrl : assetBaseUrl ?? localBaseUrl;
	const value = useMemo(() => ({ sessionId, version, baseUrl, remoteHost, artifacts }), [sessionId, version, baseUrl, remoteHost, artifacts]);
	return <ChatImageSourceContext.Provider value={value}>{children}</ChatImageSourceContext.Provider>;
}

/** Resolve an image source for chat, leaving it unchanged outside a session. */
export function useChatImageSrc(src: string | undefined): string | undefined {
	const source = useContext(ChatImageSourceContext);
	if (!source) return src;
	if (source.remoteHost && !source.baseUrl && src && !isAbsoluteMarkdownAssetSrc(src)) return undefined;
	return resolveMarkdownImageSrc(source.sessionId, "", src, source.version, source.baseUrl);
}

/**
 * The URL a thumbnail of a local image path loads from, once main has checked
 * the path against the session's real artifact directory and worktree and named
 * the confined route that serves it. `undefined` while that is pending, outside
 * a local session chat, and for a missing file or one under neither root.
 */
export function useChatLocalImageUrl(path: string): string | undefined {
	const source = useContext(ChatImageSourceContext);
	const sessionId = source && !source.remoteHost && source.baseUrl ? source.sessionId : undefined;
	const [image, setImage] = useState<{ key: string; source?: LocalImageSource }>();
	const key = `${sessionId}\0${path}`;
	useEffect(() => {
		if (!sessionId) return;
		let live = true;
		void tallpineBridge.localPath.inspect(path, sessionId).then(
			(inspection) => { if (live) setImage({ key, source: inspection.ok ? inspection.image : undefined }); },
			() => { if (live) setImage({ key }); },
		);
		return () => { live = false; };
	}, [key, path, sessionId]);
	const found = image?.key === key ? image.source : undefined;
	if (!sessionId || !source?.baseUrl || !found) return undefined;
	if (found.kind === "workspace") return buildWorkspaceBlobUrl(sessionId, found.path, source.version, source.baseUrl);
	const asset = found.path.split("/").map(encodeURIComponent).join("/");
	return `${source.baseUrl.replace(/\/+$/, "")}/api/v1/sessions/${encodeURIComponent(found.sessionId)}/artifact-files/${asset}`;
}

/** Whether the open chat runs on a remote host, whose session files the local daemon cannot serve. */
export function useChatRemoteHost(): boolean {
	return useContext(ChatImageSourceContext)?.remoteHost ?? false;
}

/**
 * What the session lists for one of its artifact files, by path: the Browser
 * panel URL, and the URL that frames the page inline from its own origin.
 */
export function useChatArtifactLinks(path: string | undefined): { sessionId: string; previewUrl?: string; inlineUrl?: string } | undefined {
	const source = useContext(ChatImageSourceContext);
	const artifact = path === undefined ? undefined : source?.artifacts?.find((candidate) => candidate.path === path);
	if (!source || !artifact) return undefined;
	const inlineUrl = artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl) ? artifact.inlineUrl : undefined;
	return { sessionId: source.sessionId, previewUrl: artifact.previewUrl, inlineUrl };
}
