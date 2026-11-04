import { describe, expect, it } from "vitest";
import { pairingAddressLines } from "../../lib/pairing-readiness";

describe("pairingAddressLines", () => {
	it("lists every lan and tailscale endpoint, not just the first", () => {
		const lines = pairingAddressLines(
			[
				{ kind: "lan", host: "192.168.1.42", port: 65221, secure: false },
				{ kind: "tailscale", host: "mac.tail.ts.net", port: 65221, secure: false },
				{ kind: "tailscale", host: "100.72.46.7", port: 65221, secure: false },
				{ kind: "tunnel", host: "x.trycloudflare.com", port: 443, secure: true },
			],
			"fallback:1",
		);
		expect(lines).toEqual(["192.168.1.42:65221", "mac.tail.ts.net:65221", "100.72.46.7:65221"]);
	});
	it("falls back when none are advertised", () => {
		expect(pairingAddressLines(undefined, "fallback:1")).toEqual(["fallback:1"]);
	});
});
