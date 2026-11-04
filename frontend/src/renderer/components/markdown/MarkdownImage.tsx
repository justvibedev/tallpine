import { mediaCrossOrigin } from "../../lib/file-media";
import { snapshotBlobUrl } from "../../hooks/workspace-files/workspace-review-prefetch";
import { getApiBaseUrl } from "../../lib/api-client";
import { useContext, useState } from "react";
import { MarkdownFileContext } from "./markdown-file-context";
import { resolveMarkdownAssetPath, resolveArtifactImageSrc, resolveMarkdownImageSrc } from "../../lib/markdown-image-resolver";
import { baseUrlForHost } from "../../lib/host-clients";
import { useRemoteImage } from "../../hooks/useRemoteImage";
import { LoadRemoteImageButton } from "../LoadRemoteImageButton";

/**
 * react-markdown's `img` override: resolves a worktree-relative `src` via the
 * workspace blob route.
 *
 * A source that fails falls back to its alt text rather than a broken-image box.
 * The common case is not a typo: the renderer's CSP allows images only from
 * itself, `data:`, and the loopback daemon, so an `https://` image in a README
 * (a shields.io badge, a hosted screenshot) never loads on render. It shows a
 * click-to-load button, and the main process fetches it on request.
 */
export function MarkdownImage({ src, alt }: { src?: string; alt?: string }) {
	const context = useContext(MarkdownFileContext);
	// The failed URL rather than a boolean: a version bump hands us a new URL for
	// the same reference, and that one deserves its own attempt.
	const [failedSrc, setFailedSrc] = useState<string | null>(null);
	const base = context?.hostId ? baseUrlForHost(context.hostId) : undefined;
	const resolvedSrc = context?.snapshotSource && src && !/^[a-z][a-z0-9+.-]*:/i.test(src) && !src.startsWith("//")
 ? (context.hostId && !base ? undefined : snapshotBlobUrl(base ?? getApiBaseUrl(),context.sessionId,resolveMarkdownAssetPath(src.startsWith("/") ? "" : context.filePath,src.replace(/^\/+/,"")),context.snapshotSource))
 : context?.artifactOrigin
		? resolveArtifactImageSrc(context.artifactOrigin, context.filePath, src)
		: context
		? context.hostId && !base ? undefined : resolveMarkdownImageSrc(context.sessionId, context.filePath, src, context.version, base)
		: src;
	// Remote https images do not load on render (CSP); the user opts in per image.
	const remote = useRemoteImage(resolvedSrc, false);
	if (!resolvedSrc || resolvedSrc === failedSrc || remote.state === "failed") return <span className="text-muted-foreground">{alt ?? ""}</span>;
	if (remote.state === "idle" || remote.state === "loading") return <LoadRemoteImageButton url={resolvedSrc} alt={alt} loading={remote.state === "loading"} onLoad={remote.load} />;
	return <img crossOrigin={mediaCrossOrigin(resolvedSrc)} src={remote.src} alt={alt ?? ""} onError={() => setFailedSrc(resolvedSrc)} />;
}
