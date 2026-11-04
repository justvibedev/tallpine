import { X509Certificate, createHash } from "node:crypto";
import { request as httpsRequest } from "node:https";
import { isIP } from "node:net";
import { connect as tlsConnect } from "node:tls";

// A LAN gateway serves a self-signed certificate. The user confirms its
// fingerprint once; the certificate is then the only trust anchor for that host,
// so a different certificate (a different machine, or someone in the middle) is
// refused instead of waved through.

export type TlsPinOptions = { ca: string; checkServerIdentity: () => undefined };

/** The certificate carries no usable name (it is addressed by IP), so the pin is the identity. */
export function pinnedTlsOptions(cert: string | undefined): TlsPinOptions | Record<string, never> {
	return cert ? { ca: cert, checkServerIdentity: () => undefined } : {};
}

/** Lowercase hex SHA-256 of the certificate's SubjectPublicKeyInfo: what Settings and the phone show. */
export function certificatePin(pem: string): string {
	const spki = new X509Certificate(pem).publicKey.export({ type: "spki", format: "der" });
	return createHash("sha256").update(spki).digest("hex");
}

export type CertificateProbe = { pin: string; cert: string; trusted: boolean };

/** Reads the certificate a host presents without trusting it; the caller shows the pin to the user. */
export function probeCertificate(rawUrl: string, timeoutMs = 5_000): Promise<CertificateProbe> {
	return new Promise((resolve, reject) => {
		const url = new URL(rawUrl.includes("://") ? rawUrl : `https://${rawUrl}`);
		if (url.protocol !== "https:") return reject(new Error("only https hosts present a certificate"));
		const hostname = url.hostname.replace(/^\[|\]$/g, "");
		const socket = tlsConnect({
			host: hostname,
			port: Number(url.port || 443),
			servername: isIP(hostname) ? undefined : hostname,
			rejectUnauthorized: false,
		});
		const timer = setTimeout(() => socket.destroy(new Error("timed out")), timeoutMs);
		socket.once("error", (error) => { clearTimeout(timer); reject(error); });
		socket.once("secureConnect", () => {
			clearTimeout(timer);
			const raw = socket.getPeerCertificate().raw;
			const trusted = socket.authorized;
			socket.end();
			if (!raw) return reject(new Error("host presented no certificate"));
			const body = raw.toString("base64").match(/.{1,64}/g)?.join("\n") ?? "";
			const cert = `-----BEGIN CERTIFICATE-----\n${body}\n-----END CERTIFICATE-----\n`;
			resolve({ pin: certificatePin(cert), cert, trusted });
		});
	});
}

/** A fetch that trusts exactly the pinned certificate, or the platform default when there is none. */
export function fetchFor(cert: string | undefined): typeof fetch {
	if (!cert) return (input, init) => fetch(input, init);
	return ((input: string | URL | Request, init?: RequestInit) => new Promise<Response>((resolve, reject) => {
		const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url);
		const headers: Record<string, string> = {};
		new Headers(init?.headers).forEach((value, key) => { headers[key] = value; });
		const request = httpsRequest(url, { method: init?.method ?? "GET", headers, signal: init?.signal ?? undefined, ...pinnedTlsOptions(cert) }, (response) => {
			const chunks: Buffer[] = [];
			response.on("data", (chunk: Buffer) => chunks.push(chunk));
			response.on("error", reject);
			response.on("end", () => {
				const status = response.statusCode ?? 502;
				if (status >= 300 && status < 400 && init?.redirect === "error") return reject(new TypeError("unexpected redirect"));
				const responseHeaders = new Headers();
				for (const [key, value] of Object.entries(response.headers)) {
					if (Array.isArray(value)) value.forEach((v) => responseHeaders.append(key, v));
					else if (value !== undefined) responseHeaders.set(key, value);
				}
				const body = [204, 205, 304].includes(status) ? null : Buffer.concat(chunks);
				resolve(new Response(body, { status, statusText: response.statusMessage, headers: responseHeaders }));
			});
		});
		request.on("error", reject);
		if (typeof init?.body === "string") request.write(init.body);
		request.end();
	})) as typeof fetch;
}
