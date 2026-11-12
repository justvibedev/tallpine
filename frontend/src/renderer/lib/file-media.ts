import { LOCAL_AUDIO_EXTENSION, LOCAL_VIDEO_EXTENSION } from "../../shared/local-image-paths";
import { isRasterImagePath } from "./image-files";

export type FileMediaKind = "image" | "video" | "audio";

/** Common media containers; decoding failures stay in the viewer. */
export function fileMediaKind(path: string): FileMediaKind | undefined {
	if (isRasterImagePath(path)) return "image";
	if (LOCAL_VIDEO_EXTENSION.test(path)) return "video";
	if (LOCAL_AUDIO_EXTENSION.test(path)) return "audio";
	return undefined;
}

/**
 * Internal media must carry the renderer Origin through the daemon/proxy guard.
 * Keep ordinary external images in their existing no-CORS mode.
 */
export function mediaCrossOrigin(src: string | undefined): "anonymous" | undefined {
	if (!src) return undefined;
	try {
		const url = new URL(src);
		const local = url.hostname === "127.0.0.1" || url.hostname === "[::1]" || url.hostname === "localhost" || url.hostname.endsWith(".localhost");
		return local && (url.protocol === "http:" || url.protocol === "https:") ? "anonymous" : undefined;
	} catch {
		return undefined;
	}
}
