import { createServer, type Server } from "node:https";
import type { AddressInfo } from "node:net";
import { afterEach, describe, expect, it } from "vitest";
import { CERT_A, CERT_B, KEY_A } from "./remote-tls.fixtures";
import { certificatePin, fetchFor, probeCertificate } from "./remote-tls";

let server: Server | undefined;
afterEach(() => { server?.close(); server = undefined; });

async function serve(): Promise<string> {
	server = createServer({ cert: CERT_A, key: KEY_A }, (_req, res) => { res.setHeader("content-type", "application/json"); res.end('{"hostId":"h1"}'); });
	await new Promise<void>((resolve) => server!.listen(0, "127.0.0.1", resolve));
	return `https://127.0.0.1:${(server!.address() as AddressInfo).port}`;
}

describe("remote TLS pinning", () => {
	it("reads the presented certificate and reports it untrusted", async () => {
		const url = await serve();
		const seen = await probeCertificate(url);
		expect(seen.trusted).toBe(false);
		expect(seen.pin).toBe(certificatePin(CERT_A));
		expect(seen.pin).toMatch(/^[0-9a-f]{64}$/);
	});

	it("fetches from a host whose certificate was confirmed", async () => {
		const url = await serve();
		const response = await fetchFor(CERT_A)(`${url}/api/v1/identity`);
		expect(await response.json()).toEqual({ hostId: "h1" });
	});

	it("refuses a host presenting a different certificate than the confirmed one", async () => {
		const url = await serve();
		await expect(fetchFor(CERT_B)(`${url}/api/v1/identity`)).rejects.toThrow();
	});

	it("refuses an unconfirmed self-signed host", async () => {
		const url = await serve();
		await expect(fetchFor(undefined)(`${url}/api/v1/identity`)).rejects.toThrow();
	});
});
