/** base64url, unpadded: shorter QR, and safe inside a URL fragment. */
export function toBase64Url(text: string): string {
	// btoa only accepts Latin1; encode UTF-8 bytes first so names like
	// "Alex’s MacBook" don't throw. ASCII output is unchanged.
	let binary = "";
	for (const byte of new TextEncoder().encode(text)) binary += String.fromCharCode(byte);
	return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** Inverse of toBase64Url. Throws on input that is not base64url. */
export function fromBase64Url(code: string): string {
	const b64 = code.replace(/-/g, "+").replace(/_/g, "/");
	// Some JS runtimes reject unpadded input, and the encoder strips padding to
	// keep the QR small, so it has to be restored here.
	const pad = b64.length % 4 === 0 ? "" : "=".repeat(4 - (b64.length % 4));
	const binary = atob(b64 + pad);
	return new TextDecoder().decode(Uint8Array.from(binary, (c) => c.charCodeAt(0)));
}
