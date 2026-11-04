/**
 * Client -> daemon frames of the /mux websocket, shared by the desktop and the
 * phone. Each side keeps its own socket lifecycle; only the wire shapes live
 * here. Terminal payloads are base64 (Go StdEncoding) because raw PTY bytes are
 * not valid UTF-8 and cannot ride in a JSON string.
 */

export function bytesToBase64(bytes: Uint8Array): string {
	let binary = "";
	const chunk = 0x8000;
	for (let i = 0; i < bytes.length; i += chunk) {
		binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
	}
	return btoa(binary);
}

/** Throws on input that is not base64; callers on a socket callback should catch. */
export function base64ToBytes(b64: string): Uint8Array {
	const binary = atob(b64);
	const bytes = new Uint8Array(binary.length);
	for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
	return bytes;
}

export type MuxRole = "primary" | "secondary";

type Scope = { projectId?: string };

export type OpenFrameOptions = Scope & { cols?: number; rows?: number; role?: MuxRole };

/** Optional fields are omitted from the JSON when undefined. */
export function openFrame(id: string, opts: OpenFrameOptions = {}): string {
	const { cols, rows, projectId, role } = opts;
	return JSON.stringify({ ch: "terminal", type: "open", id, cols, rows, projectId, role });
}

export function dataFrame(id: string, bytes: Uint8Array, opts: Scope = {}): string {
	return JSON.stringify({ ch: "terminal", type: "data", id, data: bytesToBase64(bytes), projectId: opts.projectId });
}

export function resizeFrame(id: string, cols: number, rows: number, opts: Scope & { force?: boolean } = {}): string {
	return JSON.stringify({ ch: "terminal", type: "resize", id, cols, rows, ...(opts.force ? { force: true } : {}), projectId: opts.projectId });
}

export function closeFrame(id: string, opts: Scope = {}): string {
	return JSON.stringify({ ch: "terminal", type: "close", id, projectId: opts.projectId });
}

export function pingFrame(): string {
	return JSON.stringify({ ch: "system", type: "ping" });
}

export function subscribeFrame(topics: readonly string[]): string {
	return JSON.stringify({ ch: "subscribe", topics });
}
