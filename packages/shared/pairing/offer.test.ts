import { describe, expect, it } from "vitest";
import { fromBase64Url } from "./base64url";
import { buildPairingOffer, encodePairingCode, pairingCodeUrl } from "./offer";

const input = {
	endpoints: [
		{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false },
		{ kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true, pin: "sha" },
	],
	password: "pw-123",
	hostId: "h_x",
	name: "Alex’s MacBook 🚀",
	platform: "darwin",
};

describe("pairing offer", () => {
	it("carries every endpoint and maps password to token", () => {
		const offer = buildPairingOffer(input);
		expect(offer).toEqual({ v: 2, hostId: "h_x", name: input.name, platform: "darwin", endpoints: input.endpoints, token: "pw-123" });
	});

	it("refuses an empty endpoint list", () => {
		expect(() => buildPairingOffer({ ...input, endpoints: [] })).toThrow(/no endpoints/);
	});

	it("puts the payload in the fragment and decodes back, incl. non-Latin1 names", () => {
		const offer = buildPairingOffer(input);
		const url = pairingCodeUrl(offer, "https://x.test/pair");
		expect(url.startsWith("https://x.test/pair#")).toBe(true);
		expect(url).not.toContain("?");
		expect(JSON.parse(fromBase64Url(url.slice(url.indexOf("#") + 1)))).toEqual(offer);
		expect(encodePairingCode(offer)).toBe(url.slice(url.indexOf("#") + 1));
	});
});
