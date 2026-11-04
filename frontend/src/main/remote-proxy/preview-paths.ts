/* Split out of remote-proxy.ts (B297, P53). */

import path from "node:path";

export function previewHostForSession(id: string, label = "ao-preview"): string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567";
	let bits = 0;
	let value = 0;
	let encoded = "";
	for (const byte of Buffer.from(id, "utf8")) {
		value = (value << 8) | byte;
		bits += 8;
		while (bits >= 5) {
			encoded += alphabet[(value >>> (bits -= 5)) & 31];
		}
	}
	if (bits) encoded += alphabet[(value << (5 - bits)) & 31];
	return `${label}.${encoded.match(/.{1,50}/g)?.join(".")}.localhost`;
}

// The daemon appends a per-session capability label (`k-` + 32 hex) just before
// `.localhost`; the proxy derives hosts from the session id alone, so compare
// without it. The capability itself is never trusted here: the proxy only maps
// a session's own preview host to its own remote session.
export function withoutPreviewCapability(hostname: string): string {
	return hostname.replace(/\.k-[0-9a-f]{32}(?=\.localhost$)/, "");
}

export function previewAssetPath(entry: string, requested: string): string {
	const file = path.posix.normalize(entry).replace(/^\/+/, "");
	const clean = path.posix.normalize(requested).replace(/^\/+/, "");
	if (!clean) return file;
	const root = path.posix.dirname(file);
	if (root === ".") return clean;
	if (clean === root) return file;
	return path.posix.join(root, clean.startsWith(`${root}/`) ? clean.slice(root.length + 1) : clean);
}
