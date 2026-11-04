import { tallpineBridge } from "./bridge";

/**
 * The renderer CSP loads images only from itself, `data:` and the loopback
 * daemon (shared/renderer-csp.ts). A remote https image never loads through
 * `<img src>`: the main process fetches it (`chatImage:fetch`) and the bytes
 * come back as a `data:` URL. Page script or agent markdown can therefore not
 * make the renderer beacon to an arbitrary host just by rendering an `<img>`.
 */

/** An http(s) URL that is not the loopback daemon, so the CSP will not load it. */
export function isRemoteImageUrl(src: string | undefined): src is string {
	if (!src) return false;
	try {
		const url = new URL(src);
		if (url.protocol !== "http:" && url.protocol !== "https:") return false;
		const host = url.hostname;
		return !(host === "127.0.0.1" || host === "[::1]" || host === "localhost" || host.endsWith(".localhost"));
	} catch {
		return false;
	}
}

function toBase64(bytes: Uint8Array): string {
	let binary = "";
	for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
	return btoa(binary);
}

export async function fetchRemoteImageDataUrl(url: string): Promise<string> {
	const { bytes, mimeType } = await tallpineBridge.chatImage.fetch(url);
	return `data:${mimeType};base64,${toBase64(bytes)}`;
}
