import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));

import { probeTimeoutMs, PROBE_TIMEOUT_MS, TAILSCALE_PROBE_TIMEOUT_MS } from "./connectRuntime";
import type { Endpoint } from "./endpoints";
import { pairFromCode } from "./pairFlow";
import { encodePairingCode } from "./pairingCode";
import { raceEndpoints } from "./race";

// What a Mac on a tailnet advertises while the phone is on 5G: the LAN address
// is unreachable, the 100.x IP is refused by iOS App Transport Security, and
// the MagicDNS name is the only cleartext address iOS will open.
const lan: Endpoint = { kind: "lan", host: "192.168.1.196", port: 65221, secure: true };
const tsName: Endpoint = { kind: "tailscale", host: "mac.tail99.ts.net", port: 65221, secure: true };
const tsIp: Endpoint = { kind: "tailscale", host: "100.119.224.100", port: 65221, secure: true };
const relay: Endpoint = { kind: "relay", host: "mac.tail99.ts.net", port: 443, secure: true };

const offscreenProbe = vi.fn(async (e: Endpoint) => {
	// The tailnet path answers last: the dead addresses have already failed.
	if (e.host === tsName.host && e.kind === "tailscale") {
		await new Promise((r) => setTimeout(r, 30));
		return { hostId: "h_mac" };
	}
	throw new Error("unreachable");
});

describe("reaching the Mac over Tailscale with no LAN", () => {
	it("falls back from a dead LAN, a refused IP and a dead relay to the MagicDNS name", async () => {
		const got = await raceEndpoints([lan, tsIp, tsName, relay], "h_mac", offscreenProbe, { graceMs: 0 });

		expect(got).toEqual({ ok: true, endpoint: tsName, hostId: "h_mac" });
	});

	it("pairs and remembers every advertised endpoint, not only the winner", async () => {
		const code = encodePairingCode({ v: 2, hostId: "h_mac", name: "mac", platform: "darwin", endpoints: [lan, tsIp, tsName, relay], token: "pw" });
		const saveHost = vi.fn(async () => {});
		const result = await pairFromCode(`tallpine-mobile://pair#${code}`, {
			race: (endpoints, hostId) => raceEndpoints(endpoints, hostId, offscreenProbe, { graceMs: 0 }),
			verify: vi.fn(async () => {}),
			chooseName: vi.fn(async (suggested, appearance) => ({ name: suggested, appearance })),
			saveHost,
			setActiveHost: vi.fn(async () => {}),
		});

		expect(result.ok).toBe(true);
		if (result.ok) {
			expect(result.config.host).toBe(tsName.host);
			expect(result.host.endpoints).toEqual([lan, tsIp, tsName, relay]);
		}
	});

	it("gives a tailnet path longer than a LAN address to wake up", () => {
		expect(probeTimeoutMs("tailscale")).toBe(TAILSCALE_PROBE_TIMEOUT_MS);
		expect(probeTimeoutMs("lan")).toBe(PROBE_TIMEOUT_MS);
		expect(TAILSCALE_PROBE_TIMEOUT_MS).toBeGreaterThan(PROBE_TIMEOUT_MS);
	});

	it("has no cleartext App Transport Security exception: every phone connection is https", () => {
		const app = JSON.parse(readFileSync(new URL("../app.json", import.meta.url), "utf8"));
		const ats = app.expo.ios.infoPlist.NSAppTransportSecurity;
		expect(ats.NSExceptionDomains).toBeUndefined();
		expect(ats.NSAllowsArbitraryLoads).toBeUndefined();
	});
});
