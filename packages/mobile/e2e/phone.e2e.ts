import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import type http from "node:http";
import https from "node:https";
import { join } from "node:path";
import { getSessions, pingServer } from "../lib/api";
import { getConversationPage, sendConversationMessage } from "../lib/chat/api";
import { connectToHost } from "../lib/connectRuntime";
import type { ServerConfig } from "../lib/config";
import { findHost, saveHost, setActiveHost } from "../lib/hosts";
import { getInstallId } from "../lib/installId";
import { exchangeForDeviceToken, isDeviceToken } from "../lib/pairExchange";
import { pairFromCode, resetIssuedTokens, type PairResult } from "../lib/pairFlow";
import { probeEndpoint } from "../lib/connectRuntime";
import { registerPins } from "../lib/pinning";
import { raceEndpoints } from "../lib/race";
import { lanAddress, startScratchDaemon, type ScratchDaemon } from "./harness";

/** The pair screen's wiring (app/pair.tsx attemptPair), minus the UI. */
function pair(code: string): Promise<PairResult> {
	return pairFromCode(code, {
		race: (endpoints, expectedHostId) => raceEndpoints(endpoints, expectedHostId, probeEndpoint),
		exchange: (endpoint, c) =>
			exchangeForDeviceToken(endpoint, { code: c }, { fetch: (url, init) => fetch(url, init), installId: getInstallId, deviceName: "e2e phone" }),
		verify: async (config) => void (await pingServer(config)),
		chooseName: async (suggested, appearance) => ({ name: suggested, appearance }),
		saveHost,
		setActiveHost,
	});
}

async function pairedConfig(d: ScratchDaemon): Promise<ServerConfig> {
	const paired = await pair(await d.mintPairingCode());
	expect(paired.ok).toBe(true);
	const connected = await connectToHost(d.hostId);
	if (!connected.ok) throw new Error(`connect failed: ${connected.reason}`);
	return connected.config;
}

async function waitFor<T>(fn: () => Promise<T | undefined>, what: string): Promise<T> {
	for (let i = 0; i < 50; i++) {
		const v = await fn();
		if (v !== undefined) return v;
		await new Promise((r) => setTimeout(r, 200));
	}
	throw new Error(`timed out waiting for ${what}`);
}

describe("phone against a real daemon (pinned https LAN endpoint)", () => {
	let d: ScratchDaemon;
	beforeAll(async () => { d = await startScratchDaemon(); });
	afterAll(async () => { await d?.stop(); });

	it("pairs through the real /pair/exchange and stores a device token", async () => {
		resetIssuedTokens();
		const result = await pair(d.pairingCode);
		expect(result.ok).toBe(true);
		if (!result.ok) return;
		expect(isDeviceToken(result.config.password)).toBe(true);
		expect(result.config.secure).toBe(true);
		expect(result.config.pin).toBe(d.pin);
		const saved = await findHost(d.hostId);
		expect(saved?.token).toBe(result.config.password);
	});

	it("rejects a pairing code that was already used", async () => {
		const code = await d.mintPairingCode();
		expect((await pair(code)).ok).toBe(true);
		resetIssuedTokens();
		const again = await pair(code);
		expect(again.ok).toBe(false);
		if (!again.ok) expect(again.reason).toBe("verify-failed");
	});

	it("pairing fails on a pin mismatch and succeeds with the right pin on the same endpoint", async () => {
		// The shipped phone hands the offer's pin to the native pinning module; the
		// e2e's stand-in for it enforces the pin. Same host, port and code, only the
		// pin differs, so the pin is the sole cause of the first failure.
		const code = await d.mintPairingCode();
		const offer = JSON.parse(Buffer.from(code, "base64url").toString());
		const withPin = (pin: string) => Buffer.from(JSON.stringify({ ...offer, endpoints: [{ ...d.endpoint, pin }] })).toString("base64url");
		expect(await pair(withPin("0".repeat(64)))).toMatchObject({ ok: false, reason: "none-reachable" });
		expect((await pair(withPin(d.pin))).ok).toBe(true);
	});

	it("connects, lists sessions, shows the chat view and sends a message", async () => {
		const cfg = await pairedConfig(d);
		expect(isDeviceToken(cfg.password)).toBe(true);

		const sessions = await getSessions(cfg);
		expect(sessions.sessions.map((s) => s.id)).toContain(d.sessionId);

		const view = await getConversationPage(cfg, d.sessionId);
		expect(view.sessionId).toBe(d.sessionId);
		expect(view.mode).toBe("chat");
		expect(JSON.stringify(view.items)).toContain("hello from the e2e");

		const sent = await sendConversationMessage(cfg, d.sessionId, { text: "ping from phone", clientMessageId: "e2e-1" });
		expect(sent.duplicate).toBe(false);
		await waitFor(async () => {
			const page = await getConversationPage(cfg, d.sessionId);
			return JSON.stringify(page.items).includes("echo: ping from phone") ? true : undefined;
		}, "the agent's reply");
	});

	it("refuses plain http from the network with HTTPS_REQUIRED", async () => {
		const cfg = await pairedConfig(d);
		const res = await fetch(`http://${lanAddress()}:${d.endpoint.port}/api/v1/sessions`, { headers: { Authorization: `Bearer ${cfg.password}` } });
		expect(res.status).toBe(426);
		expect(((await res.json()) as { code?: string }).code).toBe("HTTPS_REQUIRED");
	});

	it("is refused once the device token is wrong", async () => {
		const cfg = await pairedConfig(d);
		await expect(getSessions({ ...cfg, password: "vot_not-a-real-token" })).rejects.toMatchObject({ status: 401 });
	});
});

