import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useHostConnection } from "../hooks/useHostConnection";
import { snapshotBlobUrl } from "../hooks/workspace-files/workspace-review-prefetch";
import { type FilesSource } from "../hooks/workspace-files/workspace-files-types";
import { getApiBaseUrl } from "../lib/api-client";
import { buildWorkspaceStreamUrl } from "../lib/markdown-image-resolver";
import { artifactContentUrl } from "../lib/artifact-content-url";
import { mediaCrossOrigin, type FileMediaKind } from "../lib/file-media";
import { PanelMessage, RetryButton } from "./workspace-diff/DiffChrome";

export function FileMediaPreview({ hostId, kind, path, sessionId, source, version, side = "after" }: {
	side?: "before" | "after";
 hostId?: string;
	kind: FileMediaKind;
	path: string;
	sessionId: string;
	source: FilesSource;
	version: number | string;
}) {
	const { t } = useTranslation();
	const { baseUrl: remoteBaseUrl } = useHostConnection(hostId);
	const [failedSrc, setFailedSrc] = useState<string | null>(null);
	const base = hostId ? remoteBaseUrl : getApiBaseUrl();
	// Use the streamed preview route: the image-diff blob route buffers whole
	// revisions and caps them at 16 MiB, which is too small for recordings.
	const rawUrl = source.kind === "snapshot" ? base === undefined ? undefined : snapshotBlobUrl(base,sessionId,path,source,side) : source.kind === "artifact" ? artifactContentUrl(sessionId, path, source.rawUrl) : base === undefined ? undefined
		: buildWorkspaceStreamUrl(sessionId, path, version, base);
	const src = source.kind === "artifact" && rawUrl ? `${rawUrl}${rawUrl.includes("?") ? "&" : "?"}v=${encodeURIComponent(version)}` : rawUrl;
	if (!src || failedSrc === src) {
		const retry = () => {
			setFailedSrc(null);
		};
		return <PanelMessage action={src ? <RetryButton onClick={retry} /> : undefined}>{t("files.error.loadFile")}</PanelMessage>;
	}
	return (
		<div className="grid min-h-0 min-w-0 flex-1 grid-rows-[minmax(0,1fr)] place-items-center overflow-hidden p-3">
			<MediaElement kind={kind} label={path} onError={() => setFailedSrc(src)} src={src} className={kind === "audio" ? "w-full max-w-lg" : "size-full min-h-0 min-w-0 object-contain"} />
		</div>
	);
}

/** The bare image, video or audio element, shared by the Files panel and the chat timeline. */
export function MediaElement({ kind, src, label, onError, className }: {
	kind: FileMediaKind;
	src: string;
	label: string;
	className?: string;
	onError?: () => void;
}) {
	const crossOrigin = mediaCrossOrigin(src);
	if (kind === "image") return <img crossOrigin={crossOrigin} alt={label} aria-label={label} className={className} key={src} onError={onError} src={src} />;
	if (kind === "video") return <video crossOrigin={crossOrigin} aria-label={label} className={className} controls key={src} onError={onError} playsInline preload="metadata" src={src} />;
	return <audio crossOrigin={crossOrigin} aria-label={label} className={className} controls key={src} onError={onError} preload="metadata" src={src} />;
}