// requireEncryptedTransport is a retired key: the listener never serves plain
// http from the network, so the daemon ignores it. A config file written by an
// older build must still boot and serve the phone over the pinned https endpoint.
describe("daemon with the retired requireEncryptedTransport key persisted", () => {
	let d: ScratchDaemon;
	beforeAll(async () => { d = await startScratchDaemon({ requireEncryptedTransport: true }); });
	afterAll(async () => { await d?.stop(); });

	it("serves the pinned https endpoint: pair, list, chat, send", async () => {
		const cfg = await pairedConfig(d);
		expect((await getSessions(cfg)).sessions.map((s) => s.id)).toContain(d.sessionId);
		expect((await getConversationPage(cfg, d.sessionId)).mode).toBe("chat");
		const sent = await sendConversationMessage(cfg, d.sessionId, { text: "secure ping", clientMessageId: "e2e-enc-1" });
		expect(sent.duplicate).toBe(false);
	});
});

describe("API version skew", () => {
	let d: ScratchDaemon;
	let skewed: http.Server;
	let skewedPort = 0;
	let realApiVersion = 0;
	beforeAll(async () => {
		d = await startScratchDaemon();
		registerPins([d.endpoint]);
		realApiVersion = ((await (await fetch(`${d.baseUrl}/api/v1/identity`)).json()) as { apiVersion: number }).apiVersion;
		// A host that identifies exactly like the real daemon but one API version ahead of the real one.
		// Served with the daemon's own pinned key: the phone never pairs over plain http.
		const tls = { cert: readFileSync(join(d.tlsDir, "gateway-cert.pem")), key: readFileSync(join(d.tlsDir, "gateway-key.pem")) };
		skewed = https.createServer(tls, (req, res) => {
			res.setHeader("Content-Type", "application/json");
			if (req.url?.startsWith("/api/v1/identity")) return void res.end(JSON.stringify({ hostId: d.hostId, apiVersion: realApiVersion + 1 }));
			res.statusCode = 404;
			res.end("{}");
		});
		await new Promise<void>((ok) => skewed.listen(0, "127.0.0.1", ok));
		skewedPort = (skewed.address() as { port: number }).port;
	});
	afterAll(async () => {
		skewed?.close();
		await d?.stop();
	});

	it("the real daemon speaks the API version this phone build requires", async () => {
		const result = await pair(await d.mintPairingCode());
		expect(result.ok).toBe(true);
	});

	it("a host on a newer API version is reported as incompatible, not paired", async () => {
		const code = Buffer.from(
			JSON.stringify({ v: 2, hostId: d.hostId, name: "skewed", platform: "darwin", endpoints: [{ kind: "lan", host: "127.0.0.1", port: skewedPort, secure: true, pin: d.pin }], token: "x" }),
		).toString("base64url");
		registerPins([{ kind: "lan", host: "127.0.0.1", port: skewedPort, secure: true, pin: d.pin }]);
		expect(await pair(code)).toMatchObject({ ok: false, reason: "incompatible" });
	});
});
